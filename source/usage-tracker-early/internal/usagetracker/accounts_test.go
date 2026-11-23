package usagetracker

import (
	"context"
	"net/url"
	"testing"
	"time"
)

func TestAccountSwitchAndUncertainIntervals(t *testing.T) {
	app, err := New(t.TempDir(), "a")
	if err != nil {
		t.Fatal(err)
	}
	one, two := accountKey("one"), accountKey("two")
	t0 := time.Now().UTC().Truncate(time.Second)
	cfg := app.config()
	for _, sample := range []struct {
		account string
		offset  time.Duration
	}{
		{one, 0}, {one, time.Minute}, {two, 2 * time.Minute}, {two, 3 * time.Minute}, {one, 4 * time.Minute}, {"", 5 * time.Minute}, {one, 6 * time.Minute},
	} {
		cfg, err = app.observeAccount(cfg, sample.account, t0.Add(sample.offset))
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(cfg.AccountObservations) != 4 || cfg.AccountObservations[0].Account != one || cfg.AccountObservations[1].Account != two || cfg.AccountObservations[2].Account != one || cfg.AccountObservations[3].Start.Equal(cfg.AccountObservations[2].Start) {
		t.Fatalf("switch, unreadable identity or resume merged observation intervals: %+v", cfg.AccountObservations)
	}
	app.observationOpen = false // restart
	cfg, err = app.observeAccount(cfg, one, t0.Add(7*time.Minute))
	if err != nil || len(cfg.AccountObservations) != 5 {
		t.Fatal("restart extended an old observation")
	}

	end := t0.Add(3 * 24 * time.Hour)
	q1 := Quota{Account: one, Plan: "pro", Start: end.Add(-7 * 24 * time.Hour), End: end, At: t0, Used: 20, Direct: true}
	q2 := q1
	q2.Account, q2.Plan = two, "plus"
	event := q1
	event.Account = ""
	event.At = t0.Add(31 * time.Second)
	event.sourceTokens = Tokens{Input: 5, Output: 2, Cache: 10}
	event.sourceEventHash = "event"
	row := Request{ID: "request", At: t0.Add(30 * time.Second), Tokens: event.sourceTokens, sourceAccount: one, duration: 10 * time.Second, quotaEvidence: []Quota{event}}
	owners := map[string]map[string]bool{"event": {"request": true}}
	if got := observedRequestAccount(row, cfg.AccountObservations, []Quota{q1, q2}, owners); got != one {
		t.Fatal("stable observed request was not attributed")
	}
	for _, mutate := range []func(*Request){
		func(r *Request) { r.duration = 40 * time.Second }, // begins before monitoring
		func(r *Request) { r.duration = 0 },
		func(r *Request) { r.sourceAccount = two },
		func(r *Request) { r.quotaEvidence = nil },
		func(r *Request) { r.Tokens.Input++ },
	} {
		changed := row
		mutate(&changed)
		if observedRequestAccount(changed, cfg.AccountObservations, []Quota{q1, q2}, owners) != "" {
			t.Fatal("an uncertain request was silently attributed")
		}
	}
	q2.Plan = q1.Plan
	if observedRequestAccount(row, cfg.AccountObservations, []Quota{q1, q2}, owners) != "" {
		t.Fatal("same-plan matching windows are ambiguous")
	}
	q2.Plan = "plus"
	owners["event"]["other-request"] = true
	if observedRequestAccount(row, cfg.AccountObservations, []Quota{q1, q2}, owners) != "" {
		t.Fatal("a shared quota event was treated as one request")
	}
}

func TestUserConfirmsOnlyOnePendingLocalRequest(t *testing.T) {
	app, err := New(t.TempDir(), "a")
	if err != nil {
		t.Fatal(err)
	}
	account := accountKey("selected")
	app.account = func() string { return account }
	app.localAccount = account
	now := time.Now().UTC()
	one := Request{ID: digest("one"), Session: digest("session"), At: now, Model: "test", Tokens: Tokens{Input: 42}, sourceAccount: account}
	two := Request{ID: digest("two"), Session: digest("session"), At: now, Model: "test", Tokens: Tokens{Input: 11}, sourceAccount: account}
	app.local = []Day{{Device: "local", Rows: []Request{one, two}}}
	app.collect = func(_ context.Context, id, name string, _ []Quota, fixes []AccountCorrection) ([]Day, error) {
		r := one
		if len(fixes) == 1 && fixes[0].ID == one.ID {
			r.Account = fixes[0].Account
			r.confirmed = true
		}
		return []Day{{Device: id, Name: name, Rows: []Request{r, two}}}, nil
	}
	if err := app.confirmAccount(t.Context(), one.ID, account); err != nil {
		t.Fatal(err)
	}
	if len(app.config().AccountCorrections) != 1 || !app.config().AccountCorrections[0].Manual || app.local[0].Rows[0].Account != account || app.local[0].Rows[1].Account != "" {
		t.Fatal("confirmation changed other requests")
	}
	if err := app.confirmAccount(t.Context(), one.ID, account); err == nil {
		t.Fatal("duplicate confirmation was accepted")
	}
	if err := app.confirmAccount(t.Context(), two.ID, accountKey("unseen")); err == nil {
		t.Fatal("an unseen account was accepted")
	}
}

func TestAccountViewFollowsCurrentAndSeparatesUnknown(t *testing.T) {
	t0 := time.Now().UTC()
	a, b := accountKey("a"), accountKey("b")
	qa := Quota{Account: a, Plan: "pro", Start: t0.Add(-time.Hour), End: t0.Add(6 * 24 * time.Hour), At: t0, Used: 40, Direct: true}
	qb := qa
	qb.Account, qb.Used = b, 80
	day := Day{Device: "local", Quotas: []Quota{qa, qb}, Rows: []Request{
		{ID: "a", At: t0, Account: a, Tokens: Tokens{Input: 10}, Priced: true},
		{ID: "b", At: t0, Account: b, Tokens: Tokens{Input: 20}, Priced: true},
		{ID: "unknown", At: t0, Tokens: Tokens{Input: 30}, Priced: true},
	}}
	app := &App{cfg: config{Role: "a", Quotas: []Quota{qa, qb}}, local: []Day{day}, localAccount: a}
	for _, test := range []struct {
		logged, chosen string
		want           int64
		official       float64
	}{{a, "", 10, 40}, {b, "", 20, 80}, {a, b, 20, 80}, {b, a, 10, 40}} {
		app.localAccount = test.logged
		v := app.view(url.Values{"account": {test.chosen}})
		if v.Total.Input != test.want || v.Official == nil || v.Official.Used != test.official || v.UnassignedCount != 1 {
			t.Fatalf("wrong account view: %+v", v.Total)
		}
	}
	all := app.view(url.Values{"account": {"all"}})
	if all.Total.Input != 60 || all.Official != nil {
		t.Fatal("all-account log total or official allowance scope was incorrect")
	}
	app.localAccount = b
	afterSwitch := app.view(url.Values{"window": {qa.id()}})
	if afterSwitch.Quota == nil || afterSwitch.Quota.Account != b || afterSwitch.Total.Input != 20 {
		t.Fatal("a former account's selected window survived an account switch")
	}
	app.localAccount = ""
	v := app.view(nil)
	if !v.AccountUnavailable || v.Total.Count != 0 || v.Official != nil {
		t.Fatal("an unreadable login selected another account")
	}
	v = app.view(url.Values{"account": {"unassigned"}})
	if v.Total.Input != 30 || v.Official != nil {
		t.Fatal("unassigned records were mixed with an account")
	}
}
