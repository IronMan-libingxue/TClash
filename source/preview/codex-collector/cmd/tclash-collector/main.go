package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/yetone/magpie/internal/filelock"
	"github.com/yetone/magpie/internal/usagetracker"
)

type request struct {
	Window    string                        `json:"window"`
	Cache     bool                          `json:"cache"`
	Operation string                        `json:"operation,omitempty"`
	Traffic   *usagetracker.TrafficSnapshot `json:"traffic,omitempty"`
}

func run() error {
	dataDir := flag.String("data-dir", "", "collector data directory")
	verifyMigration := flag.Bool("verify-migration", false, "validate a migration without starting a collector")
	buildInfo := flag.Bool("build-info", false, "print the collector capability version")
	flag.Parse()
	if *buildInfo {
		fmt.Println("tclash-collector-device-reset-v1")
		return nil
	}
	migrationDir := ""
	legacySource := ""
	controllerHere := false
	if *dataDir == "" {
		root, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		legacySource = filepath.Join(root, "TClash Preview", "collector")
		*dataDir = usagetracker.DesktopCollectorDir(root)
		controllerHere, err = controllerOnThisUser(root)
		if err != nil {
			return err
		}
		_, existing := os.Stat(filepath.Join(*dataDir, "tracker.json"))
		if existing != nil && !errors.Is(existing, fs.ErrNotExist) {
			return existing
		}
		if !controllerHere && (existing != nil || *verifyMigration) {
			selected, migrate, err := selectCollectorDir(root, legacySource)
			if err != nil {
				return err
			}
			if migrate {
				migrationDir, legacySource = legacySource, selected
			}
		}
	}
	if *verifyMigration {
		if migrationDir == "" {
			return errors.New("未找到匹配的迁移设备")
		}
		return nil
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(*dataDir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	held, err := filelock.TryLock(lock)
	if err != nil {
		return err
	}
	if !held {
		return errors.New("collector data directory is already in use")
	}
	if legacySource != "" {
		if err := usagetracker.MigrateDesktopCollector(legacySource, *dataDir); err != nil {
			return err
		}
	}
	tracker, err := usagetracker.New(*dataDir, "")
	if err != nil {
		return err
	}
	if migrationDir != "" && !usagetracker.ConnectedCollectorDir(*dataDir) {
		return errors.New("原 B 端身份不可用，未更改原有连接")
	}
	enrollment := filepath.Join(*dataDir, "install-enrollment.json")
	if _, err := os.Stat(enrollment); err == nil && !controllerHere {
		if err = tracker.InstallDeployment(enrollment); err != nil {
			return err
		}
		if err = os.Remove(enrollment); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if !tracker.IsCollector() {
		tracker, err = usagetracker.New(*dataDir, "b")
		if err != nil {
			return err
		}
	}
	if err := usagetracker.UseLocalCodex(*dataDir); err != nil {
		return err
	}
	if err := tracker.EnableLocalHistory(); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if controllerHere {
		tracker.StartLocalOnly(ctx, "本机已有 A 端，已暂停上传以避免重复统计")
	} else {
		tracker.Start(ctx)
	}
	if migrationDir != "" {
		if err := os.WriteFile(filepath.Join(migrationDir, "migration-running"), []byte("ready"), 0o600); err != nil {
			return err
		}
		defer os.Remove(filepath.Join(migrationDir, "migration-running"))
	}
	handler := tracker.Handler(os.DirFS(*dataDir))
	input := bufio.NewScanner(os.Stdin)
	input.Buffer(make([]byte, 1024), 8<<20)
	output := bufio.NewWriter(os.Stdout)
	for input.Scan() {
		var r request
		if json.Unmarshal(input.Bytes(), &r) != nil {
			return errors.New("invalid collector request")
		}
		if r.Operation == "traffic" {
			response := map[string]any{"ok": false}
			if r.Traffic != nil {
				if err := tracker.RecordTraffic(*r.Traffic); err == nil {
					response["ok"] = true
				}
			}
			if err := json.NewEncoder(output).Encode(response); err != nil {
				return err
			}
			if err := output.Flush(); err != nil {
				return err
			}
			continue
		}
		q := url.Values{"window": {r.Window}}
		if !r.Cache {
			q.Set("cache", "0")
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/view?"+q.Encode(), nil))
		if rec.Code != 200 {
			return fmt.Errorf("collector view failed: %d", rec.Code)
		}
		if _, err := output.Write(bytes.TrimSpace(rec.Body.Bytes())); err != nil {
			return err
		}
		if err := output.WriteByte('\n'); err != nil {
			return err
		}
		if err := output.Flush(); err != nil {
			return err
		}
	}
	return input.Err()
}

func controllerOnThisUser(root string) (bool, error) {
	path := filepath.Join(root, "Codex Usage Tracker", "tracker.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("无法检查本机 A 端状态，已阻止上传")
	}
	var cfg struct {
		Role string `json:"role"`
	}
	if len(data) > 16<<20 || json.Unmarshal(data, &cfg) != nil || (cfg.Role != "a" && cfg.Role != "b") {
		return false, errors.New("无法检查本机 A 端状态，已阻止上传")
	}
	return cfg.Role == "a", nil
}

// Migration is opt-in through the A-generated migration package. It reuses the
// old directory and lock so the old and new collector cannot upload together.
func selectCollectorDir(root, local string) (string, bool, error) {
	marker := filepath.Join(local, "use-legacy-tracker")
	data, err := os.ReadFile(marker)
	if errors.Is(err, fs.ErrNotExist) {
		return local, false, nil
	} else if err != nil {
		return "", false, err
	}
	expected := strings.TrimSpace(string(data))
	if len(data) > 128 || expected == "" {
		return "", false, errors.New("迁移设备名称无效")
	}
	if _, err := os.Stat(filepath.Join(local, "install-enrollment.json")); err == nil {
		return "", false, errors.New("迁移和新设备授权不能同时使用")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}
	if usagetracker.ConnectedCollectorDir(local) {
		return "", false, errors.New("TClash 已连接设备，请先处理现有连接")
	}
	old := filepath.Join(root, "Codex Usage Tracker")
	if !usagetracker.ConnectedCollectorNamed(old, expected) {
		return "", false, errors.New("未找到已连接的原 B 端，未更改原有连接")
	}
	return old, true, nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
