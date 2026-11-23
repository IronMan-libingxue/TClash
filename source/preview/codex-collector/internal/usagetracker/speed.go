package usagetracker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/sessions"
)

type speedReading struct {
	Model string    `json:"model"`
	Rate  *float64  `json:"rate,omitempty"`
	At    time.Time `json:"at"`
	Whole bool      `json:"wholeRequest,omitempty"`
	State string    `json:"state,omitempty"`
}

func activityFresh(activity *Activity, now time.Time) bool {
	if activity == nil || activity.At.IsZero() {
		return false
	}
	age := now.Sub(activity.At)
	return age >= -5*time.Minute && age < 30*time.Second
}

func visibleSpeed(speed speedReading, activity *Activity, now time.Time) speedReading {
	if !activityFresh(activity, now) || (activity.Active == 0 && activity.Uncertain) {
		speed.Rate, speed.State = nil, "unknown"
		return speed
	}
	if activity.Active == 0 {
		zero := 0.0
		speed.Rate, speed.State = &zero, "idle"
		return speed
	}
	speed.State = "active"
	return speed
}

func (a *App) sampleSpeed(ctx context.Context) {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if a.config().Role != "" {
			speed := a.measureSpeed()
			a.mu.Lock()
			a.speed = speed
			a.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func localSpeed() speedReading {
	now := time.Now()
	model := selectedModel(sessions.CallSources())
	calls := sessions.AgentCalls(now.Add(-24*time.Hour), "codex")
	return speedFromCalls(model, calls, now)
}

func speedFromCalls(model string, calls []sessions.Call, now time.Time) speedReading {
	for _, call := range calls {
		if call.Time.After(now) || call.Compaction || call.Error != "" || call.Model == "codex-auto-review" {
			continue
		}
		if model == "" {
			model = label(call.Requested)
			if model == "" {
				model = label(call.Model)
			}
		}
		if model == "" || !strings.EqualFold(model, call.Model) || call.Output <= 0 || call.Millis <= 0 {
			continue
		}
		whole := call.TTFT <= 0 || call.TTFT >= call.Millis
		duration := call.Millis
		if !whole {
			duration -= call.TTFT
		}
		rate := float64(call.Output) * 1000 / float64(duration)
		return speedReading{Model: model, Rate: &rate, At: call.Time, Whole: whole}
	}
	return speedReading{Model: model}
}

func selectedModel(sources []sessions.CallSource) string {
	var recent sessions.CallSource
	for _, source := range sources {
		if source.Agent == "codex" && source.Modified.After(recent.Modified) {
			recent = source
		}
	}
	if recent.Path == "" {
		return ""
	}
	f, err := os.Open(recent.Path)
	if err != nil {
		return ""
	}
	defer f.Close()
	const tailSize = 512 << 10
	start := max(recent.Size-tailSize, 0)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	tail, err := io.ReadAll(io.LimitReader(f, tailSize))
	if err != nil {
		return ""
	}
	if start != 0 {
		_, tail, _ = bytes.Cut(tail, []byte{'\n'})
	}
	return modelFromTail(tail)
}

func modelFromTail(tail []byte) string {
	lines := bytes.Split(tail, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if !bytes.Contains(line, []byte("turn_context")) && !bytes.Contains(line, []byte("thread_settings_applied")) {
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				Type     string `json:"type"`
				Model    string `json:"model"`
				Settings struct {
					Model string `json:"model"`
				} `json:"settings"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		switch {
		case entry.Type == "turn_context":
			if model := label(entry.Payload.Model); model != "" {
				return model
			}
		case entry.Type == "event_msg" && entry.Payload.Type == "thread_settings_applied":
			if model := label(entry.Payload.Settings.Model); model != "" {
				return model
			}
		}
	}
	return ""
}
