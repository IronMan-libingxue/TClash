package usagetracker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/yetone/magpie/internal/catalog"
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

func (m *memoryRepo) DeviceIDs(_ context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return nil, errors.New("connection lost")
	}
	ids := map[string]bool{}
	for path := range m.files {
		id, _, ok := strings.Cut(path, "/")
		if ok && deviceID.MatchString(id) {
			ids[id] = true
		}
	}
	var result []string
	for id := range ids {
		result = append(result, id)
	}
	return result, nil
}

func (m *memoryRepo) DeviceYears(_ context.Context, id string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return nil, errors.New("connection lost")
	}
	years := map[string]bool{}
	for path := range m.files {
		if rest, ok := strings.CutPrefix(path, id+"/"); ok {
			year, _, found := strings.Cut(rest, "/")
			if found {
				years[year] = true
			}
		}
	}
	var result []string
	for year := range years {
		result = append(result, year)
	}
	return result, nil
}

func (m *memoryRepo) DeleteDeviceFile(_ context.Context, folder, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("connection lost")
	}
	delete(m.files, folder+"/"+name)
	return nil
}

func TestPrivateSyncAndDisplayedUsage(t *testing.T) {
	lookup := sessions.PriceOf
	sessions.PriceOf = func(_ settings.Settings, model string) (catalog.Price, bool) {
		return catalog.Price{Input: 2, Output: 10, CacheRead: .1}, model == "gpt-6.1-sol"
	}
	t.Cleanup(func() { sessions.PriceOf = lookup })
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
				Session: "sample-session", Model: "gpt-6.1-sol", Account: q.Account, Tier: "normal",
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
	f.Set("model", "gpt-6.1-sol")
	f.Set("session", bs[0].config().device().ID+"/sample-session")
	result = a.view(f)
	if result.Count != 1 || result.Total.USD != 1.28 || result.Rows[0].USD != 1.28 {
		t.Fatalf("A filters failed: %+v", result.Total)
	}
	reopened, err := New(a.dir, "")
	check(err)
	cached := reopened.view(url.Values{"device": {bs[0].config().device().ID}, "account": {"all"}})
	if cached.Count != 1 || cached.Rows[0].USD != 1.28 || a.cachedDays(a.config())[0].Rows[0].USD != 90 {
		t.Fatal("reopening must reprice the verified snapshot without rewriting its uploaded amount")
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
	if row.Tokens != 2_000_000 || *row.USD != 2.56 || math.Abs(*row.Percent-2.56/1000*100) > 1e-9 {
		t.Fatalf("B did not receive the signed rule: %+v", row)
	}
	if updated := a.view(f); updated.Total.USD != 1.28 || updated.Rule.Budget != 1000 {
		t.Fatal("updating the budget or device multiplier changed A's unscaled estimate")
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

func TestDisplayRuleUsesLocalCurrentPeriod(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	local := Quota{Account: "account", Start: now.Add(-72 * time.Hour), End: now.Add(96 * time.Hour), At: now, Used: 20, Direct: true}
	remote := local
	remote.Start = remote.Start.Add(62 * time.Second)
	remote.End = remote.End.Add(62 * time.Second)
	remote.Direct = false
	remoteID := strings.Repeat("a", 32)
	app, err := New(t.TempDir(), "a")
	if err != nil {
		t.Fatal(err)
	}
	app.localAccount = local.Account
	app.local = []Day{{Device: "local", Quotas: []Quota{local}}}
	app.remote = []Day{{Device: remoteID, Quotas: []Quota{remote}}}
	app.cfg.Devices = map[string]Device{remoteID: {ID: remoteID, Name: "Mac 3"}}
	view := app.view(nil)
	if view.Quota == nil || !view.Quota.End.Equal(local.End) || len(view.Windows) != 1 || !view.Windows[0].Current {
		t.Fatal("the local period must be shown as current")
	}
	rule := PeriodRule{Account: local.Account, End: local.End, Factor: 1.5, Budget: 900, Devices: map[string]float64{remoteID: 2}}
	if err := app.setRule(rule); err != nil {
		t.Fatalf("saving the current local period with a delayed remote reading: %v", err)
	}
	if got := app.config().Policy.rule(local).factor(remoteID); got != 2 {
		t.Fatalf("device factor was not saved: %v", got)
	}
	old := local
	old.Start, old.End, old.At = now.Add(-14*24*time.Hour), now.Add(-7*24*time.Hour), now.Add(-8*24*time.Hour)
	app.local = append(app.local, Day{Quotas: []Quota{old}})
	if err := app.setRule(PeriodRule{Account: old.Account, End: old.End, Factor: 2, Budget: 900}); err == nil {
		t.Fatal("an expired period must not accept changes")
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
	if v.Total.Count != 2 || v.Total.total(true) != 24 || v.Total.total(false) != 14 || v.Total.USD != 36 || v.Total.EstimateUSD != 36 {
		t.Fatalf("wrong scoped model total: %+v", v.Total)
	}
	got := map[string]Group{}
	for _, effort := range v.Efforts {
		got[effort.Name] = effort
	}
	if len(got) != 2 || got["high"].total(true) != 6 || got["xhigh"].EstimateUSD != 24 {
		t.Fatalf("wrong breakdown: %+v", got)
	}
	params.Set("session", "local/s2")
	params.Set("cache", "0")
	v = a.view(params)
	if len(v.Efforts) != 1 || v.Total.total(false) != 11 || v.Total.EstimateUSD != 24 {
		t.Fatalf("model breakdown ignored session or cache scope: %+v, %+v", v.Total, v.Efforts)
	}
}

func TestDeviceFullUsageAndBOnlyMultiplier(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	q := Quota{Account: "account", Plan: "pro", Start: now.Add(-time.Hour), End: now.Add(6 * 24 * time.Hour), At: now, Used: 43, Direct: true}
	rows := []Request{
		{ID: "known", At: now, Session: "s1", Project: "p", ProjectName: "Project", Model: "gpt-6.1-terra", Account: q.Account,
			Tokens: Tokens{Input: 334_495_280}, USD: 22.53, Priced: true},
		{ID: "unknown", At: now, Session: "s2", Project: "p", ProjectName: "Project", Model: "gpt-6.1-terra",
			Tokens: Tokens{Input: 23_800_815}, USD: 36.04/1.5 - 22.53, Priced: true},
	}
	day := Day{Device: "local", Name: "Mac 1（本机）", At: now, Quotas: []Quota{q}, Rows: rows}
	a := &App{cfg: config{Role: "a", Quotas: []Quota{q}}, localAccount: q.Account, local: []Day{day}}
	b, err := New(t.TempDir(), "b")
	if err != nil {
		t.Fatal(err)
	}
	bDay := day
	bDay.Device = b.config().device().ID
	b.local = []Day{bDay}

	actual := a.view(nil)
	if actual.Total.total(true) != 334_495_280 || math.Abs(actual.Total.EstimateUSD-22.53) > 1e-9 {
		t.Fatalf("selected account is not raw or includes unassigned rows: %+v", actual.Total)
	}
	if actual.Official == nil || actual.Official.Used != 43 {
		t.Fatal("the official account allowance changed")
	}
	var local *deviceCard
	for i := range actual.Cards {
		if actual.Cards[i].ID == "local" {
			local = &actual.Cards[i]
		}
	}
	if local == nil || local.Total.total(true) != 358_296_095 || local.Unassigned.Count != 1 ||
		local.Unassigned.total(true) != 23_800_815 || actual.DeviceTotal.total(true) != 358_296_095 ||
		actual.DeviceUnassigned.total(true) != 23_800_815 || len(local.Models) != 1 ||
		local.Models[0].total(true) != local.Total.total(true) {
		t.Fatalf("device full usage and pending account records differ: %+v", local)
	}
	if details := a.view(url.Values{"device": {"local"}, "account": {"all"}, "window": {q.id()}}); details.Quota == nil ||
		details.Total.total(true) != local.Total.total(true) {
		t.Fatal("full device detail lost rows or changed the chosen period")
	}
	if unknown := a.view(url.Values{"device": {"local"}, "account": {"unassigned"}}); unknown.Total.total(true) != 23_800_815 {
		t.Fatal("pending records cannot be inspected separately")
	}
	presented := b.view(nil)
	if len(presented.DisplayModels) != 1 || len(presented.DisplayProjects) != 1 {
		t.Fatal("B model or project usage missing")
	}
	for _, g := range []DisplayGroup{presented.DisplayModels[0], presented.DisplayProjects[0]} {
		if g.Tokens != 537_444_143 || g.USD == nil || math.Abs(*g.USD-36.04) > 1e-9 ||
			g.Percent == nil || math.Abs(*g.Percent-36.04/900*100) > 1e-9 {
			t.Fatalf("B is not 1.5 times the same full device usage: %+v", g)
		}
	}

	// A's display must stay raw when the B-device rule changes.
	rule := PeriodRule{Account: q.Account, End: q.End, Factor: 2, Budget: 900}
	a.cfg.Policy.Periods, b.cfg.Policy.Periods = []PeriodRule{rule}, []PeriodRule{rule}
	if again := a.view(nil); again.Total.EstimateUSD != actual.Total.EstimateUSD || again.Cards[0].Total.USD != local.Total.USD {
		t.Fatal("B display multiplier changed A's original usage")
	}
	if model := b.view(nil).DisplayModels[0]; model.Tokens != 716_592_190 || math.Abs(*model.USD-2*local.Total.USD) > 1e-9 {
		t.Fatal("the adjusted multiplier was not confined to B")
	}

	// Other known accounts and unpriced requests remain part of the device
	// full total without being attributed to the selected account.
	other := Request{ID: "other", At: now, Session: "s3", Project: "p", Model: "gpt-6.1-terra", Account: "other", Tokens: Tokens{Input: 100}, Priced: false}
	a.local[0].Rows = append(a.local[0].Rows, other)
	b.local[0].Rows = append(b.local[0].Rows, other)
	again := a.view(nil)
	var full *deviceCard
	for i := range again.Cards {
		if again.Cards[i].ID == "local" {
			full = &again.Cards[i]
		}
	}
	if again.Total.total(true) != 334_495_280 || full == nil || full.Total.total(true) != 358_296_195 ||
		full.OtherAccount.Count != 1 || full.Unassigned.Count != 1 || full.Total.Unpriced != 1 {
		t.Fatalf("other account or missing price was hidden or misattributed: %+v", full)
	}
	if model := b.view(nil).DisplayModels[0]; model.Tokens != 716_592_390 || model.Unpriced != 1 {
		t.Fatal("B omitted other-account or unpriced device usage")
	}
}
