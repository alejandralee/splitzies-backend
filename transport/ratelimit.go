package transport

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ipRateLimiter hands out a per-IP token bucket limiter, evicting entries
// that haven't been used in a while so the map doesn't grow unbounded.
type ipRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rateLimiterEntry
	r        rate.Limit
	burst    int
}

type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newIPRateLimiter(r rate.Limit, burst int) *ipRateLimiter {
	l := &ipRateLimiter{
		limiters: make(map[string]*rateLimiterEntry),
		r:        r,
		burst:    burst,
	}
	go l.evictStale()
	return l
}

func (l *ipRateLimiter) evictStale() {
	for range time.Tick(10 * time.Minute) {
		l.mu.Lock()
		for ip, entry := range l.limiters {
			if time.Since(entry.lastSeen) > 10*time.Minute {
				delete(l.limiters, ip)
			}
		}
		l.mu.Unlock()
	}
}

func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	entry, ok := l.limiters[ip]
	if !ok {
		entry = &rateLimiterEntry{limiter: rate.NewLimiter(l.r, l.burst)}
		l.limiters[ip] = entry
	}
	entry.lastSeen = time.Now()
	limiter := entry.limiter
	l.mu.Unlock()

	return limiter.Allow()
}

// clientIP returns the key to rate limit a request under.
//
// Behind Heroku's router, r.RemoteAddr is the router's own address, not the
// caller's — and Heroku runs many routers, so keying on it scatters one
// caller's requests across many buckets and no bucket ever fills. That is not
// a tuning problem: it silently disables rate limiting altogether.
//
// Heroku *appends* the address it accepted the connection from to the end of
// X-Forwarded-For, so the last element is the only one the caller cannot
// forge: a caller who sends "X-Forwarded-For: 1.2.3.4" gets
// "1.2.3.4, <their real IP>" by the time it reaches us. Earlier elements are
// attacker-controlled and must never be used as the key.
// clientIP returns the key to rate limit a request under.
//
// Behind Heroku's router, r.RemoteAddr is the router's own address, not the
// caller's — and Heroku runs many routers, so keying on it scatters one
// caller's requests across many buckets and no bucket ever fills. That is not
// a tuning problem: it silently disables rate limiting altogether.
//
// Heroku *appends* the address it accepted the connection from to the end of
// X-Forwarded-For, so the last element is the only one the caller cannot
// forge: a caller who sends "X-Forwarded-For: 1.2.3.4" gets
// "1.2.3.4, <their real IP>" by the time it reaches us. Earlier elements are
// attacker-controlled and must never be used as the key.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
			return ip
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// RateLimitMiddleware rejects requests from a single IP once it exceeds r
// requests/sec (with the given burst) with 429 Too Many Requests.
func RateLimitMiddleware(r rate.Limit, burst int) func(http.Handler) http.Handler {
	limiter := newIPRateLimiter(r, burst)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if !limiter.allow(clientIP(req)) {
				writeJSONError(w, http.StatusTooManyRequests, "rate_limited", "too many requests, please slow down", "")
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}
