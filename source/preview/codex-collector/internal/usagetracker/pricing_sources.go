package usagetracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/sessions"
	"github.com/yetone/magpie/internal/settings"
)

const (
	magpieSource     = "magpie"
	agentSpeedSource = "agent-speed"
	pricingFile      = "controller-pricing.json"
)

var agentPriceURLs = []string{
	"https://raw.githubusercontent.com/zoefix/agent-speed/main/spec/assets/prices.json",
	"https://cdn.jsdelivr.net/gh/zoefix/agent-speed@main/spec/assets/prices.json",
}

type priceCheck struct {
	Attempt, Success time.Time
	Failures         int
	Error            string
	URL, ETag        string
	Version          int
}

type pricePreferences struct {
	Source     string                `json:"source"`
	AutoOff    bool                  `json:"autoOff,omitempty"`
	Checks     map[string]priceCheck `json:"checks,omitempty"`
	AgentCache json.RawMessage       `json:"agentCache,omitempty"`
}

type controllerPricing struct {
	prefs       pricePreferences
	agent       *agentPriceTable
	agentCached bool
	checking    bool
	magpie      map[string]catalog.Price
	published   int64
}

type localPriceInput struct {
	Model, Provider                string
	CacheRead, CacheWrite, Write1h int
}

type missingPrice struct {
	Model  string `json:"model"`
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

type pricingStatus struct {
	Source           string         `json:"source"`
	Origin           string         `json:"origin"`
	Auto             bool           `json:"auto"`
	Checking         bool           `json:"checking"`
	Updated          string         `json:"updated,omitempty"`
	Version          int            `json:"version,omitempty"`
	Models           int            `json:"models,omitempty"`
	Attempt          time.Time      `json:"attempt,omitempty"`
	Success          time.Time      `json:"success,omitempty"`
	Error            string         `json:"error,omitempty"`
	Publication      int64          `json:"publication,omitempty"`
	PublicationError string         `json:"publicationError,omitempty"`
	Missing          []missingPrice `json:"missing,omitempty"`
}

func loadPricing(dir string) controllerPricing {
	p := controllerPricing{agent: defaultAgentPrices()}
	if raw, err := os.ReadFile(filepath.Join(dir, pricingFile)); err == nil && len(raw) <= agentPriceLimit+(64<<10) {
		var prefs pricePreferences
		if json.Unmarshal(raw, &prefs) == nil {
			p.prefs = prefs
			if table, err := parseAgentPrices(prefs.AgentCache, time.Now()); err == nil && table.Version >= p.agent.Version {
				p.agent, p.agentCached = table, true
			} else {
				p.prefs.AgentCache = nil
			}
		}
	}
	if p.prefs.Source != agentSpeedSource {
		p.prefs.Source = magpieSource
		p.magpie = magpiePriceSnapshot(p.agent)
	}
	return p
}

func (p controllerPricing) check() priceCheck {
	c := p.prefs.Checks[p.prefs.Source]
	if p.prefs.Source == magpieSource && c.Success.IsZero() {
		if info, err := os.Stat(catalog.CachePath()); err == nil {
			c.Success = info.ModTime()
		}
	}
	return c
}

func (p controllerPricing) status(days []Day, now time.Time) pricingStatus {
	check := p.check()
	s := pricingStatus{Source: p.prefs.Source, Origin: "内置", Auto: !p.prefs.AutoOff, Checking: p.checking,
		Attempt: check.Attempt, Success: check.Success, Error: check.Error}
	if p.prefs.Source == agentSpeedSource {
		s.Updated, s.Version, s.Models = p.agent.Updated, p.agent.Version, len(p.agent.Models)
		if p.agentCached {
			s.Origin = "已下载"
		}
	} else if path := catalog.Source(); path != "" {
		if info, err := os.Stat(path); err == nil {
			s.Origin = "已缓存"
			s.Updated = info.ModTime().Format(time.DateOnly)
		}
	}
	type modelReason struct{ model, reason string }
	counts := map[modelReason]int{}
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dayEnd := dayStart.AddDate(0, 0, 1)
	for _, day := range days {
		for _, row := range day.Rows {
			if row.Priced || row.priceReason == "" || row.At.Before(dayStart) || !row.At.Before(dayEnd) {
				continue
			}
			counts[modelReason{row.Model, row.priceReason}]++
		}
	}
	for key, count := range counts {
		s.Missing = append(s.Missing, missingPrice{key.model, key.reason, count})
	}
	slices.SortFunc(s.Missing, func(a, b missingPrice) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		if a.Model != b.Model {
			return strings.Compare(a.Model, b.Model)
		}
		return strings.Compare(a.Reason, b.Reason)
	})
	return s
}

func (a *App) priceStatus() pricingStatus {
	a.mu.RLock()
	prices, local, remote := a.pricing, a.local, a.remote
	revision, publicationError := a.cfg.PriceRevision, a.priceSyncError
	a.mu.RUnlock()
	status := prices.status(slices.Concat(local, remote), time.Now())
	status.Publication, status.PublicationError = revision, publicationError
	return status
}

func (a *App) savePricing(p controllerPricing, reprice bool) error {
	if reprice && p.prefs.Source == magpieSource {
		p.magpie = magpiePriceSnapshot(p.agent)
	}
	raw, err := json.Marshal(p.prefs)
	if err != nil {
		return err
	}
	if err = writePrivate(filepath.Join(a.dir, pricingFile), raw); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if reprice {
		a.local = repricedDays(a.local, p, false)
		a.remote = repricedDays(a.remote, p, true)
	}
	a.pricing = p
	return nil
}

func (a *App) changePricing(source string, auto *bool) error {
	if a.config().Role != "a" {
		return errors.New("仅 A 端可设置价格表")
	}
	if !a.pricingJob.TryLock() {
		return errors.New("正在检查价格表，请稍后再试")
	}
	defer a.pricingJob.Unlock()
	a.mu.RLock()
	p := a.pricing
	a.mu.RUnlock()
	if auto != nil {
		p.prefs.AutoOff = !*auto
		return a.savePricing(p, false)
	}
	if source != magpieSource && source != agentSpeedSource {
		return errors.New("请选择有效的价格表来源")
	}
	if source == p.prefs.Source {
		return nil
	}
	p.prefs.Source = source
	return a.savePricing(p, true)
}

func priceCheckDue(check priceCheck, now time.Time) bool {
	base, interval := check.Success, 24*time.Hour
	if check.Failures > 0 && !check.Attempt.IsZero() {
		base = check.Attempt
		if check.Failures == 1 {
			interval = time.Hour
		}
	}
	return base.IsZero() || base.After(now.Add(5*time.Minute)) || now.Sub(base) >= interval
}

func (a *App) runPriceChecks(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = a.refreshPricing(ctx, false)
		}
	}
}

func (a *App) refreshPricing(ctx context.Context, manual bool) error {
	if a.config().Role != "a" {
		return errors.New("仅 A 端可检查价格表")
	}
	if !a.pricingJob.TryLock() {
		return errors.New("正在检查价格表，请稍后再试")
	}
	defer a.pricingJob.Unlock()
	a.mu.RLock()
	p := a.pricing
	a.mu.RUnlock()
	check := p.check()
	now := time.Now().Round(0)
	if !manual && (p.prefs.AutoOff || !priceCheckDue(check, now)) {
		return nil
	}
	a.mu.Lock()
	a.pricing.checking = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.pricing.checking = false
		a.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	check.Attempt = now
	var err error
	changed := false
	if p.prefs.Source == agentSpeedSource {
		client := &http.Client{Timeout: 9 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		var table *agentPriceTable
		var raw []byte
		table, raw, check, err = fetchAgentPrices(ctx, client, agentPriceURLs, p.agent.Version, check, now)
		if table != nil {
			p.agent, p.prefs.AgentCache, p.agentCached, changed = table, raw, true, true
		}
	} else {
		if err = catalog.Sync(ctx); err != nil {
			err = errors.New("无法读取 Models.dev 价格目录")
		} else {
			changed = true
		}
	}
	if err == nil {
		check.Success, check.Failures, check.Error = time.Now().Round(0), 0, ""
	} else {
		check.Failures++
		check.Error = err.Error()
	}
	p.prefs.Checks = maps.Clone(p.prefs.Checks)
	if p.prefs.Checks == nil {
		p.prefs.Checks = map[string]priceCheck{}
	}
	p.prefs.Checks[p.prefs.Source] = check
	if saveErr := a.savePricing(p, changed); saveErr != nil {
		return errors.New("无法保存本机价格表")
	}
	return err
}

func fetchAgentPrices(ctx context.Context, client *http.Client, urls []string, currentVersion int, check priceCheck, now time.Time) (*agentPriceTable, []byte, priceCheck, error) {
	failure := errors.New("无法读取 Agent Speed 价格表")
	for _, endpoint := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Accept", "application/json")
		if check.URL == endpoint && check.Version == currentVersion && check.ETag != "" {
			req.Header.Set("If-None-Match", check.ETag)
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, agentPriceLimit+1))
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotModified && req.Header.Get("If-None-Match") != "" {
			return nil, nil, check, nil
		}
		if readErr != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		table, err := parseAgentPrices(raw, now)
		if err != nil {
			failure = err
			continue
		}
		if table.Version < currentVersion {
			failure = fmt.Errorf("服务器价格表为较早版本 %d", table.Version)
			continue
		}
		check.URL, check.Version, check.ETag = endpoint, table.Version, resp.Header.Get("ETag")
		if table.Version == currentVersion {
			return nil, nil, check, nil
		}
		return table, raw, check, nil
	}
	return nil, nil, check, failure
}

func repricedDays(days []Day, p controllerPricing, remote bool) []Day {
	priced := slices.Clone(days)
	for i := range priced {
		priced[i].Rows = slices.Clone(priced[i].Rows)
	}
	priceRequests(priced, p, remote)
	return priced
}

func priceRequests(days []Day, p controllerPricing, remote bool) {
	type magpiePrice struct {
		price catalog.Price
		known bool
	}
	magpie := map[string]magpiePrice{}
	agent := map[[2]string]agentModelMatch{}
	for _, day := range days {
		for i := range day.Rows {
			r := &day.Rows[i]
			in := r.localPriceInput
			if remote {
				in = &localPriceInput{Model: r.Model, CacheRead: int(r.Cache) - r.CacheWrites,
					CacheWrite: r.CacheWrites, Write1h: r.CacheWrites1h}
			}
			if in == nil {
				continue
			}
			r.USD, r.Priced, r.priceReason = 0, false, ""
			if r.At.IsZero() || r.Input < 0 || r.Output < 0 || in.CacheRead < 0 ||
				in.CacheWrite < 0 || in.Write1h < 0 || in.Write1h > in.CacheWrite {
				r.priceReason = "请求时间或用量不完整"
				continue
			}
			if standardPricingModel(in.Model) == "codex-auto-review" {
				r.priceReason = "无公开价格"
				continue
			}
			if p.prefs.Source == agentSpeedSource {
				key := [2]string{in.Model, in.Provider}
				match, found := agent[key]
				if !found {
					match = p.agent.resolve(in.Model, in.Provider)
					if match.model == nil && pricingMode(in.Model, "") != "unknown" {
						match = p.agent.resolve(standardPricingModel(in.Model), in.Provider)
					}
					agent[key] = match
				}
				r.priceAgent(in, match)
				continue
			}
			base := r.Model
			if pricingMode(r.Model, "") != "unknown" {
				base = standardPricingModel(r.Model)
			}
			entry, found := magpie[base]
			if !found {
				if p.magpie != nil {
					entry.price, entry.known = p.magpie[base]
					if !entry.known {
						entry.price, entry.known = p.magpie[standardPricingModel(base)]
					}
				} else {
					entry.price, entry.known = sessions.PriceOf(settings.Settings{}, base)
				}
				magpie[base] = entry
			}
			call := sessions.Call{Time: r.At, Tokens: sessions.Tokens{Input: int(r.Input), Output: int(r.Output),
				CacheRead: in.CacheRead, CacheWrite: in.CacheWrite, CacheWrite1h: in.Write1h}}
			r.USD, r.Priced = controllerCost(r.Model, r.Tier, call, entry.price, entry.known)
			if !r.Priced {
				r.priceReason = "模型或档位价格未确认"
			}
		}
	}
}

func (r *Request) priceAgent(in *localPriceInput, match agentModelMatch) {
	if match.free {
		r.Priced = true
		return
	}
	if match.model == nil || r.At.IsZero() {
		r.priceReason = match.reason
		return
	}
	tier := pricingMode(r.Model, r.Tier)
	if tier == "unknown" && match.fast {
		tier = "fast"
	}
	scale, supported := officialSpeedScale(match.model.ID, tier, r.At)
	if !supported {
		r.priceReason = "模型或档位价格未确认"
		return
	}
	rates := match.model.rates(r.At, false)
	write, longer := int64(in.CacheWrite), int64(in.Write1h)
	longer = min(max(longer, 0), max(write, 0))
	cache := int64(in.CacheRead)
	r.USD = rates.scaledCost(r.Input+cache+write, [5]int64{r.Input, cache, write - longer, longer, r.Output}, scale)
	r.Priced = true
}
