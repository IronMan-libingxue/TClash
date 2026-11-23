package usagetracker

import (
	"context"
	"errors"
	"time"

	"github.com/yetone/magpie/internal/sessions"
)

// AccountCorrection confirms individual historical requests, never a whole
// session or later activity. Identifiers and the event hash are derived
// values, not credentials or conversation text.
type AccountCorrection struct {
	Manual         bool      `json:"manual,omitempty"`
	ID             string    `json:"id"`
	Session        string    `json:"session"`
	FromAccount    string    `json:"fromAccount"`
	Account        string    `json:"account"`
	At             time.Time `json:"at"`
	Model          string    `json:"model"`
	Tokens         Tokens    `json:"tokens"`
	QuotaAt        time.Time `json:"quotaAt"`
	QuotaEnd       time.Time `json:"quotaEnd"`
	Plan           string    `json:"plan"`
	QuotaEventHash string    `json:"quotaEventHash"`
}

type attributedAccount struct{ from, to string }

type accountQuotaKey struct {
	file, from, plan, eventHash string
	at, end                     int64
}

type attributionIndex struct {
	rows   map[string]attributedAccount
	quotas map[accountQuotaKey]string
}

func attributionQuotaKey(file, from string, q Quota) accountQuotaKey {
	return accountQuotaKey{file: file, from: from, plan: q.Plan, eventHash: q.sourceEventHash,
		at: q.At.UnixNano(), end: q.End.UnixNano()}
}

func callThread(call sessions.Call) string {
	if call.ThreadID != "" {
		return call.ThreadID
	}
	if call.Session != "" {
		return call.Session
	}
	return call.File
}

// verifiedAttributions applies a confirmation only when the same request,
// original account, quota event and independently known target cycle are
// still present. A missing or changed source leaves the request unchanged.
func (c *Collector) verifiedAttributions(ctx context.Context, calls []sessions.Call, known []Quota, corrections []AccountCorrection) (attributionIndex, error) {
	result := attributionIndex{rows: map[string]attributedAccount{}, quotas: map[accountQuotaKey]string{}}
	if len(corrections) == 0 {
		return result, nil
	}
	byID := make(map[string]AccountCorrection, len(corrections))
	for _, correction := range corrections {
		if _, exists := byID[correction.ID]; exists {
			return result, errors.New("已确认的账号归属包含重复请求")
		}
		byID[correction.ID] = correction
	}
	for _, call := range calls {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if call.File == "" || (call.Upstream != "" && call.Upstream != "openai") {
			continue
		}
		thread := callThread(call)
		id := requestID(call, thread)
		fix, ok := byID[id]
		if !ok || !deviceID.MatchString(fix.ID) || !deviceID.MatchString(fix.Session) ||
			(fix.FromAccount != "" && !deviceID.MatchString(fix.FromAccount)) || !deviceID.MatchString(fix.Account) ||
			fix.Session != digest("session", thread) || fix.FromAccount != accountKey(call.AccountID) ||
			!fix.At.Equal(call.Time) || fix.Model != label(call.Model) ||
			fix.Tokens != (Tokens{Input: int64(call.Input), Output: int64(call.Output), Cache: int64(call.CacheRead + call.CacheWrite)}) {
			continue
		}
		if fix.Manual {
			result.rows[id] = attributedAccount{from: fix.FromAccount, to: fix.Account}
			continue
		}
		if !deviceID.MatchString(fix.FromAccount) || !deviceID.MatchString(fix.QuotaEventHash) || fix.Account == fix.FromAccount {
			continue
		}
		if delay := fix.QuotaAt.Sub(call.Time); delay < 0 || delay > 2*time.Minute {
			continue
		}
		knownCycle := false
		for _, q := range known {
			if q.valid() && q.Account == fix.Account && q.Plan == fix.Plan && q.End.Equal(fix.QuotaEnd) && q.includes(call.Time) {
				knownCycle = true
				break
			}
		}
		if !knownCycle {
			continue
		}
		readings, err := c.quotaReadings(call.File)
		if err != nil {
			return result, errors.New("读取已确认请求的额度记录失败")
		}
		for _, q := range readings {
			if !q.At.Equal(fix.QuotaAt) || !q.End.Equal(fix.QuotaEnd) || q.Plan != fix.Plan || q.sourceEventHash != fix.QuotaEventHash {
				continue
			}
			key := attributionQuotaKey(call.File, fix.FromAccount, q)
			if previous, exists := result.quotas[key]; exists && previous != fix.Account {
				return result, errors.New("已确认的额度记录存在冲突的账号归属")
			}
			result.rows[id] = attributedAccount{from: fix.FromAccount, to: fix.Account}
			result.quotas[key] = fix.Account
			break
		}
	}
	return result, nil
}
