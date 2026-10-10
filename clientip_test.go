package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientIP(t *testing.T) {
	previous := config.TrustedProxies
	t.Cleanup(func() { config.TrustedProxies = previous })

	cases := []struct {
		name    string
		trusted string
		remote  string
		headers []string
		want    string
	}{
		{"direct client", defaultTrustedProxies, "203.0.113.7:5000", nil, "203.0.113.7"},
		{"direct client forging the header", defaultTrustedProxies, "203.0.113.7:5000", []string{"198.51.100.1"}, "203.0.113.7"},
		{"behind a proxy", defaultTrustedProxies, "10.0.1.5:5000", []string{"203.0.113.7"}, "203.0.113.7"},
		{"behind a proxy, client forging an entry", defaultTrustedProxies, "10.0.1.5:5000", []string{"198.51.100.1, 203.0.113.7"}, "203.0.113.7"},
		{"behind two proxies", defaultTrustedProxies, "10.0.1.5:5000", []string{"203.0.113.7, 172.18.0.3"}, "203.0.113.7"},
		{"header split over lines", defaultTrustedProxies, "10.0.1.5:5000", []string{"198.51.100.1", "203.0.113.7"}, "203.0.113.7"},
		{"unreadable entry", defaultTrustedProxies, "10.0.1.5:5000", []string{"garbage"}, "10.0.1.5"},
		{"proxy without the header", defaultTrustedProxies, "10.0.1.5:5000", nil, "10.0.1.5"},
		{"IPv6 behind a proxy", defaultTrustedProxies, "[fd00::5]:5000", []string{"2001:db8::7"}, "2001:db8::7"},
		{"no trusted proxies", "none", "10.0.1.5:5000", []string{"203.0.113.7"}, "10.0.1.5"},
		{"a single trusted address", "10.0.1.5", "10.0.1.5:5000", []string{"203.0.113.7"}, "203.0.113.7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TRUSTED_PROXIES", c.trusted)
			config.TrustedProxies = getTrustedProxies()
			request := httptest.NewRequest("GET", "/private", nil)
			request.RemoteAddr = c.remote
			for _, header := range c.headers {
				request.Header.Add("X-Forwarded-For", header)
			}
			if got := clientIP(request); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestRateLimiterIsPerKey(t *testing.T) {
	limited := newRateLimiter(1, time.Hour, 2)
	for range 2 {
		if limited("a") {
			t.Fatal("limited within the allowance")
		}
	}
	if !limited("a") {
		t.Error("not limited past the allowance")
	}
	if limited("b") {
		t.Error("one key's use limited another key")
	}
}
