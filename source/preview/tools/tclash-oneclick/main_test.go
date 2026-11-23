package main

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakePlatform struct {
	data, local                                     string
	runningNames                                    map[string]bool
	cancel                                          bool
	installerRuns, starts, disables, handlerBackups int
	failCollector                                   bool
	registered                                      []string
	installTo                                       string
	startedPath                                     string
}

func (p *fakePlatform) running(s string) (bool, error) { return p.runningNames[s], nil }
func (p *fakePlatform) askExit(s string) bool {
	if p.cancel {
		return false
	}
	p.runningNames[s] = false
	return true
}
func (p *fakePlatform) runInstaller(s string, update bool) error {
	p.installerRuns++
	dir := filepath.Join(p.local, "TClash Preview")
	if p.installTo != "" {
		dir = p.installTo
		p.registered = []string{dir}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "tclash-preview.exe"), []byte("fresh-app"), 0600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "tclash-collector.exe"), []byte("new-collector"), 0600)
}
func (p *fakePlatform) registeredDirs() ([]string, error)   { return p.registered, nil }
func (p *fakePlatform) prepareHandlers(s string) error      { p.handlerBackups++; return nil }
func (p *fakePlatform) disableLegacyStartup(s string) error { p.disables++; return nil }
func (p *fakePlatform) collectorOK(s string) error {
	if p.failCollector {
		return errors.New("invalid")
	}
	b, err := os.ReadFile(s)
	if err != nil {
		return err
	}
	if string(b) != "new-collector" {
		return errors.New("old collector")
	}
	return nil
}
func (p *fakePlatform) start(s string) error {
	p.starts++
	p.startedPath = s
	dir := filepath.Join(p.data, "TClash Preview", "collector")
	pending := filepath.Join(dir, "install-enrollment.json")
	raw, err := os.ReadFile(pending)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var d deployment
	if err = json.Unmarshal(raw, &d); err != nil {
		return err
	}
	c := tracker{Role: "b", Name: d.Name, Repo: d.Pair.Repo, Pair: d.Pair, Token: d.Token}
	b, _ := json.Marshal(c)
	if err = os.WriteFile(filepath.Join(dir, "tracker.json"), b, 0600); err != nil {
		return err
	}
	return os.Remove(pending)
}

func TestExistingCustomInstallLocation(t *testing.T) {
	bundle, local, data, _, p := fixture(t)
	custom := filepath.Join(t.TempDir(), "自定义安装", "TClash Preview")
	file(t, filepath.Join(custom, "tclash-preview.exe"), []byte("old-app"))
	file(t, filepath.Join(custom, "tclash-collector.exe"), []byte("collector"))
	file(t, filepath.Join(data, "TClash Preview", "collector", "tracker.json"), []byte(`{"role":"b"}`))
	p.registered = []string{custom}
	if err := install(bundle, local, data, "x64", p); err != nil {
		t.Fatal(err)
	}
	if p.installerRuns != 0 || p.startedPath != filepath.Join(custom, "tclash-preview.exe") {
		t.Fatal("custom installation was not updated")
	}
	b, err := os.ReadFile(filepath.Join(custom, "tclash-preview.exe"))
	if err != nil || string(b) != "new-app" {
		t.Fatal("custom application was not replaced")
	}
}

func TestInstallerRestoresCustomLocation(t *testing.T) {
	bundle, local, data, _, p := fixture(t)
	custom := filepath.Join(t.TempDir(), "自定义安装", "TClash Preview")
	p.installTo = custom
	if err := install(bundle, local, data, "x64", p); err != nil {
		t.Fatal(err)
	}
	if p.installerRuns != 1 || p.startedPath != filepath.Join(custom, "tclash-preview.exe") {
		t.Fatal("restored install location was not used")
	}
}

func TestConflictingInstallLocationsDoNotOverwrite(t *testing.T) {
	bundle, local, data, _, p := fixture(t)
	installed(t, local, data)
	custom := filepath.Join(t.TempDir(), "TClash Preview")
	file(t, filepath.Join(custom, "tclash-preview.exe"), []byte("different-app"))
	p.registered = []string{custom}
	if err := install(bundle, local, data, "x64", p); err == nil {
		t.Fatal("conflicting installations accepted")
	}
	b, _ := os.ReadFile(filepath.Join(custom, "tclash-preview.exe"))
	if string(b) != "different-app" || p.starts != 0 {
		t.Fatal("a conflicting installation was changed")
	}
}
func (p *fakePlatform) sleep(time.Duration) {}
func fixture(t *testing.T) (string, string, string, []byte, *fakePlatform) {
	t.Helper()
	root := t.TempDir()
	local := filepath.Join(root, "Local")
	data := filepath.Join(root, "Roaming")
	d := deployment{Name: "Windows 1", Token: "fixture-token", Pair: pair{Repo: "fixture-repo", Recipient: "fixture-recipient"}, Secret: make([]byte, 32), StartFresh: true}
	raw, _ := json.Marshal(d)
	path := filepath.Join(root, "fixture.exe")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte("MZ sample executable prefix")); err != nil {
		t.Fatal(err)
	}
	offset, _ := f.Seek(0, 1)
	z := zip.NewWriter(f)
	z.SetOffset(offset)
	for name, body := range map[string][]byte{"enrollment.json": raw, "app-x64.exe": []byte("new-app"), "collector-x64.exe": []byte("new-collector"), "setup-x64.exe": []byte("setup")} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return path, local, data, raw, &fakePlatform{data: data, local: local, runningNames: map[string]bool{}}
}
func file(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}
func installed(t *testing.T, local, data string) {
	t.Helper()
	file(t, filepath.Join(local, "TClash Preview", "tclash-preview.exe"), []byte("old-app"))
	file(t, filepath.Join(local, "TClash Preview", "tclash-collector.exe"), []byte("collector"))
	file(t, filepath.Join(data, "TClash Preview", "collector", "tracker.json"), []byte(`{"role":"b"}`))
	file(t, filepath.Join(data, "TClash Preview", "proxy-config.yml"), []byte("unchanged"))
}
func TestUpdateUnpairedAndPreserveData(t *testing.T) {
	bundle, local, data, _, p := fixture(t)
	installed(t, local, data)
	if err := install(bundle, local, data, "x64", p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(local, "TClash Preview", "tclash-preview.exe"))
	if string(b) != "new-app" {
		t.Fatal("app not updated")
	}
	backups, _ := filepath.Glob(filepath.Join(local, "TClash Preview", "tclash-preview-backup-*.exe"))
	if len(backups) != 1 {
		t.Fatalf("backup count: %d", len(backups))
	}
	b, _ = os.ReadFile(backups[0])
	if string(b) != "old-app" {
		t.Fatal("backup mismatch")
	}
	backups, _ = filepath.Glob(filepath.Join(local, "TClash Preview", "tclash-collector-backup-*.exe"))
	if len(backups) != 1 {
		t.Fatal("old collector was not backed up")
	}
	b, _ = os.ReadFile(backups[0])
	if string(b) != "collector" {
		t.Fatal("old collector backup mismatch")
	}
	b, _ = os.ReadFile(filepath.Join(data, "TClash Preview", "proxy-config.yml"))
	if string(b) != "unchanged" {
		t.Fatal("proxy config changed")
	}
	c, ok, err := readTracker(filepath.Join(data, "TClash Preview", "collector", "tracker.json"))
	if err != nil || !ok || c.Name != "Windows 1" {
		t.Fatal("authorization not applied")
	}
	if p.installerRuns != 0 || p.starts != 1 {
		t.Fatal("wrong update flow")
	}
}
func TestFreshInstall(t *testing.T) {
	bundle, local, data, _, p := fixture(t)
	if err := install(bundle, local, data, "x64", p); err != nil {
		t.Fatal(err)
	}
	if p.installerRuns != 1 || p.starts != 1 || p.handlerBackups != 1 {
		t.Fatal("fresh installation incomplete")
	}
}
func TestAlreadyPairedDoesNotReenroll(t *testing.T) {
	bundle, local, data, raw, p := fixture(t)
	installed(t, local, data)
	var d deployment
	_ = json.Unmarshal(raw, &d)
	b, _ := json.Marshal(tracker{Role: "b", Name: d.Name, Repo: d.Pair.Repo, Pair: d.Pair, Token: d.Token})
	path := filepath.Join(data, "TClash Preview", "collector", "tracker.json")
	file(t, path, b)
	if err := install(bundle, local, data, "x64", p); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(b) {
		t.Fatal("identity reset on reinstall")
	}
}
func TestConflictsStopWithoutReplacingApp(t *testing.T) {
	for _, kind := range []string{"other-device", "pending", "controller", "legacy-marker", "cancel", "collector"} {
		t.Run(kind, func(t *testing.T) {
			bundle, local, data, _, p := fixture(t)
			installed(t, local, data)
			switch kind {
			case "other-device":
				file(t, filepath.Join(data, "TClash Preview", "collector", "tracker.json"), []byte(`{"role":"b","name":"Another","repo":"other"}`))
			case "pending":
				file(t, filepath.Join(data, "TClash Preview", "collector", "install-enrollment.json"), []byte(`{"other":true}`))
			case "controller":
				file(t, filepath.Join(data, "Codex Usage Tracker", "tracker.json"), []byte(`{"role":"a"}`))
			case "legacy-marker":
				file(t, filepath.Join(data, "TClash Preview", "collector", "use-legacy-tracker"), []byte("old"))
			case "cancel":
				p.runningNames["tclash-preview.exe"] = true
				p.cancel = true
			case "collector":
				p.failCollector = true
			}
			if err := install(bundle, local, data, "x64", p); err == nil {
				t.Fatal("expected safe failure")
			}
			b, _ := os.ReadFile(filepath.Join(local, "TClash Preview", "tclash-preview.exe"))
			if string(b) != "old-app" {
				t.Fatal("existing app changed on failure")
			}
			if p.starts != 0 {
				t.Fatal("started on failure")
			}
		})
	}
}
func TestLegacyStartupDisabledOnlyAfterIdentityChecks(t *testing.T) {
	bundle, local, data, _, p := fixture(t)
	installed(t, local, data)
	file(t, filepath.Join(data, "Codex Usage Tracker", "tracker.json"), []byte(`{"role":"b"}`))
	file(t, filepath.Join(data, "TClash Preview", "collector", "install-enrollment.json"), []byte(`{"other":true}`))
	if err := install(bundle, local, data, "x64", p); err == nil || p.disables != 0 {
		t.Fatal("legacy startup changed before conflict checks")
	}
	_ = os.Remove(filepath.Join(data, "TClash Preview", "collector", "install-enrollment.json"))
	if err := install(bundle, local, data, "x64", p); err != nil {
		t.Fatal(err)
	}
	if p.disables != 1 {
		t.Fatal("old startup not disabled")
	}
}

func TestLegacyStartupKeptIfNewInstallFails(t *testing.T) {
	bundle, local, data, _, p := fixture(t)
	installed(t, local, data)
	file(t, filepath.Join(data, "Codex Usage Tracker", "tracker.json"), []byte(`{"role":"b"}`))
	p.failCollector = true
	if err := install(bundle, local, data, "x64", p); err == nil {
		t.Fatal("expected installation failure")
	}
	if p.disables != 0 {
		t.Fatal("old startup was disabled despite installation failure")
	}
}
func TestBadPackageAndArchitecture(t *testing.T) {
	bundle, local, data, _, p := fixture(t)
	if err := install(bundle, local, data, "x86", p); err == nil {
		t.Fatal("unsupported system accepted")
	}
	if err := install(strings.TrimSuffix(bundle, ".exe"), local, data, "x64", p); err == nil {
		t.Fatal("missing package accepted")
	}
}

func TestRealWindowsDeliveryArchive(t *testing.T) {
	path := os.Getenv("TCLASH_TEST_WINDOWS_PACKAGE")
	if path == "" {
		t.Skip("real delivery archive not requested")
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	for _, name := range []string{"enrollment.json", "setup-x64.exe", "setup-arm64.exe", "app-x64.exe", "app-arm64.exe", "collector-x64.exe", "collector-arm64.exe"} {
		f, err := z.Open(name)
		if err != nil {
			t.Fatalf("missing %s", name)
		}
		if name == "enrollment.json" {
			var d deployment
			if err := json.NewDecoder(f).Decode(&d); err != nil || d.Name != "Windows 1" || !d.StartFresh || len(d.Secret) != 32 {
				t.Fatal("incorrect Windows 1 authorization")
			}
		}
		_ = f.Close()
	}
}
