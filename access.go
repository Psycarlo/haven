package main

import (
	"github.com/barrydeen/haven/pkg/wot"
	"github.com/fiatjaf/khatru"
	"github.com/fiatjaf/khatru/policies"
	"github.com/nbd-wtf/go-nostr"
)

// Who may read a relay is checked in three places, because khatru treats them
// apart: RejectFilter runs only for REQs that ask for stored events, so a REQ
// with "limit": 0 skips it and still gets a live listener. PreventBroadcast is
// what keeps live events from connections that couldn't read them stored, and
// RejectCountFilter does the same for COUNT.

// privateAccess lets only clients logged in with a whitelisted key read /private.
func privateAccess(rl *khatru.Relay) {
	rl.RejectFilter = append(rl.RejectFilter, policies.MustAuth, MustBeWhitelistedToQuery)
	rl.RejectCountFilter = append(rl.RejectCountFilter, policies.MustAuth, MustBeWhitelistedToQuery)
	rl.PreventBroadcast = append(rl.PreventBroadcast, notWhitelistedToRead)
}

// chatAccess lets only clients logged in with a key in the web of trust read /chat.
func chatAccess(rl *khatru.Relay) {
	rl.RejectFilter = append(rl.RejectFilter, policies.MustAuth, MustBeInWotToQuery)
	rl.RejectCountFilter = append(rl.RejectCountFilter, policies.MustAuth, MustBeInWotToQuery)
	rl.PreventBroadcast = append(rl.PreventBroadcast, notInWotToRead)
}

func notWhitelistedToRead(ws *khatru.WebSocket, _ *nostr.Event) bool {
	_, ok := config.WhitelistedPubKeys[ws.AuthedPublicKey]
	return !ok
}

func notInWotToRead(ws *khatru.WebSocket, _ *nostr.Event) bool {
	return ws.AuthedPublicKey == "" || !wot.GetInstance().Has(ws.Context, ws.AuthedPublicKey)
}
