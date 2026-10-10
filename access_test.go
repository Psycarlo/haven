package main

import (
	"context"
	"maps"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fiatjaf/eventstore/slicestore"
	"github.com/fiatjaf/khatru"
	"github.com/nbd-wtf/go-nostr"
)

// A relay set up like /private, on an in-memory store, with any extra setup.
func newPrivateTestRelay(t *testing.T, setup ...func(*khatru.Relay)) string {
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
	for _, apply := range setup {
		apply(rl)
	}

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
	previous := config.WhitelistedPubKeys.Set()
	keys := maps.Clone(previous)
	keys[pubkey] = struct{}{}
	config.WhitelistedPubKeys.replace(keys)
	t.Cleanup(func() { config.WhitelistedPubKeys.replace(maps.Clone(previous)) })
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
	event, err := c.send(t, content)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return event
}

// send publishes a text note signed with the client's key and returns the relay's answer.
func (c *testClient) send(t *testing.T, content string) (nostr.Event, error) {
	t.Helper()
	event := nostr.Event{CreatedAt: nostr.Now(), Kind: nostr.KindTextNote, Content: content}
	if err := event.Sign(c.secret); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return event, c.relay.Publish(ctx, event)
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

func (c *testClient) count(t *testing.T) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	count, _, err := c.relay.Count(ctx, nostr.Filters{{Kinds: []int{nostr.KindTextNote}}})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return count
}

func TestPrivateCountsOnlyForWhitelistedReaders(t *testing.T) {
	url := newPrivateTestRelay(t)
	memberSecret, memberPubkey := newKey(t)
	outsiderSecret, _ := newKey(t)
	whitelist(t, memberPubkey)

	member := connect(t, url, memberSecret)
	member.publish(t, "private note")

	if got := member.count(t); got != 1 {
		t.Errorf("a whitelisted reader counted %d notes, want 1", got)
	}
	if got := connect(t, url, outsiderSecret).count(t); got != 0 {
		t.Errorf("a key that isn't whitelisted counted %d notes", got)
	}
	if got := connect(t, url, "").count(t); got != 0 {
		t.Errorf("a client that never logged in counted %d notes", got)
	}
}
