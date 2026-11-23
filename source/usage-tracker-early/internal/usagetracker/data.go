package usagetracker

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"time"
)

const defaultFactor = 1.5
const defaultBudget = 900
const resetTolerance = time.Minute
const defaultSyncMinutes = 5

type Tokens struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
	Cache  int64 `json:"cache"`
}

func (t Tokens) total(cache bool) int64 {
	if cache {
		return t.Input + t.Output + t.Cache
	}
	return t.Input + t.Output
}

type Request struct {
	ID          string    `json:"id"`
	At          time.Time `json:"at"`
	Project     string    `json:"project"`
	ProjectName string    `json:"projectName"`
	Session     string    `json:"session"`
	Model       string    `json:"model"`
	Effort      string    `json:"effort,omitempty"`
	Account     string    `json:"account,omitempty"`
	Attribution string    `json:"attribution,omitempty"`
	Tokens
	USD    float64 `json:"usd"`
	Priced bool    `json:"priced"`
	// A-only evidence stays in memory and is never uploaded with B snapshots.
	sourceAccount string
	duration      time.Duration
	confirmed     bool
	quotaEvidence []Quota
}

type Quota struct {
	Account string     `json:"account"`
	Plan    string     `json:"plan"`
	Start   time.Time  `json:"start"`
	End     time.Time  `json:"end"`
	Cutoff  *time.Time `json:"cutoff,omitempty"`
	At      time.Time  `json:"at"`
	Used    float64    `json:"used"`
	Direct  bool       `json:"direct,omitempty"`
	// sourceEventHash is kept only in memory; it identifies the exact local
	// quota event used to verify a confirmed historical account change.
	sourceEventHash string
	sourceTokens    Tokens
}

// An interval is limited to this app's continuously observed runtime. It is
// never extended over a restart, a failed read, or a long observation gap.
type AccountObservation struct {
	Account string    `json:"account"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
}

func (q Quota) valid() bool {
	return q.Account != "" && q.Used >= 0 && q.Used <= 100 && q.End.After(q.Start) &&
		!q.At.Before(q.Start) && q.At.Before(q.End)
}

func (q Quota) id() string {
	return q.Account + "/" + strconv.FormatInt(q.End.Unix(), 10)
}

func (q Quota) until() time.Time {
	if q.Cutoff != nil {
		return *q.Cutoff
	}
	return q.End
}

func (q Quota) includes(at time.Time) bool {
	return !at.Before(q.Start) && at.Before(q.until())
}

type Day struct {
	Device string    `json:"device"`
	Name   string    `json:"name"`
	Date   string    `json:"date"`
	At     time.Time `json:"at"`
	Rows   []Request `json:"rows"`
	Quotas []Quota   `json:"quotas,omitempty"`
}

type Policy struct {
	Version     int64        `json:"version"`
	Periods     []PeriodRule `json:"periods,omitempty"`
	SyncMinutes int          `json:"syncMinutes,omitempty"`
}

func (p Policy) interval() time.Duration {
	if !validSyncMinutes(p.SyncMinutes) {
		return defaultSyncMinutes * time.Minute
	}
	return time.Duration(p.SyncMinutes) * time.Minute
}

func validSyncMinutes(n int) bool { return n >= 1 && n <= 1440 }

type PeriodRule struct {
	Account string             `json:"account"`
	End     time.Time          `json:"end"`
	Factor  float64            `json:"factor"`
	Budget  float64            `json:"budget"`
	Devices map[string]float64 `json:"devices,omitempty"`
}

func (p Policy) rule(q Quota) PeriodRule {
	chosen := PeriodRule{Factor: defaultFactor, Budget: defaultBudget}
	for _, r := range p.Periods {
		if r.Account == q.Account && !r.End.After(q.End.Add(resetTolerance)) && r.End.After(chosen.End) {
			chosen = r
		}
	}
	return chosen
}

func (r PeriodRule) factor(device string) float64 {
	if value, ok := r.Devices[device]; ok {
		return value
	}
	return r.Factor
}

func (r PeriodRule) valid() bool {
	if !positive(r.Factor, 100) || !positive(r.Budget, 1e9) || r.Account == "" || r.End.IsZero() {
		return false
	}
	for id, value := range r.Devices {
		if !deviceID.MatchString(id) || !positive(value, 100) {
			return false
		}
	}
	return true
}

func positive(n, max float64) bool {
	return n > 0 && n <= max
}

type Total struct {
	Tokens
	Count        int     `json:"count"`
	USD          float64 `json:"usd"`
	Unpriced     int     `json:"unpriced"`
	Unassigned   int     `json:"unassigned"`
	OtherAccount int     `json:"otherAccount"`
	EstimateUSD  float64 `json:"estimateUsd"`
	Estimated    int     `json:"estimated"`
	Inferred     int     `json:"inferred"`
}

func (t *Total) add(r Request, q *Quota, factor float64) {
	t.Count++
	if r.Attribution == "inferred" {
		t.Inferred++
	}
	t.Input += r.Input
	t.Output += r.Output
	t.Cache += r.Cache
	if r.Priced {
		t.USD += r.USD
	} else {
		t.Unpriced++
	}
	if q != nil && r.Account == q.Account && r.Priced {
		t.EstimateUSD += r.USD * factor
		t.Estimated++
	}
	if r.Account == "" || (q != nil && r.Account != q.Account) {
		t.Unassigned++
	}
	if r.Account != "" && q != nil && r.Account != q.Account {
		t.OtherAccount++
	}
}

type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Total
}

type DisplayGroup struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Tokens   int64    `json:"tokens"`
	USD      *float64 `json:"usd"`
	Percent  *float64 `json:"percent,omitempty"`
	Unpriced int      `json:"unpriced,omitempty"`
}

func display(g Group, cache bool, factor, budget float64, withPeriod bool) DisplayGroup {
	out := DisplayGroup{ID: g.ID, Name: g.Name, Tokens: int64(math.Round(float64(g.total(cache)) * factor)), Unpriced: g.Unpriced}
	if g.Count > g.Unpriced {
		usd := g.USD * factor
		out.USD = &usd
		if withPeriod && g.Estimated > 0 {
			percent := g.EstimateUSD / budget * 100
			out.Percent = &percent
		}
	}
	return out
}

func groups(m map[string]*Group, withCache bool) []Group {
	out := make([]Group, 0, len(m))
	for _, g := range m {
		out = append(out, *g)
	}
	slices.SortFunc(out, func(a, b Group) int {
		return cmp.Or(cmp.Compare(b.total(withCache), a.total(withCache)), cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// Codex may record the same reset a few seconds apart. Match those samples
// before deciding whether a new cycle started, and use a direct OpenAI read
// for the exact boundary when available.
func periods(days []Day) []Quota {
	longest := make(map[string]time.Duration)
	var readings []Quota
	for _, day := range days {
		for _, q := range day.Quotas {
			if q.valid() {
				longest[q.Account] = max(longest[q.Account], q.End.Sub(q.Start))
				readings = append(readings, q)
			}
		}
	}
	slices.SortFunc(readings, func(a, b Quota) int {
		return cmp.Or(cmp.Compare(a.Account, b.Account), a.End.Compare(b.End), a.At.Compare(b.At))
	})
	type window struct {
		boundary Quota
		latest   Quota
	}
	var windows []window
	for _, q := range readings {
		if q.End.Sub(q.Start) != longest[q.Account] {
			continue
		}
		if len(windows) > 0 {
			last := &windows[len(windows)-1]
			if sameReset(last.boundary, q) {
				if q.At.After(last.latest.At) {
					last.latest = q
				}
				if q.Direct && (!last.boundary.Direct || q.At.After(last.boundary.At)) {
					last.boundary = q
				}
				continue
			}
		}
		windows = append(windows, window{boundary: q, latest: q})
	}
	out := make([]Quota, 0, len(windows))
	for _, w := range windows {
		q := w.latest
		q.Start, q.End, q.Direct = w.boundary.Start, w.boundary.End, w.boundary.Direct
		out = append(out, q)
	}
	slices.SortFunc(out, func(a, b Quota) int {
		return cmp.Or(b.End.Compare(a.End), cmp.Compare(a.Account, b.Account))
	})
	next := map[string]time.Time{}
	for i := range out {
		q := &out[i]
		if start := next[q.Account]; start.After(q.Start) && start.Before(q.End) {
			q.Cutoff = &start
		}
		next[q.Account] = q.Start
	}
	return out
}

func sameReset(a, b Quota) bool {
	return a.Account == b.Account && a.End.Sub(b.End).Abs() <= resetTolerance
}
