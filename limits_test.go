package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fiatjaf/khatru"
	"github.com/nbd-wtf/go-nostr"
)

// Everyone in these tests connects from the same address, like a team in one office.
func TestPrivateEventsAreLimitedPerKeyWhenLoggedIn(t *testing.T) {
	url := newPrivateTestRelay(t, func(rl *khatru.Relay) {
		rl.RejectEvent = append([]func(ctx context.Context, event *nostr.Event) (bool, string){
			eventKeyOrIPRateLimiter(1, time.Hour, 3, 1, time.Hour, 1),
		}, rl.RejectEvent...)
	})
	aliceSecret, alicePubkey := newKey(t)
	bobSecret, bobPubkey := newKey(t)
	whitelist(t, alicePubkey)
	whitelist(t, bobPubkey)

	alice := connect(t, url, aliceSecret)
	bob := connect(t, url, bobSecret)
	for i := range 3 {
		alice.publish(t, fmt.Sprintf("alice %d", i))
		bob.publish(t, fmt.Sprintf("bob %d", i))
	}
	if _, err := alice.send(t, "one too many"); err == nil || !strings.Contains(err.Error(), "rate-limited") {
		t.Errorf("alice wasn't limited past her allowance: %v", err)
	}

	// Without logging in, the same keys share the address's allowance.
	anonymous := connect(t, url, "")
	anonymous.secret = aliceSecret
	anonymous.publish(t, "first from the address")
	if _, err := anonymous.send(t, "second from the address"); err == nil || !strings.Contains(err.Error(), "rate-limited") {
		t.Errorf("a client that never logged in wasn't limited by address: %v", err)
	}
}
