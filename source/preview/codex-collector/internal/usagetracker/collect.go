package usagetracker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/yetone/magpie/internal/appdir"
	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/sessions"
	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/usage"
)

const retention = 90

type Collector struct {
	mu    sync.Mutex
	files map[string]rolloutCache
}

type rolloutCache struct {
	size     int64
	mod      time.Time
	readings []Quota
}

func UseLocalCodex(dataDir string) error {
	// Upstream's parser may write its indexes. Keep all of them in this
	// application's directory, including when a portable Magpie is nearby.
	appdir.UseExecutable("")
	if err := os.Setenv("XDG_CONFIG_HOME", filepath.Join(dataDir, "parser")); err != nil {
		return err
	}
	if err := os.Setenv("XDG_CACHE_HOME", filepath.Join(dataDir, "cache")); err != nil {
		return err
	}
	if err := settings.Save(settings.Settings{NoAccountDiscovery: true}); err != nil {
		return err
	}
	return settings.SetSessionReads(func(*settings.SessionReads) *settings.SessionReads {
		return &settings.SessionReads{Asked: true, Agents: map[string]bool{"codex": true}}
	})
}

func digest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}

func accountKey(id string) string {
	if id == "" {
		return ""
	}
	return digest("account", id)
}

func currentAccount() string {
	id, err := provider.ReadCodexAccountID()
	if err != nil {
		return ""
	}
	return accountKey(id)
}

func (c *Collector) Days(ctx context.Context, device, name string, extra []Quota, corrections []AccountCorrection) ([]Day, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC()
	since := now.Truncate(24*time.Hour).AddDate(0, 0, 1-retention)
	if _, err := os.Stat(filepath.Join(sessions.CodexDir(), "sessions")); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("无法读取本机 Codex 日志目录")
	}
	days := map[string]*Day{}
	dayAt := func(at time.Time) *Day {
		date := at.UTC().Format(time.DateOnly)
		if days[date] == nil {
			days[date] = &Day{Device: device, Name: name, Date: date, At: now}
		}
		return days[date]
	}
	// Record the most recent official reading per day/window.
	seenQuota := map[string]Quota{}
	recordQuota := func(q Quota) {
		if !q.valid() || q.At.Before(since) {
			return
		}
		key := q.At.UTC().Format(time.DateOnly) + "/" + q.id()
		if old, ok := seenQuota[key]; !ok || q.At.After(old.At) {
			seenQuota[key] = q
		}
	}
	for _, q := range extra {
		recordQuota(q)
	}
	current := currentAccount()
	calls := usage.NativeCodexCalls(since)
	attributions, err := c.verifiedAttributions(ctx, calls, extra, corrections)
	if err != nil {
		return nil, err
	}
	processed := map[string]string{}
	prices := map[string]catalog.Price{}
	priceKnown := map[string]bool{}
	for _, call := range calls {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if call.Time.Before(since) || call.Time.After(now.Add(5*time.Minute)) {
			continue
		}
		account := ""
		if call.Upstream == "openai" || call.Upstream == "" {
			if cached, ok := processed[call.File]; ok {
				account = cached
			} else if call.File != "" {
				readings, err := c.quotaReadings(call.File)
				if err != nil {
					return nil, errors.New("读取 Codex 日志中的额度失败，请重试")
				}
				if len(readings) > 0 {
					account = accountKey(call.AccountID)
					if account == "" && device != "local" {
						account = current
					}
					for _, q := range readings {
						q.Account = account
						target, ok := attributions.quotas[attributionQuotaKey(call.File, account, q)]
						if ok {
							q.Account = target
						}
						if device != "local" || ok {
							recordQuota(q)
						}
					}
				}
				processed[call.File] = account
			}
		}
		model := label(call.Model)
		if model == "" {
			model = "模型未知"
		}
		if _, ok := priceKnown[model]; !ok {
			prices[model], priceKnown[model] = sessions.PriceOf(settings.Settings{}, model)
		}
		project, projectName := projectLabel(call.Cwd)
		thread := call.ThreadID
		if thread == "" {
			thread = call.Session
		}
		if thread == "" {
			thread = call.File
		}
		id := requestID(call, thread)
		corrected, confirmed := attributions.rows[id]
		if confirmed && (device == "local" || account == corrected.from) {
			account = corrected.to
		}
		var evidence []Quota
		if device == "local" && call.File != "" {
			evidence, _ = c.quotaReadings(call.File)
		}
		day := dayAt(call.Time)
		day.Rows = append(day.Rows, Request{
			ID: id, At: call.Time.UTC(), Project: project, ProjectName: projectName,
			Session: digest("session", thread), Model: model, Effort: strings.ToLower(label(call.Effort)), Account: account,
			Tokens: Tokens{Input: int64(call.Input), Output: int64(call.Output), Cache: int64(call.CacheRead + call.CacheWrite)},
			USD:    prices[model].At(0).CostSplit(call.Input, call.Output, call.CacheRead, call.CacheWrite, call.CacheWrite1h),
			Priced: priceKnown[model], sourceAccount: accountKey(call.AccountID), duration: time.Duration(call.Millis) * time.Millisecond,
			confirmed: confirmed, quotaEvidence: evidence,
		})
	}
	for _, q := range seenQuota {
		day := dayAt(q.At)
		day.Quotas = append(day.Quotas, q)
	}
	return sortedDays(days), nil
}

func projectLabel(path string) (string, string) {
	if path == "" {
		return "unknown", "未记录项目"
	}
	normalized := strings.ReplaceAll(path, "\\", "/")
	normalized = strings.TrimRight(normalized, "/")
	name := label(normalized[strings.LastIndex(normalized, "/")+1:])
	if name == "" || strings.HasSuffix(name, ":") {
		name = "未记录项目"
	}
	return digest("project", normalized), name
}

func requestID(call sessions.Call, thread string) string {
	if call.ResponseID != "" {
		return digest("response", call.ResponseID, thread, call.Time.UTC().Format(time.RFC3339Nano))
	}
	return digest("request", thread, call.Time.UTC().Format(time.RFC3339Nano),
		call.Model, call.RequestID, call.Msg)
}

type loggedWindow struct {
	Used    *float64 `json:"used_percent"`
	Minutes int64    `json:"window_minutes"`
	End     int64    `json:"resets_at"`
}

func (w loggedWindow) reading(at time.Time, plan string) (Quota, bool) {
	if w.Used == nil || w.Minutes <= 0 || w.Minutes > 31*24*60 || w.End <= 0 {
		return Quota{}, false
	}
	end := time.Unix(w.End, 0).UTC()
	q := Quota{Start: end.Add(-time.Duration(w.Minutes) * time.Minute), End: end, At: at.UTC(),
		Used: *w.Used, Plan: plan}
	return q, !q.At.Before(q.Start) && q.At.Before(q.End)
}

func (c *Collector) quotaReadings(path string) ([]Quota, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if cached := c.files[path]; cached.size == info.Size() && cached.mod.Equal(info.ModTime()) {
		return cached.readings, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var source io.Reader = file
	if strings.HasSuffix(path, ".zst") {
		reader, err := zstd.NewReader(file, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		source = reader
	}
	got, err := readQuotaLines(source)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(got, func(a, b Quota) int { return a.At.Compare(b.At) })
	if c.files == nil {
		c.files = map[string]rolloutCache{}
	}
	c.files[path] = rolloutCache{size: info.Size(), mod: info.ModTime(), readings: got}
	return got, nil
}

func readQuotaLines(source io.Reader) ([]Quota, error) {
	var result []Quota
	reader := bufio.NewReaderSize(source, 64<<10)
	for {
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = reader.ReadSlice('\n')
			}
			if errors.Is(err, io.EOF) {
				return result, nil
			}
			if err != nil {
				return nil, err
			}
			continue
		}
		if errors.Is(err, io.EOF) {
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		if !bytes.Contains(line, []byte(`"token_count"`)) || !bytes.Contains(line, []byte(`"rate_limits"`)) {
			continue
		}
		var event struct {
			Type    string    `json:"type"`
			At      time.Time `json:"timestamp"`
			Payload struct {
				Type string `json:"type"`
				Info struct {
					Last struct {
						Input  int64 `json:"input_tokens"`
						Output int64 `json:"output_tokens"`
						Cached int64 `json:"cached_input_tokens"`
						Write  int64 `json:"cache_write_input_tokens"`
					} `json:"last_token_usage"`
				} `json:"info"`
				Rate struct {
					ID        string       `json:"limit_id"`
					Plan      string       `json:"plan_type"`
					Primary   loggedWindow `json:"primary"`
					Secondary loggedWindow `json:"secondary"`
				} `json:"rate_limits"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &event) != nil || event.Type != "event_msg" ||
			event.Payload.Type != "token_count" || (event.Payload.Rate.ID != "" && event.Payload.Rate.ID != "codex") {
			continue
		}
		rate := event.Payload.Rate
		for _, w := range []loggedWindow{rate.Primary, rate.Secondary} {
			if q, ok := w.reading(event.At, rate.Plan); ok {
				q.sourceEventHash = digest("quota-event", string(line))
				last := event.Payload.Info.Last
				q.sourceTokens = Tokens{Input: max(0, last.Input-last.Cached-last.Write), Output: last.Output, Cache: last.Cached + last.Write}
				result = append(result, q)
			}
		}
	}
}

// ReadOfficial makes only the quota GET. Codex owns its login and refreshes
// its credentials itself. No authentication token is persisted by this app.
func ReadOfficial(ctx context.Context) ([]Quota, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	account, plan, windows, err := provider.ReadCodexAllowance(ctx)
	if err != nil || account == "" {
		return nil, errors.New("暂时无法读取官方额度，请检查 Codex 登录和网络")
	}
	now := time.Now().UTC()
	var got []Quota
	for _, window := range windows {
		start, end, ok := window.Bounds(now)
		if !ok {
			continue
		}
		q := Quota{Account: accountKey(account), Plan: plan, Start: start.UTC(), End: end.UTC(),
			At: now, Used: window.Used, Direct: true}
		if q.valid() {
			got = append(got, q)
		}
	}
	if len(got) == 0 {
		return nil, errors.New("OpenAI 未返回带重置时间的额度")
	}
	return got, nil
}
