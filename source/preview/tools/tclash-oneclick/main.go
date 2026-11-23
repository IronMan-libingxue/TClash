package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	runInstaller(string, bool) error
	registeredDirs() ([]string, error)
	prepareHandlers(string) error
	collectorOK(string) error
	disableLegacyStartup(string) error
	start(string) error
	sleep(time.Duration)
}

var errCancelled = errors.New("安装已取消")

func readEntry(z *zip.ReadCloser, name string, limit uint64) ([]byte, error) {
	for _, f := range z.File {
		if f.Name == name {
			if f.UncompressedSize64 > limit {
				return nil, errors.New("安装包内容过大")
			}
			in, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer in.Close()
			return io.ReadAll(io.LimitReader(in, int64(limit)+1))
		}
	}
	return nil, errors.New("安装包缺少必要文件")
}
func writeEntry(z *zip.ReadCloser, name, dest string, limit uint64) error {
	for _, f := range z.File {
		if f.Name == name {
			if f.UncompressedSize64 > limit {
				return errors.New("安装程序内容过大")
			}
			in, e := f.Open()
			if e != nil {
				return e
			}
			defer in.Close()
			out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return e
			}
			_, e = io.Copy(out, in)
			closeErr := out.Close()
			if e != nil {
				return e
			}
			return closeErr
		}
	}
	return errors.New("安装包缺少程序文件")
}
func readTracker(path string) (tracker, bool, error) {
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
func compatible(c tracker, exists bool, d deployment) (bool, error) {
	if !exists {
		return false, nil
	}
	if c.Role == "b" && c.Repo == "" && c.Pair.Recipient == "" {
		return false, nil
	}
	if c.Role == "b" && c.Name == d.Name && c.Repo == d.Pair.Repo && c.Pair.Recipient == d.Pair.Recipient && c.Token == d.Token {
		return true, nil
	}
	return false, errors.New("此设备已有其他连接，为保护现有数据，安装已停止。")
}
func architecture() string {
	a := os.Getenv("PROCESSOR_ARCHITEW6432")
	if a == "" {
		a = os.Getenv("PROCESSOR_ARCHITECTURE")
	}
	switch strings.ToUpper(a) {
	case "AMD64":
		return "x64"
	case "ARM64":
		return "arm64"
	default:
		return ""
	}
}

func findInstalled(localAppData string, p platform) (string, bool, error) {
	registered, err := p.registeredDirs()
	if err != nil {
		return "", false, errors.New("无法读取 TClash 的安装位置。")
	}
	candidates := append(registered, filepath.Join(localAppData, "TClash Preview"))
	var found string
	for _, dir := range candidates {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		dir = filepath.Clean(dir)
		info, err := os.Stat(filepath.Join(dir, "tclash-preview.exe"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		if !info.Mode().IsRegular() {
			return "", false, errors.New("TClash 程序位置异常，安装已停止。")
		}
		if found != "" && !strings.EqualFold(found, dir) {
			return "", false, errors.New("发现多个 TClash 安装位置，请先确认要保留哪个版本。")
		}
		found = dir
	}
	return found, found != "", nil
}
func install(bundle, localAppData, appData, arch string, p platform) error {
	if arch != "x64" && arch != "arm64" {
		return errors.New("不支持此 Windows 处理器类型。")
	}
	z, e := zip.OpenReader(bundle)
	if e != nil {
		return errors.New("无法读取完整安装包。")
	}
	defer z.Close()
	raw, e := readEntry(z, "enrollment.json", 16<<10)
	if e != nil {
		return e
	}
	var d deployment
	if json.Unmarshal(raw, &d) != nil || d.Name == "" || d.Token == "" || d.Pair.Repo == "" || d.Pair.Recipient == "" || len(d.Secret) != 32 || !d.StartFresh {
		return errors.New("设备授权无效。")
	}
	for _, name := range []string{"tclash-preview.exe", "tclash-collector.exe", "Codex Usage.exe"} {
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
	legacy := filepath.Join(appData, "Codex Usage Tracker", "tracker.json")
	old, hasOld, e := readTracker(legacy)
	if e != nil {
		return e
	}
	if hasOld && old.Role == "a" {
		return errors.New("本机已有 A 端，已停止以避免重复采集。")
	}
	collectorDir := filepath.Join(appData, "TClash Preview", "collector")
	if _, err := os.Stat(filepath.Join(collectorDir, "use-legacy-tracker")); err == nil {
		return errors.New("此设备正在使用旧连接，安装已停止以保护现有数据。")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	statePath := filepath.Join(collectorDir, "tracker.json")
	c, exists, e := readTracker(statePath)
	if e != nil {
		return e
	}
	paired, e := compatible(c, exists, d)
	if e != nil {
		return e
	}
	pending := filepath.Join(collectorDir, "install-enrollment.json")
	if b, err := os.ReadFile(pending); err == nil {
		if !bytes.Equal(bytes.TrimSpace(b), bytes.TrimSpace(raw)) {
			return errors.New("已有待处理的设备授权，未覆盖原文件。")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	appDir, existsApp, e := findInstalled(localAppData, p)
	if e != nil {
		return e
	}
	if !existsApp {
		if e = p.prepareHandlers(appData); e != nil {
			return errors.New("无法备份现有订阅链接设置，安装已停止。")
		}
		tmp, e := os.MkdirTemp("", "tclash-setup-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(tmp)
		setup := filepath.Join(tmp, "setup.exe")
		if e = writeEntry(z, "setup-"+arch+".exe", setup, 512<<20); e != nil {
			return e
		}
		if e = p.runInstaller(setup, false); e != nil {
			return errors.New("TClash 安装未完成，原有数据已保留。")
		}
		appDir, existsApp, e = findInstalled(localAppData, p)
		if e != nil {
			return e
		}
		if !existsApp {
			return errors.New("安装后仍未找到 TClash 程序，请保留现有文件并联系协助。")
		}
	} else {
		app := filepath.Join(appDir, "tclash-preview.exe")
		collector := filepath.Join(appDir, "tclash-collector.exe")
		tmp := filepath.Join(appDir, "tclash-preview-oneclick-new.exe")
		tmpCollector := filepath.Join(appDir, "tclash-collector-oneclick-new.exe")
		if _, err := os.Stat(tmp); err == nil {
			return errors.New("发现未完成的程序更新，请先检查。")
		}
		if _, err := os.Stat(tmpCollector); err == nil {
			return errors.New("发现未完成的采集程序更新，请先检查。")
		}
		if e = writeEntry(z, "app-"+arch+".exe", tmp, 256<<20); e != nil {
			return e
		}
		defer os.Remove(tmp)
		if e = writeEntry(z, "collector-"+arch+".exe", tmpCollector, 256<<20); e != nil {
			return e
		}
		defer os.Remove(tmpCollector)
		if e = p.collectorOK(tmpCollector); e != nil {
			return errors.New("安装包中的用量采集程序校验未通过，现有程序未更改。")
		}
		stamp := time.Now().UnixNano()
		backup := filepath.Join(appDir, fmt.Sprintf("tclash-preview-backup-%d.exe", stamp))
		collectorBackup := filepath.Join(appDir, fmt.Sprintf("tclash-collector-backup-%d.exe", stamp))
		if e = os.Rename(app, backup); e != nil {
			return errors.New("无法备份现有程序，请检查是否已完全退出。")
		}
		hadCollector := false
		if _, err := os.Stat(collector); err == nil {
			if e = os.Rename(collector, collectorBackup); e != nil {
				_ = os.Rename(backup, app)
				return errors.New("无法备份现有采集程序，已尝试恢复原版本。")
			}
			hadCollector = true
		} else if !errors.Is(err, os.ErrNotExist) {
			_ = os.Rename(backup, app)
			return err
		}
		if e = os.Rename(tmpCollector, collector); e != nil {
			if hadCollector {
				_ = os.Rename(collectorBackup, collector)
			}
			_ = os.Rename(backup, app)
			return errors.New("更新采集程序失败，已尝试恢复原版本。")
		}
		if e = os.Rename(tmp, app); e != nil {
			_ = os.Remove(collector)
			if hadCollector {
				_ = os.Rename(collectorBackup, collector)
			}
			_ = os.Rename(backup, app)
			return errors.New("更新程序失败，已尝试恢复原版本。")
		}
	}
	app := filepath.Join(appDir, "tclash-preview.exe")
	collector := filepath.Join(appDir, "tclash-collector.exe")
	if e = p.collectorOK(collector); e != nil {
		return errors.New("安装后未找到匹配的用量采集程序。")
	}
	if !paired {
		if e = os.MkdirAll(collectorDir, 0700); e != nil {
			return e
		}
		f, e := os.OpenFile(pending, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil && !errors.Is(e, os.ErrExist) {
			return e
		}
		if e == nil {
			_, e = f.Write(raw)
			ce := f.Close()
			if e != nil {
				return e
			}
			if ce != nil {
				return ce
			}
		}
	}
	if e = p.start(app); e != nil {
		return errors.New("程序已准备好，但无法自动启动。")
	}
	for i := 0; i < 40; i++ {
		p.sleep(500 * time.Millisecond)
		state, ok, err := readTracker(statePath)
		if err == nil && ok {
			same, err := compatible(state, true, d)
			if err == nil && same {
				if hasOld && old.Role == "b" {
					if err := p.disableLegacyStartup(appData); err != nil {
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
	release, available, lockErr := acquireInstallerLock()
	if lockErr != nil {
		notify("无法检查安装程序状态，请稍后重试。")
		os.Exit(1)
	}
	if !available {
		notify("安装程序已经在运行，请返回原窗口继续。")
		os.Exit(1)
	}
	defer release()
	self, e := os.Executable()
	if e != nil {
		notify("无法定位安装程序。")
		os.Exit(1)
	}
	appData, local := os.Getenv("APPDATA"), os.Getenv("LOCALAPPDATA")
	if appData == "" || local == "" {
		notify("无法确定 Windows 用户目录。")
		os.Exit(1)
	}
	e = install(self, local, appData, architecture(), newPlatform())
	if errors.Is(e, errCancelled) {
		os.Exit(1)
	}
	if e != nil {
		notify(e.Error())
		os.Exit(1)
	}
	notify("TClash 已安装并启动，Windows 设备授权已加载，正在自动连接。请保持程序运行，完成一条新的 Codex 任务后，到 A 端确认数据。")
}
