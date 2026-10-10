package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fiatjaf/khatru"
	"github.com/nbd-wtf/go-nostr"
)

// Proxies on the same machine or private network, as Docker and most reverse
// proxy setups have them.
const defaultTrustedProxies = "127.0.0.0/8,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7"

// getTrustedProxies reads TRUSTED_PROXIES: the networks whose X-Forwarded-For
// entries are believed, or "none" to use only the connection's own address.
func getTrustedProxies() []*net.IPNet {
	value := strings.TrimSpace(getEnvString("TRUSTED_PROXIES", defaultTrustedProxies))
	if value == "" || strings.EqualFold(value, "none") {
		return nil
	}
	var networks []*net.IPNet
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if !strings.Contains(entry, "/") {
			if ip := net.ParseIP(entry); ip != nil && ip.To4() != nil {
				entry += "/32"
			} else {
				entry += "/128"
			}
		}
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			log.Fatalf("Invalid network %q in TRUSTED_PROXIES: %v", entry, err)
		}
		networks = append(networks, network)
	}
	return networks
}

func isTrustedProxy(ip net.IP) bool {
	for _, network := range config.TrustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP is the address a request came from. Each proxy appends the address
// it got the request from to X-Forwarded-For, so the list is read from the
// right: entries added by trusted proxies are skipped, and the first one that
// isn't trusted is the client. Entries further left were written by the client
// itself, so they are never believed.
func clientIP(r *http.Request) string {
	address, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		address = r.RemoteAddr
	}
	ip := net.ParseIP(address)
	if ip == nil || !isTrustedProxy(ip) {
		return address
	}
	var hops []string
	for _, header := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(header, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		hop := net.ParseIP(strings.TrimSpace(hops[i]))
		if hop == nil {
			// Unreadable: stay with the last address a trusted proxy vouched for.
			return address
		}
		address = hop.String()
		if !isTrustedProxy(hop) {
			return address
		}
	}
	return address
}

// newRateLimiter works like khatru's: each key may act maxTokens times, and
// tokensPerInterval of those come back every interval.
func newRateLimiter(tokensPerInterval int, interval time.Duration, maxTokens int) func(key string) (limited bool) {
	var mutex sync.Mutex
	used := map[string]int{}

	go func() {
		for {
			time.Sleep(interval)
			mutex.Lock()
			for key, count := range used {
				if count <= tokensPerInterval {
					delete(used, key)
				} else {
					used[key] = count - tokensPerInterval
				}
			}
			mutex.Unlock()
		}
	}()

	return func(key string) bool {
		mutex.Lock()
		defer mutex.Unlock()
		if used[key] >= maxTokens {
			return true
		}
		used[key]++
		return false
	}
}

// eventIPRateLimiter limits how many events each client address may send.
func eventIPRateLimiter(tokensPerInterval int, interval time.Duration, maxTokens int) func(ctx context.Context, _ *nostr.Event) (bool, string) {
	limited := newRateLimiter(tokensPerInterval, interval, maxTokens)
	return func(ctx context.Context, _ *nostr.Event) (bool, string) {
		conn := khatru.GetConnection(ctx)
		if conn == nil {
			// Not from a client, such as a deletion the relay carries out.
			return false, ""
		}
		return limited(clientIP(conn.Request)), "rate-limited: slow down, please"
	}
}

// connectionRateLimiter limits how many connections each client address may open.
func connectionRateLimiter(tokensPerInterval int, interval time.Duration, maxTokens int) func(r *http.Request) bool {
	limited := newRateLimiter(tokensPerInterval, interval, maxTokens)
	return func(r *http.Request) bool {
		return limited(clientIP(r))
	}
}
