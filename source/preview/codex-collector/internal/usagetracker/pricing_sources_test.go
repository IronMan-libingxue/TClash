package usagetracker

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/sessions"
	"github.com/yetone/magpie/internal/settings"
)

func TestControllerPriceSourceRepricesLocalAndRemoteEstimates(t *testing.T) {
	lookup := sessions.PriceOf
	sessions.PriceOf = func(_ settings.Settings, id string) (catalog.Price, bool) {
		return catalog.Price{Input: 2, Output: 10, CacheRead: .1, CacheWrite: 2.5}, id == "gpt-6.1-sol"
	}
	t.Cleanup(func() { sessions.PriceOf = lookup })
	a, err := New(t.TempDir(), "a")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	row := Request{Model: "gpt-6.1-sol", Tier: "fast", At: at, Tokens: Tokens{Input: 10_000, Cache: 20_000, Output: 4000},
		localPriceInput: &localPriceInput{Model: "gpt-6.1-sol", Provider: "openai", CacheRead: 20_000}}
	newModel := row
	newModel.Model, newModel.Tier = "gpt-6.2-sol", "ultrafast"
	newModel.localPriceInput = &localPriceInput{Model: newModel.Model, Provider: "openai", CacheRead: 20_000}
	a.local = []Day{{Device: "local", Rows: []Request{row, newModel}}}
	deviceRow, deviceNewModel := row, newModel
	deviceRow.localPriceInput, deviceNewModel.localPriceInput = nil, nil
	deviceRow.USD, deviceRow.Priced = 3.25, true
	deviceNewModel.USD, deviceNewModel.Priced = 4.25, true
	a.remote = []Day{{Device: "remote", Rows: []Request{deviceRow, deviceNewModel}}}
	priceRequests(a.local, a.pricing, false)
	before := a.local[0].Rows
	remoteBefore := a.remote[0].Rows
	if !before[0].Priced || before[0].USD != .124 || !before[1].Priced || before[1].USD != .186 {
		t.Fatalf("existing Magpie estimates changed: %.6f, %.6f", before[0].USD, before[1].USD)
	}
	if err := a.changePricing(agentSpeedSource, nil); err != nil {
		t.Fatal(err)
	}
	after, remoteAfter := a.local[0].Rows, a.remote[0].Rows
	if !after[0].Priced || after[0].USD != .124 || after[0].Tier != "fast" || after[0].Tokens != row.Tokens ||
		after[1].Priced || after[1].priceReason != "价格表没有此模型" || before[0].USD != .124 {
		t.Fatalf("wrong local price or mode: %.6f, missing=%q", after[0].USD, after[1].priceReason)
	}
	if !remoteAfter[0].Priced || remoteAfter[0].USD != .124 || remoteAfter[0].Tokens != deviceRow.Tokens ||
		remoteAfter[1].Priced || remoteAfter[1].USD != 0 || remoteBefore[0].USD != 3.25 || remoteBefore[1].USD != 4.25 {
		t.Fatalf("wrong remote prices or uploaded estimates mutated: %+v", remoteAfter)
	}
	if missing := a.priceStatus().Missing; len(missing) != 1 || missing[0].Count != 2 || missing[0].Model != newModel.Model {
		t.Fatalf("missing prices should include synchronized requests: %+v", missing)
	}
	disabled := false
	if err := a.changePricing("", &disabled); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(a.dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if view := reopened.priceStatus(); view.Source != agentSpeedSource || view.Auto || view.Version < 8 || view.Models < 202 {
		t.Fatalf("pricing settings did not survive reopening: %+v", view)
	}
}

func TestAgentSpeedDailyCheckAndFallback(t *testing.T) {
	current := defaultAgentPrices()
	var newerDocument map[string]json.RawMessage
	if err := json.Unmarshal(bundledAgentPrices, &newerDocument); err != nil {
		t.Fatal(err)
	}
	newerDocument["version"], _ = json.Marshal(current.Version + 1)
	var models []map[string]json.RawMessage
	if err := json.Unmarshal(newerDocument["models"], &models); err != nil {
		t.Fatal(err)
	}
	for _, model := range models {
		if string(model["id"]) == `"gpt-6.1-sol"` {
			model["prices"] = json.RawMessage(`{"input":4,"cachedInput":0.1,"cacheWrite":2.5,"output":10}`)
		}
	}
	newerDocument["models"], _ = json.Marshal(models)
	newer, _ := json.Marshal(newerDocument)
	var primary, mirror atomic.Int32
	var stale atomic.Bool
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/primary" {
			primary.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "{corrupted")
			return
		}
		mirror.Add(1)
		if stale.Load() {
			_, _ = w.Write(bundledAgentPrices)
			return
		}
		if r.Header.Get("If-None-Match") == `"new-table"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"new-table"`)
		_, _ = w.Write(newer)
	}))
	defer host.Close()
	prior := agentPriceURLs
	agentPriceURLs = []string{host.URL + "/primary", host.URL + "/mirror"}
	t.Cleanup(func() { agentPriceURLs = prior })
	a, err := New(t.TempDir(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.changePricing(agentSpeedSource, nil); err != nil {
		t.Fatal(err)
	}
	a.remote = []Day{{Device: "remote", Rows: []Request{{
		Model: "gpt-6.1-sol", At: time.Now(), Tier: "fast", Tokens: Tokens{Input: 10_000, Cache: 20_000, Output: 4_000},
	}}}}
	disabled := false
	if err := a.changePricing("", &disabled); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshPricing(t.Context(), false); err != nil || primary.Load() != 0 {
		t.Fatalf("disabled automatic update reached the network: %v", err)
	}
	if err := a.refreshPricing(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	status := a.priceStatus()
	if status.Version != current.Version+1 || status.Success.IsZero() || status.Origin != "已下载" ||
		primary.Load() != 1 || mirror.Load() != 1 {
		t.Fatalf("fallback did not update price table: %+v", status)
	}
	if row := a.remote[0].Rows[0]; !row.Priced || row.USD != .164 {
		t.Fatalf("the new table was not used for a synchronized request: %+v", row)
	}
	if err := a.refreshPricing(t.Context(), true); err != nil || primary.Load() != 2 || mirror.Load() != 2 {
		t.Fatalf("conditional price check failed: %v", err)
	}
	stale.Store(true)
	if err := a.refreshPricing(t.Context(), true); err == nil {
		t.Fatal("an older table should not replace the verified cache")
	}
	if row := a.remote[0].Rows[0]; !row.Priced || row.USD != .164 {
		t.Fatalf("a failed update changed the device estimate: %+v", row)
	}
	reopened, err := New(a.dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.priceStatus(); got.Version != status.Version || got.Error == "" || !bytes.Equal(reopened.pricing.prefs.AgentCache, newer) {
		t.Errorf("verified price cache was lost: %+v", got)
	}
	check := priceCheck{Success: time.Now()}
	if priceCheckDue(check, check.Success.Add(24*time.Hour-time.Second)) ||
		!priceCheckDue(check, check.Success.Add(24*time.Hour)) {
		t.Fatal("automatic checks are not scheduled daily")
	}
}

func TestBReceivesSignedEffectivePricesAndKeepsLastGoodVersion(t *testing.T) {
	c, err := New(t.TempDir(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.changePricing(agentSpeedSource, nil); err != nil {
		t.Fatal(err)
	}
	b, err := New(t.TempDir(), "b")
	if err != nil {
		t.Fatal(err)
	}
	cc, bc := c.config(), b.config()
	bc.Pair.Verifier = cc.Signer.Public().(ed25519.PublicKey)
	bc.LastSync = time.Now()
	at := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	record := Request{Model: "gpt-6.1-sol", Tier: "ultrafast", At: at,
		Tokens: Tokens{Input: 10000, Cache: 20000, Output: 4000}}
	b.local = []Day{{Rows: []Request{record}}}
	mem := &memoryRepo{files: map[string][]byte{}}
	name := controls + "/" + sharedPriceFile
	if err = c.publishPricing(t.Context(), mem, &cc, at); err != nil {
		t.Fatal(err)
	}
	old := bytes.Clone(mem.files[name])
	if err = b.receivePricing(t.Context(), mem, bc); err != nil {
		t.Fatal(err)
	}
	r := b.local[0].Rows[0]
	if !r.Priced || r.USD != .372 || r.Tokens != record.Tokens {
		t.Fatalf("wrong signed-table Ultrafast estimate: %+v", r)
	}
	c.pricing.prefs.Source = magpieSource
	c.pricing.magpie = map[string]catalog.Price{"gpt-6.1-sol": {Input: 3, CacheRead: .1, Output: 10}}
	if err = c.publishPricing(t.Context(), mem, &cc, at); err != nil {
		t.Fatal(err)
	}
	if err = b.receivePricing(t.Context(), mem, bc); err != nil {
		t.Fatal(err)
	}
	if quoted := b.local[0].Rows[0].USD; quoted != .432 {
		t.Fatalf("corrected A rate was not applied to old usage: %v", quoted)
	}
	mem.files[name] = old
	if err = b.receivePricing(t.Context(), mem, bc); err == nil {
		t.Fatal("stale signed prices accepted")
	}
	old[len(old)-1] ^= 1
	if err = b.receivePricing(t.Context(), mem, bc); err == nil {
		t.Fatal("tampered pricing accepted")
	}
	p, err := readPublishedPricing(b.dir, bc.Pair.Verifier)
	if err != nil || p.published != cc.PriceRevision || b.local[0].Rows[0].USD != .432 {
		t.Fatal("last signed price table did not survive a failed check")
	}
}
