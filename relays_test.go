package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnabledRelaysDefaultToAll(t *testing.T) {
	t.Setenv("RELAYS", "")
	enabled := getEnabledRelays()
	for _, name := range relayNames {
		if !enabled[name] {
			t.Errorf("%s isn't enabled by default", name)
		}
	}
}

func TestEnabledRelaysFromList(t *testing.T) {
	t.Setenv("RELAYS", " Private , outbox")
	enabled := getEnabledRelays()
	if !enabled["private"] || !enabled["outbox"] || enabled["chat"] || enabled["inbox"] {
		t.Errorf("got %v, want only private and outbox", enabled)
	}
}

func TestDisabledRelaysAnswerNotFound(t *testing.T) {
	previous := config.Relays
	config.Relays = map[string]bool{"private": true}
	t.Cleanup(func() { config.Relays = previous })

	for path, want := range map[string]int{
		"/private": http.StatusOK,
		"/chat":    http.StatusNotFound,
		"/inbox":   http.StatusNotFound,
		"/":        http.StatusNotFound,
		"/upload":  http.StatusNotFound,
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Accept", "application/nostr+json")
		response := httptest.NewRecorder()
		dynamicRelayHandler(response, request)
		if response.Code != want {
			t.Errorf("GET %s answered %d, want %d", path, response.Code, want)
		}
	}
}
