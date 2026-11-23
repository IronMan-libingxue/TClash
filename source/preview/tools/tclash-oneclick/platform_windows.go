//go:build windows

package main

import (
	"encoding/base64"
	"encoding/csv"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type windowsPlatform struct{}

func newPlatform() platform { return windowsPlatform{} }
func hidden(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
func message(text string, flags uintptr) uintptr {
	user32 := syscall.NewLazyDLL("user32.dll")
	fn := user32.NewProc("MessageBoxW")
	content, _ := syscall.UTF16PtrFromString(text)
	title, _ := syscall.UTF16PtrFromString("TClash Windows 设备安装")
	r, _, _ := fn.Call(0, uintptr(unsafe.Pointer(content)), uintptr(unsafe.Pointer(title)), flags)
	return r
}
func notify(text string) { message(text, 0x40) }
func acquireInstallerLock() (func(), bool, error) {
	name, err := syscall.UTF16PtrFromString(`Local\TClashPreviewDeviceInstaller`)
	if err != nil {
		return nil, false, err
	}
	fn := syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")
	handle, _, callErr := fn.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return nil, false, callErr
	}
	if errors.Is(callErr, syscall.ERROR_ALREADY_EXISTS) {
		_ = syscall.CloseHandle(syscall.Handle(handle))
		return nil, false, nil
	}
	return func() { _ = syscall.CloseHandle(syscall.Handle(handle)) }, true, nil
}
func (windowsPlatform) askExit(name string) bool {
	return message("安装需要先安全退出 "+name+"。请从系统托盘退出后点击“重试”；安装会自动继续。", 0x5|0x30) == 4
}
func (windowsPlatform) running(name string) (bool, error) {
	c := exec.Command("tasklist.exe", "/FI", "IMAGENAME eq "+name, "/NH", "/FO", "CSV")
	hidden(c)
	b, e := c.Output()
	if e != nil {
		return false, e
	}
	rows, e := csv.NewReader(strings.NewReader(string(b))).ReadAll()
	if e != nil {
		return false, e
	}
	for _, row := range rows {
		if len(row) > 0 && strings.EqualFold(strings.TrimSpace(row[0]), name) {
			return true, nil
		}
	}
	return false, nil
}
func (windowsPlatform) runInstaller(path string, update bool) error {
	args := []string{"/S"}
	if update {
		args = append(args, "/UPDATE")
	}
	c := exec.Command(path, args...)
	hidden(c)
	return c.Run()
}

func registryString(root syscall.Handle, key, name string, view uint32) (string, error) {
	subkey, err := syscall.UTF16PtrFromString(key)
	if err != nil {
		return "", err
	}
	var handle syscall.Handle
	if err = syscall.RegOpenKeyEx(root, subkey, 0, syscall.KEY_QUERY_VALUE|view, &handle); err != nil {
		if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) || errors.Is(err, syscall.ERROR_PATH_NOT_FOUND) {
			return "", nil
		}
		return "", err
	}
	defer syscall.RegCloseKey(handle)
	var valueName *uint16
	if name != "" {
		valueName, err = syscall.UTF16PtrFromString(name)
		if err != nil {
			return "", err
		}
	}
	var kind, size uint32
	err = syscall.RegQueryValueEx(handle, valueName, nil, &kind, nil, &size)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if kind != syscall.REG_SZ && kind != syscall.REG_EXPAND_SZ {
		return "", nil
	}
	if size == 0 {
		return "", nil
	}
	if size > 65536 || size%2 != 0 {
		return "", errors.New("invalid install path")
	}
	buffer := make([]uint16, int(size/2)+1)
	err = syscall.RegQueryValueEx(handle, valueName, nil, &kind, (*byte)(unsafe.Pointer(&buffer[0])), &size)
	if err != nil {
		return "", err
	}
	value := syscall.UTF16ToString(buffer)
	if kind == syscall.REG_EXPAND_SZ {
		input, err := syscall.UTF16PtrFromString(value)
		if err != nil {
			return "", err
		}
		fn := syscall.NewLazyDLL("kernel32.dll").NewProc("ExpandEnvironmentStringsW")
		n, _, callErr := fn.Call(uintptr(unsafe.Pointer(input)), 0, 0)
		if n == 0 || n > 32768 {
			return "", callErr
		}
		expanded := make([]uint16, n)
		written, _, callErr := fn.Call(uintptr(unsafe.Pointer(input)), uintptr(unsafe.Pointer(&expanded[0])), n)
		if written == 0 || written > n {
			return "", callErr
		}
		value = syscall.UTF16ToString(expanded)
	}
	return strings.Trim(strings.TrimSpace(value), `"`), nil
}

func (windowsPlatform) registeredDirs() ([]string, error) {
	locations := []struct {
		key, name  string
		executable bool
	}{
		{`Software\TClash Preview\TClash Preview`, "", false},
		{`Software\Microsoft\Windows\CurrentVersion\Uninstall\TClash Preview`, "InstallLocation", false},
		{`Software\Microsoft\Windows\CurrentVersion\Uninstall\TClash Preview`, "DisplayIcon", true},
		{`Software\Microsoft\Windows\CurrentVersion\Uninstall\TClash Preview`, "UninstallString", true},
	}
	var dirs []string
	for _, root := range []syscall.Handle{syscall.HKEY_CURRENT_USER, syscall.HKEY_LOCAL_MACHINE} {
		for _, view := range []uint32{syscall.KEY_WOW64_64KEY, syscall.KEY_WOW64_32KEY} {
			for _, item := range locations {
				value, err := registryString(root, item.key, item.name, view)
				if err != nil {
					return nil, err
				}
				if item.executable {
					value = executableDir(value)
				}
				if value != "" {
					dirs = append(dirs, value)
				}
			}
		}
	}
	// Older TClash copies can be installed directly at a drive's root without
	// a usable registry entry. Check local disks as well as the desktop link.
	// In particular this covers D:\TClash Preview on the Windows 1 device.
	driveDirs, err := fixedDriveInstallDirs()
	if err != nil {
		return nil, err
	}
	dirs = append(dirs, driveDirs...)
	shortcuts, err := desktopShortcutDirs()
	if err != nil && len(dirs) == 0 {
		return nil, err
	}
	dirs = append(dirs, shortcuts...)
	return dirs, nil
}

func fixedDriveInstallDirs() ([]string, error) {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	mask, _, callErr := kernel.NewProc("GetLogicalDrives").Call()
	if mask == 0 {
		return nil, callErr
	}
	getType := kernel.NewProc("GetDriveTypeW")
	var dirs []string
	for i := uint(0); i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		wide, err := syscall.UTF16PtrFromString(root)
		if err != nil {
			return nil, err
		}
		kind, _, _ := getType.Call(uintptr(unsafe.Pointer(wide)))
		if kind != 2 && kind != 3 { // Removable or fixed; avoid network drives.
			continue
		}
		dir := filepath.Join(root, "TClash Preview")
		info, err := os.Stat(filepath.Join(dir, "tclash-preview.exe"))
		if err == nil && info.Mode().IsRegular() {
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}

// Windows records an executable in these values with optional quotes, icon
// indices or command-line arguments. Only consider the two expected names;
// findInstalled will separately confirm that the TClash program exists.
func executableDir(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if value[0] == '"' {
		value = value[1:]
		if end := strings.IndexByte(value, '"'); end >= 0 {
			value = value[:end]
		}
	}
	if end := strings.Index(strings.ToLower(value), ".exe"); end >= 0 {
		value = value[:end+4]
	} else {
		return ""
	}
	name := filepath.Base(value)
	if !strings.EqualFold(name, "tclash-preview.exe") && !strings.EqualFold(name, "uninstall.exe") {
		return ""
	}
	return filepath.Dir(value)
}

func desktopShortcutDirs() ([]string, error) {
	const script = `$ErrorActionPreference='Stop'; $shell=New-Object -ComObject WScript.Shell; $dirs=@([Environment]::GetFolderPath('DesktopDirectory'),[Environment]::GetFolderPath('CommonDesktopDirectory'),[Environment]::GetFolderPath('Programs'),[Environment]::GetFolderPath('CommonPrograms')); foreach($dir in $dirs) { if ($dir -and (Test-Path -LiteralPath $dir)) { Get-ChildItem -LiteralPath $dir -Filter '*.lnk' -File -ErrorAction SilentlyContinue | ForEach-Object { try { $p=$shell.CreateShortcut($_.FullName).TargetPath; if($p -and [IO.Path]::GetFileName($p) -ieq 'tclash-preview.exe') { [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($p)) } } catch {} } } }`
	c := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	hidden(c)
	out, err := c.Output()
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			return nil, err
		}
		target := string(decoded)
		if strings.EqualFold(filepath.Base(target), "tclash-preview.exe") {
			dirs = append(dirs, filepath.Dir(target))
		}
	}
	return dirs, nil
}
func (windowsPlatform) prepareHandlers(appData string) error {
	dir := filepath.Join(appData, "local.tclash.preview", "previous-url-handlers")
	if _, err := os.Stat(filepath.Join(dir, "prepared")); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	for _, scheme := range []string{"clash", "clash-verge"} {
		key := `HKCU\Software\Classes\` + scheme
		query := exec.Command("reg.exe", "query", key)
		hidden(query)
		if err := query.Run(); err != nil {
			parent := exec.Command("reg.exe", "query", `HKCU\Software\Classes`)
			hidden(parent)
			if err := parent.Run(); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, scheme+".absent"), nil, 0600); err != nil {
				return err
			}
			continue
		}
		export := exec.Command("reg.exe", "export", key, filepath.Join(dir, scheme+".reg"), "/y")
		hidden(export)
		if err := export.Run(); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, "prepared"), nil, 0600)
}
func (windowsPlatform) collectorOK(path string) error {
	c := exec.Command(path, "-build-info")
	hidden(c)
	b, e := c.Output()
	if e != nil {
		return e
	}
	if strings.TrimSpace(string(b)) != "tclash-collector-device-reset-v1" {
		return syscall.EINVAL
	}
	return nil
}
func (windowsPlatform) disableLegacyStartup(appData string) error {
	key := `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	query := exec.Command("reg.exe", "query", key, "/v", "Codex Usage Tracker")
	hidden(query)
	b, err := query.Output()
	if err != nil {
		// A missing value is normal. Verify that the key itself is readable so
		// that a permission error does not masquerade as an absent startup item.
		check := exec.Command("reg.exe", "query", key)
		hidden(check)
		return check.Run()
	}
	if !strings.Contains(strings.ToLower(string(b)), `\programs\codex usage tracker\codex usage.exe`) {
		return errors.New("unrecognized legacy startup item")
	}
	backup := filepath.Join(appData, "Codex Usage Tracker", "autostart-before-tclash.txt")
	f, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err == nil {
		_, err = f.Write(b)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	remove := exec.Command("reg.exe", "delete", key, "/v", "Codex Usage Tracker", "/f")
	hidden(remove)
	return remove.Run()
}
func (windowsPlatform) start(path string) error {
	c := exec.Command(path)
	// Keep the main application window visible while preventing a console from
	// being attached if an older Windows build used the console subsystem.
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	return c.Start()
}
func (windowsPlatform) sleep(t time.Duration) { time.Sleep(t) }
