package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fiatjaf/eventstore/slicestore"
	"github.com/nbd-wtf/go-nostr"
)

type brokenStore struct{}

func (brokenStore) QueryEvents(context.Context, nostr.Filter) (chan *nostr.Event, error) {
	return nil, errors.New("closed")
}

func TestHealth(t *testing.T) {
	store := &slicestore.SliceStore{}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		db   eventQuerier
		want int
	}{
		"database answers": {store, http.StatusOK},
		"database fails":   {brokenStore{}, http.StatusServiceUnavailable},
	} {
		response := httptest.NewRecorder()
		healthHandler(c.db)(response, httptest.NewRequest(http.MethodGet, "/health", nil))
		if response.Code != c.want {
			t.Errorf("%s: answered %d, want %d", name, response.Code, c.want)
		}
	}
}
