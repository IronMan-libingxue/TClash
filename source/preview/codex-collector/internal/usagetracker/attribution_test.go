package usagetracker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/sessions"
)

func TestAccountCorrectionsPersistInTrackerConfig(t *testing.T) {
	a, err := New(t.TempDir(), "a")
	if err != nil {
		t.Fatal(err)
	}
	cfg := a.config()
	cfg.AccountCorrections = []AccountCorrection{{ID: digest("request"), Account: accountKey("current")}}
	if err := a.save(cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(a.dir, "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields["accountCorrections"]) == 0 || len(fields["AccountCorrections"]) != 0 {
		t.Fatal("confirmed request records were not saved under the expected field")
	}
	loaded, err := loadConfig(a.dir)
	if err != nil || len(loaded.AccountCorrections) != 1 || loaded.AccountCorrections[0].ID != cfg.AccountCorrections[0].ID {
		t.Fatal("confirmed request records did not survive a reload")
	}
}

func TestConfirmedAccountSwitchRequiresMatchingEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	end := now.Add(24 * time.Hour)
	eventAt := now.Add(time.Second)
	line := fmt.Sprintf("{\"timestamp\":%q,\"type\":\"event_msg\",\"payload\":{\"type\":\"token_count\",\"rate_limits\":{\"limit_id\":\"codex\",\"plan_type\":\"pro\",\"primary\":{\"used_percent\":3,\"window_minutes\":10080,\"resets_at\":%d}}}}\n", eventAt.Format(time.RFC3339Nano), end.Unix())
	file := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(file, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	call := sessions.Call{Time: now, ThreadID: "session", File: file, Upstream: "openai", AccountID: "old-account", Model: "model",
		Tokens: sessions.Tokens{Input: 5, Output: 3, CacheRead: 11}}
	old, target := accountKey("old-account"), accountKey("current-account")
	known := []Quota{{Account: target, Plan: "pro", Start: end.Add(-7 * 24 * time.Hour), End: end, At: now, Used: 3}}
	fix := AccountCorrection{ID: requestID(call, callThread(call)), Session: digest("session", "session"), FromAccount: old, Account: target,
		At: now, Model: "model", Tokens: Tokens{Input: 5, Output: 3, Cache: 11}, QuotaAt: eventAt,
		QuotaEnd: end, Plan: "pro", QuotaEventHash: digest("quota-event", line)}
	collector := &Collector{}
	verify := func(fix AccountCorrection, quotas []Quota, candidate sessions.Call) attributionIndex {
		t.Helper()
		got, err := collector.verifiedAttributions(context.Background(), []sessions.Call{candidate}, quotas, []AccountCorrection{fix})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := verify(fix, known, call)
	if got.rows[fix.ID] != (attributedAccount{old, target}) || len(got.quotas) != 1 {
		t.Fatal("confirmed request and its quota were not assigned to the target account")
	}
	for name, mutate := range map[string]func(*AccountCorrection){
		"different source account": func(v *AccountCorrection) { v.FromAccount = accountKey("another") },
		"changed token count":      func(v *AccountCorrection) { v.Tokens.Input++ },
		"changed event":            func(v *AccountCorrection) { v.QuotaEventHash = digest("changed") },
		"changed cycle":            func(v *AccountCorrection) { v.QuotaEnd = v.QuotaEnd.Add(time.Second) },
		"changed model":            func(v *AccountCorrection) { v.Model = "other" },
		"late quota event":         func(v *AccountCorrection) { v.QuotaAt = now.Add(3 * time.Minute) },
	} {
		t.Run(name, func(t *testing.T) {
			altered := fix
			mutate(&altered)
			got := verify(altered, known, call)
			if len(got.rows) != 0 || len(got.quotas) != 0 {
				t.Fatal("an unverified record was attributed")
			}
		})
	}
	if got := verify(fix, nil, call); len(got.rows) != 0 || len(got.quotas) != 0 {
		t.Fatal("a target cycle with no independent account evidence was accepted")
	}
	other := call
	other.Upstream = "custom"
	if got := verify(fix, known, other); len(got.rows) != 0 || len(got.quotas) != 0 {
		t.Fatal("a non-OpenAI request was attributed to the subscription")
	}
	if _, err := collector.verifiedAttributions(context.Background(), []sessions.Call{call}, known, []AccountCorrection{fix, fix}); err == nil {
		t.Fatal("duplicate confirmations were silently accepted")
	}
	manual := fix
	manual.Manual, manual.Account, manual.QuotaEventHash = true, old, ""
	if got := verify(manual, nil, call); got.rows[manual.ID].to != old {
		t.Fatal("explicitly confirmed old-account request was lost")
	}
	manual.Tokens.Cache++
	if got := verify(manual, nil, call); len(got.rows) != 0 {
		t.Fatal("a manual confirmation survived a changed original record")
	}
}

func TestOverviewSeparatesOtherAccountsFromUnknown(t *testing.T) {
	now := time.Now().UTC()
	q := Quota{Account: "current", Plan: "pro", Start: now.Add(-time.Hour), End: now.Add(6 * 24 * time.Hour), At: now, Used: 30}
	days := []Day{{Device: "local", Name: "Mac 1（本机）", At: now, Quotas: []Quota{q}, Rows: []Request{
		{ID: "current", At: now, Session: "s", Model: "m", Account: "current", Tokens: Tokens{Input: 10}, Priced: true},
		{ID: "other", At: now, Session: "s", Model: "m", Account: "other", Tokens: Tokens{Input: 20}, Priced: true},
		{ID: "unknown", At: now, Session: "s", Model: "m", Tokens: Tokens{Input: 30}, Priced: true},
	}}}
	a := &App{cfg: config{Role: "a"}, local: days, localAccount: "current"}
	v := a.view(nil)
	if v.Total.Input != 10 || v.UnassignedCount != 1 || v.Total.Estimated != 1 {
		t.Fatalf("account totals were not distinguished: %+v", v.Total)
	}
	other := a.view(url.Values{"account": {"other"}})
	unknown := a.view(url.Values{"account": {"unassigned"}})
	if other.Total.Input != 20 || unknown.Total.Input != 30 || len(v.Rows) != 1 || v.Rows[0].Account != "official" {
		t.Fatal("accounts or unassigned requests were mixed")
	}
}
