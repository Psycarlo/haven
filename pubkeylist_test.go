package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr/nip19"
)

func newNpub(t *testing.T) (string, string) {
	t.Helper()
	_, pubkey := newKey(t)
	npub, err := nip19.EncodePublicKey(pubkey)
	if err != nil {
		t.Fatal(err)
	}
	return npub, pubkey
}

func writeList(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// Make sure the change shows in the modification time, however coarse the filesystem's clock.
	later := time.Now().Add(time.Duration(len(content)) * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

func TestPubkeyListReloadsWhenTheFileChanges(t *testing.T) {
	_, owner := newKey(t)
	aliceNpub, alice := newNpub(t)
	bobNpub, bob := newNpub(t)
	path := filepath.Join(t.TempDir(), "whitelisted_npubs.json")
	writeList(t, path, `["`+aliceNpub+`"]`)

	list := loadPubkeyList(path, owner)
	if !list.Has(alice) || !list.Has(owner) || list.Has(bob) {
		t.Fatalf("after loading: alice %v, owner %v, bob %v", list.Has(alice), list.Has(owner), list.Has(bob))
	}

	if changed, err := list.reload(); changed || err != nil {
		t.Errorf("reloaded an unchanged file: %v, %v", changed, err)
	}

	writeList(t, path, `["`+bobNpub+`", "`+aliceNpub+`"]`)
	if changed, err := list.reload(); !changed || err != nil {
		t.Fatalf("didn't reload a changed file: %v, %v", changed, err)
	}
	if !list.Has(bob) || !list.Has(alice) || !list.Has(owner) {
		t.Error("the new version is missing pubkeys")
	}

	writeList(t, path, `["`+bobNpub+`", "not an npub at all"]`)
	if _, err := list.reload(); err == nil {
		t.Error("accepted a file with an invalid npub")
	}
	if !list.Has(alice) || !list.Has(bob) {
		t.Error("an invalid file replaced the last good version")
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := list.reload(); err == nil {
		t.Error("a missing file wasn't reported")
	}
	if !list.Has(alice) {
		t.Error("a missing file emptied the list")
	}
}

func TestPubkeyListWithoutFile(t *testing.T) {
	_, owner := newKey(t)
	list := loadPubkeyList("", owner)
	if list.Len() != 1 || !list.Has(owner) {
		t.Errorf("got %v, want only the owner", list.List())
	}
	if changed, err := list.reload(); changed || err != nil {
		t.Errorf("reloading without a file: %v, %v", changed, err)
	}
}
