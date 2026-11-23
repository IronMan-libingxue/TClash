package usagetracker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/yetone/magpie/internal/sessions"
	"github.com/yetone/magpie/internal/settings"
)

type memoryRepo struct {
	mu    sync.Mutex
	files map[string][]byte
	fail  bool
}

func (m *memoryRepo) List(_ context.Context, folder string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for path, blob := range m.files {
		if name, ok := strings.CutPrefix(path, folder+"/"); ok {
			out[name] = digest(string(blob))
		}
	}
	return out, nil
}

func (m *memoryRepo) Read(_ context.Context, folder, name string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return nil, errors.New("connection lost")
	}
	if data, ok := m.files[folder+"/"+name]; ok {
		return data, nil
	}
	return nil, fs.ErrNotExist
}

func (m *memoryRepo) Write(_ context.Context, folder, name string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("connection lost")
	}
	m.files[folder+"/"+name] = bytes.Clone(data)
	return nil
}

func TestPrivateSyncAndDisplayedUsage(t *testing.T) {
	ctx := t.Context()
	repo := &memoryRepo{files: map[string][]byte{}}
	open := func(context.Context, string, string) (transport, error) { return repo, nil }
	newApp := func(role string) *App {
		t.Helper()
		app, err := New(t.TempDir(), role)
		if err != nil {
			t.Fatal(err)
		}
		app.openRepo = open
		return app
	}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	a := newApp("a")
	check(a.configure(ctx, setup{Repo: "fixture/private", Token: "fixture"}))
	pair, err := a.config().pair()
	check(err)
	pairCode, err := code(pairPrefix, pair)
	check(err)

	now := time.Now().UTC().Truncate(time.Second)
	end := now.Add(2 * time.Hour)
	q := Quota{Account: "fixture-account", Plan: "pro", At: now,
		Start: end.Add(-7 * 24 * time.Hour), End: end, Used: 43}
	var bs []*App
	for _, name := range []string{"Home Mac", "Home Windows"} {
		b := newApp("b")
		check(b.configure(ctx, setup{Pair: pairCode, Name: name, Token: "fixture"}))
		id, err := code(devicePrefix, b.config().device())
		check(err)
		check(a.register(id))
		b.local = []Day{{Device: b.config().device().ID, Name: name, Date: now.Format(time.DateOnly), At: now,
			Rows: []Request{{ID: "request", At: now, Project: "project", ProjectName: "Sample Project",
				Session: "sample-session", Model: "test-model", Account: q.Account,
				Tokens: Tokens{Input: 100_000, Output: 100_000, Cache: 800_000}, USD: 90, Priced: true}},
			Quotas: []Quota{q}}}
		bs = append(bs, b)
	}

	// A connection failure keeps the collected usage pending.
	repo.fail = true
	if err := bs[0].syncNow(ctx, bs[0].config()); err == nil {
		t.Fatal("disconnected sync succeeded")
	}
	repo.fail = false

	var jobs sync.WaitGroup
	for _, b := range bs {
		jobs.Go(func() {
			if err := b.syncNow(ctx, b.config()); err != nil {
				t.Error(err)
			}
		})
	}
	jobs.Wait()
	if len(repo.files) != 2 {
		t.Fatalf("want one file per device, got %d", len(repo.files))
	}
	for _, b := range bs {
		day := b.local[0]
		blob := repo.files[day.Device+"/"+day.Date[:4]+"/"+day.Date+daySuffix]
		if bytes.Contains(blob, []byte("Sample Project")) {
			t.Fatal("repository contains plaintext")
		}
		wrong, err := age.GenerateX25519Identity()
		check(err)
		if _, err := openDay(blob, wrong.String(), b.config().device(), day.Date); err == nil {
			t.Fatal("an unrelated identity decrypted the usage")
		}
		copy := bytes.Clone(blob)
		copy[len(copy)-1] ^= 1
		if _, err := openDay(copy, a.config().Identity, b.config().device(), day.Date); err == nil {
			t.Fatal("tampered usage was accepted")
		}
	}
	check(a.syncNow(ctx, a.config()))
	check(a.syncNow(ctx, a.config()))
	a.localAccount = q.Account
	a.local = []Day{{Device: "local", Quotas: []Quota{q}}}
	f := url.Values{"device": {"all"}}
	result := a.view(f)
	if result.Count != 2 || result.Total.total(true) != 2_000_000 || result.Quota.Used != 43 {
		t.Fatalf("A must see both raw records and a single official reading: %+v", result.Total)
	}
	f.Set("device", bs[0].config().device().ID)
	f.Set("project", "project")
	f.Set("model", "test-model")
	f.Set("session", bs[0].config().device().ID+"/sample-session")
	result = a.view(f)
	if result.Count != 1 || result.Total.USD != 90 {
		t.Fatalf("A filters failed: %+v", result.Total)
	}
	b := bs[0]
	display := b.view(nil)
	row := display.DisplayModels[0]
	if row.Tokens != 1_500_000 || *row.USD != 135 || *row.Percent != 15 {
		t.Fatalf("unexpected B presentation: %+v", row)
	}
	if raw := b.view(url.Values{"cache": {"0"}}).DisplayModels[0]; raw.Tokens != 300_000 || *raw.USD != 135 {
		t.Fatalf("cache toggle must only affect token total: %+v", raw)
	}
	check(a.setRule(PeriodRule{Account: q.Account, End: end, Factor: 1.5, Budget: 1000,
		Devices: map[string]float64{b.config().device().ID: 2}}))
	check(a.syncNow(ctx, a.config()))
	check(b.syncNow(ctx, b.config()))
	row = b.view(nil).DisplayModels[0]
	if row.Tokens != 2_000_000 || *row.USD != 180 || *row.Percent != 18 {
		t.Fatalf("B did not receive the signed rule: %+v", row)
	}
	if a.view(f).Total.USD != 90 {
		t.Fatal("display rule changed A's raw statistics")
	}

	response := httptest.NewRecorder()
	b.Handler(nil).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/view?device=all&raw=1", nil))
	var published map[string]json.RawMessage
	check(json.Unmarshal(response.Body.Bytes(), &published))
	for _, key := range []string{"rows", "quota", "total", "rule", "projects", "sessions"} {
		if _, ok := published[key]; ok {
			t.Fatalf("B exposed %s via the UI endpoint", key)
		}
	}

	restarted, err := New(a.dir, "a")
	check(err)
	restarted.localAccount = q.Account
	if result := restarted.view(url.Values{"device": {"all"}}); result.Count != 2 {
		t.Fatal("A did not retain its last verified usage offline")
	}
}

func TestResetBoundariesAndPriceUnknown(t *testing.T) {
	reset := time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)
	old := Quota{Account: "account", Start: reset.Add(-7 * 24 * time.Hour), End: reset, At: reset.Add(-time.Minute), Used: 99}
	next := Quota{Account: old.Account, Start: reset, End: reset.Add(7 * 24 * time.Hour), At: reset, Used: 0}
	a := &App{cfg: config{Role: "a"}, localAccount: "account", local: []Day{{Device: "local", Quotas: []Quota{old, next},
		Rows: []Request{
			{ID: "before", At: reset.Add(-time.Nanosecond), Session: "s", Model: "m", Account: old.Account, Tokens: Tokens{Input: 11}},
			{ID: "at", At: reset, Session: "s", Model: "m", Account: next.Account, Tokens: Tokens{Input: 12}},
		}}}}
	for _, q := range []Quota{old, next} {
		view := a.view(url.Values{"window": {q.id()}, "session": {"local/s"}})
		if view.Count != 1 || view.Total.Unpriced != 1 || view.Total.Estimated != 0 {
			t.Fatalf("reset boundary or unknown price is wrong for %s: %+v", q.id(), view.Total)
		}
	}
}

func TestLocalCodexPermission(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := UseLocalCodex(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if !sessions.Reads("codex") || sessions.Reads("claude") || !settings.AccountDiscoveryOff() {
		t.Fatal("collector permission must enable Codex alone and keep account discovery disabled")
	}
}

func TestLoggedResetDriftUsesLatestReading(t *testing.T) {
	end := time.Date(2026, 11, 13, 3, 30, 11, 0, time.UTC)
	a := Quota{Account: "fixture", Start: end.Add(-7 * 24 * time.Hour), End: end,
		At: end.Add(-3 * time.Hour), Used: 62, Direct: true}
	stale := a
	stale.At = stale.At.Add(-2 * time.Hour)
	stale.Used, stale.Direct = 11, false
	stale.End = stale.End.Add(4 * time.Second)
	stale.Start = stale.Start.Add(4 * time.Second)
	got := periods([]Day{{Quotas: []Quota{a, stale}}})
	if len(got) != 1 || got[0].Used != 62 || !got[0].End.Equal(end) || got[0].Cutoff != nil {
		t.Fatalf("one weekly window became several: %+v", got)
	}
}

func TestModelEffortBreakdownMatchesFilteredUsage(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	window := Quota{Account: "account", Start: now.AddDate(0, 0, -3), End: now.AddDate(0, 0, 4), At: now, Used: 42}
	call := func(model, effort, project, session, account string, at time.Time, tokens Tokens, usd float64) Request {
		return Request{Model: model, Effort: effort, Project: project, Session: session, Account: account,
			At: at, Tokens: tokens, USD: usd, Priced: true}
	}
	local := Day{Device: "local", Quotas: []Quota{window}, Rows: []Request{
		call("model-a", "high", "p", "s1", "account", now, Tokens{Input: 1, Output: 2, Cache: 3}, 12),
		call("model-a", "xhigh", "p", "s2", "account", now, Tokens{Input: 5, Output: 6, Cache: 7}, 24),
		call("model-a", "", "p", "s2", "", now, Tokens{Input: 9, Output: 10, Cache: 11}, 48),
		call("model-a", "high", "p", "s2", "account", window.Start.Add(-time.Second), Tokens{Input: 99}, 99),
		call("model-a", "low", "other", "s2", "account", now, Tokens{Input: 99}, 99),
		call("model-b", "high", "p", "s1", "account", now, Tokens{Input: 99}, 99),
	}}
	remote := Day{Device: "remote", Rows: []Request{
		call("model-a", "xhigh", "p", "s2", "account", now, Tokens{Input: 99}, 99),
	}}
	a := &App{cfg: config{Role: "a"}, local: []Day{local}, remote: []Day{remote}, localAccount: "account"}
	params := url.Values{"device": {"local"}, "window": {window.id()}, "project": {"p"}, "model": {"model-a"}}
	v := a.view(params)
	if v.Total.Count != 2 || v.Total.total(true) != 24 || v.Total.total(false) != 14 || v.Total.USD != 36 || v.Total.EstimateUSD != 54 {
		t.Fatalf("wrong scoped model total: %+v", v.Total)
	}
	got := map[string]Group{}
	for _, effort := range v.Efforts {
		got[effort.Name] = effort
	}
	if len(got) != 2 || got["high"].total(true) != 6 || got["xhigh"].EstimateUSD != 36 {
		t.Fatalf("wrong breakdown: %+v", got)
	}
	params.Set("session", "local/s2")
	params.Set("cache", "0")
	v = a.view(params)
	if len(v.Efforts) != 1 || v.Total.total(false) != 11 || v.Total.EstimateUSD != 36 {
		t.Fatalf("model breakdown ignored session or cache scope: %+v, %+v", v.Total, v.Efforts)
	}
}
