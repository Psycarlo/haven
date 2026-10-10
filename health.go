package main

import (
	"context"
	"net/http"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

type eventQuerier interface {
	QueryEvents(ctx context.Context, filter nostr.Filter) (chan *nostr.Event, error)
}

// healthHandler answers 200 once the database answers a query, for container
// health checks. It reads at most one event and never returns it.
func healthHandler(db eventQuerier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		events, err := db.QueryEvents(ctx, nostr.Filter{Limit: 1})
		if err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		for range events {
		}
		if ctx.Err() != nil {
			http.Error(w, "database too slow", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	}
}
