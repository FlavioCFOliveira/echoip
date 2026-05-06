package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"
)

// proxyProtoHeaderTimeout caps the read deadline for parsing the
// initial PROXY header — protects against slow-loris-style attacks
// on the listener wrapper itself.
const proxyProtoHeaderTimeout = 5 * time.Second

// proxyProtoListener wraps an underlying listener and decodes the
// HAProxy PROXY protocol (v1 text or v2 binary) on every accepted
// connection. RemoteAddr() of the returned conn reports the original
// client address from the header instead of the upstream proxy.
//
// When the header is missing or malformed the connection is dropped
// (logged at WARN) and Accept loops to the next one. http.Server is
// never woken with a per-connection PROXY error.
type proxyProtoListener struct {
	net.Listener
}

func (l *proxyProtoListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		pc, err := wrapProxyProto(c, proxyProtoHeaderTimeout)
		if err != nil {
			slog.Warn("PROXY protocol parse failed",
				"remote", c.RemoteAddr().String(),
				"Error", err.Error(),
			)
			_ = c.Close()
			continue
		}
		return pc, nil
	}
}

type proxyProtoConn struct {
	net.Conn
	reader     *bufio.Reader
	remoteAddr net.Addr
}

func (c *proxyProtoConn) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}

func (c *proxyProtoConn) RemoteAddr() net.Addr {
	if c.remoteAddr != nil {
		return c.remoteAddr
	}
	return c.Conn.RemoteAddr()
}

var (
	proxyV1Prefix = []byte("PROXY ")
	proxyV2Sig    = []byte{0x0D, 0x0A, 0x0D, 0x0A, 0x00, 0x0D, 0x0A, 0x51, 0x55, 0x49, 0x54, 0x0A}
)

func wrapProxyProto(c net.Conn, timeout time.Duration) (net.Conn, error) {
	if timeout > 0 {
		if err := c.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			return nil, fmt.Errorf("set read deadline: %w", err)
		}
		defer func() { _ = c.SetReadDeadline(time.Time{}) }()
	}

	br := bufio.NewReader(c)
	head, err := br.Peek(12)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("peek: %w", err)
	}

	if len(head) >= 12 && bytes.Equal(head, proxyV2Sig) {
		return parseProxyV2(c, br)
	}
	if len(head) >= 6 && bytes.Equal(head[:6], proxyV1Prefix) {
		return parseProxyV1(c, br)
	}
	return nil, errors.New("missing PROXY header")
}

func parseProxyV1(c net.Conn, br *bufio.Reader) (net.Conn, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("v1 read line: %w", err)
	}
	line = strings.TrimRight(line, "\r\n")

	parts := strings.Fields(line)
	if len(parts) < 2 || parts[0] != "PROXY" {
		return nil, fmt.Errorf("v1 malformed header %q", line)
	}

	switch parts[1] {
	case "TCP4", "TCP6":
		if len(parts) != 6 {
			return nil, fmt.Errorf("v1 %s needs 6 fields, got %d", parts[1], len(parts))
		}
		port, err := strconv.Atoi(parts[4])
		if err != nil || port < 0 || port > 65535 {
			return nil, fmt.Errorf("v1 invalid source port %q", parts[4])
		}
		ip := net.ParseIP(parts[2])
		if ip == nil {
			return nil, fmt.Errorf("v1 invalid source IP %q", parts[2])
		}
		return &proxyProtoConn{
			Conn:       c,
			reader:     br,
			remoteAddr: &net.TCPAddr{IP: ip, Port: port},
		}, nil
	case "UNKNOWN":
		// Proxy reports it could not gather original addresses —
		// keep the upstream RemoteAddr as the source of truth.
		return &proxyProtoConn{Conn: c, reader: br}, nil
	default:
		return nil, fmt.Errorf("v1 unsupported family %q", parts[1])
	}
}

func parseProxyV2(c net.Conn, br *bufio.Reader) (net.Conn, error) {
	// Consume the 12-byte signature that Peek already validated.
	if _, err := br.Discard(12); err != nil {
		return nil, fmt.Errorf("v2 discard sig: %w", err)
	}
	var hdr [4]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		return nil, fmt.Errorf("v2 header: %w", err)
	}
	verCmd := hdr[0]
	famProto := hdr[1]
	addrLen := binary.BigEndian.Uint16(hdr[2:4])

	if verCmd>>4 != 0x2 {
		return nil, fmt.Errorf("v2 unsupported version 0x%x", verCmd>>4)
	}

	addrBytes := make([]byte, addrLen)
	if _, err := io.ReadFull(br, addrBytes); err != nil {
		return nil, fmt.Errorf("v2 addresses: %w", err)
	}

	cmd := verCmd & 0x0F
	if cmd == 0 { // LOCAL — health probe from the proxy itself
		return &proxyProtoConn{Conn: c, reader: br}, nil
	}
	if cmd != 1 { // anything other than PROXY
		return nil, fmt.Errorf("v2 unsupported command %d", cmd)
	}

	switch famProto >> 4 {
	case 0x1: // AF_INET
		if len(addrBytes) < 12 {
			return nil, fmt.Errorf("v2 AF_INET addrLen %d < 12", len(addrBytes))
		}
		return &proxyProtoConn{
			Conn:   c,
			reader: br,
			remoteAddr: &net.TCPAddr{
				IP:   net.IP(addrBytes[:4]),
				Port: int(binary.BigEndian.Uint16(addrBytes[8:10])),
			},
		}, nil
	case 0x2: // AF_INET6
		if len(addrBytes) < 36 {
			return nil, fmt.Errorf("v2 AF_INET6 addrLen %d < 36", len(addrBytes))
		}
		return &proxyProtoConn{
			Conn:   c,
			reader: br,
			remoteAddr: &net.TCPAddr{
				IP:   net.IP(addrBytes[:16]),
				Port: int(binary.BigEndian.Uint16(addrBytes[32:34])),
			},
		}, nil
	default:
		// AF_UNIX or AF_UNSPEC — accept the connection but keep the
		// upstream RemoteAddr as the source of truth.
		return &proxyProtoConn{Conn: c, reader: br}, nil
	}
}
