package usagetracker

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/sessions"
)

func TestEncryptedTelemetrySyncKeepsUsageAndHeartbeat(t *testing.T) {
	repo := &memoryRepo{files: map[string][]byte{}}
	open := func(context.Context, string, string) (transport, error) { return repo, nil }
	a, err := New(t.TempDir(), "a")
	if err != nil {
		t.Fatal(err)
	}
	a.openRepo = open
	if err = a.configure(t.Context(), setup{Repo: "fixture/private", Token: "fixture"}); err != nil {
		t.Fatal(err)
	}
	pair, err := a.config().pair()
	if err != nil {
		t.Fatal(err)
	}
	pairCode, err := code(pairPrefix, pair)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(t.TempDir(), "b")
	if err != nil {
		t.Fatal(err)
	}
	b.openRepo = open
	if err = b.configure(t.Context(), setup{Pair: pairCode, Name: "fixture-device", Token: "fixture"}); err != nil {
		t.Fatal(err)
	}
	deviceCode, err := code(devicePrefix, b.config().device())
	if err != nil {
		t.Fatal(err)
	}
	if err = a.register(deviceCode); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	quota := Quota{Account: "fixture-account", Plan: "pro", At: now, Start: now.Add(-time.Hour), End: now.Add(time.Hour), Used: 10}
	b.local = []Day{{Device: b.config().device().ID, Name: "fixture-device", Date: now.Format(time.DateOnly), At: now,
		Rows: []Request{{ID: "one", At: now, Session: "child", FastOwner: "main", Tier: "fast", Model: "sample", Account: quota.Account, Tokens: Tokens{Input: 100, Output: 7, Cache: 20}}}}}
	a.local = []Day{{Device: "local", Quotas: []Quota{quota}}}
	a.localAccount = quota.Account
	b.traffic.started = now.Add(-time.Second)
	if err = b.RecordTraffic(TrafficSnapshot{At: now, Connections: []TrafficConnection{{ID: "connection-secret", Start: now, Download: 100, Groups: []string{"private-group"}}}}); err != nil {
		t.Fatal(err)
	}
	b.telemetryMu.Lock()
	b.telemetry.Activity = &Activity{At: now, Active: 2, Fast: 1, Incomplete: true}
	b.telemetryMu.Unlock()
	if err = b.syncNow(t.Context(), b.config()); err != nil {
		t.Fatal(err)
	}
	for _, blob := range repo.files {
		if bytes.Contains(blob, []byte("private-group")) || bytes.Contains(blob, []byte("connection-secret")) {
			t.Fatal("private telemetry appeared in plaintext")
		}
	}
	if err = a.syncNow(t.Context(), a.config()); err != nil {
		t.Fatal(err)
	}
	result := a.view(url.Values{"device": {"all"}})
	var card *deviceCard
	for i := range result.Cards {
		if result.Cards[i].ID == b.config().device().ID {
			card = &result.Cards[i]
		}
	}
	if card == nil || card.Telemetry == nil || card.Telemetry.Traffic == nil || card.Telemetry.Activity == nil || card.Telemetry.Activity.Active != 2 || card.Telemetry.Traffic.Download != 100 || card.Total.Input != 100 {
		t.Fatalf("A did not receive the device snapshot: %+v", card)
	}
	if card.FastTotal.Input != 100 || card.FastTotal.Output != 7 || card.FastTotal.Cache != 20 || len(card.FastModels) != 1 || len(card.FastSessions) != 1 || card.FastSessions[0].ID != "main" {
		t.Fatalf("Fast use was not attributed to the main session: %+v", card)
	}
	b.telemetryMu.Lock()
	b.telemetry.Activity.At = now.Add(time.Second)
	b.telemetry.Activity.Active = 1
	b.telemetryMu.Unlock()
	if err = b.syncNow(t.Context(), b.config()); err != nil {
		t.Fatal(err)
	}
	if err = a.syncNow(t.Context(), a.config()); err != nil {
		t.Fatal(err)
	}
	result = a.view(url.Values{"device": {"all"}})
	for _, card := range result.Cards {
		if card.ID == b.config().device().ID && (card.Total.Input != 100 || card.Telemetry == nil || card.Telemetry.Activity.Active != 1) {
			t.Fatalf("heartbeat duplicated usage or failed to update: %+v", card)
		}
	}
}

func TestActivityScanFollowsIndependentSessionEvents(t *testing.T) {
	t0 := time.Now().UTC().Truncate(time.Second)
	m := newActivityMonitor(t0)
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "one.jsonl"), filepath.Join(dir, "two.jsonl")}
	ids := map[string]sessions.CodexIdentity{"one": {ID: "one", Role: "main", RootSession: "one"}, "two": {ID: "two", Role: "main", RootSession: "two"}}
	for _, path := range paths {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sources := func(now time.Time) []sessions.CallSource {
		out := make([]sessions.CallSource, 0, len(paths))
		for i, path := range paths {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, sessions.CallSource{Path: path, Key: []string{"codex:one", "codex:two"}[i], Agent: "codex", Size: info.Size(), Modified: now})
		}
		return out
	}
	m.scan(sources(t0), ids, t0)
	for i, path := range paths {
		at := t0.Add(time.Duration(i+1) * time.Second).Format(time.RFC3339Nano)
		data := fmt.Sprintf("{\"timestamp\":%q,\"type\":\"event_msg\",\"payload\":{\"type\":\"thread_settings_applied\",\"thread_settings\":{\"service_tier\":\"priority\"}}}\n{\"timestamp\":%q,\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\",\"turn_id\":\"turn\"}}\n", at, at)
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result := m.scan(sources(t0.Add(3*time.Second)), ids, t0.Add(3*time.Second))
	if result.Active != 2 || result.Fast != 2 {
		t.Fatalf("independent active Fast sessions not detected: %+v", result)
	}
	f, err := os.OpenFile(paths[1], os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	at := t0.Add(4 * time.Second).Format(time.RFC3339Nano)
	_, err = fmt.Fprintf(f, "{\"timestamp\":%q,\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\",\"turn_id\":\"turn\"}}\n", at)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	result = m.scan(sources(t0.Add(5*time.Second)), ids, t0.Add(5*time.Second))
	if result.Active != 1 || result.Fast != 1 {
		t.Fatalf("completed session remained active: %+v", result)
	}
}

func TestLoggedFastTierKeepsSettingsAndUnknownSeparate(t *testing.T) {
	input := strings.Join([]string{
		`{"timestamp":"2026-11-13T00:00:00Z","type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"service_tier":"priority"}}}`,
		`{"timestamp":"2026-11-13T00:01:00Z","type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"service_tier":"default"}}}`,
		`{"timestamp":"2026-11-13T00:02:00Z","type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"service_tier":null}}}`,
		"",
	}, "\n")
	_, tiers, err := readQuotaLines(strings.NewReader(input), false)
	if err != nil || len(tiers) != 3 || tiers[0].Tier != "fast" || tiers[1].Tier != "normal" || tiers[2].Tier != "unknown" {
		t.Fatalf("incorrect tier timeline: %+v %v", tiers, err)
	}
}

func TestTrafficCountsEachObservedGroupAndDoesNotReplay(t *testing.T) {
	start := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	l := newTrafficLedger(t.TempDir())
	l.started = start
	old := TrafficConnection{ID: "prior", Start: start.Add(-time.Hour), Download: 100, Groups: []string{"A"}}
	if _, err := l.observe(TrafficSnapshot{At: start.Add(time.Second), Connections: []TrafficConnection{old}}); err != nil {
		t.Fatal(err)
	}
	flow := TrafficConnection{ID: "private-id", Start: start.Add(2 * time.Second), Download: 100, Groups: []string{"A", "B"}}
	result, err := l.observe(TrafficSnapshot{At: start.Add(3 * time.Second), Connections: []TrafficConnection{old, flow}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Download != 200 || len(result.Groups) != 2 || result.Groups[0].Download != 100 || result.Groups[1].Download != 100 || !result.Incomplete {
		t.Fatalf("unexpected group totals: %+v", result)
	}
	result, err = l.observe(TrafficSnapshot{At: start.Add(4 * time.Second), Connections: []TrafficConnection{old, flow}})
	if err != nil || result.Download != 200 {
		t.Fatalf("replayed snapshot: %+v %v", result, err)
	}
	data, err := os.ReadFile(filepath.Clean(l.path))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-id") {
		t.Fatal("connection identifiers must not persist")
	}
	if mode, err := os.Stat(l.path); err != nil || mode.Mode().Perm()&0o077 != 0 {
		t.Fatalf("ledger permissions: %v %v", mode, err)
	}
}

func TestTrafficSeparatesSameNameAcrossProfiles(t *testing.T) {
	t0 := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	l := newTrafficLedger(t.TempDir())
	l.started = t0
	first := TrafficConnection{ID: "connection", Start: t0.Add(time.Second), Download: 100, Groups: []string{"same name"}}
	if _, err := l.observe(TrafficSnapshot{At: t0.Add(2 * time.Second), Profile: "first", Connections: []TrafficConnection{first}}); err != nil {
		t.Fatal(err)
	}
	first.Download = 150
	if _, err := l.observe(TrafficSnapshot{At: t0.Add(3 * time.Second), Profile: "second", Connections: []TrafficConnection{first}}); err != nil {
		t.Fatal(err)
	}
	first.Download = 170
	result, err := l.observe(TrafficSnapshot{At: t0.Add(4 * time.Second), Profile: "second", Connections: []TrafficConnection{first}})
	if err != nil || result.Download != 120 || len(result.Groups) != 2 || result.Groups[0].Scope == result.Groups[1].Scope {
		t.Fatalf("profile switch merged groups or reused old bytes: %+v, %v", result, err)
	}
}

func TestConcurrentIntervalsExcludeSubagentsAndSplitAtReset(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	m := newActivityMonitor(t0)
	m.identities = map[string]sessions.CodexIdentity{
		"a":     {ID: "a", Role: "main", RootSession: "a"},
		"b":     {ID: "b", Role: "main", RootSession: "b"},
		"c":     {ID: "c", Role: "main", RootSession: "c"},
		"child": {ID: "child", Role: "subagent", RootSession: "a"},
	}
	apply := func(seconds int, id, kind, tier string) {
		m.apply(activityEvent{At: t0.Add(time.Duration(seconds) * time.Second), Session: id, Kind: kind, Tier: tier}, true)
	}
	apply(1, "a", "task_started", "")
	apply(2, "b", "task_started", "")
	apply(3, "child", "task_started", "")
	apply(4, "child", "thread_settings_applied", "fast")
	active, fast, _, _ := m.counts(t0.Add(4 * time.Second))
	if active != 2 || fast != 1 {
		t.Fatalf("subagent counted as independent or Fast lost: active=%d fast=%d", active, fast)
	}
	apply(5, "c", "task_started", "")
	apply(7, "c", "task_complete", "")
	apply(12, "b", "task_complete", "")
	activity := &Activity{Intervals: m.intervals, Incomplete: true}
	q := &Quota{Start: t0, End: t0.Add(time.Minute)}
	stats := activityStats(activity, q)
	if stats.Occurrences != 1 || stats.Seconds != 10 || stats.Peak != 3 {
		t.Fatalf("unexpected concurrency stats: %+v", stats)
	}
	q2 := &Quota{Start: t0.Add(6 * time.Second), End: t0.Add(time.Minute)}
	stats = activityStats(activity, q2)
	if stats.Occurrences != 0 || stats.Seconds != 6 || stats.Peak != 3 {
		t.Fatalf("incorrect reset split: %+v", stats)
	}
}

func TestSingleSessionPeakDoesNotCountAsConcurrency(t *testing.T) {
	t0 := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	m := newActivityMonitor(t0)
	m.identities = map[string]sessions.CodexIdentity{
		"a": {ID: "a", Role: "main", RootSession: "a"},
	}
	m.apply(activityEvent{At: t0.Add(time.Second), Session: "a", Kind: "task_started"}, true)
	m.apply(activityEvent{At: t0.Add(11 * time.Second), Session: "a", Kind: "task_complete"}, true)
	stats := activityStats(&Activity{Intervals: m.intervals}, &Quota{Start: t0, End: t0.Add(time.Minute)})
	if stats.Occurrences != 0 || stats.Seconds != 0 || stats.Peak != 1 {
		t.Fatalf("single session should set peak without counting concurrency: %+v", stats)
	}
}

func TestActivityHistoryRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity-history.json")
	if _, writable := loadActivityHistory(path); !writable {
		t.Fatal("a missing history should be writable")
	}
	broken := []byte("{not valid json")
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, writable := loadActivityHistory(path); writable {
		t.Fatal("corrupt history must not be overwritten")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(broken) {
		t.Fatalf("history changed: %q, %v", got, err)
	}
}

func TestVisibleSpeedFollowsKnownActivity(t *testing.T) {
	now := time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)
	rate := 42.5
	reading := speedReading{Model: "fixture", Rate: &rate}
	for _, tc := range []struct {
		name     string
		activity *Activity
		state    string
		rate     *float64
	}{
		{"active", &Activity{At: now, Active: 1}, "active", &rate},
		{"active with other unknown sessions", &Activity{At: now, Active: 1, Uncertain: true}, "active", &rate},
		{"idle", &Activity{At: now}, "idle", new(float64)},
		{"uncertain", &Activity{At: now, Uncertain: true}, "unknown", nil},
		{"stale", &Activity{At: now.Add(-time.Minute)}, "unknown", nil},
		{"missing", nil, "unknown", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := visibleSpeed(reading, tc.activity, now)
			if got.State != tc.state || (got.Rate == nil) != (tc.rate == nil) || (got.Rate != nil && *got.Rate != *tc.rate) {
				t.Fatalf("unexpected speed: %+v", got)
			}
		})
	}
}

func TestLocalViewsUseActivityForSpeed(t *testing.T) {
	now := time.Now().UTC()
	rate := 31.25
	app := &App{
		cfg:       config{Role: "a"},
		speed:     speedReading{Model: "fixture", Rate: &rate},
		telemetry: Telemetry{Version: 1, Activity: &Activity{At: now}},
		localTelemetry: func() *Telemetry {
			return &Telemetry{Version: 1, Activity: &Activity{At: now.Add(-time.Minute), Active: 1}}
		},
	}
	got := app.view(nil)
	if got.Speed.State != "idle" || got.Speed.Rate == nil || *got.Speed.Rate != 0 {
		t.Fatalf("A did not use its fresh local activity: %+v", got.Speed)
	}
	app.telemetry.Activity.Active = 1
	got = app.view(nil)
	if got.Speed.State != "active" || got.Speed.Rate == nil || *got.Speed.Rate != rate {
		t.Fatalf("A did not show the active speed: %+v", got.Speed)
	}
	b, err := New(t.TempDir(), "b")
	if err != nil {
		t.Fatal(err)
	}
	b.speed = app.speed
	b.telemetry = Telemetry{Version: 1, Activity: &Activity{At: now}}
	got = b.view(nil)
	if got.Speed.State != "idle" || got.Speed.Rate == nil || *got.Speed.Rate != 0 {
		t.Fatalf("B did not show zero when idle: %+v", got.Speed)
	}
}
