package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"github.com/yetone/magpie/internal/usagetracker"
)

func TestMigrationOnlyUsesAnExplicitPairedLegacyCollector(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "TClash Preview", "collector")
	if err := os.MkdirAll(local, 0o700); err != nil {
		t.Fatal(err)
	}
	if got, migrated, err := selectCollectorDir(root, local); err != nil || migrated || got != local {
		t.Fatal("an ordinary startup should not use another directory")
	}
	if err := os.WriteFile(filepath.Join(local, "use-legacy-tracker"), []byte("Windows 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := selectCollectorDir(root, local); err == nil {
		t.Fatal("missing legacy B was accepted")
	}
	legacy := filepath.Join(root, "Codex Usage Tracker")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	pub, signer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	config := struct {
		Role   string             `json:"role"`
		Name   string             `json:"name"`
		Repo   string             `json:"repo"`
		Token  string             `json:"token"`
		Signer ed25519.PrivateKey `json:"signer"`
		Pair   usagetracker.Pair  `json:"pair"`
	}{"b", "Windows 1", "fixture/private", "secret", signer, usagetracker.Pair{Repo: "fixture/private", Recipient: identity.Recipient().String(), Verifier: pub}}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "tracker.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, migrated, err := selectCollectorDir(root, local); err != nil || !migrated || got != legacy {
		t.Fatal("paired legacy identity was not selected")
	}
	if err := os.WriteFile(filepath.Join(local, "use-legacy-tracker"), []byte("Windows 2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := selectCollectorDir(root, local); err == nil {
		t.Fatal("migration package accepted the wrong device")
	}
	if err := os.WriteFile(filepath.Join(local, "use-legacy-tracker"), []byte("Windows 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "tracker.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := selectCollectorDir(root, local); err == nil {
		t.Fatal("existing TClash identity would be overwritten")
	}
}

func TestLocalControllerPreventsSecondDeviceUpload(t *testing.T) {
	root := t.TempDir()
	if found, err := controllerOnThisUser(root); found || err != nil {
		t.Fatal("empty machine was classified as A")
	}
	dir := filepath.Join(root, "Codex Usage Tracker")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "tracker.json")
	if err := os.WriteFile(path, []byte(`{"role":"a"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if found, err := controllerOnThisUser(root); !found || err != nil {
		t.Fatal("local A was not detected")
	}
	if err := os.WriteFile(path, []byte(`{"role":"b"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if found, err := controllerOnThisUser(root); found || err != nil {
		t.Fatal("legacy B was classified as A")
	}
	if err := os.WriteFile(path, []byte(`not valid json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := controllerOnThisUser(root); err == nil {
		t.Fatal("unknown A state must block upload")
	}
}
