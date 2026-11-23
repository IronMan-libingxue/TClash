package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakePlatform struct {
	home                       string
	runningNames               map[string]bool
	cancel                     bool
	starts, disables, extracts int
	failVerify                 bool
}

func (p *fakePlatform) running(s string) (bool, error) { return p.runningNames[s], nil }
func (p *fakePlatform) askExit(s string) bool {
	if p.cancel {
		return false
	}
	p.runningNames[s] = false
	return true
}
func (p *fakePlatform) disableLegacyStartup(string) error { p.disables++; return nil }
func (p *fakePlatform) extract(_, dest string) error {
	p.extracts++
	return os.MkdirAll(filepath.Join(dest, "TClash Preview.app", "Contents"), 0700)
}
func (p *fakePlatform) verify(string) error {
	if p.failVerify {
		return errors.New("invalid")
	}
	return nil
}
func (p *fakePlatform) start(string) error {
	p.starts++
	dir := filepath.Join(p.home, "Library", "Application Support", "TClash Preview", "collector")
	pending := filepath.Join(dir, "install-enrollment.json")
	b, e := os.ReadFile(pending)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	var d deployment
	if e = json.Unmarshal(b, &d); e != nil {
		return e
	}
	b, _ = json.Marshal(tracker{Role: "b", Name: d.Name, Repo: d.Pair.Repo, Token: d.Token, Pair: d.Pair})
	if e = os.WriteFile(filepath.Join(dir, "tracker.json"), b, 0600); e != nil {
		return e
	}
	return os.Remove(pending)
}
func (p *fakePlatform) sleep(time.Duration) {}
func put(t *testing.T, p string, b []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T) (string, string, []byte, *fakePlatform) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	res := filepath.Join(root, "resources")
	d := deployment{Name: "Mac 2", Token: "fixture-token", Pair: pair{Repo: "fixture-repo", Recipient: "fixture-recipient"}, Secret: make([]byte, 32), StartFresh: true}
	raw, _ := json.Marshal(d)
	put(t, filepath.Join(res, "enrollment.json"), raw)
	put(t, filepath.Join(home, "Applications", "TClash Preview.app", "old"), []byte("old app"))
	put(t, filepath.Join(home, "Library", "Application Support", "TClash Preview", "collector", "tracker.json"), []byte(`{"role":"b"}`))
	put(t, filepath.Join(home, "Library", "Application Support", "TClash Preview", "proxy.yml"), []byte("unchanged"))
	return res, home, raw, &fakePlatform{home: home, runningNames: map[string]bool{}}
}
func TestInstallAndPreserveExisting(t *testing.T) {
	res, home, _, p := fixture(t)
	if e := install(res, home, p); e != nil {
		t.Fatal(e)
	}
	backups, _ := filepath.Glob(filepath.Join(home, "Applications", "TClash Preview 安装前备份 *.app"))
	if len(backups) != 1 {
		t.Fatal("missing backup")
	}
	b, _ := os.ReadFile(filepath.Join(backups[0], "old"))
	if string(b) != "old app" {
		t.Fatal("bad backup")
	}
	b, _ = os.ReadFile(filepath.Join(home, "Library", "Application Support", "TClash Preview", "proxy.yml"))
	if string(b) != "unchanged" {
		t.Fatal("proxy data changed")
	}
	c, ok, e := state(filepath.Join(home, "Library", "Application Support", "TClash Preview", "collector", "tracker.json"))
	if e != nil || !ok || c.Name != "Mac 2" || p.starts != 1 {
		t.Fatal("authorization not applied")
	}
}
func TestReinstallSameIdentityDoesNotReset(t *testing.T) {
	res, home, raw, p := fixture(t)
	var d deployment
	_ = json.Unmarshal(raw, &d)
	b, _ := json.Marshal(tracker{Role: "b", Name: d.Name, Repo: d.Pair.Repo, Pair: d.Pair, Token: d.Token})
	path := filepath.Join(home, "Library", "Application Support", "TClash Preview", "collector", "tracker.json")
	put(t, path, b)
	if e := install(res, home, p); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(path)
	if string(b) != string(after) {
		t.Fatal("identity changed")
	}
}
func TestConflictsAreSafe(t *testing.T) {
	for _, kind := range []string{"other", "pending", "a", "marker", "cancel", "verify"} {
		t.Run(kind, func(t *testing.T) {
			res, home, _, p := fixture(t)
			base := filepath.Join(home, "Library", "Application Support")
			switch kind {
			case "other":
				put(t, filepath.Join(base, "TClash Preview", "collector", "tracker.json"), []byte(`{"role":"b","repo":"other"}`))
			case "pending":
				put(t, filepath.Join(base, "TClash Preview", "collector", "install-enrollment.json"), []byte(`{"other":true}`))
			case "a":
				put(t, filepath.Join(base, "Codex Usage Tracker", "tracker.json"), []byte(`{"role":"a"}`))
			case "marker":
				put(t, filepath.Join(base, "TClash Preview", "collector", "use-legacy-tracker"), []byte("old"))
			case "cancel":
				p.runningNames["TClash Preview"] = true
				p.cancel = true
			case "verify":
				p.failVerify = true
			}
			if e := install(res, home, p); e == nil {
				t.Fatal("expected error")
			}
			if _, e := os.Stat(filepath.Join(home, "Applications", "TClash Preview.app", "old")); e != nil {
				t.Fatal("old app changed")
			}
			if p.starts != 0 {
				t.Fatal("started during failure")
			}
		})
	}
}
func TestOldStartupOnlyAfterChecks(t *testing.T) {
	res, home, _, p := fixture(t)
	base := filepath.Join(home, "Library", "Application Support")
	put(t, filepath.Join(base, "Codex Usage Tracker", "tracker.json"), []byte(`{"role":"b"}`))
	pending := filepath.Join(base, "TClash Preview", "collector", "install-enrollment.json")
	put(t, pending, []byte(`{"other":true}`))
	if e := install(res, home, p); e == nil || p.disables != 0 {
		t.Fatal("startup changed despite conflict")
	}
	_ = os.Remove(pending)
	if e := install(res, home, p); e != nil {
		t.Fatal(e)
	}
	if p.disables != 1 {
		t.Fatal("old startup not disabled")
	}
}

func TestOldStartupPreservedWhenInstallationFails(t *testing.T) {
	res, home, _, p := fixture(t)
	base := filepath.Join(home, "Library", "Application Support")
	put(t, filepath.Join(base, "Codex Usage Tracker", "tracker.json"), []byte(`{"role":"b"}`))
	p.failVerify = true
	if err := install(res, home, p); err == nil {
		t.Fatal("expected installation failure")
	}
	if p.disables != 0 {
		t.Fatal("old collector startup was changed despite failed installation")
	}
}

type realPayloadPlatform struct{ *fakePlatform }

func (p realPayloadPlatform) extract(src, dest string) error {
	return (macPlatform{}).extract(src, dest)
}
func (p realPayloadPlatform) verify(app string) error { return (macPlatform{}).verify(app) }
func TestRealMacPayloadInIsolatedHome(t *testing.T) {
	assets := os.Getenv("TCLASH_TEST_ASSETS")
	if assets == "" {
		t.Skip("real Mac payload integration not requested")
	}
	res, home, _, fake := fixture(t)
	if e := os.Symlink(filepath.Join(assets, "TClash Preview Mac.zip"), filepath.Join(res, "TClash Preview Mac.zip")); e != nil {
		t.Fatal(e)
	}
	if e := install(res, home, realPayloadPlatform{fake}); e != nil {
		t.Fatal(e)
	}
	app := filepath.Join(home, "Applications", "TClash Preview.app")
	if e := (macPlatform{}).verify(app); e != nil {
		t.Fatalf("installed app verification: %v", e)
	}
	if fake.starts != 1 {
		t.Fatal("automatic start was not requested")
	}
}
