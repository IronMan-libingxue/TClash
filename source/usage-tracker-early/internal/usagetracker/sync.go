package usagetracker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const controls = "controls"
const policyFile = "policy.magpie-quotas"
const daySuffix = ".magpie-usage"

func (a *App) syncNow(ctx context.Context, cfg config) error {
	if cfg.Repo == "" || cfg.Token == "" {
		return errors.New("请先连接 GitHub 私有仓库")
	}
	store, err := a.openRepo(ctx, cfg.Repo, cfg.Token)
	if err != nil {
		return err
	}
	if cfg.Role == "a" {
		err = a.syncA(ctx, store, &cfg)
	} else {
		err = a.syncB(ctx, store, &cfg)
	}
	if err == nil {
		cfg.LastSync = time.Now()
	}
	// A successful part of a sync is still useful if the next file fails.
	return errors.Join(err, a.save(cfg))
}

func (a *App) syncA(ctx context.Context, store transport, cfg *config) error {
	if cfg.Policy.Version > cfg.Published {
		body, err := json.Marshal(cfg.Policy)
		if err != nil {
			return err
		}
		if err = store.Write(ctx, controls, policyFile, sign(policyFormat, body, cfg.Signer)); err != nil {
			return err
		}
		cfg.Published = cfg.Policy.Version
	}
	if err := receiveEnrollments(ctx, store, cfg); err != nil {
		return err
	}
	old := a.cachedDays(*cfg)
	defer func() {
		days := a.cachedDays(*cfg)
		a.mu.Lock()
		a.remote = days
		a.mu.Unlock()
	}()
	byDay := make(map[string]Day, len(old))
	for _, day := range old {
		byDay[day.Device+"/"+day.Date] = day
	}
	cfg.Uploaded = maps.Clone(cfg.Uploaded)
	if cfg.Uploaded == nil {
		cfg.Uploaded = map[string]string{}
	}
	now := time.Now().UTC()
	from := now.AddDate(0, 0, 1-retention)
	for _, device := range cfg.Devices {
		for year := from.Year(); year <= now.Year(); year++ {
			folder := device.ID + "/" + strconv.Itoa(year)
			files, err := store.List(ctx, folder)
			if err != nil {
				return err
			}
			for name, sha := range files {
				date, ok := strings.CutSuffix(name, daySuffix)
				parsed, parseErr := time.Parse(time.DateOnly, date)
				if !ok || parseErr != nil || parsed.Year() != year {
					continue
				}
				index := folder + "/" + name
				previous := byDay[device.ID+"/"+date]
				if previous.Device != "" && cfg.Uploaded[index] == sha {
					continue
				}
				blob, err := store.Read(ctx, folder, name)
				if err != nil {
					return err
				}
				day, err := openDay(blob, cfg.Identity, device, date)
				if err != nil {
					return fmt.Errorf("设备 %s 的 %s 数据无法验证 · %w", device.Name, date, err)
				}
				if !previous.At.IsZero() && day.At.Before(previous.At) {
					continue
				}
				if err := writePrivate(a.cachePath(device.ID, date), blob); err != nil {
					return err
				}
				cfg.Uploaded[index] = sha
			}
		}
	}
	return nil
}

func receivePolicy(ctx context.Context, store transport, cfg *config) error {
	blob, err := store.Read(ctx, controls, policyFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err == nil {
		body, err := verify(policyFormat, blob, cfg.Pair.Verifier)
		if err != nil {
			return err
		}
		var policy Policy
		if json.Unmarshal(body, &policy) != nil || policy.Version < 1 || (policy.SyncMinutes != 0 && !validSyncMinutes(policy.SyncMinutes)) {
			return errors.New("A 端发布的展示参数无法读取")
		}
		for _, rule := range policy.Periods {
			if !rule.valid() {
				return errors.New("A 端发布的展示参数无效")
			}
		}
		if policy.Version > cfg.Policy.Version {
			cfg.Policy = policy
		}
	}
	return nil
}

func (a *App) syncB(ctx context.Context, store transport, cfg *config) error {
	if err := receivePolicy(ctx, store, cfg); err != nil {
		return err
	}
	return a.upload(ctx, store, cfg)
}

func (a *App) runCollector(ctx context.Context, cfg config, first bool, now time.Time) error {
	store, err := a.openRepo(ctx, cfg.Repo, cfg.Token)
	if err != nil {
		if first {
			return errors.Join(err, a.refreshLocal(ctx, cfg))
		}
		return err
	}
	oldVersion := cfg.Policy.Version
	if err := receivePolicy(ctx, store, &cfg); err != nil {
		if first {
			return errors.Join(err, a.refreshLocal(ctx, cfg))
		}
		return err
	}
	if !first && !cfg.LastSync.After(now) && now.Sub(cfg.LastSync) < cfg.Policy.interval() {
		if cfg.Policy.Version != oldVersion {
			return a.save(cfg)
		}
		return nil
	}
	if err := a.refreshLocal(ctx, cfg); err != nil {
		return err
	}
	err = a.upload(ctx, store, &cfg)
	if err == nil {
		cfg.LastSync = now
	}
	return errors.Join(err, a.save(cfg))
}

func (a *App) upload(ctx context.Context, store transport, cfg *config) error {
	if err := publishEnrollment(ctx, store, cfg); err != nil {
		return err
	}
	a.mu.RLock()
	days := a.local
	a.mu.RUnlock()
	cfg.Uploaded = maps.Clone(cfg.Uploaded)
	if cfg.Uploaded == nil {
		cfg.Uploaded = map[string]string{}
	}
	for _, day := range days {
		folder := day.Device + "/" + day.Date[:4]
		filename := day.Date + daySuffix
		index := folder + "/" + filename
		// The time the collector ran is not a change to the day's usage.
		copy := day
		copy.At = time.Time{}
		body, err := json.Marshal(copy)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		hash := hex.EncodeToString(sum[:])
		if cfg.Uploaded[index] == hash {
			continue
		}
		data, err := sealDay(day, cfg.Pair.Recipient, cfg.Signer)
		if err != nil {
			return err
		}
		if err = store.Write(ctx, folder, filename, data); err != nil {
			return err
		}
		cfg.Uploaded[index] = hash
	}
	return nil
}

func (a *App) cachePath(device, date string) string {
	return filepath.Join(a.dir, "received", device, date+daySuffix)
}

func (a *App) cachedDays(cfg config) []Day {
	var out []Day
	for _, device := range cfg.Devices {
		entries, _ := os.ReadDir(filepath.Dir(a.cachePath(device.ID, "")))
		for _, item := range entries {
			date, ok := strings.CutSuffix(item.Name(), daySuffix)
			if _, err := time.Parse(time.DateOnly, date); !ok || err != nil || item.IsDir() {
				continue
			}
			blob, err := os.ReadFile(a.cachePath(device.ID, date))
			if err != nil {
				continue
			}
			day, err := openDay(blob, cfg.Identity, device, date)
			if err == nil {
				out = append(out, day)
			}
		}
	}
	return out
}

func (a *App) setRule(rule PeriodRule) error {
	cfg := a.config()
	if cfg.Role != "a" || !rule.valid() {
		return errors.New("请输入有效的展示系数和周期估算总额")
	}
	for id := range rule.Devices {
		if _, ok := cfg.Devices[id]; !ok {
			return errors.New("未找到该 B 端设备")
		}
	}
	a.mu.RLock()
	windows := periods(slices.Concat(a.local, a.remote))
	a.mu.RUnlock()
	found := false
	for _, q := range windows {
		if q.Account == rule.Account && q.End.Equal(rule.End) && q.includes(time.Now()) {
			found = true
			break
		}
	}
	if !found {
		return errors.New("请选择正在进行的官方额度周期")
	}
	cfg.Policy.Periods = slices.DeleteFunc(slices.Clone(cfg.Policy.Periods), func(r PeriodRule) bool {
		return r.Account == rule.Account && r.End.Sub(rule.End).Abs() <= resetTolerance
	})
	cfg.Policy.Periods = append(cfg.Policy.Periods, rule)
	cfg.Policy.Version++
	return a.save(cfg)
}

func (a *App) setFrequency(minutes int) error {
	cfg := a.config()
	if cfg.Role != "a" || !validSyncMinutes(minutes) {
		return errors.New("请输入 1–1440 分钟之间的整数")
	}
	cfg.Policy.SyncMinutes = minutes
	cfg.Policy.Version++
	if err := a.save(cfg); err != nil {
		return err
	}
	if cfg.SimulationRoot != "" {
		if err := setSimulationFrequency(cfg.SimulationRoot, minutes); err != nil {
			return fmt.Errorf("上传间隔已保存，但模拟设备未收到：%w", err)
		}
	}
	return nil
}
