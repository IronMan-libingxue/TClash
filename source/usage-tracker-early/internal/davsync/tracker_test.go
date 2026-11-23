package davsync

import (
	"bytes"
	"errors"
	"io/fs"
	"testing"
)

func TestTrackerGitHubStoresOnlyScopedUsage(t *testing.T) {
	f := newGithubFixture(t)
	store, err := OpenUsageRepository(t.Context(), "alice/private", "fixture-github-token")
	if err != nil {
		t.Fatal(err)
	}
	folder := "00000000000000000000000000000000/2026"
	name := "2026-11-12.magpie-usage"
	if _, err := store.Read(t.Context(), folder, name); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing usage: %v", err)
	}
	contents := []byte("encrypted usage fixture")
	if err := store.Write(t.Context(), folder, name, contents); err != nil {
		t.Fatal(err)
	}
	files, err := store.List(t.Context(), folder)
	if err != nil || files[name] == "" {
		t.Fatalf("list: %v %v", files, err)
	}
	got, err := store.Read(t.Context(), folder, name)
	if err != nil || !bytes.Equal(got, contents) {
		t.Fatalf("read: %q %v", got, err)
	}
	if err := store.Write(t.Context(), folder, "settings.json", nil); err == nil {
		t.Fatal("settings file accepted in usage storage")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.objects) != 1 {
		t.Fatalf("want only the usage object, got %d", len(f.objects))
	}
}

func TestTrackerGitHubRefusesPublicRepo(t *testing.T) {
	f := newGithubFixture(t)
	f.public = true
	if _, err := OpenUsageRepository(t.Context(), "alice/private", "fixture-github-token"); err == nil {
		t.Fatal("accepted a public repository")
	}
}
