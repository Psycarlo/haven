package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nbd-wtf/go-nostr/nip19"
)

// pubkeyList is a set of pubkeys read from a file of npubs, like the whitelist.
// A new version replaces the whole set at once, so readers never see it half
// updated, and a file that doesn't read keeps the last good version.
type pubkeyList struct {
	path string
	// Always in the list, whatever the file says, like the relay owner.
	always []string
	keys   atomic.Pointer[map[string]struct{}]
	// The file's size and modification time when it was last read.
	stamp string
	// The last reload error, so each problem is logged once.
	lastError string
}

// loadPubkeyList reads the list at startup, stopping the relay if the file is missing or invalid.
func loadPubkeyList(path string, always ...string) *pubkeyList {
	list := &pubkeyList{path: path, always: always}
	if err := list.load(); err != nil {
		log.Fatalf("Failed to load %s: %v", path, err)
	}
	return list
}

func (l *pubkeyList) Has(pubkey string) bool {
	_, ok := (*l.keys.Load())[pubkey]
	return ok
}

func (l *pubkeyList) Len() int {
	return len(*l.keys.Load())
}

// Set returns the current pubkeys. It must not be modified.
func (l *pubkeyList) Set() map[string]struct{} {
	return *l.keys.Load()
}

func (l *pubkeyList) List() []string {
	return slices.Collect(maps.Keys(l.Set()))
}

func (l *pubkeyList) replace(keys map[string]struct{}) {
	for _, pubkey := range l.always {
		keys[pubkey] = struct{}{}
	}
	l.keys.Store(&keys)
}

func fileStamp(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano()), nil
}

func (l *pubkeyList) load() error {
	if l.path == "" {
		l.replace(map[string]struct{}{})
		return nil
	}
	stamp, err := fileStamp(l.path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(l.path)
	if err != nil {
		return err
	}
	keys, err := parseNpubs(data)
	if err != nil {
		return err
	}
	l.replace(keys)
	l.stamp = stamp
	return nil
}

// reload reads the file again when it changed since it was last read.
func (l *pubkeyList) reload() (changed bool, err error) {
	if l.path == "" {
		return false, nil
	}
	stamp, err := fileStamp(l.path)
	if err != nil || stamp == l.stamp {
		return false, err
	}
	if err := l.load(); err != nil {
		return false, err
	}
	return true, nil
}

func parseNpubs(data []byte) (map[string]struct{}, error) {
	var npubs []string
	if err := json.Unmarshal(data, &npubs); err != nil {
		return nil, fmt.Errorf("not a JSON list of npubs: %w", err)
	}
	keys := make(map[string]struct{}, len(npubs))
	for _, npub := range npubs {
		npub = strings.TrimSpace(npub)
		prefix, value, err := nip19.Decode(npub)
		if err != nil || prefix != "npub" {
			return nil, fmt.Errorf("%q is not an npub", npub)
		}
		keys[value.(string)] = struct{}{}
	}
	return keys, nil
}

// watchPubkeyLists reads the whitelist and blacklist again whenever their files
// change, so editing them needs no restart. A zero interval turns this off.
func watchPubkeyLists(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	lists := map[string]*pubkeyList{
		"whitelist": config.WhitelistedPubKeys,
		"blacklist": config.BlacklistedPubKeys,
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for name, list := range lists {
				changed, err := list.reload()
				switch {
				case err != nil:
					if err.Error() != list.lastError {
						log.Printf("🚫 keeping the current %s: %v", name, err)
					}
					list.lastError = err.Error()
				case changed:
					log.Printf("🔄 %s reloaded: %d pubkeys", name, list.Len())
					list.lastError = ""
				}
			}
		}
	}
}
