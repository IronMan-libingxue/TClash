package usagetracker

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const simulationPrefix = "simulation-"

func (a *App) ConfigureSimulation(root string) error {
	cfg := a.config()
	if cfg.Role != "a" {
		return errors.New("只有 A 端可以显示模拟设备")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	days, devices, err := readSimulation(root, time.Now())
	if err != nil {
		return errors.New("本机模拟数据无法读取")
	}
	minutes := cfg.Policy.SyncMinutes
	if !validSyncMinutes(minutes) {
		minutes = defaultSyncMinutes
	}
	if err := setSimulationFrequency(root, minutes); err != nil {
		return err
	}
	cfg.SimulationRoot, cfg.ShowSimulation = root, true
	if err := a.save(cfg); err != nil {
		return err
	}
	a.setSimulationData(days, devices, "")
	return nil
}

func (a *App) setSimulationEnabled(enabled bool) error {
	cfg := a.config()
	if cfg.Role != "a" || cfg.SimulationRoot == "" {
		return errors.New("尚未配置本机模拟数据")
	}
	var days []Day
	var devices []choice
	if enabled {
		var err error
		days, devices, err = readSimulation(cfg.SimulationRoot, time.Now())
		if err != nil {
			return errors.New("本机模拟数据无法读取")
		}
	}
	cfg.ShowSimulation = enabled
	if err := a.save(cfg); err != nil {
		return err
	}
	a.setSimulationData(days, devices, "")
	return nil
}

func (a *App) refreshSimulation(cfg config) {
	days, devices, err := readSimulation(cfg.SimulationRoot, time.Now())
	if err != nil {
		a.mu.Lock()
		a.simulationStatus = "本机模拟数据暂不可读取。"
		if len(a.simulationDevices) > 0 {
			a.simulationStatus = "本机模拟数据暂不可读取，显示最近一次读取的数据。"
		}
		a.mu.Unlock()
		return
	}
	a.setSimulationData(days, devices, "")
}

func (a *App) setSimulationData(days []Day, devices []choice, status string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.simulated, a.simulationDevices, a.simulationStatus = days, devices, status
}

// The local demonstration has its own signing key and synthetic account. Only
// the interval is copied: real account rules and credentials never leave A.
func setSimulationFrequency(root string, minutes int) error {
	demo, err := loadConfig(filepath.Join(root, "a"))
	if err != nil || demo.Role != "a" || demo.Repo != "local/preview" || !validSyncMinutes(minutes) {
		return errors.New("本机模拟设备的同步设置无法读取")
	}
	path := filepath.Join(root, "relay", controls, policyFile)
	policy := demo.Policy
	blob, err := os.ReadFile(path)
	if err == nil {
		body, verifyErr := verify(policyFormat, blob, demo.Signer.Public().(ed25519.PublicKey))
		if verifyErr != nil || json.Unmarshal(body, &policy) != nil || policy.Version < 1 {
			return errors.New("本机模拟设备的同步设置无法验证")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return errors.New("本机模拟设备的同步设置无法读取")
	}
	for _, rule := range policy.Periods {
		if !rule.valid() || rule.Account != "simulated-account" {
			return errors.New("本机模拟设备的同步设置不是模拟数据")
		}
	}
	if policy.SyncMinutes == minutes && policy.Version > 0 {
		return nil
	}
	policy.Version = max(policy.Version, demo.Policy.Version) + 1
	if policy.Version <= 0 {
		return errors.New("本机模拟设备的同步设置版本无效")
	}
	policy.SyncMinutes = minutes
	body, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	if err := writePrivate(path, sign(policyFormat, body, demo.Signer)); err != nil {
		return errors.New("本机模拟设备的同步设置无法保存")
	}
	return nil
}

// readSimulation reads only the existing local test relay. Each object must
// authenticate against the test identity and contain the fixed demo account.
func readSimulation(root string, now time.Time) ([]Day, []choice, error) {
	cfg, err := loadConfig(filepath.Join(root, "a"))
	if err != nil || cfg.Role != "a" || cfg.Repo != "local/preview" {
		return nil, nil, errors.New("模拟数据来源无效")
	}
	allowed := map[string]bool{}
	for _, device := range deploymentDevices {
		allowed[device.Name] = true
	}
	seen := map[string]bool{}
	var days []Day
	var devices []choice
	today := now.UTC().Truncate(24 * time.Hour)
	since := today.AddDate(0, 0, 1-retention)
	for _, device := range cfg.Devices {
		if !device.valid() || !allowed[device.Name] || seen[device.Name] {
			return nil, nil, errors.New("模拟设备身份无效")
		}
		seen[device.Name] = true
		id := simulationPrefix + device.ID
		devices = append(devices, choice{id, device.Name})
		for year := since.Year(); year <= today.Year(); year++ {
			dir := filepath.Join(root, "relay", device.ID, strconv.Itoa(year))
			entries, err := os.ReadDir(dir)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, nil, err
			}
			for _, item := range entries {
				name := item.Name()
				date, ok := strings.CutSuffix(name, daySuffix)
				parsed, err := time.Parse(time.DateOnly, date)
				if !ok || err != nil || item.IsDir() || parsed.Year() != year ||
					parsed.Before(since) || parsed.After(today) {
					continue
				}
				data, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					return nil, nil, err
				}
				day, err := openDay(data, cfg.Identity, device, date)
				if err != nil {
					return nil, nil, fmt.Errorf("模拟设备 %s 的数据无法验证", device.Name)
				}
				for _, quota := range day.Quotas {
					if quota.Account != "simulated-account" || quota.Plan != "simulated" {
						return nil, nil, errors.New("模拟数据含未知账号")
					}
				}
				for _, row := range day.Rows {
					if row.Account != "simulated-account" {
						return nil, nil, errors.New("模拟用量含未知账号")
					}
				}
				day.Device, day.Name, day.Quotas = id, device.Name, nil
				days = append(days, day)
			}
		}
	}
	if len(devices) == 0 || len(days) == 0 {
		return nil, nil, errors.New("模拟设备尚未准备完成")
	}
	slices.SortFunc(devices, compareChoices)
	return days, devices, nil
}
