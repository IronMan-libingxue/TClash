package usagetracker

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

var beijing = time.FixedZone("Asia/Shanghai", 8*60*60)

type TrafficConnection struct {
	ID       string    `json:"id"`
	Start    time.Time `json:"start"`
	Upload   int64     `json:"upload"`
	Download int64     `json:"download"`
	Groups   []string  `json:"groups"`
}

type TrafficSnapshot struct {
	At          time.Time           `json:"at"`
	Profile     string              `json:"profile,omitempty"`
	Groups      []string            `json:"groups,omitempty"`
	Connections []TrafficConnection `json:"connections"`
}

type TrafficGroup struct {
	Name     string `json:"name"`
	Scope    string `json:"scope,omitempty"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
}

type TrafficMonth struct {
	Month      string         `json:"month"`
	Since      time.Time      `json:"since"`
	At         time.Time      `json:"at"`
	Upload     int64          `json:"upload"`
	Download   int64          `json:"download"`
	Groups     []TrafficGroup `json:"groups"`
	Incomplete bool           `json:"incomplete"`
}

type Telemetry struct {
	Version  int           `json:"version"`
	Traffic  *TrafficMonth `json:"traffic,omitempty"`
	Activity *Activity     `json:"activity,omitempty"`
}

type trafficLedger struct {
	path     string
	started  time.Time
	months   map[string]TrafficMonth
	previous map[string]TrafficConnection
	scope    string
	invalid  bool
}

func newTrafficLedger(dir string) *trafficLedger {
	l := &trafficLedger{path: filepath.Join(dir, "traffic-ledger.json"), started: time.Now().UTC(), months: map[string]TrafficMonth{}, previous: map[string]TrafficConnection{}}
	data, err := os.ReadFile(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return l
	}
	if err != nil || len(data) > 8<<20 || json.Unmarshal(data, &l.months) != nil || l.months == nil {
		l.invalid = true
	}
	return l
}

func (l *trafficLedger) observe(s TrafficSnapshot) (TrafficMonth, error) {
	if l.invalid {
		return TrafficMonth{}, errors.New("代理组流量记录无法读取，已保留原文件")
	}
	if s.At.IsZero() || s.At.After(time.Now().Add(5*time.Minute)) {
		return TrafficMonth{}, errors.New("代理组流量采样时间无效")
	}
	month := s.At.In(beijing).Format("2006-01")
	current := l.months[month]
	if current.Month == "" {
		current = TrafficMonth{Month: month, Since: s.At, Incomplete: true}
	}
	if s.At.Before(current.At) {
		return current, errors.New("代理组流量采样时间倒退")
	}
	scope := "unknown"
	if s.Profile != "" {
		scope = digest("traffic-profile", s.Profile)
	}
	if l.scope != "" && l.scope != scope {
		// Existing connections cannot safely be attributed across a profile switch.
		l.previous = map[string]TrafficConnection{}
		l.started = s.At
	}
	l.scope = scope
	groupKey := func(scope, name string) string { return scope + "\x00" + name }
	groups := make(map[string]TrafficGroup, len(current.Groups))
	for _, group := range current.Groups {
		if group.Scope == "" {
			group.Scope = "legacy"
		}
		groups[groupKey(group.Scope, group.Name)] = group
	}
	for _, raw := range s.Groups {
		name := label(strings.TrimSpace(raw))
		if name != "" {
			key := groupKey(scope, name)
			if _, exists := groups[key]; !exists {
				groups[key] = TrafficGroup{Name: name, Scope: scope}
			}
		}
	}
	next := make(map[string]TrafficConnection, len(s.Connections))
	for _, item := range s.Connections {
		if item.ID == "" || item.Upload < 0 || item.Download < 0 {
			continue
		}
		old, seen := l.previous[item.ID]
		up, down := int64(0), int64(0)
		if seen && item.Start.Equal(old.Start) && item.Upload >= old.Upload && item.Download >= old.Download && slices.Equal(item.Groups, old.Groups) {
			up, down = item.Upload-old.Upload, item.Download-old.Download
		} else if !seen && !item.Start.IsZero() && !item.Start.Before(l.started) && !item.Start.After(s.At) {
			up, down = item.Upload, item.Download
		}
		unique := map[string]bool{}
		for _, raw := range item.Groups {
			name := label(strings.TrimSpace(raw))
			if name == "" || unique[name] {
				continue
			}
			unique[name] = true
			key := groupKey(scope, name)
			group := groups[key]
			group.Name, group.Scope, group.Upload, group.Download = name, scope, group.Upload+up, group.Download+down
			groups[key] = group
		}
		next[item.ID] = item
	}
	current.At = s.At
	current.Groups = current.Groups[:0]
	current.Upload, current.Download = 0, 0
	for _, group := range groups {
		current.Groups = append(current.Groups, group)
		current.Upload += group.Upload
		current.Download += group.Download
	}
	slices.SortFunc(current.Groups, func(a, b TrafficGroup) int {
		if order := strings.Compare(a.Name, b.Name); order != 0 {
			return order
		}
		return strings.Compare(a.Scope, b.Scope)
	})
	l.months[month] = current
	l.previous = next
	data, err := json.Marshal(l.months)
	if err != nil {
		return TrafficMonth{}, err
	}
	if err = writePrivate(l.path, data); err != nil {
		return TrafficMonth{}, err
	}
	return current, nil
}

func (a *App) RecordTraffic(s TrafficSnapshot) error {
	a.telemetryMu.Lock()
	defer a.telemetryMu.Unlock()
	traffic, err := a.traffic.observe(s)
	if err != nil {
		return err
	}
	a.telemetry.Version, a.telemetry.Traffic = 1, &traffic
	return a.writeTelemetryLocked()
}

func (a *App) writeTelemetryLocked() error {
	data, err := json.Marshal(a.telemetry)
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(a.dir, "shared-telemetry.json"), data)
}

func (a *App) telemetrySnapshot() Telemetry {
	a.telemetryMu.Lock()
	defer a.telemetryMu.Unlock()
	data, _ := json.Marshal(a.telemetry)
	var result Telemetry
	_ = json.Unmarshal(data, &result)
	return result
}

func readLocalTelemetry() *Telemetry {
	root, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	for _, dir := range []string{DesktopCollectorDir(root), filepath.Join(root, "TClash Preview", "collector")} {
		data, err := os.ReadFile(filepath.Join(dir, "shared-telemetry.json"))
		if err != nil || len(data) > 8<<20 {
			continue
		}
		var value Telemetry
		if json.Unmarshal(data, &value) == nil && value.Version == 1 {
			return &value
		}
	}
	return nil
}
