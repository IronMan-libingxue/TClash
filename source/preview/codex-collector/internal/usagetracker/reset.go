package usagetracker

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type resetTransport interface {
	DeviceIDs(context.Context) ([]string, error)
	DeviceYears(context.Context, string) ([]string, error)
	DeleteDeviceFile(context.Context, string, string) error
}

type resetFile struct{ folder, name string }
type stagedReset struct{ path, staged string }

func (a *App) resetRemoteDevices(ctx context.Context) error {
	cfg := a.config()
	if cfg.Role != "a" || cfg.Repo == "" || cfg.Token == "" {
		return errors.New("请先在 A 端连接私有仓库")
	}
	store, err := a.openRepo(ctx, cfg.Repo, cfg.Token)
	if err != nil {
		return err
	}
	cleaner, ok := store.(resetTransport)
	if !ok {
		return errors.New("当前存储服务不支持清理设备数据")
	}
	ids := map[string]bool{}
	remoteIDs, err := cleaner.DeviceIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range remoteIDs {
		if !deviceID.MatchString(id) {
			return errors.New("私库包含无法安全识别的设备")
		}
		ids[id] = true
	}
	slots := map[string]bool{}
	for id, device := range cfg.Devices {
		if !device.valid() || id != device.ID {
			return errors.New("已有设备身份无效，未执行清理")
		}
		ids[id], slots[device.Name] = true, true
	}
	for _, invite := range cfg.Invites {
		slots[invite.Name] = true
	}
	for name := range cfg.DeploymentTokenHashes {
		slots[name] = true
	}
	allowed := map[string]bool{}
	for _, target := range deploymentDevices {
		allowed[target.Name] = true
	}
	for slot := range slots {
		if !allowed[slot] {
			return errors.New("发现未知的旧设备名称，未执行清理")
		}
	}

	var files []resetFile
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		years, err := cleaner.DeviceYears(ctx, id)
		if err != nil {
			return err
		}
		for _, year := range years {
			folder := id + "/" + year
			entries, err := store.List(ctx, folder)
			if err != nil {
				return err
			}
			for _, name := range slices.Sorted(maps.Keys(entries)) {
				date, valid := strings.CutSuffix(name, daySuffix)
				parsed, parseErr := time.Parse(time.DateOnly, date)
				if !valid || parseErr != nil || parsed.Format("2006") != year {
					return errors.New("发现无法安全识别的旧设备统计文件")
				}
				files = append(files, resetFile{folder, name})
			}
		}
	}
	enrollmentsFound, err := store.List(ctx, enrollments)
	if err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(enrollmentsFound)) {
		base, suffix := strings.CutSuffix(name, daySuffix)
		inviteID, id, split := strings.Cut(base, "-")
		if !suffix || !split || !deviceID.MatchString(inviteID) || !deviceID.MatchString(id) {
			return errors.New("发现无法安全识别的旧设备授权文件")
		}
		files = append(files, resetFile{enrollments, name})
	}

	var paths []string
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		paths = append(paths, filepath.Join(a.dir, "received", id))
	}
	if cfg.DeploymentDir != "" {
		for _, name := range slices.Sorted(maps.Keys(slots)) {
			for _, suffix := range []string{".zip", " TClash.zip", " TClash.exe", " TClash 安装.app", " TClash 迁移.zip"} {
				paths = append(paths, filepath.Join(cfg.DeploymentDir, name+suffix))
			}
		}
	}
	for _, path := range paths {
		if _, err := os.Lstat(path + ".reset-staging"); err == nil {
			return errors.New("发现未完成的设备清理，请先检查")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, file := range files {
		if err := cleaner.DeleteDeviceFile(ctx, file.folder, file.name); err != nil {
			return fmt.Errorf("部分私库文件可能已清理，请重试 · %w", err)
		}
	}
	var staged []stagedReset
	restore := func() {
		for _, item := range staged {
			_ = os.Rename(item.staged, item.path)
		}
	}
	for _, path := range paths {
		item := stagedReset{path, path + ".reset-staging"}
		if err := os.Rename(item.path, item.staged); err == nil {
			staged = append(staged, item)
		} else if !errors.Is(err, os.ErrNotExist) {
			restore()
			return fmt.Errorf("私库旧文件已清理，本机文件未全部清理 · %w", err)
		}
	}
	cfg.Devices, cfg.Invites, cfg.DeploymentTokenHashes = nil, nil, nil
	cfg.DeviceAliases = map[string]string{}
	if local := a.config().DeviceAliases["local"]; local != "" {
		cfg.DeviceAliases["local"] = local
	}
	cfg.Uploaded = maps.Clone(cfg.Uploaded)
	for name := range cfg.Uploaded {
		if id, _, ok := strings.Cut(name, "/"); ok && ids[id] {
			delete(cfg.Uploaded, name)
		}
	}
	changed := false
	cfg.Policy.Periods = slices.Clone(cfg.Policy.Periods)
	for i := range cfg.Policy.Periods {
		rule := &cfg.Policy.Periods[i]
		rule.Devices = maps.Clone(rule.Devices)
		for id := range rule.Devices {
			if ids[id] {
				delete(rule.Devices, id)
				changed = true
			}
		}
	}
	if changed {
		cfg.Policy.Version++
	}
	if err := a.save(cfg); err != nil {
		restore()
		return fmt.Errorf("私库旧文件已清理，本机设置未保存 · %w", err)
	}
	a.mu.Lock()
	a.remote = nil
	a.mu.Unlock()
	for _, item := range staged {
		if err := os.RemoveAll(item.staged); err != nil {
			return errors.New("设备已重置，但部分本机旧文件未能清理")
		}
	}
	return nil
}
