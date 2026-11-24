package usagetracker

import (
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const localHistoryFile = "usage-history.json"

func (a *App) EnableLocalHistory() error {
	a.localHistoryEnabled = true
	path := filepath.Join(a.dir, localHistoryFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var days []Day
	if err = json.Unmarshal(data, &days); err != nil {
		return errors.New("新版 TClash 的本机历史无法读取")
	}
	a.localHistoryDigest = sha256.Sum256(data)
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	a.readAt = info.ModTime()
	a.local = mergeLocalHistory(a.cfg, days, nil, time.Now())
	return nil
}

func (a *App) saveLocalHistory(days []Day) error {
	stored := slices.Clone(days)
	for i := range stored {
		stored[i].At = time.Time{}
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if a.localHistoryDigest == digest {
		return nil
	}
	if err = writePrivate(filepath.Join(a.dir, localHistoryFile), data); err != nil {
		return err
	}
	a.localHistoryDigest = digest
	return nil
}

func mergeLocalHistory(cfg config, saved, scanned []Day, now time.Time) []Day {
	type entry struct {
		day    Day
		rows   map[string]Request
		quotas map[string]Quota
	}
	byDate := map[string]*entry{}
	cutoff := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, 1-retention)
	device := cfg.device()
	for _, source := range [][]Day{saved, scanned} {
		for _, day := range source {
			date, err := time.Parse(time.DateOnly, day.Date)
			if err != nil || date.Before(cutoff) || day.Device != device.ID {
				continue
			}
			item := byDate[day.Date]
			if item == nil {
				item = &entry{rows: map[string]Request{}, quotas: map[string]Quota{}}
				byDate[day.Date] = item
			}
			item.day = Day{Device: device.ID, Name: cfg.Name, Date: day.Date, At: day.At, CountSince: cfg.CountSince}
			if item.day.At.IsZero() {
				item.day.At = now.UTC()
			}
			for _, row := range day.Rows {
				if row.ID != "" && !row.At.Before(cfg.CountSince) {
					item.rows[row.ID] = row
				}
			}
			for _, quota := range day.Quotas {
				if old := item.quotas[quota.id()]; quota.At.After(old.At) && !quota.At.Before(cfg.CountSince) {
					item.quotas[quota.id()] = quota
				}
			}
		}
	}
	days := make([]Day, 0, len(byDate))
	for _, entry := range byDate {
		day := entry.day
		for _, row := range entry.rows {
			day.Rows = append(day.Rows, row)
		}
		for _, quota := range entry.quotas {
			day.Quotas = append(day.Quotas, quota)
		}
		if len(day.Rows) == 0 && len(day.Quotas) == 0 {
			continue
		}
		slices.SortFunc(day.Rows, func(a, b Request) int {
			if order := a.At.Compare(b.At); order != 0 {
				return order
			}
			return cmp.Compare(a.ID, b.ID)
		})
		slices.SortFunc(day.Quotas, func(a, b Quota) int { return cmp.Compare(a.id(), b.id()) })
		days = append(days, day)
	}
	slices.SortFunc(days, func(a, b Day) int { return cmp.Compare(a.Date, b.Date) })
	return days
}
