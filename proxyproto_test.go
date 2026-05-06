package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// pipeConn is a synchronous in-memory net.Conn pair used to feed the
// PROXY protocol parser without spinning up a real listener.
type pipeConn struct {
	*bytes.Buffer
	remote net.Addr
}

func (p *pipeConn) Close() error                       { return nil }
func (p *pipeConn) LocalAddr() net.Addr                { return p.remote }
func (p *pipeConn) RemoteAddr() net.Addr               { return p.remote }
func (p *pipeConn) SetDeadline(_ time.Time) error      { return nil }
func (p *pipeConn) SetReadDeadline(_ time.Time) error  { return nil }
func (p *pipeConn) SetWriteDeadline(_ time.Time) error { return nil }
func (p *pipeConn) Write(b []byte) (int, error)        { return p.Buffer.Write(b) }
func (p *pipeConn) Read(b []byte) (int, error)         { return p.Buffer.Read(b) }

func newPipe(payload []byte) *pipeConn {
	return &pipeConn{
		Buffer: bytes.NewBuffer(payload),
		remote: &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 1234},
	}
}

func TestProxyProtoV1_TCP4(t *testing.T) {
	body := []byte("GET / HTTP/1.1\r\n\r\n")
	header := []byte("PROXY TCP4 192.0.2.42 198.51.100.1 56789 80\r\n")
	c := newPipe(append(header, body...))

	pc, err := wrapProxyProto(c, 0)
	if err != nil {
		t.Fatalf("wrap error: %v", err)
	}
	addr, ok := pc.RemoteAddr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("RemoteAddr type %T, want *net.TCPAddr", pc.RemoteAddr())
	}
	if addr.IP.String() != "192.0.2.42" || addr.Port != 56789 {
		t.Errorf("RemoteAddr = %s, want 192.0.2.42:56789", addr.String())
	}
	got, _ := io.ReadAll(pc)
	if !bytes.Equal(got, body) {
		t.Errorf("body = %q, want %q", got, body)
	}
}

func TestProxyProtoV1_TCP6(t *testing.T) {
	body := []byte("GET / HTTP/1.1\r\n\r\n")
	header := []byte("PROXY TCP6 2001:db8::1 2001:db8::2 5000 443\r\n")
	c := newPipe(append(header, body...))

	pc, err := wrapProxyProto(c, 0)
	if err != nil {
		t.Fatalf("wrap error: %v", err)
	}
	addr := pc.RemoteAddr().(*net.TCPAddr)
	if addr.IP.String() != "2001:db8::1" || addr.Port != 5000 {
		t.Errorf("RemoteAddr = %s, want 2001:db8::1:5000", addr.String())
	}
}

func TestProxyProtoV1_UnknownKeepsRemote(t *testing.T) {
	c := newPipe([]byte("PROXY UNKNOWN\r\nGET / HTTP/1.1\r\n\r\n"))
	pc, err := wrapProxyProto(c, 0)
	if err != nil {
		t.Fatalf("wrap error: %v", err)
	}
	if pc.RemoteAddr().String() != "10.0.0.1:1234" {
		t.Errorf("RemoteAddr = %s, want original 10.0.0.1:1234", pc.RemoteAddr())
	}
}

func TestProxyProtoV1_Invalid(t *testing.T) {
	cases := []string{
		"PROXY TCP4 1.2.3.4 5.6.7.8 abc 80\r\n",    // bad port
		"PROXY TCP4 not.an.ip 5.6.7.8 1234 80\r\n", // bad IP
		"PROXY TCP9 1.2.3.4 5.6.7.8 1234 80\r\n",   // bad family
		"PROXY TCP4 1.2.3.4\r\n",                   // too few fields
	}
	for _, header := range cases {
		t.Run(strings.Split(header, "\r")[0], func(t *testing.T) {
			c := newPipe([]byte(header))
			if _, err := wrapProxyProto(c, 0); err == nil {
				t.Error("wrap returned nil error, want error")
			}
		})
	}
}

func TestProxyProtoNoHeader_Rejected(t *testing.T) {
	c := newPipe([]byte("GET / HTTP/1.1\r\nHost: example\r\n\r\n"))
	if _, err := wrapProxyProto(c, 0); err == nil {
		t.Error("wrap returned nil error, want error (require mode)")
	}
}

func TestProxyProtoV2_TCP4(t *testing.T) {
	// Build a v2 PROXY/AF_INET/STREAM header for 192.0.2.42:56789 -> 198.51.100.1:80
	var buf bytes.Buffer
	buf.Write(proxyV2Sig)
	buf.WriteByte(0x21) // version=2 cmd=PROXY
	buf.WriteByte(0x11) // family=AF_INET proto=STREAM
	_ = binary.Write(&buf, binary.BigEndian, uint16(12))
	buf.Write([]byte{192, 0, 2, 42})   // src IP
	buf.Write([]byte{198, 51, 100, 1}) // dst IP
	_ = binary.Write(&buf, binary.BigEndian, uint16(56789))
	_ = binary.Write(&buf, binary.BigEndian, uint16(80))
	buf.WriteString("DATA")

	c := newPipe(buf.Bytes())
	pc, err := wrapProxyProto(c, 0)
	if err != nil {
		t.Fatalf("wrap error: %v", err)
	}
	addr := pc.RemoteAddr().(*net.TCPAddr)
	if addr.IP.To4().String() != "192.0.2.42" || addr.Port != 56789 {
		t.Errorf("RemoteAddr = %s, want 192.0.2.42:56789", addr.String())
	}
	rest, _ := io.ReadAll(pc)
	if string(rest) != "DATA" {
		t.Errorf("body = %q, want DATA", rest)
	}
}

func TestProxyProtoV2_LocalKeepsRemote(t *testing.T) {
	// Build a v2 LOCAL header (cmd=0) — typical proxy health check.
	var buf bytes.Buffer
	buf.Write(proxyV2Sig)
	buf.WriteByte(0x20) // version=2 cmd=LOCAL
	buf.WriteByte(0x00) // family=AF_UNSPEC
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))

	c := newPipe(buf.Bytes())
	pc, err := wrapProxyProto(c, 0)
	if err != nil {
		t.Fatalf("wrap error: %v", err)
	}
	if pc.RemoteAddr().String() != "10.0.0.1:1234" {
		t.Errorf("RemoteAddr = %s, want original (LOCAL keeps it)", pc.RemoteAddr())
	}
}
