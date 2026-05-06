package main

import (
	"context"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// connLimitListener caps the number of simultaneously-open accepted
// connections. Excess Accepts block until a previously accepted
// connection is closed — the kernel TCP backlog absorbs the surge
// instead of the goroutine pool.
type connLimitListener struct {
	net.Listener
	sem chan struct{}
}

func newConnLimitListener(ln net.Listener, max int) *connLimitListener {
	return &connLimitListener{
		Listener: ln,
		sem:      make(chan struct{}, max),
	}
}

func (l *connLimitListener) Accept() (net.Conn, error) {
	l.sem <- struct{}{}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.sem
		return nil, err
	}
	return &connLimitConn{Conn: c, release: func() { <-l.sem }}, nil
}

type connLimitConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *connLimitConn) Close() error {
	c.once.Do(c.release)
	return c.Conn.Close()
}

// ipBucket holds a token-bucket state for a single client IP.
type ipBucket struct {
	mu       sync.Mutex
	tokens   float64
	lastSeen time.Time
}

// rateLimiter throttles requests on a per-client-IP basis using a
// token-bucket refilled at rate tokens/second, capped at burst.
type rateLimiter struct {
	rate    float64 // tokens per second
	burst   float64 // max tokens any single bucket holds
	buckets sync.Map
	// purgedTotal is observable for tests / metrics; only ever
	// incremented from cleanup.
	purgedTotal atomic.Uint64
}

func newRateLimiter(perMinute int) *rateLimiter {
	burst := float64(perMinute)
	return &rateLimiter{
		rate:  burst / 60.0,
		burst: burst,
	}
}

// allow consumes one token for addr. Returns the time the caller
// should wait before retrying when rejected.
func (rl *rateLimiter) allow(addr netip.Addr) (bool, time.Duration) {
	now := time.Now()

	val, _ := rl.buckets.LoadOrStore(addr, &ipBucket{
		tokens:   rl.burst,
		lastSeen: now,
	})
	b := val.(*ipBucket)

	b.mu.Lock()
	defer b.mu.Unlock()

	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens = math.Min(rl.burst, b.tokens+elapsed*rl.rate)
	b.lastSeen = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	// Compute time-to-refill for the missing fraction.
	missing := 1 - b.tokens
	wait := time.Duration(missing / rl.rate * float64(time.Second))
	return false, wait
}

// startCleanup runs until ctx is done, periodically evicting entries
// that have not been touched for at least maxAge so the map size
// stays bounded.
func (rl *rateLimiter) startCleanup(ctx context.Context, every, maxAge time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			rl.buckets.Range(func(key, val any) bool {
				b := val.(*ipBucket)
				b.mu.Lock()
				stale := now.Sub(b.lastSeen) > maxAge
				b.mu.Unlock()
				if stale {
					rl.buckets.Delete(key)
					rl.purgedTotal.Add(1)
				}
				return true
			})
		}
	}
}

// middleware enforces the rate limit on next, identifying clients
// via clientIP (which already honours trustedProxies). Requests with
// no resolvable client IP are passed through unrestricted — the
// underlying handler will still emit its 500 if appropriate.
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addr := clientIP(r)
		if !addr.IsValid() {
			next.ServeHTTP(w, r)
			return
		}
		if allowed, retry := rl.allow(addr); !allowed {
			retrySec := int(retry.Seconds())
			if retrySec < 1 {
				retrySec = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retrySec))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
