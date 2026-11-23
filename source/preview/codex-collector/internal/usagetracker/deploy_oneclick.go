package usagetracker

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

func copyInstallerFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}

func writeTClashWindowsOneClick(path, assets string, d deployment) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, f.Close())
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	boot, err := os.Open(filepath.Join(assets, tclashWindowsBootstrap))
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(f, boot)
	if err = errors.Join(copyErr, boot.Close()); err != nil {
		return err
	}
	z := zip.NewWriter(f)
	z.SetOffset(n)
	defer func() { err = errors.Join(err, z.Close()) }()
	header := &zip.FileHeader{Name: "enrollment.json", Method: zip.Deflate}
	header.SetMode(0600)
	w, err := z.CreateHeader(header)
	if err != nil {
		return err
	}
	if err = json.NewEncoder(w).Encode(d); err != nil {
		return err
	}
	for _, file := range []struct {
		asset, name string
		compressed  bool
	}{
		{tclashLicense, "license.txt", true},
		{tclashWindowsAMD64, "setup-x64.exe", false},
		{tclashWindowsARM64, "setup-arm64.exe", false},
		{tclashWindowsAppAMD64, "app-x64.exe", true},
		{tclashWindowsAppARM64, "app-arm64.exe", true},
		{tclashWindowsCollectorAMD64, "collector-x64.exe", true},
		{tclashWindowsCollectorARM64, "collector-arm64.exe", true},
	} {
		in, openErr := os.Open(filepath.Join(assets, file.asset))
		if openErr != nil {
			return openErr
		}
		method := uint16(zip.Store)
		if file.compressed {
			method = zip.Deflate
		}
		w, err = z.CreateHeader(&zip.FileHeader{Name: file.name, Method: method})
		if err == nil {
			_, err = io.Copy(w, in)
		}
		if err = errors.Join(err, in.Close()); err != nil {
			return err
		}
	}
	return nil
}

const tclashInstallerInfo = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>tclash-oneclick</string>
<key>CFBundleIdentifier</key><string>local.tclash.preview.device-installer</string>
<key>CFBundleName</key><string>TClash 设备安装</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>1.0</string>
<key>CFBundleVersion</key><string>1</string>
<key>LSMinimumSystemVersion</key><string>12.0</string>
<key>LSUIElement</key><true/>
</dict></plist>`

var signTClashInstaller = func(path string) error {
	if err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", path).Run(); err != nil {
		return err
	}
	return exec.Command("/usr/bin/codesign", "--verify", "--strict", path).Run()
}

func writeTClashMacOneClick(path, assets string, d deployment) (err error) {
	if err = os.Mkdir(path, 0700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(path)
		}
	}()
	macos := filepath.Join(path, "Contents", "MacOS")
	resources := filepath.Join(path, "Contents", "Resources")
	if err = os.MkdirAll(macos, 0700); err != nil {
		return err
	}
	if err = os.MkdirAll(resources, 0700); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(path, "Contents", "Info.plist"), []byte(tclashInstallerInfo), 0600); err != nil {
		return err
	}
	if err = copyInstallerFile(filepath.Join(assets, tclashMacBootstrap), filepath.Join(macos, "tclash-oneclick"), 0700); err != nil {
		return err
	}
	if err = copyInstallerFile(filepath.Join(assets, tclashMacTemplate), filepath.Join(resources, tclashMacTemplate), 0600); err != nil {
		return err
	}
	if err = copyInstallerFile(filepath.Join(assets, tclashLicense), filepath.Join(resources, tclashLicense), 0600); err != nil {
		return err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(resources, "enrollment.json"), b, 0600); err != nil {
		return err
	}
	return signTClashInstaller(path)
}
