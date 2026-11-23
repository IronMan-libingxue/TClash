package usagetracker

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUnifiedAFrequencyReachesOnlySimulatedCollectors(t *testing.T) {
	root := t.TempDir()
	demo, err := New(filepath.Join(root, "a"), "a")
	if err != nil {
		t.Fatal(err)
	}
	demoCfg := demo.config()
	demoCfg.Repo = "local/preview"
	demoCfg.Policy = Policy{Version: 2, SyncMinutes: 5, Periods: []PeriodRule{{
		Account: "simulated-account", End: time.Now().Add(time.Hour), Factor: 1.5, Budget: 900,
	}}}
	if err := demo.save(demoCfg); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(demoCfg.Policy)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "relay", controls, policyFile)
	if err := writePrivate(path, sign(policyFormat, body, demoCfg.Signer)); err != nil {
		t.Fatal(err)
	}
	main, err := New(t.TempDir(), "a")
	if err != nil {
		t.Fatal(err)
	}
	mainCfg := main.config()
	mainCfg.SimulationRoot = root
	mainCfg.Policy.Periods = []PeriodRule{{Account: "real-account-private", End: time.Now().Add(time.Hour), Factor: 2, Budget: 1200}}
	if err := main.save(mainCfg); err != nil {
		t.Fatal(err)
	}
	if err := main.setFrequency(1); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body, err = verify(policyFormat, updated, demoCfg.Signer.Public().(ed25519.PublicKey))
	if err != nil || bytes.Contains(body, []byte("real-account-private")) {
		t.Fatal("demo policy did not verify or included actual account data")
	}
	collector := config{Pair: Pair{Verifier: demoCfg.Signer.Public().(ed25519.PublicKey)}, Policy: demoCfg.Policy}
	repo := &memoryRepo{files: map[string][]byte{controls + "/" + policyFile: updated}}
	if err := receivePolicy(t.Context(), repo, &collector); err != nil {
		t.Fatal(err)
	}
	if collector.Policy.Version != 3 || collector.Policy.SyncMinutes != 1 ||
		len(collector.Policy.Periods) != 1 || collector.Policy.Periods[0].Account != "simulated-account" {
		t.Fatal("simulated collector did not receive its own signed frequency")
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := main.setFrequency(5); err == nil {
		t.Fatal("corrupt simulated control was silently overwritten")
	}
	remaining, err := os.ReadFile(path)
	if err != nil || string(remaining) != "corrupt" {
		t.Fatal("invalid simulated control was overwritten")
	}
}

func TestSimulatedDevicesStaySeparateFromRealTotals(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	q := Quota{Account: "real-account", Start: now.Add(-time.Hour), End: now.Add(7 * 24 * time.Hour),
		At: now, Used: 73}
	local := Day{Device: "local", Name: "Mac 1（本机）", At: now, Quotas: []Quota{q}, Rows: []Request{
		{ID: "real-local", Model: "real-model", Effort: "high", Session: "local-session",
			At: now, Account: q.Account, Tokens: Tokens{Input: 10, Output: 5, Cache: 20}, USD: 4, Priced: true},
	}}
	remote := Day{Device: "real-device", Name: "Mac 2", At: now, Rows: []Request{
		{ID: "real-remote", Model: "remote-model", Session: "real-session",
			At: now, Account: q.Account, Tokens: Tokens{Input: 7, Output: 3, Cache: 10}, USD: 2, Priced: true},
	}}
	testDays := []Day{
		{Device: "simulation-old-mac2", Name: "Mac 2", At: now, Rows: []Request{
			{ID: "superseded", Model: "mock", At: now, Account: "simulated-account", Tokens: Tokens{Input: 999}, USD: 999, Priced: true},
		}},
		{Device: "simulation-mac3", Name: "Mac 3", At: now, Rows: []Request{
			{ID: "mock-1", Model: "mock-model", Effort: "high", Session: "demo-session",
				At: now.AddDate(0, 0, -10), Account: "simulated-account", Tokens: Tokens{Input: 100, Output: 30, Cache: 70}, USD: 10, Priced: true},
			{ID: "mock-2", Model: "mock-model", Session: "demo-session",
				At: now.AddDate(0, 0, -10), Account: "simulated-account", Tokens: Tokens{Input: 10, Output: 5, Cache: 5}, USD: 2, Priced: true},
		}},
	}
	devices := []choice{{"simulation-old-mac2", "Mac 2"}, {"simulation-mac3", "Mac 3"}}
	cfg := config{Role: "a", ShowSimulation: true, SimulationRoot: "fixture", Devices: map[string]Device{
		"real-device": {ID: "real-device", Name: "Mac 2"},
	}}
	a := &App{cfg: cfg, local: []Day{local}, remote: []Day{remote}, simulated: testDays, simulationDevices: devices, localAccount: q.Account}
	v := a.view(nil)
	if v.Count != 2 || v.Total.total(true) != 55 || v.Total.USD != 6 ||
		v.Total.EstimateUSD != 6 || v.Quota == nil || v.Quota.Used != 73 {
		t.Fatalf("simulated usage changed real totals or official reading: %+v", v.Total)
	}
	if len(v.Cards) != 1+len(deploymentDevices) {
		t.Fatalf("expected local and all preset cards, got %d", len(v.Cards))
	}
	var sim *deviceCard
	for i := range v.Cards {
		card := &v.Cards[i]
		if card.Name == "Mac 2" && (card.Simulated || card.Total.total(true) != 20) {
			t.Fatal("simulation replaced a connected real device")
		}
		if card.Name == "Mac 3" {
			sim = card
		}
	}
	if sim == nil || !sim.Simulated || sim.Total.total(true) != 220 || sim.Total.EstimateUSD != 12 {
		t.Fatalf("simulation card missing or incorrect: %+v", sim)
	}
	filter := url.Values{"device": {"simulation-mac3"}, "model": {"mock-model"}, "window": {q.id()}}
	detail := a.view(filter)
	if !detail.SimulatedDevice || detail.Count != 2 || detail.Total.total(true) != 220 || len(detail.Efforts) != 2 {
		t.Fatal("simulation detail did not retain model and reasoning breakdown")
	}
	for _, row := range detail.Rows {
		if !row.Simulated || row.Account != "" || row.DeviceName != "Mac 3（模拟）" {
			t.Fatal("a simulated request was shown as an official request")
		}
	}
	cfg.ShowSimulation = false
	a.cfg = cfg
	hidden := a.view(nil)
	if hidden.Count != 2 || hidden.Total.total(true) != 55 || len(hidden.Cards) != 1+len(deploymentDevices) {
		t.Fatal("hiding simulation changed real data or missing-device placeholders")
	}
	for _, card := range hidden.Cards {
		if card.Simulated {
			t.Fatal("simulation remained visible after it was disabled")
		}
	}
}

func TestSimulationReadsOnlyAuthenticatedMockData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	root := t.TempDir()
	owner, err := New(filepath.Join(root, "a"), "a")
	if err != nil {
		t.Fatal(err)
	}
	cfg := owner.config()
	cfg.Repo = "local/preview"
	cfg.Devices = map[string]Device{}
	var first Device
	var firstSigner ed25519.PrivateKey
	var firstPath string
	for _, target := range deploymentDevices {
		b, err := New(t.TempDir(), "b")
		if err != nil {
			t.Fatal(err)
		}
		bcfg := b.config()
		bcfg.Name = target.Name
		device := bcfg.device()
		cfg.Devices[device.ID] = device
		if first.ID == "" {
			first, firstSigner = device, bcfg.Signer
		}
		date := now.Format(time.DateOnly)
		day := Day{Device: device.ID, Name: device.Name, Date: date, At: now,
			Quotas: []Quota{{Account: "simulated-account", Plan: "simulated"}},
			Rows:   []Request{{ID: "mock", Model: "mock", At: now, Account: "simulated-account"}}}
		data, err := sealDay(day, cfg.pairRecipient(t), bcfg.Signer)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "relay", device.ID, date[:4], date+daySuffix)
		if device.ID == first.ID {
			firstPath = path
		}
		if err := writePrivate(path, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := owner.save(cfg); err != nil {
		t.Fatal(err)
	}
	days, devices, err := readSimulation(root, now)
	if err != nil || len(days) != len(deploymentDevices) || len(devices) != len(deploymentDevices) {
		t.Fatalf("could not read isolated test data: %v", err)
	}
	if len(days[0].Quotas) != 0 || days[0].Device[:len(simulationPrefix)] != simulationPrefix {
		t.Fatal("simulated official quota was retained")
	}
	oldCfg := cfg
	oldCfg.Devices = map[string]Device{}
	for id, device := range cfg.Devices {
		if device.Name != "Mac 5" && device.Name != "Mac 6" && device.Name != "Windows 3" {
			oldCfg.Devices[id] = device
		}
	}
	if err := owner.save(oldCfg); err != nil {
		t.Fatal(err)
	}
	if _, oldDevices, err := readSimulation(root, now); err != nil || len(oldDevices) != 5 {
		t.Fatal("the existing five-device simulation is no longer readable")
	}
	if err := owner.save(cfg); err != nil {
		t.Fatal(err)
	}
	real := Day{Device: first.ID, Name: first.Name, Date: now.Format(time.DateOnly), At: now,
		Rows: []Request{{ID: "not-a-demo", Model: "real", At: now, Account: "real-account"}}}
	data, err := sealDay(real, cfg.pairRecipient(t), firstSigner)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(firstPath, data); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readSimulation(root, now); err == nil {
		t.Fatal("non-demo usage was accepted into the simulation")
	}

	var corrupt string
	err = filepath.WalkDir(filepath.Join(root, "relay"), func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && corrupt == "" {
			corrupt = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corrupt, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readSimulation(root, now); err == nil {
		t.Fatal("unsigned simulation data was accepted")
	}
}

func (c config) pairRecipient(t *testing.T) string {
	t.Helper()
	pair, err := c.pair()
	if err != nil {
		t.Fatal(err)
	}
	return pair.Recipient
}
