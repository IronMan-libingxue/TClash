//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExecutableDirectoryFromWindowsRegistration(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`"C:\Custom Folder\TClash Preview\tclash-preview.exe",0`, `C:\Custom Folder\TClash Preview`},
		{`"C:\Custom Folder\TClash Preview\uninstall.exe" /S`, `C:\Custom Folder\TClash Preview`},
		{`C:\Custom Folder\TClash Preview\tclash-preview.exe /arg`, `C:\Custom Folder\TClash Preview`},
		{`"C:\Other\other.exe"`, ""},
		{"", ""},
	} {
		if got := executableDir(tc.input); got != tc.want {
			t.Fatalf("executable directory for %q: got %q, want %q", tc.input, got, tc.want)
		}
	}
}

// This touches only the test bottle's registry and is never enabled by
// ordinary Windows tests.
func TestIsolatedWindowsRegisteredLocation(t *testing.T) {
	if os.Getenv("TCLASH_WIN_ISOLATED_TEST") != "1" {
		t.Skip("isolated Windows environment not requested")
	}
	const key = `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\TClash Preview`
	t.Cleanup(func() { _ = exec.Command("reg.exe", "delete", key, "/f").Run() })
	dir := filepath.Join(t.TempDir(), "Custom location", "TClash Preview")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tclash-preview.exe"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	value := `"` + filepath.Join(dir, "tclash-preview.exe") + `",0`
	cmd := exec.Command("reg.exe", "add", key, "/v", "DisplayIcon", "/t", "REG_SZ", "/d", value, "/f")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create isolated registration: %v: %s", err, output)
	}
	got, found, err := findInstalled(t.TempDir(), windowsPlatform{})
	if err != nil || !found || !strings.EqualFold(got, dir) {
		t.Fatalf("custom installation not found: found=%v, got=%q, err=%v", found, got, err)
	}
}

func TestIsolatedWindowsFindsRootDriveInstallation(t *testing.T) {
	if os.Getenv("TCLASH_WIN_ISOLATED_TEST") != "1" {
		t.Skip("isolated Windows environment not requested")
	}
	want := `D:\TClash Preview`
	if _, err := os.Stat(filepath.Join(want, "tclash-preview.exe")); err != nil {
		t.Skip("isolated D drive fixture not present")
	}
	dirs, err := fixedDriveInstallDirs()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		if strings.EqualFold(dir, want) {
			return
		}
	}
	t.Fatal("could not locate the installation on the isolated D drive")
}

func init() {
	if target := os.Getenv("TCLASH_WIN_CONSOLE_TEST_FILE"); target != "" {
		fn := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow")
		handle, _, _ := fn.Call()
		result := "no console"
		if handle != 0 {
			result = "console visible"
		}
		_ = os.WriteFile(target, []byte(result), 0600)
		os.Exit(0)
	}
}

func TestWindowsStartHasNoConsole(t *testing.T) {
	if os.Getenv("TCLASH_WIN_ISOLATED_TEST") != "1" {
		t.Skip("isolated Windows environment not requested")
	}
	result := filepath.Join(t.TempDir(), "console-result.txt")
	t.Setenv("TCLASH_WIN_CONSOLE_TEST_FILE", result)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := (windowsPlatform{}).start(self); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if b, err := os.ReadFile(result); err == nil {
			if string(b) != "no console" {
				t.Fatal("a console was created")
			}
			return
		}
	}
	t.Fatal("application did not start")
}
