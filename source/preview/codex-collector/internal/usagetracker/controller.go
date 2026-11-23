package usagetracker

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type controllerPreferences struct {
	Scaled map[string]bool `json:"scaled,omitempty"`
	Muted  bool            `json:"muted,omitempty"`
}

type controllerState struct {
	mu          sync.Mutex
	preferences controllerPreferences
	devices     map[string]deviceNoticeState
	desktop     DesktopNotices
}

// DesktopNotices is supplied by the macOS A window. The HTTP and B
// collectors have no access to the system notification center.
type DesktopNotices struct {
	Status  func() string
	Request func()
	Show    func(title, body string)
}

func (a *App) loadControllerState() {
	if data, err := os.ReadFile(filepath.Join(a.dir, "controller-preferences.json")); err == nil {
		_ = json.Unmarshal(data, &a.controller.preferences)
	}
	if data, err := os.ReadFile(filepath.Join(a.dir, "controller-notices.json")); err == nil {
		_ = json.Unmarshal(data, &a.controller.devices)
	}
}

func (a *App) AttachDesktopNotices(desktop DesktopNotices) {
	a.controller.mu.Lock()
	a.controller.desktop = desktop
	a.controller.mu.Unlock()
}

func (a *App) RequestDesktopNotices() {
	a.controller.mu.Lock()
	request := a.controller.desktop.Request
	muted := a.controller.preferences.Muted
	a.controller.mu.Unlock()
	if !muted && request != nil && a.config().Role == "a" {
		request()
	}
}

func (a *App) controllerSettings() (controllerPreferences, string) {
	a.controller.mu.Lock()
	prefs, status := a.controller.preferences, a.controller.desktop.Status
	a.controller.mu.Unlock()
	if status == nil {
		return prefs, "unavailable"
	}
	return prefs, status()
}

func (a *App) setDeviceScale(id string, enabled bool) error {
	cfg := a.config()
	if _, ok := cfg.Devices[id]; cfg.Role != "a" || !ok {
		return errors.New("未找到该 B 端设备")
	}
	a.controller.mu.Lock()
	defer a.controller.mu.Unlock()
	prefs := a.controller.preferences
	prefs.Scaled = maps.Clone(prefs.Scaled)
	if enabled {
		if prefs.Scaled == nil {
			prefs.Scaled = map[string]bool{}
		}
		prefs.Scaled[id] = true
	} else {
		delete(prefs.Scaled, id)
	}
	return a.saveControllerPreferences(prefs)
}

func (a *App) setNoticesEnabled(enabled bool) error {
	if a.config().Role != "a" {
		return errors.New("仅 A 端可设置设备通知")
	}
	a.controller.mu.Lock()
	prefs := a.controller.preferences
	prefs.Muted = !enabled
	err := a.saveControllerPreferences(prefs)
	a.controller.mu.Unlock()
	if err == nil && enabled {
		a.RequestDesktopNotices()
	}
	return err
}

func (a *App) saveControllerPreferences(prefs controllerPreferences) error {
	data, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	if err := writePrivate(filepath.Join(a.dir, "controller-preferences.json"), data); err != nil {
		return err
	}
	a.controller.preferences = prefs
	return nil
}

const (
	noticeFast = 1 << iota
	noticeParallel
	noticeUltrafast
)

func activityFlags(look *Activity) int {
	if look == nil || look.Active == 0 {
		return 0
	}
	var flags int
	if look.Fast > 0 {
		flags |= noticeFast
	}
	if look.Active >= 2 {
		flags |= noticeParallel
	}
	if look.Ultrafast != nil && *look.Ultrafast > 0 {
		flags |= noticeUltrafast
	}
	return flags
}

func activityLevel(flags int, active bool) string {
	switch {
	case flags&noticeUltrafast != 0 && flags&noticeParallel != 0:
		return "ultrafast-parallel"
	case flags&noticeFast != 0 && flags&noticeParallel != 0:
		return "special"
	case flags&noticeUltrafast != 0:
		return "ultrafast"
	case flags&noticeParallel != 0:
		return "concurrent"
	case flags&noticeFast != 0:
		return "fast"
	case active:
		return "updated"
	default:
		return ""
	}
}

func observationLimit(id string, policy Policy) time.Duration {
	if id == "local" {
		return 30 * time.Second
	}
	return max(2*time.Minute, 2*policy.interval()+time.Minute)
}

func freshObservation(activity *Activity, now time.Time, limit time.Duration) bool {
	return activity != nil && !activity.At.IsZero() && !activity.At.After(now.Add(5*time.Minute)) && now.Sub(activity.At) <= limit
}

type deviceNoticeState struct {
	Observed  time.Time `json:"observed,omitempty"`
	Request   time.Time `json:"request,omitempty"`
	IdleSince time.Time `json:"idleSince,omitempty"`
	Used      bool      `json:"used,omitempty"`
	Armed     bool      `json:"armed,omitempty"`
	Flags     int       `json:"flags,omitempty"`
}

type noticeObservation struct {
	id       string
	activity *Activity
	request  time.Time
}

func (s deviceNoticeState) advance(look noticeObservation, now time.Time, limit time.Duration) (deviceNoticeState, string) {
	var ordinary bool
	var entered int
	tierObservation := look.activity
	if !s.Observed.IsZero() && ((tierObservation != nil && tierObservation.At.Sub(s.Observed) > limit) ||
		(!freshObservation(tierObservation, now, limit) && now.Sub(s.Observed) > limit)) {
		s.IdleSince = time.Time{}
		s.Armed = false
	}
	if freshObservation(tierObservation, now, limit) && tierObservation.At.After(s.Observed) {
		flags := activityFlags(tierObservation)
		known := flags
		if !tierObservation.Uncertain {
			known |= noticeParallel
			if tierObservation.Active == 0 {
				known |= noticeFast | noticeUltrafast
			}
		}
		if !tierObservation.FastUncertain && !tierObservation.Uncertain {
			known |= noticeFast
			if tierObservation.Ultrafast != nil {
				known |= noticeUltrafast
			}
		}
		entered = flags &^ s.Flags
		s.Flags = (s.Flags &^ known) | flags
		switch {
		case tierObservation.Active == 0 && !tierObservation.Uncertain:
			if s.IdleSince.IsZero() || tierObservation.At.Sub(s.Observed) > limit {
				s.IdleSince = tierObservation.At
				s.Armed = false
			}
			if tierObservation.At.Sub(s.IdleSince) > 10*time.Minute {
				s.Armed = true
			}
		default:
			s.IdleSince = time.Time{}
		}
		s.Observed = tierObservation.At
	}
	if look.request.After(s.Request) && !look.request.After(now.Add(5*time.Minute)) {
		// A newly received file can contain days of older requests.
		// A request must also be recent to turn that upload into an alert.
		recent := now.Sub(look.request) <= limit && (s.IdleSince.IsZero() || look.request.After(s.IdleSince))
		ordinary = look.id != "local" && recent && (!s.Used || s.Armed)
		s.Used = true
		s.Request = look.request
		if recent {
			s.Armed = false
			if !s.IdleSince.IsZero() {
				s.IdleSince = s.Observed
			}
		}
	}
	if entered != 0 {
		// A lower-priority entry must neither re-alert an unchanged higher
		// state, nor generate a second notification within this observation.
		flags := activityFlags(tierObservation)
		level := activityLevel(flags, false)
		if level != activityLevel(flags&^entered, false) {
			s.Used, s.Armed = true, false
			return s, level
		}
		return s, ""
	}
	if ordinary {
		return s, "updated"
	}
	return s, ""
}

func observations(days []Day) []noticeObservation {
	byDevice := map[string]noticeObservation{}
	for _, day := range days {
		look := byDevice[day.Device]
		look.id = day.Device
		if day.Telemetry != nil {
			look.activity = latestActivity(look.activity, day.Telemetry.Activity)
		}
		for _, row := range day.Rows {
			if row.At.After(look.request) {
				look.request = row.At
			}
		}
		byDevice[day.Device] = look
	}
	out := make([]noticeObservation, 0, len(byDevice))
	for _, look := range byDevice {
		out = append(out, look)
	}
	return out
}

func (a *App) observeNotices(cfg config, current []noticeObservation, now time.Time, baseline ...noticeObservation) {
	type message struct{ title, body string }
	var out []message
	a.controller.mu.Lock()
	states := a.controller.devices
	var next map[string]deviceNoticeState
	set := func(id string, value deviceNoticeState) {
		if next == nil {
			next = maps.Clone(states)
			if next == nil {
				next = map[string]deviceNoticeState{}
			}
		}
		next[id] = value
	}
	for _, look := range baseline {
		if _, exists := states[look.id]; !exists {
			state := deviceNoticeState{Request: look.request, Used: !look.request.IsZero()}
			if freshObservation(look.activity, now, observationLimit(look.id, cfg.Policy)) {
				state, _ = state.advance(look, now, observationLimit(look.id, cfg.Policy))
			}
			set(look.id, state)
		}
	}
	for _, look := range current {
		device, paired := cfg.Devices[look.id]
		if look.id != "local" && !paired {
			continue
		}
		source := states
		if next != nil {
			source = next
		}
		old := source[look.id]
		state, level := old.advance(look, now, observationLimit(look.id, cfg.Policy))
		if state == old {
			continue
		}
		set(look.id, state)
		if level != "" {
			slot := device.Name
			if look.id == "local" {
				slot = "Mac 1"
			}
			name := deviceDisplayName(look.id, slot, cfg.DeviceAliases)
			title, body := notificationText(name, level, look.activity)
			out = append(out, message{title, body})
		}
	}
	if next != nil {
		data, err := json.Marshal(next)
		if err == nil {
			err = writePrivate(filepath.Join(a.dir, "controller-notices.json"), data)
		}
		if err != nil {
			out = nil
		} else {
			a.controller.devices = next
		}
	}
	send, muted := a.controller.desktop.Show, a.controller.preferences.Muted
	a.controller.mu.Unlock()
	if send != nil && !muted {
		for _, event := range out {
			send(event.title, event.body)
		}
	}
}

const ultrafastLabel = "⚡⚡ Ultrafast"

func notificationText(name, level string, activity *Activity) (string, string) {
	if level == "updated" || activity == nil {
		return "● 开始使用 Codex", name + " 有新用量"
	}
	parts := []string{name}
	title := "◉ 高并发"
	if level == "ultrafast" || level == "ultrafast-parallel" {
		title = ultrafastLabel
		parts = append(parts, modelOrUnknown(activity.UltraModel))
	} else if level == "fast" || level == "special" {
		title = "⚡ Fast"
		parts = append(parts, modelOrUnknown(activity.FastModel))
	}
	if level == "special" || level == "ultrafast-parallel" || level == "concurrent" {
		if level != "concurrent" {
			title += "＋高并发"
		}
		var projects []string
		for _, project := range activity.Projects {
			if clean := label(project); clean != "" {
				projects = append(projects, clean)
			}
		}
		project := "项目未确认"
		if len(projects) > 0 {
			project = "项目 " + strings.Join(projects, "、")
		}
		parts = append(parts, fmt.Sprintf("%d 个独立会话", activity.Active), project)
	}
	return title, strings.Join(parts, " · ")
}

func latestActivity(current, candidate *Activity) *Activity {
	if candidate != nil && (current == nil || candidate.At.After(current.At)) {
		return candidate
	}
	return current
}

func mergeTelemetry(current, candidate *Telemetry) *Telemetry {
	if current == nil {
		return candidate
	}
	if candidate == nil {
		return current
	}
	merged := *current
	merged.Version = max(current.Version, candidate.Version)
	merged.Activity = latestActivity(merged.Activity, candidate.Activity)
	if candidate.Traffic != nil && (merged.Traffic == nil || candidate.Traffic.At.After(merged.Traffic.At)) {
		merged.Traffic = candidate.Traffic
	}
	return &merged
}

func modelOrUnknown(model string) string {
	if model = label(model); model != "" {
		return model
	}
	return "模型未确认"
}
