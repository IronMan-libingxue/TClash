package usagetracker

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/filelock"
)

func TestDesktopUpgradeKeepsHistoryAndPairedIdentity(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	legacy := filepath.Join(root, "TClash Preview", "collector")
	destination := DesktopCollectorDir(root)
	store := &memoryRepo{files: map[string][]byte{}}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	open := func(dir, role string) *App {
		t.Helper()
		app, err := New(dir, role)
		check(err)
		app.openRepo = func(_ context.Context, repo, token string) (transport, error) {
			if repo != "fixture/private" || token == "" {
				return nil, errors.New("pairing credentials were lost")
			}
			return store, nil
		}
		app.account = func() string { return "" }
		return app
	}
	controller := open(t.TempDir(), "a")
	check(controller.configure(ctx, setup{Repo: "fixture/private", Token: "fixture-a"}))
	pair, err := controller.config().pair()
	check(err)
	pairCode, err := code(pairPrefix, pair)
	check(err)
	old := open(legacy, "b")
	check(old.configure(ctx, setup{Pair: pairCode, Name: "Paired Mac", Token: "fixture-b"}))
	original := old.config()
	deviceCode, err := code(devicePrefix, original.device())
	check(err)
	check(controller.register(deviceCode))
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-time.Hour)
	oldRows := []Request{{ID: "previous", At: at, Model: "gpt-6.1-sol", Tokens: Tokens{Input: 100, Cache: 50, Output: 7}}}
	rows := func(with []Request) func(context.Context, string, string, []Quota, []AccountCorrection) ([]Day, error) {
		return func(_ context.Context, id, name string, _ []Quota, _ []AccountCorrection) ([]Day, error) {
			return []Day{{Device: id, Name: name, Date: at.Format(time.DateOnly), At: now, Rows: with}}, nil
		}
	}
	old.collect = rows(oldRows)
	check(old.runCollector(ctx, original, true, now))
	check(controller.syncNow(ctx, controller.config()))
	check(MigrateDesktopCollector(legacy, destination))

	upgraded := open(destination, "")
	received := upgraded.config()
	if !bytes.Equal(received.Signer, original.Signer) || received.Token != original.Token ||
		received.Pair.Recipient != original.Pair.Recipient || received.device().ID != original.device().ID {
		t.Fatal("upgrade changed the existing pairing")
	}
	check(upgraded.EnableLocalHistory())
	upgraded.collect = rows(oldRows)
	check(upgraded.runCollector(ctx, received, true, now))
	check(os.Rename(filepath.Dir(legacy), filepath.Join(root, "retired")))
	check(MigrateDesktopCollector(legacy, destination))

	offline := open(destination, "")
	check(offline.EnableLocalHistory())
	if got := offline.view(url.Values{"window": {"all"}}); got.Count != 1 || got.DisplayTotal == nil || got.DisplayTotal.Tokens != 236 {
		t.Fatalf("old usage after removing legacy data: %d requests, %#v", got.Count, got.DisplayTotal)
	}
	offline.collect = rows([]Request{{ID: "new", At: at.Add(time.Minute), Model: "gpt-6.1-sol", Tokens: Tokens{Input: 23}}})
	check(offline.runCollector(ctx, offline.config(), true, now))
	check(offline.runCollector(ctx, offline.config(), true, now))
	check(controller.syncNow(ctx, controller.config()))
	got := controller.view(url.Values{"window": {"all"}, "account": {"all"}})
	if len(got.Paired) != 1 || got.Count != 2 || got.Total.total(true) != 180 {
		t.Fatalf("existing pairing did not receive both old and new usage exactly once: %d pairings, %d requests, %d Token",
			len(got.Paired), got.Count, got.Total.total(true))
	}
}

func TestDesktopUpgradeRetriesWithoutTouchingActiveOldCollector(t *testing.T) {
	root := t.TempDir()
	legacy, destination := filepath.Join(root, "old"), DesktopCollectorDir(root)
	if _, err := New(legacy, "b"); err != nil {
		t.Fatal(err)
	}
	lock, err := os.Create(filepath.Join(legacy, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	held, err := filelock.TryLock(lock)
	if err != nil || !held {
		t.Fatal("could not hold the previous collector lock", err)
	}
	if err := MigrateDesktopCollector(legacy, destination); err == nil {
		t.Fatal("migration ran while the previous collector was active")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := MigrateDesktopCollector(legacy, destination); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(destination, "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateDesktopCollector(legacy, destination); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(destination, "tracker.json"))
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("retry altered a committed migration")
	}
}
