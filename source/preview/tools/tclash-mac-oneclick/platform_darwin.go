//go:build darwin

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type macPlatform struct{}

func newPlatform() platform { return macPlatform{} }
func dialog(msg string, retry bool) bool {
	script := `on run argv
 set msg to item 1 of argv
 set retryMode to item 2 of argv
 if retryMode is "yes" then
  display dialog msg with title "TClash 设备安装" buttons {"取消", "重试"} default button "重试" with icon caution
 else
  display dialog msg with title "TClash 设备安装" buttons {"好"} default button "好"
 end if
end run`
	mode := "no"
	if retry {
		mode = "yes"
	}
	return exec.Command("/usr/bin/osascript", "-e", script, "--", msg, mode).Run() == nil
}
func notify(s string) { dialog(s, false) }
func (macPlatform) askExit(s string) bool {
	return dialog("请先从菜单栏安全退出 "+s+"，再点击重试。", true)
}
func (macPlatform) running(name string) (bool, error) {
	var args []string
	switch name {
	case "TClash Preview":
		args = []string{"-f", "/TClash Preview.app/Contents/MacOS/clash-verge"}
	case "tclash-collector":
		args = []string{"-x", "tclash-collector"}
	case "Codex Usage Tracker":
		args = []string{"-f", "/Codex 用量.app/Contents/MacOS/codex-usage"}
	default:
		return false, errors.New("unknown process")
	}
	e := exec.Command("/usr/bin/pgrep", args...).Run()
	if e == nil {
		return true, nil
	}
	var x *exec.ExitError
	if errors.As(e, &x) && x.ExitCode() == 1 {
		return false, nil
	}
	return false, e
}
func (macPlatform) disableLegacyStartup(home string) error {
	path := filepath.Join(home, "Library", "LaunchAgents", "local.codex-usage-tracker.collector.plist")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	out, err := exec.Command("/usr/bin/plutil", "-convert", "json", "-o", "-", path).Output()
	if err != nil {
		return err
	}
	var plist struct {
		Label string   `json:"Label"`
		Args  []string `json:"ProgramArguments"`
	}
	if json.Unmarshal(out, &plist) != nil || plist.Label != "local.codex-usage-tracker.collector" || len(plist.Args) < 3 || !strings.Contains(plist.Args[0], "/Codex 用量.app/Contents/MacOS/codex-usage") || plist.Args[1] != "-data-dir" || plist.Args[2] != filepath.Join(home, "Library", "Application Support", "Codex Usage Tracker") {
		return errors.New("unrecognized legacy startup")
	}
	service := fmt.Sprintf("gui/%d/%s", os.Getuid(), plist.Label)
	if exec.Command("/bin/launchctl", "print", service).Run() == nil {
		if err = exec.Command("/bin/launchctl", "bootout", service).Run(); err != nil {
			return err
		}
	}
	return os.Rename(path, path+fmt.Sprintf(".tclash-backup-%d", time.Now().UnixNano()))
}
func (macPlatform) extract(zip, dest string) error {
	return exec.Command("/usr/bin/ditto", "-x", "-k", zip, dest).Run()
}
func (macPlatform) verify(app string) error {
	if err := exec.Command("/usr/bin/codesign", "--verify", "--strict", "--deep", app).Run(); err != nil {
		return err
	}
	b, err := exec.Command(filepath.Join(app, "Contents", "MacOS", "tclash-collector"), "-build-info").Output()
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(b)) != "tclash-collector-device-reset-v1" {
		return errors.New("invalid collector")
	}
	return nil
}
func (macPlatform) start(app string) error { return exec.Command("/usr/bin/open", "-a", app).Run() }
func (macPlatform) sleep(t time.Duration)  { time.Sleep(t) }
