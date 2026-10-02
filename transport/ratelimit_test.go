package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestClientIPPrefersLastForwardedForEntry(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{
			name:       "no proxy header falls back to remote addr",
			remoteAddr: "203.0.113.7:54321",
			want:       "203.0.113.7",
		},
		{
			// What Heroku actually sends: it appends the connecting peer.
			name:       "heroku appends the real client last",
			remoteAddr: "10.1.2.3:80",
			xff:        "203.0.113.7",
			want:       "203.0.113.7",
		},
		{
			// A caller spoofing the header cannot move itself off its own
			// bucket, because Heroku appends the address it really saw.
			name:       "spoofed leading entries are ignored",
			remoteAddr: "10.1.2.3:80",
			xff:        "1.2.3.4, 5.6.7.8, 203.0.113.7",
			want:       "203.0.113.7",
		},
		{
			name:       "whitespace around entries is trimmed",
			remoteAddr: "10.1.2.3:80",
			xff:        "1.2.3.4,   203.0.113.7   ",
			want:       "203.0.113.7",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}

			if got := clientIP(r); got != tc.want {
				t.Fatalf("clientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The regression this guards: with the limiter keyed on RemoteAddr, every
// request from one caller behind a proxy landed in a different bucket and the
// burst was never spent.
func TestRateLimitMiddlewareSpendsBurstForOneProxiedClient(t *testing.T) {
	const burst = 3

	handler := RateLimitMiddleware(rate.Every(10*time.Second), burst)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	send := func(routerAddr string) int {
		r := httptest.NewRequest(http.MethodPost, "/receipts/image", nil)
		// Each request arrives via a different Heroku router, as it does in
		// production, while the client behind them is the same.
		r.RemoteAddr = routerAddr
		r.Header.Set("X-Forwarded-For", "203.0.113.7")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}

	routers := []string{"10.1.1.1:80", "10.1.1.2:80", "10.1.1.3:80", "10.1.1.4:80", "10.1.1.5:80"}
	for i, addr := range routers {
		code := send(addr)
		if i < burst {
			if code != http.StatusOK {
				t.Fatalf("request %d: got %d, want %d within burst", i+1, code, http.StatusOK)
			}
			continue
		}
		if code != http.StatusTooManyRequests {
			t.Fatalf("request %d: got %d, want %d once burst is spent", i+1, code, http.StatusTooManyRequests)
		}
	}
}

// A second client must not be throttled by the first one's burst.
func TestRateLimitMiddlewareIsolatesClients(t *testing.T) {
	handler := RateLimitMiddleware(rate.Every(10*time.Second), 1)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	send := func(clientAddr string) int {
		r := httptest.NewRequest(http.MethodPost, "/receipts/image", nil)
		r.RemoteAddr = "10.1.1.1:80"
		r.Header.Set("X-Forwarded-For", clientAddr)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}

	if code := send("203.0.113.7"); code != http.StatusOK {
		t.Fatalf("first client first request: got %d, want %d", code, http.StatusOK)
	}
	if code := send("203.0.113.7"); code != http.StatusTooManyRequests {
		t.Fatalf("first client second request: got %d, want %d", code, http.StatusTooManyRequests)
	}
	if code := send("198.51.100.4"); code != http.StatusOK {
		t.Fatalf("second client: got %d, want %d", code, http.StatusOK)
	}
}
