package usagetracker

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOneWindowsPackageAndIndependentDeviceAuthorization(t *testing.T) {
	ctx := t.Context()
	cloud := &memoryRepo{files: map[string][]byte{}}
	a, err := New(t.TempDir(), "a")
	if err != nil {
		t.Fatal(err)
	}
	a.openRepo = func(context.Context, string, string) (transport, error) { return cloud, nil }
	if err := a.configure(ctx, setup{Repo: "fixture/private", Token: "a-token"}); err != nil {
		t.Fatal(err)
	}
	assets := t.TempDir()
	for _, file := range []string{macTemplate, windowsAMD64, windowsARM64, magpieLicense} {
		if err := os.WriteFile(filepath.Join(assets, file), []byte(file), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a.SetInstallerAssets(assets)
	if err := a.exportOneDeployment(ctx, "Windows 1", "a-token"); err == nil {
		t.Fatal("A token was accepted as the device token")
	}
	if err := a.exportOneDeployment(ctx, "Windows 1", "windows-token"); err != nil {
		t.Fatal(err)
	}
	cfg := a.config()
	if len(cfg.Invites) != 1 || len(cfg.DeploymentTokenHashes) != 1 {
		t.Fatal("one package created the wrong set of invitations")
	}
	file := filepath.Join(cfg.DeploymentDir, "Windows 1.zip")
	z, err := zip.OpenReader(file)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"Install.cmd": false, "使用说明.txt": false, "enrollment.json": false, windowsAMD64: false, windowsARM64: false, magpieLicense: false}
	var enrollmentBody []byte
	for _, item := range z.File {
		if _, ok := want[item.Name]; !ok {
			t.Fatalf("unexpected package file %q", item.Name)
		}
		want[item.Name] = true
		if item.Name == "enrollment.json" {
			f, err := item.Open()
			if err != nil {
				t.Fatal(err)
			}
			enrollmentBody, err = io.ReadAll(f)
			_ = f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	_ = z.Close()
	for name, exists := range want {
		if !exists {
			t.Fatalf("package missing %s", name)
		}
	}
	var d deployment
	if json.Unmarshal(enrollmentBody, &d) != nil || d.Name != "Windows 1" || d.Token != "windows-token" {
		t.Fatal("Windows enrollment was not preconfigured")
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.exportOneDeployment(ctx, "Windows 1", "different-token"); err == nil {
		t.Fatal("duplicate device was accepted")
	}
	if err := a.exportOneDeployment(ctx, "Windows 2", "windows-token"); err == nil {
		t.Fatal("a token was reused across devices")
	}
	after, err := os.ReadFile(file)
	if err != nil || string(before) != string(after) {
		t.Fatal("failed generation damaged the existing package")
	}
	b, err := New(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "enrollment.json")
	if err := os.WriteFile(p, enrollmentBody, 0600); err != nil {
		t.Fatal(err)
	}
	if err := b.InstallDeployment(p); err != nil {
		t.Fatal(err)
	}
	if b.config().Role != "b" || b.config().Repo != "fixture/private" || b.config().Name != "Windows 1" {
		t.Fatal("installer did not configure Windows B")
	}
	if err := publishEnrollment(ctx, cloud, &b.cfg); err != nil {
		t.Fatal(err)
	}
	next := a.config()
	if err := receiveEnrollments(ctx, cloud, &next); err != nil {
		t.Fatal(err)
	}
	if len(next.Devices) != 1 || len(next.Invites) != 0 {
		t.Fatal("A did not automatically register its Windows device")
	}
}
