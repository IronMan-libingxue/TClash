package usagetracker

import (
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/sessions"
)

// API-equivalent estimates. ChatGPT allowance and credit rates are separate.
var ultrafastListPriceAt = time.Date(2027, time.March, 1, 8, 0, 0, 0, time.UTC)
var datedPricingModel = regexp.MustCompile(`-\d{8}$`)

func standardPricingModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	model = model[strings.LastIndexByte(model, '/')+1:]
	model = strings.TrimSuffix(strings.TrimSuffix(model, "-fast"), "-ultrafast")
	return datedPricingModel.ReplaceAllString(model, "")
}

func pricingMode(model, tier string) string {
	mode := modeOf(tier)
	if mode != "unknown" {
		return mode
	}
	name := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasSuffix(name, "-ultrafast"):
		return "ultrafast"
	case strings.HasSuffix(name, "-fast"):
		return "fast"
	}
	return mode
}

func officialSpeedScale(model, tier string, at time.Time) (int64, bool) {
	id := standardPricingModel(model)
	newModel := id == "gpt-6.2-sol" || id == "gpt-6.2-astra"
	olderUltra := id == "gpt-6.1-sol" || id == "gpt-6-astra"
	switch pricingMode(model, tier) {
	case "normal":
		return 1, true
	case "fast":
		return 2, newModel || olderUltra || id == "gpt-6.1-terra" || id == "gpt-6-sol" || id == "gpt-6-luna"
	case "ultrafast":
		if newModel && at.Before(ultrafastListPriceAt) {
			return 3, true
		}
		return 6, newModel || olderUltra
	default:
		return 0, false
	}
}

func controllerCost(model, tier string, call sessions.Call, price catalog.Price, known bool) (float64, bool) {
	id := standardPricingModel(model)
	newModel := id == "gpt-6.2-sol" || id == "gpt-6.2-astra"
	// The published rates cover a first offline launch with an older
	// bundled catalog. A price present in Models.dev takes precedence.
	if newModel {
		if !known {
			scale := 1.0
			if id == "gpt-6.2-astra" {
				scale = 4
			}
			price = catalog.Price{Input: 2 * scale, Output: 10 * scale, CacheRead: .1 * scale, CacheWrite: 2.5 * scale}
			known = true
		}
		if price.CacheWrite == 0 {
			price.CacheWrite = price.Input * 1.25
		}
		if len(price.Tiers) == 0 {
			price.Tiers = []catalog.Tier{{Above: 272_000, Input: price.Input * 2, Output: price.Output * 1.5,
				CacheRead: price.CacheRead * 2, CacheWrite: price.CacheWrite * 2}}
		}
	}
	if !known {
		return 0, false
	}
	scale, supported := officialSpeedScale(model, tier, call.Time)
	if !supported {
		return 0, false
	}
	writes1h := call.CacheWrite1h
	if newModel {
		// Codex native usage has no recorded 12-hour token split. The
		// upstream one-hour field is for Claude, so it cannot supply one.
		writes1h = 0
	}
	// Select the tier before applying the speed multiplier. CostSplit
	// uses the same calculation as Magpie, including cached input.
	price = price.At(call.Input + call.CacheRead + call.CacheWrite).Times(float64(scale))
	value := price.CostSplit(call.Input, call.Output, call.CacheRead, call.CacheWrite, writes1h)
	return math.Round(value*1e6) / 1e6, true
}
