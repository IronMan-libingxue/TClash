package usagetracker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/sessions"
)

type ActivityInterval struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Count int       `json:"count"`
	New   bool      `json:"new"`
}

type Activity struct {
	At            time.Time          `json:"at"`
	Active        int                `json:"active"`
	Fast          int                `json:"fast"`
	Ultrafast     *int               `json:"ultrafast,omitempty"`
	FastModel     string             `json:"fastModel,omitempty"`
	UltraModel    string             `json:"ultrafastModel,omitempty"`
	Projects      []string           `json:"projects,omitempty"`
	Uncertain     bool               `json:"uncertain"`
	FastUncertain bool               `json:"fastUncertain"`
	Incomplete    bool               `json:"incomplete"`
	Intervals     []ActivityInterval `json:"intervals,omitempty"`
}

type ActivityStats struct {
	Occurrences int   `json:"occurrences"`
	Seconds     int64 `json:"seconds"`
	Peak        int   `json:"peak"`
	Incomplete  bool  `json:"incomplete"`
}

type activityEvent struct {
	At      time.Time
	Session string
	Kind    string
	Turn    string
	Tier    string
	Model   string
	Project string
}

type activitySourceFn func() ([]sessions.CallSource, map[string]sessions.CodexIdentity)

type activityFile struct {
	offset   int64
	size     int64
	session  string
	modified time.Time
	boundary bool
}

type activitySession struct {
	running bool
	trusted bool
	turn    string
	tier    string
	model   string
	project string
	last    time.Time
}

type activityMonitor struct {
	controller bool
	started    time.Time
	files      map[string]*activityFile
	sessions   map[string]*activitySession
	identities map[string]sessions.CodexIdentity
	intervals  []ActivityInterval
	open       *ActivityInterval
	lastCount  int
	incomplete bool
}

func newActivityMonitor(now time.Time) *activityMonitor {
	return &activityMonitor{started: now, files: map[string]*activityFile{}, sessions: map[string]*activitySession{}, incomplete: true}
}

func (m *activityMonitor) parseLine(line []byte, session string) (activityEvent, bool) {
	var e struct {
		Type    string    `json:"type"`
		At      time.Time `json:"timestamp"`
		Payload struct {
			Type        string  `json:"type"`
			Turn        string  `json:"turn_id"`
			ServiceTier *string `json:"service_tier"`
			Model       string  `json:"model"`
			Cwd         string  `json:"cwd"`
			Settings    *struct {
				ServiceTier *string `json:"service_tier"`
			} `json:"settings"`
			ThreadSettings *struct {
				ServiceTier *string `json:"service_tier"`
				Model       string  `json:"model"`
			} `json:"thread_settings"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &e) != nil || e.At.IsZero() {
		return activityEvent{}, false
	}
	result := activityEvent{At: e.At, Session: session, Kind: e.Payload.Type, Turn: e.Payload.Turn}
	if m.controller && e.Type == "turn_context" {
		result.Kind, result.Model = "turn_context", label(e.Payload.Model)
		if e.Payload.Cwd != "" {
			if id, name := projectLabel(e.Payload.Cwd); id != "unknown" && name != "未记录项目" {
				result.Project = name
			}
		}
		if e.Payload.ServiceTier != nil {
			result.Tier = modeOf(*e.Payload.ServiceTier)
		}
		return result, true
	}
	if e.Type != "event_msg" {
		return activityEvent{}, false
	}
	switch result.Kind {
	case "task_started", "task_complete", "turn_aborted", "task_aborted":
		return result, true
	case "thread_settings_applied":
		tier := e.Payload.ServiceTier
		if e.Payload.ThreadSettings != nil {
			tier = e.Payload.ThreadSettings.ServiceTier
			if m.controller {
				result.Model = label(e.Payload.ThreadSettings.Model)
			}
		}
		if e.Payload.Settings != nil {
			tier = e.Payload.Settings.ServiceTier
		}
		result.Tier = "unknown"
		if tier != nil {
			result.Tier = modeOf(*tier)
			if !m.controller && result.Tier == "ultrafast" {
				result.Tier = "unknown"
			}
		}
		return result, true
	default:
		return activityEvent{}, false
	}
}

func modeOf(tier string) string {
	switch strings.ToLower(tier) {
	case "normal", "default", "standard":
		return "normal"
	case "priority", "fast":
		return "fast"
	case "ultrafast", "ultra_fast":
		return "ultrafast"
	default:
		return "unknown"
	}
}

func activityRead(path string, start, size int64, session string, m *activityMonitor) ([]activityEvent, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, start, err
	}
	defer f.Close()
	if _, err = f.Seek(start, io.SeekStart); err != nil {
		return nil, start, err
	}
	r := bufio.NewReaderSize(io.LimitReader(f, size-start), 64<<10)
	pos := start
	var events []activityEvent
	for {
		begin := pos
		var line []byte
		oversized := false
		for {
			fragment, readErr := r.ReadSlice('\n')
			pos += int64(len(fragment))
			if len(line)+len(fragment) <= 1<<20 && !oversized {
				line = append(line, fragment...)
			} else {
				oversized = true
				line = nil
			}
			if errors.Is(readErr, bufio.ErrBufferFull) {
				continue
			}
			if errors.Is(readErr, io.EOF) {
				return events, begin, nil
			}
			if readErr != nil {
				return events, begin, readErr
			}
			break
		}
		if !oversized {
			if event, ok := m.parseLine(line, session); ok {
				events = append(events, event)
			}
		}
	}
}

func (m *activityMonitor) apply(e activityEvent, live bool) {
	s := m.sessions[e.Session]
	if s == nil {
		s = &activitySession{tier: "unknown"}
		m.sessions[e.Session] = s
	}
	if e.At.Before(s.last) {
		return
	}
	s.last = e.At
	switch e.Kind {
	case "task_started":
		if m.controller && s.turn != e.Turn {
			s.project = ""
		}
		s.running, s.turn = true, e.Turn
		s.trusted = live && !e.At.Before(m.started)
	case "task_complete", "turn_aborted", "task_aborted":
		if e.Turn == "" || s.turn == "" || e.Turn == s.turn {
			s.running = false
			s.trusted = live
		}
	case "thread_settings_applied":
		s.tier = e.Tier
		if e.Model != "" {
			s.model = e.Model
		}
		if live && s.running {
			s.trusted = true
		}
	case "turn_context":
		if s.running && (e.Turn == "" || s.turn == "" || e.Turn == s.turn) {
			s.model, s.project = e.Model, e.Project
			if e.Tier != "" {
				s.tier = e.Tier
			}
		}
	case "observation":
		if live && s.running {
			s.trusted = true
		}
	}
	if live {
		m.updateInterval(e.At)
	}
}

func (m *activityMonitor) counts(now time.Time) (int, int, bool, bool) {
	activity := m.runningActivity(now)
	return activity.Active, activity.Fast, activity.Uncertain, activity.FastUncertain
}

func (m *activityMonitor) runningActivity(now time.Time) Activity {
	main := map[string]*activitySession{}
	unknown, fastUnknown := false, false
	for id, state := range m.sessions {
		identity, ok := m.identities[id]
		if !state.running {
			continue
		}
		if !ok || identity.Role == "unknown" {
			unknown = true
			continue
		}
		if identity.Role != "main" {
			continue
		}
		if !state.trusted || now.Sub(state.last) > 15*time.Minute {
			unknown = true
			continue
		}
		main[id] = state
	}
	fastRoots := map[string]bool{}
	ultraRoots := map[string]bool{}
	var fastModel, ultraModel string
	var fastAt, ultraAt time.Time
	for id, state := range m.sessions {
		if !state.running {
			continue
		}
		identity, ok := m.identities[id]
		if !ok {
			continue
		}
		root := identity.RootSession
		if identity.Role == "main" {
			root = id
		}
		if main[root] == nil {
			continue
		}
		if !state.trusted || now.Sub(state.last) > 15*time.Minute {
			fastUnknown = true
			continue
		}
		if state.tier == "fast" {
			fastRoots[root] = true
			if state.model != "" && (state.last.After(fastAt) || (state.last.Equal(fastAt) && state.model < fastModel)) {
				fastModel, fastAt = state.model, state.last
			}
		} else if m.controller && state.tier == "ultrafast" {
			ultraRoots[root] = true
			if state.model != "" && (state.last.After(ultraAt) || (state.last.Equal(ultraAt) && state.model < ultraModel)) {
				ultraModel, ultraAt = state.model, state.last
			}
		} else if state.tier != "normal" {
			fastUnknown = true
		}
	}
	activity := Activity{At: now, Active: len(main), Fast: len(fastRoots), Uncertain: unknown, FastUncertain: fastUnknown}
	if m.controller {
		count := len(ultraRoots)
		activity.Ultrafast, activity.FastModel, activity.UltraModel = &count, fastModel, ultraModel
		if len(main) >= 2 {
			for _, state := range main {
				if state.project == "" {
					activity.Projects = nil
					break
				}
				activity.Projects = append(activity.Projects, state.project)
			}
			slices.Sort(activity.Projects)
			activity.Projects = slices.Compact(activity.Projects)
		}
	}
	return activity
}

func (m *activityMonitor) updateInterval(at time.Time) {
	count, _, uncertain, _ := m.counts(at)
	if count == m.lastCount {
		return
	}
	if m.open != nil {
		if at.After(m.open.Start) {
			closed := *m.open
			closed.End = at
			m.intervals = append(m.intervals, closed)
		}
		m.open = nil
	}
	if count > 0 {
		m.open = &ActivityInterval{Start: at, Count: count, New: count >= 2 && m.lastCount < 2 && !uncertain}
	}
	m.lastCount = count
}

func (m *activityMonitor) scan(sources []sessions.CallSource, ids map[string]sessions.CodexIdentity, now time.Time) Activity {
	m.identities = ids
	slices.SortFunc(sources, func(a, b sessions.CallSource) int { return a.Modified.Compare(b.Modified) })
	var live []activityEvent
	uncertain := false
	for _, source := range sources {
		if source.Agent != "codex" || strings.HasSuffix(source.Path, ".zst") {
			continue
		}
		cursor := m.files[source.Path]
		if cursor == nil && source.Modified.Before(now.Add(-48*time.Hour)) {
			continue
		}
		session := strings.TrimPrefix(source.Key, "codex:")
		if session == "" {
			uncertain = true
			continue
		}
		if cursor == nil || source.Size < cursor.offset {
			cursor = &activityFile{session: session}
			m.files[source.Path] = cursor
			start := max(int64(0), source.Size-(128<<20))
			if start > 0 {
				f, err := os.Open(source.Path)
				if err != nil {
					uncertain = true
					continue
				}
				_, _ = f.Seek(start, io.SeekStart)
				r := bufio.NewReader(f)
				_, _ = r.ReadBytes('\n')
				pos, _ := f.Seek(0, io.SeekCurrent)
				start = pos - int64(r.Buffered())
				_ = f.Close()
			}
			events, off, err := activityRead(source.Path, start, source.Size, session, m)
			if err != nil {
				uncertain = true
				continue
			}
			cursor.offset, cursor.size, cursor.modified = off, source.Size, source.Modified
			for _, event := range events {
				if event.Kind == "task_started" || event.Kind == "task_complete" || event.Kind == "turn_aborted" || event.Kind == "task_aborted" {
					cursor.boundary = true
				}
				if !event.At.Before(m.started) {
					live = append(live, event)
				} else {
					m.apply(event, false)
				}
			}
			if !cursor.boundary && source.Modified.After(now.Add(-15*time.Minute)) {
				uncertain = true
			}
			continue
		}
		if source.Size <= cursor.offset {
			if !cursor.boundary && source.Modified.After(now.Add(-15*time.Minute)) {
				uncertain = true
			}
			continue
		}
		events, off, err := activityRead(source.Path, cursor.offset, source.Size, session, m)
		if err != nil {
			uncertain = true
			continue
		}
		cursor.offset, cursor.size, cursor.modified = off, source.Size, source.Modified
		for _, event := range events {
			if event.Kind == "task_started" || event.Kind == "task_complete" || event.Kind == "turn_aborted" || event.Kind == "task_aborted" {
				cursor.boundary = true
			}
		}
		live = append(live, events...)
		if source.Modified.After(m.started) {
			live = append(live, activityEvent{At: source.Modified, Session: session, Kind: "observation"})
		}
		if !cursor.boundary && source.Modified.After(now.Add(-15*time.Minute)) {
			uncertain = true
		}
	}
	slices.SortFunc(live, func(a, b activityEvent) int { return a.At.Compare(b.At) })
	for _, event := range live {
		m.apply(event, true)
	}
	m.updateInterval(now)
	activity := m.runningActivity(now)
	intervals := slices.Clone(m.intervals)
	if m.open != nil && now.After(m.open.Start) {
		open := *m.open
		open.End = now
		intervals = append(intervals, open)
	}
	activity.Uncertain = activity.Uncertain || uncertain
	activity.Incomplete, activity.Intervals = m.incomplete, intervals
	return activity
}

func activityStats(activity *Activity, q *Quota) *ActivityStats {
	if activity == nil || q == nil {
		return nil
	}
	stats := &ActivityStats{Incomplete: activity.Incomplete}
	for _, interval := range activity.Intervals {
		start, end := interval.Start, interval.End
		if start.Before(q.Start) {
			start = q.Start
		}
		if end.After(q.until()) {
			end = q.until()
		}
		if !end.After(start) {
			continue
		}
		stats.Peak = max(stats.Peak, interval.Count)
		if interval.Count >= 2 {
			stats.Seconds += int64(end.Sub(start).Seconds())
			if interval.New && !interval.Start.Before(q.Start) && interval.Start.Before(q.until()) {
				stats.Occurrences++
			}
		}
	}
	return stats
}

func loadActivityHistory(path string) ([]ActivityInterval, bool) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, true
	}
	if err != nil || len(data) > 8<<20 {
		return nil, false
	}
	var saved []ActivityInterval
	if json.Unmarshal(data, &saved) != nil {
		return nil, false
	}
	return saved, true
}

func (a *App) sampleActivity(ctx context.Context) {
	monitor := newActivityMonitor(time.Now().UTC())
	path := filepath.Join(a.dir, "activity-history.json")
	var historyWritable bool
	monitor.intervals, historyWritable = loadActivityHistory(path)
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		cfg := a.config()
		monitor.controller = true
		sources, ids := a.activitySources()
		activity := monitor.scan(sources, ids, time.Now().UTC())
		cutoff := activity.At.AddDate(0, 0, -retention)
		monitor.intervals = slices.DeleteFunc(monitor.intervals, func(x ActivityInterval) bool { return x.End.Before(cutoff) })
		if historyWritable {
			if data, err := json.Marshal(activity.Intervals); err == nil {
				_ = writePrivate(path, data)
			}
		}
		a.telemetryMu.Lock()
		a.telemetry.Version, a.telemetry.Activity = 1, &activity
		_ = a.writeTelemetryLocked()
		a.telemetryMu.Unlock()
		if cfg.Role == "a" {
			a.observeNotices(cfg, []noticeObservation{{id: "local", activity: &activity}}, activity.At)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
