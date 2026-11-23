package davsync

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
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

func TestDeviceResetListsAndDeletesOnlyDeviceUsage(t *testing.T) {
	f := &githubFixture{objects: map[string][]byte{}, defaultBranch: "main", heads: map[string]string{},
		blobs: map[string][]byte{}, trees: map[string]fixtureTree{}, commits: map[string]fixtureCommit{}, contentsLimit: 1 << 20, blobLimit: 24 << 20}
	oldClient := syncClient
	syncClient = &http.Client{Transport: fixtureRoundTripper{fixture: f}}
	t.Cleanup(func() { syncClient = oldClient })
	store, err := OpenUsageRepository(t.Context(), "alice/private", "fixture-github-token")
	if err != nil {
		t.Fatal(err)
	}
	id := "00000000000000000000000000000000"
	folder, name := id+"/2026", "2026-11-12.magpie-usage"
	if err := store.Write(t.Context(), folder, name, []byte("fixture")); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(t.Context(), "controls", "policy.magpie-quotas", []byte("keep")); err != nil {
		t.Fatal(err)
	}
	ids, err := store.DeviceIDs(t.Context())
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("could not enumerate device IDs: %v %v", ids, err)
	}
	years, err := store.DeviceYears(t.Context(), id)
	if err != nil || len(years) != 1 || years[0] != "2026" {
		t.Fatalf("could not enumerate device years: %v %v", years, err)
	}
	if err := store.DeleteDeviceFile(t.Context(), "controls", "policy.magpie-quotas"); err == nil {
		t.Fatal("device cleanup accepted the shared control file")
	}
	if err := store.DeleteDeviceFile(t.Context(), folder, "2025-11-12.magpie-usage"); err == nil {
		t.Fatal("device cleanup accepted a mismatched year")
	}
	if err := store.DeleteDeviceFile(t.Context(), folder, name); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(t.Context(), folder, name); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("device usage was not removed: %v", err)
	}
	if got, err := store.Read(t.Context(), "controls", "policy.magpie-quotas"); err != nil || string(got) != "keep" {
		t.Fatal("device cleanup changed shared policy")
	}
}

type fixtureRoundTripper struct{ fixture *githubFixture }

func (f fixtureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	response := httptest.NewRecorder()
	f.fixture.serve(response, req)
	return response.Result(), nil
}
