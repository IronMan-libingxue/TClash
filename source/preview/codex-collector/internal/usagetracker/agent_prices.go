package usagetracker

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const agentPriceLimit = 1 << 20

// The bundled table is Agent Speed v8 at 63c153bbde036e6a114b9a6e3863ac595fb8b113.
//
//go:embed assets/agent-speed-prices.json
var bundledAgentPrices []byte

var defaultAgentPrices = sync.OnceValue(func() *agentPriceTable {
	table, err := parseAgentPrices(bundledAgentPrices, time.Time{})
	if err != nil {
		panic(err)
	}
	return table
})

type agentPriceTable struct {
	Schema    int                `json:"schema"`
	Version   int                `json:"version"`
	Updated   string             `json:"updated"`
	Currency  string             `json:"currency"`
	Unit      string             `json:"unit"`
	Models    []agentPriceModel  `json:"models"`
	Normalize agentNormalization `json:"normalize"`
	Unknown   []agentUnknownRule `json:"unknown"`
	Tests     []struct {
		Model, Provider, Expect string
	}
	byKey map[string]*agentPriceModel
}

type agentUnknownRule struct{ Match, Value, Reason string }

type agentPriceModel struct {
	ID      string
	Aliases []string
	Free    bool
	Fast    *agentTieredPrices
	Periods []agentPricePeriod
	agentPriceVariant
}

type agentPriceVariant struct {
	agentTieredPrices
	OffPeak *agentOffPeak
}

type agentPricePeriod struct {
	From, To string
	agentPriceVariant
	start, end time.Time
}

type agentOffPeak struct {
	PeakUTC      []string `json:"peakUtc"`
	PeakWeekdays []int
	OffPeakDates []string
	agentTieredPrices
	windows [][2]int
}

type agentTieredPrices struct {
	Prices *agentPriceRates
	Tiers  []agentPriceTier
}

type agentPriceTier struct {
	Above int64
	agentPriceRates
}

type agentPriceRates struct {
	Input, CachedInput, CacheWrite, CacheWrite1h, Output *float64
	micro                                                [5]int64
}

func parseAgentPrices(raw []byte, now time.Time) (*agentPriceTable, error) {
	var t agentPriceTable
	if len(raw) > agentPriceLimit || !utf8.Valid(raw) || json.Unmarshal(raw, &t) != nil {
		return nil, errors.New("Agent Speed 价格表无法读取")
	}
	if t.Schema != 1 || t.Currency != "USD" || t.Unit != "perMillionTokens" {
		return nil, errors.New("Agent Speed 价格表格式需要新版应用支持")
	}
	date, err := time.Parse(time.DateOnly, t.Updated)
	if err != nil || (!now.IsZero() && date.Format(time.DateOnly) > now.AddDate(0, 0, 2).Format(time.DateOnly)) ||
		t.Version < 1 || len(t.Models) < 1 || len(t.Models) > 2000 || len(t.Tests) == 0 {
		return nil, errors.New("Agent Speed 价格表版本、日期或模型清单无效")
	}
	t.byKey = make(map[string]*agentPriceModel, len(t.Models))
	for i := range t.Models {
		model := &t.Models[i]
		if err := model.compile(); err != nil {
			return nil, fmt.Errorf("Agent Speed 价格项无效: %w", err)
		}
		for _, key := range slices.Concat([]string{model.ID}, model.Aliases) {
			if key == "" || agentModelKey(key) != key || t.byKey[key] != nil {
				return nil, errors.New("Agent Speed 价格表包含重复或无效模型名")
			}
			t.byKey[key] = model
		}
	}
	for _, rule := range t.Unknown {
		if rule.Value == "" || rule.Reason == "" || (rule.Match != "exact" && rule.Match != "prefix") {
			return nil, errors.New("Agent Speed 价格表的未知模型规则无效")
		}
	}
	for _, fixture := range t.Tests {
		result := t.resolve(fixture.Model, fixture.Provider)
		actual := "unknown"
		if result.free {
			actual = "free"
		} else if result.model != nil {
			actual = result.model.ID
			if result.fast {
				actual += " fast"
			}
		}
		if actual != fixture.Expect {
			return nil, fmt.Errorf("Agent Speed 模型识别校验未通过（%s）", label(fixture.Model))
		}
	}
	return &t, nil
}

func (m *agentPriceModel) compile() error {
	if m.Free {
		if m.Prices != nil || m.OffPeak != nil || len(m.Tiers) != 0 || len(m.Periods) != 0 || m.Fast != nil {
			return errors.New("免费模型同时包含收费规则")
		}
		return nil
	}
	if err := m.agentPriceVariant.compile(); err != nil {
		return err
	}
	if m.Fast != nil {
		if err := m.Fast.compile(); err != nil {
			return err
		}
	}
	for i := range m.Periods {
		p := &m.Periods[i]
		var err error
		if p.start, err = agentPriceDate(p.From); err != nil {
			return err
		}
		if p.end, err = agentPriceDate(p.To); err != nil {
			return err
		}
		if (p.start.IsZero() && p.end.IsZero()) || (!p.start.IsZero() && !p.end.IsZero() && !p.start.Before(p.end)) {
			return errors.New("计价生效期间无效")
		}
		for j := range i {
			q := m.Periods[j]
			if (p.end.IsZero() || q.start.IsZero() || q.start.Before(p.end)) &&
				(q.end.IsZero() || p.start.IsZero() || p.start.Before(q.end)) {
				return errors.New("计价生效期间重叠")
			}
		}
		if err := p.agentPriceVariant.compile(); err != nil {
			return err
		}
	}
	return nil
}

func agentPriceDate(s string) (time.Time, error) {
	switch len(s) {
	case 0:
		return time.Time{}, nil
	case 10:
		return time.Parse(time.DateOnly, s)
	default:
		return time.Parse("2006-01-02T15:04Z", s)
	}
}

func (v *agentPriceVariant) compile() error {
	if err := v.agentTieredPrices.compile(); err != nil {
		return err
	}
	p := v.OffPeak
	if p == nil {
		return nil
	}
	if len(p.PeakUTC) == 0 || len(p.PeakWeekdays) == 0 {
		return errors.New("峰谷计价时间无效")
	}
	for _, day := range p.PeakWeekdays {
		if day < 1 || day > 7 {
			return errors.New("峰谷计价星期无效")
		}
	}
	for _, day := range p.OffPeakDates {
		if _, err := time.Parse(time.DateOnly, day); err != nil {
			return err
		}
	}
	for _, span := range p.PeakUTC {
		left, right, _ := strings.Cut(span, "-")
		start, end := agentPriceMinute(left), agentPriceMinute(right)
		if start < 0 || end <= start {
			return errors.New("峰谷计价时段无效")
		}
		p.windows = append(p.windows, [2]int{start, end})
	}
	return p.agentTieredPrices.compile()
}

func agentPriceMinute(s string) int {
	if len(s) != 5 || s[2] != ':' {
		return -1
	}
	h, hErr := strconv.Atoi(s[:2])
	m, mErr := strconv.Atoi(s[3:])
	if hErr != nil || mErr != nil || h < 0 || h > 24 || m < 0 || m > 59 || (h == 24 && m != 0) {
		return -1
	}
	return h*60 + m
}

func (p *agentTieredPrices) compile() error {
	if p.Prices == nil {
		return errors.New("缺少基础价格")
	}
	if err := p.Prices.compile(); err != nil {
		return err
	}
	last := int64(0)
	for i := range p.Tiers {
		tier := &p.Tiers[i]
		if tier.Above <= last {
			return errors.New("上下文计价阈值无效")
		}
		if err := tier.agentPriceRates.compile(); err != nil {
			return err
		}
		if (tier.CachedInput == nil) != (p.Prices.CachedInput == nil) ||
			(tier.CacheWrite == nil) != (p.Prices.CacheWrite == nil) ||
			(tier.CacheWrite1h == nil) != (p.Prices.CacheWrite1h == nil) {
			return errors.New("上下文档与基础价的计价项目不同")
		}
		last = tier.Above
	}
	return nil
}

func (p *agentPriceRates) compile() error {
	if p.Input == nil || p.Output == nil {
		return errors.New("缺少输入或输出单价")
	}
	values := []*float64{p.Input, p.CachedInput, p.CacheWrite, p.CacheWrite1h, p.Output}
	fallback := []int{0, 0, 0, 2, 4}
	for i, price := range values {
		if price == nil {
			p.micro[i] = p.micro[fallback[i]]
			continue
		}
		scaled := *price * 1e6
		if *price < 0 || *price > 10000 || math.Abs(scaled-math.Round(scaled)) > 1e-5 {
			return errors.New("单价超出价格表范围")
		}
		p.micro[i] = int64(math.Round(scaled))
	}
	return nil
}

func (p *agentOffPeak) isOffPeak(at time.Time) bool {
	weekday := (int(at.Weekday())+6)%7 + 1
	if slices.Contains(p.OffPeakDates, at.Format(time.DateOnly)) || !slices.Contains(p.PeakWeekdays, weekday) {
		return true
	}
	minute := at.Hour()*60 + at.Minute()
	return !slices.ContainsFunc(p.windows, func(r [2]int) bool { return r[0] <= minute && minute < r[1] })
}

func (m *agentPriceModel) rates(at time.Time, fast bool) *agentTieredPrices {
	if fast {
		return m.Fast
	}
	v := &m.agentPriceVariant
	for i := range m.Periods {
		p := &m.Periods[i]
		if (p.start.IsZero() || !at.Before(p.start)) && (p.end.IsZero() || at.Before(p.end)) {
			v = &p.agentPriceVariant
			break
		}
	}
	if v.OffPeak != nil && v.OffPeak.isOffPeak(at.UTC()) {
		return &v.OffPeak.agentTieredPrices
	}
	return &v.agentTieredPrices
}

func (p *agentTieredPrices) cost(atInput int64, classes [5]int64) float64 {
	return p.scaledCost(atInput, classes, 1)
}

func (p *agentTieredPrices) scaledCost(atInput int64, classes [5]int64, scale int64) float64 {
	rates := &p.Prices.micro
	for _, tier := range p.Tiers {
		if atInput <= tier.Above {
			break
		}
		rates = &tier.micro
	}
	var picoUSD int64
	for i, n := range classes {
		picoUSD += min(max(n, 0), 100_000_000) * rates[i]
	}
	microUSD := (picoUSD/1_000_000)*scale + (picoUSD%1_000_000*scale+500_000)/1_000_000
	return float64(microUSD) / 1_000_000
}
