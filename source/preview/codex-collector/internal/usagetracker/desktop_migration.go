package usagetracker

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/yetone/magpie/internal/filelock"
)

func DesktopCollectorDir(root string) string {
	return filepath.Join(root, "TClash", "collector")
}

// The caller holds the destination lock. Commit tracker.json last so an
// interrupted copy can be retried without treating partial data as migrated.
func MigrateDesktopCollector(source, destination string) error {
	if _, err := os.Stat(filepath.Join(destination, "tracker.json")); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if _, err := os.Stat(source); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(source, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	held, err := filelock.TryLock(lock)
	if err != nil {
		return err
	}
	if !held {
		return errors.New("旧版 TClash 正在读写用量资料，请退出旧版后重试")
	}
	cfg, err := loadConfig(source)
	if err != nil {
		return err
	}
	if cfg.Role == "a" {
		return errors.New("找到 A 端资料，未将其迁入 B 端")
	}
	for _, name := range []string{
		"install-enrollment.json", sharedPriceFile, "activity-history.json",
		"traffic-ledger.json", "shared-telemetry.json", localHistoryFile, "tracker.json",
	} {
		from := filepath.Join(source, name)
		info, err := os.Lstat(from)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("用量资料格式不受支持：%s", name)
		}
		data, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		to := filepath.Join(destination, name)
		if name == "install-enrollment.json" {
			if _, err := os.Lstat(to); err == nil {
				continue
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		if err = writePrivate(to, data); err != nil {
			return err
		}
	}
	return nil
}
