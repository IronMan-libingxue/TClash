package usagetracker

import (
	"regexp"
	"slices"
	"strings"
)

type agentNormalization struct {
	FreeProviders, CloudSuffixes, BedrockRegions, BedrockVendors    []string
	FreeSuffixes, VariantSuffixes, FastSuffixes, DateSuffixPrefixes []string
	DotHyphenPrefixes                                               []string
	Rewrites                                                        []struct{ Prefix, To string }
	ModifierSuffixes                                                []struct {
		Prefix   string
		Suffixes []string
	}
}

type agentModelMatch struct {
	model  *agentPriceModel
	free   bool
	fast   bool
	reason string
}

var (
	agentBrackets       = regexp.MustCompile(`\[[^\]]*\]`)
	agentCustomIndex    = regexp.MustCompile(`-\p{Nd}+$`)
	agentBedrockVersion = regexp.MustCompile(`-v[0-9]+(?::[0-9]+)?$`)
	agentVertexDate     = regexp.MustCompile(`^[0-9]{8}$`)
	agentDateSuffix     = regexp.MustCompile(`-[0-9]{8}$`)
)

func agentModelKey(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(agentBrackets.ReplaceAllString(s, "")), " "))
}

func agentHasPrefix(value string, prefixes []string) bool {
	return slices.ContainsFunc(prefixes, func(p string) bool { return p != "" && strings.HasPrefix(value, p) })
}

func agentHasSuffix(value string, suffixes []string) bool {
	return slices.ContainsFunc(suffixes, func(s string) bool { return s != "" && strings.HasSuffix(value, s) })
}

func trimAgentSuffixes(value string, suffixes []string, minLength int) string {
	for {
		prior := value
		for _, suffix := range suffixes {
			if suffix != "" && len(value) > minLength+len(suffix) {
				value = strings.TrimSuffix(value, suffix)
			}
		}
		if prior == value {
			return value
		}
	}
}

func (n agentNormalization) cloudKey(k string) string {
	if region, rest, ok := strings.Cut(k, "."); ok && slices.Contains(n.BedrockRegions, region) {
		if vendor, _, ok := strings.Cut(rest, "."); ok && slices.Contains(n.BedrockVendors, vendor) {
			k = rest
		}
	}
	if vendor, rest, ok := strings.Cut(k, "."); ok && slices.Contains(n.BedrockVendors, vendor) {
		k = agentBedrockVersion.ReplaceAllString(rest, "")
	}
	if i := strings.LastIndexByte(k, '@'); i >= 0 {
		base, tag := k[:i], k[i+1:]
		k = base
		if agentVertexDate.MatchString(tag) {
			k += "-" + tag
		}
	}
	return k
}

type agentModelCandidate struct {
	key        string
	fast, free bool
}

func (n agentNormalization) candidates(k string) []agentModelCandidate {
	out := []agentModelCandidate{{key: k}}
	if raw, ok := strings.CutPrefix(k, "custom:"); ok {
		k = strings.Trim(agentCustomIndex.ReplaceAllString(raw, ""), " -")
		out = append(out, agentModelCandidate{key: k})
	}
	if i := strings.LastIndexByte(k, '/'); i >= 0 {
		k = k[i+1:]
		out = append(out, agentModelCandidate{key: k})
	}
	k = n.cloudKey(k)
	out = append(out, agentModelCandidate{key: k})
	for _, suffix := range n.FreeSuffixes {
		if suffix != "" && len(k) > len(suffix) && strings.HasSuffix(k, suffix) {
			return append(out, agentModelCandidate{free: true})
		}
	}
	k = trimAgentSuffixes(k, n.VariantSuffixes, 0)
	out = append(out, agentModelCandidate{key: k})
	for _, rule := range n.Rewrites {
		if rest, ok := strings.CutPrefix(k, rule.Prefix); ok && rule.Prefix != "" {
			k = rule.To + rest
			break
		}
	}
	out = append(out, agentModelCandidate{key: k})
	var fast bool
	for _, suffix := range n.FastSuffixes {
		if rest, ok := strings.CutSuffix(k, suffix); ok && suffix != "" && rest != "" {
			k, fast = rest, true
			out = append(out, agentModelCandidate{key: k, fast: true})
			break
		}
	}
	if agentHasPrefix(k, n.DateSuffixPrefixes) {
		k = agentDateSuffix.ReplaceAllString(k, "")
	}
	out = append(out, agentModelCandidate{key: k, fast: fast})
	for _, rule := range n.ModifierSuffixes {
		if rule.Prefix != "" && strings.HasPrefix(k, rule.Prefix) {
			k = trimAgentSuffixes(k, rule.Suffixes, len(rule.Prefix))
			break
		}
	}
	out = append(out, agentModelCandidate{key: k, fast: fast})
	if agentHasPrefix(k, n.DotHyphenPrefixes) {
		k = strings.ReplaceAll(k, ".", "-")
	}
	return append(out, agentModelCandidate{key: k, fast: fast})
}

func (t *agentPriceTable) resolve(model, provider string) agentModelMatch {
	n := t.Normalize
	if slices.Contains(n.FreeProviders, strings.ToLower(strings.TrimSpace(provider))) &&
		!agentHasSuffix(strings.ToLower(strings.TrimSpace(model)), n.CloudSuffixes) {
		return agentModelMatch{free: true}
	}
	key := agentModelKey(model)
	if key == "" {
		return agentModelMatch{reason: "缺少模型"}
	}
	for _, candidate := range n.candidates(key) {
		if candidate.free {
			return agentModelMatch{free: true}
		}
		if m := t.byKey[candidate.key]; m != nil {
			if candidate.fast && m.Fast == nil && !m.Free {
				return agentModelMatch{reason: "价格表没有 Fast 价格"}
			}
			return agentModelMatch{model: m, free: m.Free, fast: candidate.fast}
		}
		for _, rule := range t.Unknown {
			if (rule.Match == "exact" && candidate.key == rule.Value) ||
				(rule.Match == "prefix" && strings.HasPrefix(candidate.key, rule.Value)) {
				return agentModelMatch{reason: "无公开价格"}
			}
		}
	}
	return agentModelMatch{reason: "价格表没有此模型"}
}
