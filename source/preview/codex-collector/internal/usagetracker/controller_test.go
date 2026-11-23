package usagetracker

import (
	"encoding/json"
	"math"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/sessions"
)

func TestControllerPricing(t *testing.T) {
	cutover := ultrafastListPriceAt
	call := sessions.Call{Time: cutover.Add(-time.Nanosecond), Tokens: sessions.Tokens{Input: 10000, CacheRead: 20000, Output: 4000}}
	check := func(model, tier string, call sessions.Call, price catalog.Price, valid bool, want float64) {
		t.Helper()
		if got, ok := controllerCost(model, tier, call, price, valid); !ok || math.Abs(got-want) > 1e-10 {
			t.Errorf("%s %s = %v, %v; want %v", model, tier, got, ok, want)
		}
	}
	for _, tc := range []struct {
		model string
		scale float64
	}{{"gpt-6.2-sol", 1}, {"gpt-6.2-astra", 4}} {
		check(tc.model, "normal", call, catalog.Price{}, false, .062*tc.scale)
		check(tc.model, "fast", call, catalog.Price{}, false, .124*tc.scale)
		check(tc.model, "ultrafast", call, catalog.Price{}, false, .186*tc.scale)
		after := call
		after.Time = cutover
		check(tc.model, "ultrafast", after, catalog.Price{}, false, .372*tc.scale)
	}
	call.Input, call.CacheWrite = 251000, 1000
	check("gpt-6.2-sol", "normal", call, catalog.Price{}, false, .5465)
	call.Input++
	check("gpt-6.2-sol", "normal", call, catalog.Price{}, false, 1.073004)
	check("gpt-6.2-astra", "fast", call, catalog.Price{}, false, 1.073004*8)
	check("gpt-6.2-sol", "normal", call, catalog.Price{Input: 2, Output: 12, CacheRead: .1, CacheWrite: 2.5}, true, 1.085004)
	if _, ok := controllerCost("gpt-6.2-sol", "", call, catalog.Price{}, false); ok {
		t.Error("missing mode was assigned an unverified price")
	}
}

func TestControllerDisplayScaleAndSessionScope(t *testing.T) {
	dir := t.TempDir()
	a, err := New(dir, "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(t.TempDir(), "b")
	if err != nil {
		t.Fatal(err)
	}
	device := b.config().device()
	device.Name = "Windows 2"
	cfg := a.config()
	cfg.Devices = map[string]Device{device.ID: device}
	now := time.Now().UTC()
	cycle := Quota{Account: "fixture", Start: now.Add(-time.Hour), End: now.AddDate(0, 0, 7).Add(-time.Hour), At: now}
	closed := Quota{Account: cycle.Account, Start: cycle.Start.AddDate(0, 0, -7), End: cycle.Start, At: cycle.Start.Add(-time.Minute)}
	cfg.Policy = Policy{Version: 9, SyncMinutes: 1, Periods: []PeriodRule{
		{Account: cycle.Account, End: closed.End, Factor: 2, Budget: 600},
		{Account: cycle.Account, End: cycle.End, Factor: 1.5, Budget: 900, Devices: map[string]float64{device.ID: 3.5}},
	}}
	if err = a.save(cfg); err != nil {
		t.Fatal(err)
	}
	r := Request{At: now.Add(-time.Minute), Account: cycle.Account, ID: "request", Session: "main", Project: "projectA",
		Model: "gpt-6.2-sol", Effort: "xhigh", Tier: "normal", Tokens: Tokens{Input: 10, Cache: 20, Output: 2}, USD: .075, Priced: true}
	a.local = []Day{{Device: "local", Quotas: []Quota{cycle, closed}, Rows: []Request{r}}}
	var rows []Request
	for range 51 {
		rows = append(rows, r)
	}
	child := r
	child.Tier, child.Model, child.Session, child.FastOwner, child.Project = "fast", "gpt-6.2-astra", "child", "main", "projectB"
	ultra := r
	ultra.Tier, ultra.Model, ultra.Project = "ultrafast", child.Model, child.Project
	unknown := r
	unknown.Tier, unknown.Effort, unknown.Priced = "", "", false
	older := r
	older.At = closed.At
	rows = append(rows, child, ultra, unknown, older)
	a.remote = []Day{{Device: device.ID, Rows: rows}}
	a.localAccount = cycle.Account

	if err = a.setDeviceScale(device.ID, true); err != nil {
		t.Fatal(err)
	}
	if err = a.renameDevice(device.ID, "验收设备"); err != nil {
		t.Fatal(err)
	}
	again, err := New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if stored, _ := again.controllerSettings(); !stored.Scaled[device.ID] || again.config().Policy.Version != 9 {
		t.Fatal("display preference was not saved independently of the synced policy")
	}
	all := a.view(url.Values{})
	if all.DisplayFactor != 0 || all.Total.Count != 55 || all.Total.Input != 550 {
		t.Fatalf("mixed totals changed: %+v", all.Total)
	}
	for _, card := range all.Cards {
		if card.ID == device.ID && (!card.Scaled || card.Total.Count != 54 || card.DisplayFactor != 3.5 || card.FastTotal.Count != 1) {
			t.Fatalf("device card mismatch: %+v", card)
		}
		if card.ID == "local" && card.Scaled {
			t.Fatal("local card was scaled")
		}
	}
	q := url.Values{"device": {device.ID}, "session": {device.ID + "/main"}, "model": {r.Model}, "project": {r.Project}, "page": {"1"}}
	v := a.view(q)
	if v.DisplayFactor != 3.5 || v.Count != 52 || len(v.Rows) != 2 || len(v.SessionEfforts) != 4 {
		t.Fatalf("detail and session scopes diverged: factor=%v count=%d rows=%d summary=%d", v.DisplayFactor, v.Count, len(v.Rows), len(v.SessionEfforts))
	}
	groups := map[string]Group{}
	for _, g := range v.SessionEfforts {
		groups[g.ID] = g
	}
	if !strings.Contains(groups["xhigh/fast"].Name, "⚡ Fast") || groups["xhigh/fast"].Count != 1 ||
		groups["xhigh/normal"].Count != 51 || groups["xhigh/ultrafast"].Count != 1 || groups["/unknown"].Unpriced != 1 {
		t.Fatalf("bad summary: %+v", groups)
	}
	q.Set("window", closed.id())
	historical := a.view(q)
	if historical.Count != 1 || historical.DisplayFactor != 2 || historical.SessionRule.Budget != 900 || len(historical.SessionEfforts) != 4 {
		t.Fatal("selected history changed the current-session summary")
	}
	if err = a.setDeviceScale(device.ID, false); err != nil {
		t.Fatal(err)
	}
	if unscaled := a.view(q); unscaled.Scaled || unscaled.Total.Input != historical.Total.Input || unscaled.Total.USD != historical.Total.USD {
		t.Fatal("changing presentation modified stored values")
	}
}

func TestControllerNoticeTransitions(t *testing.T) {
	now := time.Date(2026, 11, 18, 4, 0, 0, 0, time.UTC)
	var state deviceNoticeState
	step := func(at time.Time, count, fast, ultra int, used bool, want string) {
		t.Helper()
		look := noticeObservation{id: "fixture", activity: &Activity{At: at, Active: count, Fast: fast, Ultrafast: &ultra}}
		if used {
			look.request = at
		}
		next, got := state.advance(look, at, 3*time.Minute)
		if got != want {
			t.Fatalf("%v notification %q; want %q (state=%+v)", at.Sub(now), got, want, next)
		}
		state = next
	}
	step(now, 1, 0, 0, true, "updated")
	step(now.Add(time.Minute), 1, 0, 0, true, "")
	for i := 2; i <= 12; i++ {
		step(now.Add(time.Duration(i)*time.Minute), 0, 0, 0, false, "")
	}
	step(now.Add(12*time.Minute+time.Second), 1, 0, 0, true, "")
	for i := 13; i <= 24; i++ {
		step(now.Add(time.Duration(i)*time.Minute), 0, 0, 0, false, "")
	}
	step(now.Add(25*time.Minute), 1, 0, 0, true, "updated")
	step(now.Add(26*time.Minute), 1, 1, 0, false, "fast")
	step(now.Add(27*time.Minute), 2, 1, 0, true, "special")
	step(now.Add(28*time.Minute), 2, 1, 1, true, "ultrafast-parallel")
	step(now.Add(29*time.Minute), 2, 1, 1, true, "")
	persisted, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	state = deviceNoticeState{}
	if err := json.Unmarshal(persisted, &state); err != nil {
		t.Fatal(err)
	}
	step(now.Add(30*time.Minute), 2, 0, 1, false, "")
	step(now.Add(31*time.Minute), 1, 0, 1, false, "")
	step(now.Add(32*time.Minute), 2, 0, 1, false, "ultrafast-parallel")
	step(now.Add(33*time.Minute), 0, 0, 0, false, "")
	step(now.Add(34*time.Minute), 1, 0, 1, false, "ultrafast")
	step(now.Add(35*time.Minute), 2, 0, 0, false, "concurrent")
	step(now.Add(36*time.Minute), 0, 0, 0, false, "")
	step(now.Add(55*time.Minute), 1, 0, 0, true, "")
	old := noticeObservation{id: "offline", request: now}
	_, got := (deviceNoticeState{}).advance(old, now.Add(time.Hour), 3*time.Minute)
	if got != "" {
		t.Fatal("offline replay triggered a notification")
	}
	_, body := notificationText("Mac 3", "ultrafast-parallel", &Activity{Active: 2})
	if !strings.Contains(body, "2 个独立会话 · 项目未确认") {
		t.Fatalf("missing concurrent project status: %s", body)
	}
}

func TestControllerIndependentTelemetry(t *testing.T) {
	now := time.Now().UTC()
	ultra := 1
	earlier := &Telemetry{Version: 1, Activity: &Activity{At: now.Add(-time.Minute), Active: 2, Ultrafast: &ultra}}
	later := &Telemetry{Version: 1, Traffic: &TrafficMonth{At: now, Download: 100}}
	merged := mergeTelemetry(earlier, later)
	if merged.Traffic.Download != 100 || activityLevel(activityFlags(merged.Activity), true) != "ultrafast-parallel" {
		t.Fatal("a later traffic upload hid the combined running state")
	}
	if freshObservation(merged.Activity, now.Add(5*time.Minute), 3*time.Minute) {
		t.Fatal("stale activity can still light the card")
	}
	if got := activityLevel(activityFlags(&Activity{Active: 1, Fast: 1}), true); got != "fast" {
		t.Fatalf("legacy B observation gave %s", got)
	}
}
