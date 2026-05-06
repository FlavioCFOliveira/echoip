package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"
)

func TestRateLimiter_AllowsBurstThenBlocks(t *testing.T) {
	rl := newRateLimiter(60) // burst=60, refill=1/s
	addr := netip.MustParseAddr("198.51.100.5")

	for i := range 60 {
		ok, _ := rl.allow(addr)
		if !ok {
			t.Fatalf("request %d denied within burst", i)
		}
	}
	ok, retry := rl.allow(addr)
	if ok {
		t.Fatal("61st request allowed, want denied")
	}
	if retry < 500*time.Millisecond || retry > 2*time.Second {
		t.Errorf("retry-after = %v, want ~1s", retry)
	}
}

func TestRateLimiter_PerIPIndependent(t *testing.T) {
	rl := newRateLimiter(2) // tiny burst to surface per-IP isolation
	a := netip.MustParseAddr("198.51.100.5")
	b := netip.MustParseAddr("198.51.100.6")

	for i := range 2 {
		if ok, _ := rl.allow(a); !ok {
			t.Fatalf("a request %d denied", i)
		}
	}
	if ok, _ := rl.allow(a); ok {
		t.Fatal("a third request allowed, want denied")
	}
	// b should still have its full burst.
	if ok, _ := rl.allow(b); !ok {
		t.Fatal("b first request denied — IPs are not isolated")
	}
}

func TestRateLimiter_Cleanup(t *testing.T) {
	rl := newRateLimiter(60)
	addr := netip.MustParseAddr("198.51.100.5")
	rl.allow(addr)

	ctx, cancel := context.WithCancel(t.Context())
	go rl.startCleanup(ctx, 10*time.Millisecond, 1*time.Millisecond)
	defer cancel()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if rl.purgedTotal.Load() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("cleanup did not purge a stale bucket within 500ms")
}

func TestConnLimitListener_BlocksAtCapAndReleasesOnClose(t *testing.T) {
	var lc net.ListenConfig
	raw, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()

	ln := newConnLimitListener(raw, 2)
	addr := raw.Addr().String()

	accepted := make(chan net.Conn, 4)
	acceptErr := make(chan error, 1)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				acceptErr <- err
				return
			}
			accepted <- c
		}
	}()

	dialer := &net.Dialer{Timeout: time.Second}
	dial := func() net.Conn {
		c, err := dialer.DialContext(t.Context(), "tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c1Client, c2Client := dial(), dial()
	defer func() { _ = c1Client.Close() }()

	// Drain the two server conns the limiter let through; we must
	// keep references so we can Close() them and free slots.
	srvConns := make([]net.Conn, 0, 2)
	for range 2 {
		select {
		case c := <-accepted:
			srvConns = append(srvConns, c)
		case <-time.After(time.Second):
			t.Fatal("Accept did not return within 1s while under cap")
		}
	}

	c3Client := dial()
	defer func() { _ = c3Client.Close() }()
	select {
	case <-accepted:
		t.Fatal("third conn slipped past the limit")
	case <-time.After(100 * time.Millisecond):
		// expected — the kernel accepted, our wrapper held the slot.
	}

	// Closing the server-side conn returned by Accept must release
	// the semaphore slot. Closing the client conn does not, because
	// the wrapper only releases on its own Close().
	_ = srvConns[0].Close()
	_ = c2Client.Close()

	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("third conn not accepted within 1s after release")
	}
}

func TestRateLimiter_Middleware_429(t *testing.T) {
	rl := newRateLimiter(1) // burst=1
	withTrustedProxies(t, []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")})

	wrapped := rl.middleware(http.HandlerFunc(homeHandler))
	make := func() *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.RemoteAddr = "198.51.100.5:1234"
		req.Header.Set("X-Real-IP", "203.0.113.42")
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		return rr
	}

	if rr := make(); rr.Code != http.StatusOK {
		t.Errorf("first call status = %d, want 200", rr.Code)
	}
	rr := make()
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("second call status = %d, want 429", rr.Code)
	}
	if got := rr.Header().Get("Retry-After"); got == "" {
		t.Error("Retry-After missing on 429")
	}
}

// Concurrent allow on the same bucket must be race-free under -race.
func TestRateLimiter_RaceFreeUnderConcurrency(t *testing.T) {
	t.Helper()
	rl := newRateLimiter(1000)
	addr := netip.MustParseAddr("198.51.100.5")
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				_, _ = rl.allow(addr)
			}
		}()
	}
	wg.Wait()
}
