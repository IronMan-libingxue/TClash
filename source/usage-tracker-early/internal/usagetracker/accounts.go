package usagetracker

import (
	"context"
	"errors"
	"sort"
	"time"
)

// classifyLocal refuses to turn a session creator into proof of every request
// made in that session. A confirmed correction wins; otherwise the request
// needs a complete observed interval, a matching creator and an independently
// read, unambiguous quota event with the same token counts.
func classifyLocal(days []Day, cfg config) {
	owners := map[string]map[string]bool{}
	var earliest time.Time
	for _, observation := range cfg.AccountObservations {
		if earliest.IsZero() || observation.Start.Before(earliest) {
			earliest = observation.Start
		}
	}
	for _, day := range days {
		for _, r := range day.Rows {
			if earliest.IsZero() || r.At.Before(earliest.Add(-2*time.Minute)) {
				continue
			}
			for _, event := range requestQuotaEvents(r) {
				if event.sourceEventHash == "" || event.sourceTokens != r.Tokens || event.At.Before(r.At) || event.At.After(r.At.Add(2*time.Minute)) {
					continue
				}
				if owners[event.sourceEventHash] == nil {
					owners[event.sourceEventHash] = map[string]bool{}
				}
				owners[event.sourceEventHash][r.ID] = true
			}
		}
	}
	for di := range days {
		for ri := range days[di].Rows {
			r := &days[di].Rows[ri]
			if r.confirmed {
				r.Attribution = "confirmed"
				continue
			}
			r.Account = observedRequestAccount(*r, cfg.AccountObservations, cfg.Quotas, owners)
			if r.Account != "" {
				r.Attribution = "inferred"
			}
		}
	}
}

func observedRequestAccount(r Request, observations []AccountObservation, known []Quota, owners map[string]map[string]bool) string {
	if r.sourceAccount == "" || r.duration <= 0 || r.duration > 24*time.Hour || r.Tokens.total(true) == 0 {
		return ""
	}
	start := r.At.Add(-r.duration)
	for _, interval := range observations {
		if interval.Account != r.sourceAccount || start.Before(interval.Start) || r.At.After(interval.End) {
			continue
		}
		matched := ""
		for _, event := range requestQuotaEvents(r) {
			if event.Plan == "" || event.sourceTokens != r.Tokens || event.At.Before(r.At) || event.At.After(r.At.Add(2*time.Minute)) || event.At.After(interval.End) {
				continue
			}
			if event.sourceEventHash == "" || len(owners[event.sourceEventHash]) != 1 || !owners[event.sourceEventHash][r.ID] {
				continue
			}
			for _, q := range known {
				if !q.Direct || !q.valid() || q.Plan != event.Plan || q.End.Sub(event.End).Abs() > resetTolerance || !q.includes(r.At) {
					continue
				}
				if matched != "" && matched != q.Account {
					return ""
				}
				matched = q.Account
			}
		}
		if matched == interval.Account {
			return matched
		}
	}
	return ""
}

func requestQuotaEvents(r Request) []Quota {
	start := sort.Search(len(r.quotaEvidence), func(i int) bool { return !r.quotaEvidence[i].At.Before(r.At) })
	end := sort.Search(len(r.quotaEvidence), func(i int) bool { return r.quotaEvidence[i].At.After(r.At.Add(2 * time.Minute)) })
	return r.quotaEvidence[start:end]
}

// A user can confirm a single currently unassigned local request. The saved
// fingerprint is rechecked against the original request on every collection.
func (a *App) confirmAccount(ctx context.Context, id, account string) error {
	if !deviceID.MatchString(id) || !deviceID.MatchString(account) {
		return errors.New("请选择要确认的请求和账号")
	}
	cfg := a.config()
	if cfg.Role != "a" {
		return errors.New("仅 A 端可确认账号归属")
	}
	a.mu.RLock()
	var found Request
	count := 0
	known := account == a.localAccount
	for _, day := range a.local {
		for _, row := range day.Rows {
			if row.ID == id {
				found = row
				count++
			}
			if row.Account == account {
				known = true
			}
		}
	}
	a.mu.RUnlock()
	for _, q := range cfg.Quotas {
		if q.Account == account {
			known = true
		}
	}
	for _, c := range cfg.AccountCorrections {
		if c.Account == account || c.FromAccount == account {
			known = true
		}
		if c.ID == id {
			return errors.New("这条请求已有确认记录")
		}
	}
	if !known || count != 1 || found.Account != "" {
		return errors.New("请求或账号已变化，请刷新后重试")
	}
	fix := AccountCorrection{Manual: true, ID: found.ID, Session: found.Session, FromAccount: found.sourceAccount,
		Account: account, At: found.At, Model: found.Model, Tokens: found.Tokens}
	cfg.AccountCorrections = append(cfg.AccountCorrections, fix)
	if err := a.save(cfg); err != nil {
		return err
	}
	return a.refreshLocal(ctx, cfg)
}
