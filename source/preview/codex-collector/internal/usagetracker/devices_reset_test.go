package usagetracker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeviceAliasesKeepIdentityAndUsage(t *testing.T) {
	a, err := New(t.TempDir(), "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(t.TempDir(), "b")
	if err != nil {
		t.Fatal(err)
	}
	bCfg := b.config()
	bCfg.Name = "Windows 1"
	device := bCfg.device()
	cfg := a.config()
	cfg.Devices = map[string]Device{device.ID: device}
	if err := a.save(cfg); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	a.local = []Day{{Device: "local", Rows: []Request{{At: now, Model: "m", Session: "s", Tokens: Tokens{Input: 10}}}}}
	a.remote = []Day{{Device: device.ID, Name: "Windows 1", Rows: []Request{{At: now, Model: "m", Session: "s", Tokens: Tokens{Input: 20}}}}}
	if err := a.renameDevice("local", "书房"); err != nil {
		t.Fatal(err)
	}
	if err := a.renameDevice(device.ID, "书房"); err != nil {
		t.Fatal(err)
	}
	v := a.view(url.Values{"account": {"all"}})
	if len(v.Cards) != 1+len(deploymentDevices) || len(v.Paired) != 1 || v.Paired[0].ID != device.ID || v.Paired[0].Name != "Windows 1" {
		t.Fatal("device slot or identity changed during rename")
	}
	seen := map[string]int64{}
	for _, card := range v.Cards {
		seen[card.Name] = card.Total.Input
	}
	if seen["书房 · Mac 1（本机）"] != 10 || seen["书房 · Windows 1"] != 20 {
		t.Fatalf("names or original usage changed: %+v", seen)
	}
	detail := a.view(url.Values{"account": {"all"}, "device": {device.ID}})
	if len(detail.Rows) != 1 || detail.Rows[0].DeviceName != "书房 · Windows 1" {
		t.Fatal("device detail does not use its display name")
	}
	restarted, err := New(a.dir, "a")
	if err != nil || restarted.config().DeviceAliases[device.ID] != "书房" || restarted.config().DeviceAliases["local"] != "书房" {
		t.Fatal("device names did not survive a restart")
	}
	if err := a.renameDevice("unknown", "bad"); err == nil {
		t.Fatal("an unknown device was renamed")
	}
	if err := a.renameDevice(device.ID, strings.Repeat("x", 130)); err == nil {
		t.Fatal("an overlong name was accepted")
	}
	if err := a.renameDevice(device.ID, ""); err != nil || a.config().DeviceAliases[device.ID] != "" {
		t.Fatal("default device name could not be restored")
	}
}

func TestNewEnrollmentStartsFromFirstConnection(t *testing.T) {
	owner, err := New(t.TempDir(), "a")
	if err != nil {
		t.Fatal(err)
	}
	cfg := owner.config()
	cfg.Repo = "fixture/private"
	pair, err := cfg.pair()
	if err != nil {
		t.Fatal(err)
	}
	inv, err := newInvitation("Windows 1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(deployment{Pair: pair, Name: "Windows 1", Token: "fixture-token", Secret: inv.Secret, StartFresh: true})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"activity-history.json", "traffic-ledger.json", "shared-telemetry.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(t.TempDir(), "enrollment.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	b, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.InstallDeployment(file); err != nil {
		t.Fatal(err)
	}
	since := b.config().CountSince
	if since.IsZero() {
		t.Fatal("new enrollment did not record its start time")
	}
	for _, name := range []string{"activity-history.json", "traffic-ledger.json", "shared-telemetry.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("old collector history remained: %s", name)
		}
	}
	b.account = func() string { return "account" }
	b.collect = func(context.Context, string, string, []Quota, []AccountCorrection) ([]Day, error) {
		return []Day{{Device: b.config().device().ID, Rows: []Request{
			{At: since.Add(-time.Second), Tokens: Tokens{Input: 10}},
			{At: since, Tokens: Tokens{Input: 20}},
			{At: since.Add(time.Second), Tokens: Tokens{Input: 30}},
		}, Quotas: []Quota{{At: since.Add(-time.Second)}, {At: since}}},
			{Device: b.config().device().ID, Rows: []Request{{At: since.Add(-24 * time.Hour), Tokens: Tokens{Input: 9}}},
				Quotas: []Quota{{At: since.Add(-24 * time.Hour)}}}}, nil
	}
	if err := b.refreshLocal(t.Context(), b.config()); err != nil {
		t.Fatal(err)
	}
	if len(b.local) != 1 || len(b.local[0].Rows) != 2 || b.local[0].Rows[0].Input != 20 || b.local[0].Rows[1].Input != 30 ||
		len(b.local[0].Quotas) != 1 || !b.local[0].Quotas[0].At.Equal(since) || !b.local[0].CountSince.Equal(since) {
		t.Fatal("usage outside the new start boundary was included")
	}
	restarted, err := New(dir, "")
	if err != nil || !restarted.config().CountSince.Equal(since) {
		t.Fatal("new start boundary did not survive restart")
	}
	if err := restarted.InstallDeployment(file); err != nil || !restarted.config().CountSince.Equal(since) {
		t.Fatal("reinstall unexpectedly reset the start boundary")
	}
	var different deployment
	if err := json.Unmarshal(data, &different); err != nil {
		t.Fatal(err)
	}
	different.Token = "different-token"
	data, _ = json.Marshal(different)
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := restarted.InstallDeployment(file); err == nil {
		t.Fatal("new authorization silently reused an old device connection")
	}
}

func TestResetRemoteDevicesKeepsOnlyController(t *testing.T) {
	a, err := New(t.TempDir(), "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(t.TempDir(), "b")
	if err != nil {
		t.Fatal(err)
	}
	bCfg := b.config()
	bCfg.Name = "Windows 1"
	device := bCfg.device()
	inv, err := newInvitation("Mac 2")
	if err != nil {
		t.Fatal(err)
	}
	cfg := a.config()
	cfg.Repo, cfg.Token = "fixture/private", "fixture-a-token"
	cfg.Devices = map[string]Device{device.ID: device}
	cfg.Invites = map[string]invitation{invitationID(inv.Secret): inv}
	cfg.DeviceAliases = map[string]string{"local": "书房 Mac", device.ID: "旧 Windows"}
	cfg.DeploymentTokenHashes = map[string]string{"Windows 1": "fixture-hash"}
	cfg.DeploymentDir = t.TempDir()
	cfg.Policy = Policy{Version: 7, SyncMinutes: 1, Periods: []PeriodRule{{Factor: 1.5, Budget: 900, Devices: map[string]float64{device.ID: 2}}}}
	if err := a.save(cfg); err != nil {
		t.Fatal(err)
	}
	oldToken, oldIdentity := cfg.Token, cfg.Identity
	oldPath := filepath.Join(cfg.DeploymentDir, "Windows 1.zip")
	if err := os.WriteFile(oldPath, []byte("old-device-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	keepPath := filepath.Join(cfg.DeploymentDir, "unrelated.txt")
	if err := os.WriteFile(keepPath, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	received := a.cachePath(device.ID, "2026-11-13")
	if err := writePrivate(received, []byte("encrypted old device")); err != nil {
		t.Fatal(err)
	}
	orphan := strings.Repeat("a", 32)
	claim := invitationID(inv.Secret) + "-" + device.ID + daySuffix
	repo := &memoryRepo{files: map[string][]byte{
		device.ID + "/2026/2026-11-13" + daySuffix: []byte("old"),
		orphan + "/2025/2025-12-31" + daySuffix:    []byte("orphan"),
		enrollments + "/" + claim:                  []byte("old enrollment"),
		controls + "/" + policyFile:                []byte("preserve"),
		"unrelated/file":                           []byte("preserve"),
	}}
	a.openRepo = func(context.Context, string, string) (transport, error) { return repo, nil }
	a.local = []Day{{Device: "local", Rows: []Request{{Tokens: Tokens{Input: 123}}}}}
	a.remote = []Day{{Device: device.ID, Rows: []Request{{Tokens: Tokens{Input: 99}}}}}
	repo.fail = true
	if err := a.resetRemoteDevices(t.Context()); err == nil || len(a.config().Devices) != 1 {
		t.Fatal("failed cloud access changed local device identity")
	}
	repo.fail = false
	if err := a.resetRemoteDevices(t.Context()); err != nil {
		t.Fatal(err)
	}
	result := a.config()
	if len(result.Devices) != 0 || len(result.Invites) != 0 || len(result.DeploymentTokenHashes) != 0 ||
		result.Token != oldToken || result.Identity != oldIdentity || result.DeviceAliases["local"] != "书房 Mac" ||
		result.Policy.SyncMinutes != 1 || result.Policy.Periods[0].Factor != 1.5 || result.Policy.Periods[0].Budget != 900 ||
		len(result.Policy.Periods[0].Devices) != 0 || len(a.local) != 1 || len(a.remote) != 0 {
		t.Fatal("reset changed controller identity, local usage or settings")
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatal("old credential-bearing package remains")
	}
	if _, err := os.Stat(received); !os.IsNotExist(err) {
		t.Fatal("old remote cache remains")
	}
	if body, err := os.ReadFile(keepPath); err != nil || string(body) != "keep" {
		t.Fatal("unrelated local file was changed")
	}
	if len(repo.files) != 2 || !bytes.Equal(repo.files[controls+"/"+policyFile], []byte("preserve")) ||
		!bytes.Equal(repo.files["unrelated/file"], []byte("preserve")) {
		t.Fatal("remote cleanup missed old devices or changed unrelated files")
	}
}
