package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fiatjaf/eventstore/slicestore"
	"github.com/fiatjaf/khatru"
	"github.com/nbd-wtf/go-nostr"
)

// A relay set up like /private, on an in-memory store.
func newPrivateTestRelay(t *testing.T) string {
	t.Helper()
	store := &slicestore.SliceStore{}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	rl := khatru.NewRelay()
	rl.StoreEvent = append(rl.StoreEvent, store.SaveEvent)
	rl.QueryEvents = append(rl.QueryEvents, store.QueryEvents)
	rl.CountEvents = append(rl.CountEvents, store.CountEvents)
	rl.DeleteEvent = append(rl.DeleteEvent, store.DeleteEvent)
	rl.ReplaceEvent = append(rl.ReplaceEvent, store.ReplaceEvent)
	rl.OnConnect = append(rl.OnConnect, khatru.RequestAuth)
	rl.RejectEvent = append(rl.RejectEvent, MustBeWhitelistedToPost)
	privateAccess(rl)

	server := httptest.NewServer(rl)
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

type testClient struct {
	relay  *nostr.Relay
	secret string
}

func newKey(t *testing.T) (string, string) {
	t.Helper()
	secret := nostr.GeneratePrivateKey()
	pubkey, err := nostr.GetPublicKey(secret)
	if err != nil {
		t.Fatal(err)
	}
	return secret, pubkey
}

func whitelist(t *testing.T, pubkey string) {
	t.Helper()
	config.WhitelistedPubKeys[pubkey] = struct{}{}
	t.Cleanup(func() { delete(config.WhitelistedPubKeys, pubkey) })
}

// connect opens a connection, logged in as the key when one is given.
func connect(t *testing.T, url, secret string) *testClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	relay, err := nostr.RelayConnect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relay.Close() })
	client := &testClient{relay: relay, secret: secret}
	if secret == "" {
		return client
	}
	// The challenge arrives right after connecting; retry until it has.
	for {
		err := relay.Auth(ctx, func(event *nostr.Event) error { return event.Sign(secret) })
		if err == nil {
			return client
		}
		if ctx.Err() != nil {
			t.Fatalf("auth: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (c *testClient) publish(t *testing.T, content string) nostr.Event {
	t.Helper()
	event := nostr.Event{CreatedAt: nostr.Now(), Kind: nostr.KindTextNote, Content: content}
	if err := event.Sign(c.secret); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.relay.Publish(ctx, event); err != nil {
		t.Fatalf("publish: %v", err)
	}
	return event
}

// live subscribes to new text notes only, as clients do with "limit": 0.
func (c *testClient) live(t *testing.T) *nostr.Subscription {
	t.Helper()
	sub, err := c.relay.Subscribe(context.Background(), nostr.Filters{{
		Kinds:     []int{nostr.KindTextNote},
		LimitZero: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sub.Unsub)
	return sub
}

func received(sub *nostr.Subscription, id string, wait time.Duration) bool {
	timeout := time.After(wait)
	for {
		select {
		case event := <-sub.Events:
			if event.ID == id {
				return true
			}
		case <-sub.ClosedReason:
			return false
		case <-timeout:
			return false
		}
	}
}

func TestPrivateLiveEventsOnlyReachWhitelistedReaders(t *testing.T) {
	url := newPrivateTestRelay(t)
	memberSecret, memberPubkey := newKey(t)
	outsiderSecret, _ := newKey(t)
	whitelist(t, memberPubkey)

	member := connect(t, url, memberSecret)
	outsider := connect(t, url, outsiderSecret)
	anonymous := connect(t, url, "")

	memberSub := member.live(t)
	outsiderSub := outsider.live(t)
	anonymousSub := anonymous.live(t)
	time.Sleep(100 * time.Millisecond)

	event := member.publish(t, "private note")

	if !received(memberSub, event.ID, 2*time.Second) {
		t.Error("a whitelisted reader didn't get the live event")
	}
	if received(outsiderSub, event.ID, 300*time.Millisecond) {
		t.Error("a key that isn't whitelisted got the live event")
	}
	if received(anonymousSub, event.ID, 300*time.Millisecond) {
		t.Error("a client that never logged in got the live event")
	}
}
