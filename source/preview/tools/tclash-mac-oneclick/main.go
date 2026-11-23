package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type pair struct {
	Repo      string `json:"repo"`
	Recipient string `json:"recipient"`
}
type deployment struct {
	Name       string `json:"name"`
	Token      string `json:"token"`
	Pair       pair   `json:"pair"`
	Secret     []byte `json:"secret"`
	StartFresh bool   `json:"startFresh"`
}
type tracker struct {
	Role  string `json:"role"`
	Name  string `json:"name"`
	Repo  string `json:"repo"`
	Token string `json:"token"`
	Pair  pair   `json:"pair"`
}
type platform interface {
	running(string) (bool, error)
	askExit(string) bool
	disableLegacyStartup(string) error
	extract(string, string) error
	verify(string) error
	start(string) error
	sleep(time.Duration)
}

var errCancelled = errors.New("安装已取消")

func state(path string) (tracker, bool, error) {
	b, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		return tracker{}, false, nil
	}
	if e != nil {
		return tracker{}, false, e
	}
	if len(b) > 16<<20 {
		return tracker{}, false, errors.New("现有设备信息无法核对")
	}
	var c tracker
	if json.Unmarshal(b, &c) != nil {
		return tracker{}, false, errors.New("现有设备信息无法核对")
	}
	return c, true, nil
}
func same(c tracker, exists bool, d deployment) (bool, error) {
	if !exists || (c.Role == "b" && c.Repo == "" && c.Pair.Recipient == "") {
		return false, nil
	}
	if c.Role == "b" && c.Name == d.Name && c.Repo == d.Pair.Repo && c.Pair.Recipient == d.Pair.Recipient && c.Token == d.Token {
		return true, nil
	}
	return false, errors.New("此设备已有其他连接，为保护现有数据，安装已停止。")
}
func install(resources, home string, p platform) error {
	raw, e := os.ReadFile(filepath.Join(resources, "enrollment.json"))
	if e != nil || len(raw) > 16<<10 {
		return errors.New("设备授权缺失或无效。")
	}
	var d deployment
	if json.Unmarshal(raw, &d) != nil || d.Name == "" || d.Token == "" || d.Pair.Repo == "" || d.Pair.Recipient == "" || len(d.Secret) != 32 || !d.StartFresh {
		return errors.New("设备授权无效。")
	}
	appData := filepath.Join(home, "Library", "Application Support")
	legacy, hasOld, e := state(filepath.Join(appData, "Codex Usage Tracker", "tracker.json"))
	if e != nil {
		return e
	}
	if hasOld && legacy.Role == "a" {
		return errors.New("本机已有 A 端，已停止以避免重复采集。")
	}
	dir := filepath.Join(appData, "TClash Preview", "collector")
	if _, e = os.Stat(filepath.Join(dir, "use-legacy-tracker")); e == nil {
		return errors.New("此设备正在使用旧连接，安装已停止以保护现有数据。")
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	track := filepath.Join(dir, "tracker.json")
	c, exists, e := state(track)
	if e != nil {
		return e
	}
	paired, e := same(c, exists, d)
	if e != nil {
		return e
	}
	pending := filepath.Join(dir, "install-enrollment.json")
	if b, err := os.ReadFile(pending); err == nil {
		if !bytes.Equal(bytes.TrimSpace(b), bytes.TrimSpace(raw)) {
			return errors.New("已有待处理的设备授权，未覆盖原文件。")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, name := range []string{"TClash Preview", "tclash-collector", "Codex Usage Tracker"} {
		if name == "Codex Usage Tracker" && (!hasOld || legacy.Role != "b") {
			continue
		}
		for {
			active, err := p.running(name)
			if err != nil {
				return errors.New("无法检查正在运行的程序。")
			}
			if !active {
				break
			}
			if !p.askExit(name) {
				return errCancelled
			}
			p.sleep(time.Second)
		}
	}
	apps := filepath.Join(home, "Applications")
	if e = os.MkdirAll(apps, 0700); e != nil {
		return e
	}
	stage, e := os.MkdirTemp(apps, ".tclash-install-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	if e = p.extract(filepath.Join(resources, "TClash Preview Mac.zip"), stage); e != nil {
		return errors.New("无法解开 TClash 安装文件。")
	}
	next := filepath.Join(stage, "TClash Preview.app")
	if e = p.verify(next); e != nil {
		return errors.New("安装文件校验未通过。")
	}
	target := filepath.Join(apps, "TClash Preview.app")
	backup := ""
	if _, e = os.Stat(target); e == nil {
		backup = filepath.Join(apps, fmt.Sprintf("TClash Preview 安装前备份 %d.app", time.Now().UnixNano()))
		if e = os.Rename(target, backup); e != nil {
			return errors.New("无法备份现有程序，请检查是否已完全退出。")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if e = os.Rename(next, target); e != nil {
		if backup != "" {
			_ = os.Rename(backup, target)
		}
		return errors.New("更新程序失败，已尝试恢复原版本。")
	}
	if !paired {
		if e = os.MkdirAll(dir, 0700); e != nil {
			return e
		}
		f, err := os.OpenFile(pending, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if err == nil {
			_, err = f.Write(raw)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	if e = p.start(target); e != nil {
		return errors.New("程序已准备好，但无法自动启动。")
	}
	for i := 0; i < 60; i++ {
		p.sleep(500 * time.Millisecond)
		c, ok, err := state(track)
		if err == nil && ok {
			matched, err := same(c, true, d)
			if err == nil && matched {
				// The old collector's login item is changed only once the new
				// application has started and accepted this device identity.
				if hasOld && legacy.Role == "b" {
					if err = p.disableLegacyStartup(home); err != nil {
						return errors.New("TClash 已启动，但旧采集程序的自动启动未能安全关闭，请联系协助。")
					}
				}
				return nil
			}
		}
	}
	return errors.New("程序已启动，但暂未确认授权加载。请检查 TClash 是否打开，再联系协助。")
}
func main() {
	exe, e := os.Executable()
	if e != nil {
		notify("无法定位安装程序。")
		return
	}
	home, e := os.UserHomeDir()
	if e != nil {
		notify("无法确定 Mac 用户目录。")
		return
	}
	resources := filepath.Clean(filepath.Join(filepath.Dir(exe), "..", "Resources"))
	e = install(resources, home, newPlatform())
	if errors.Is(e, errCancelled) {
		return
	}
	if e != nil {
		notify(e.Error())
		return
	}
	notify("TClash 已安装并启动，设备授权已加载。请运行一条 Codex 任务，再到 A 端确认同步。")
}
