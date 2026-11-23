package usagetracker

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocalOnlyCollectorNeverConnectsToRepository(t *testing.T) {
	app, err := New(t.TempDir(), "b")
	if err != nil {
		t.Fatal(err)
	}
	cfg := app.config()
	cfg.Repo, cfg.Token = "fixture/private", "fixture-secret"
	if err := app.save(cfg); err != nil {
		t.Fatal(err)
	}
	var opens atomic.Int32
	app.openRepo = func(context.Context, string, string) (transport, error) {
		opens.Add(1)
		return nil, errors.New("must not connect")
	}
	app.account = func() string { return "" }
	app.measureSpeed = func() speedReading { return speedReading{} }
	refreshed := make(chan struct{}, 1)
	app.collect = func(_ context.Context, _, _ string, _ []Quota, _ []AccountCorrection) ([]Day, error) {
		refreshed <- struct{}{}
		return []Day{}, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	app.StartLocalOnly(ctx, "same machine")
	select {
	case <-refreshed:
	case <-time.After(5 * time.Second):
		t.Fatal("local data did not refresh")
	}
	if opens.Load() != 0 {
		t.Fatal("same-machine preview connected to the repository")
	}
}

func TestTClashMigrationPackageKeepsExistingIdentityPrivate(t *testing.T) {
	cloud := &memoryRepo{files: map[string][]byte{}}
	a, err := New(t.TempDir(), "a")
	if err != nil {
		t.Fatal(err)
	}
	a.openRepo = func(context.Context, string, string) (transport, error) { return cloud, nil }
	if err = a.configure(t.Context(), setup{Repo: "fixture/private", Token: "fixture-a-secret"}); err != nil {
		t.Fatal(err)
	}
	b, err := New(t.TempDir(), "b")
	if err != nil {
		t.Fatal(err)
	}
	cfg := a.config()
	bcfg := b.config()
	bcfg.Name = "Windows 1"
	bcfg.Repo, bcfg.Token = "fixture/private", "fixture-b-secret"
	bcfg.Pair, err = cfg.pair()
	if err != nil {
		t.Fatal(err)
	}
	if err = b.save(bcfg); err != nil {
		t.Fatal(err)
	}
	if !ConnectedCollectorDir(b.dir) {
		t.Fatal("paired legacy B was not recognized")
	}
	cfg.Devices = map[string]Device{bcfg.device().ID: bcfg.device()}
	if err = a.save(cfg); err != nil {
		t.Fatal(err)
	}
	assets := t.TempDir()
	for _, name := range []string{tclashWindowsAMD64, tclashWindowsARM64, tclashLicense} {
		if err = os.WriteFile(filepath.Join(assets, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a.SetInstallerAssets(assets)
	if err = a.exportTClashMigration("Windows 1"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.config().DeploymentDir, "Windows 1 TClash 迁移.zip")
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("fixture-a-secret")) || bytes.Contains(blob, []byte("fixture-b-secret")) {
		t.Fatal("migration package contains credentials")
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	marker, err := z.Open("reuse-b.txt")
	if err != nil {
		t.Fatal(err)
	}
	name, err := io.ReadAll(marker)
	_ = marker.Close()
	if err != nil || string(name) != "Windows 1\n" {
		t.Fatal("migration package does not identify the selected device")
	}
	if _, err = z.Open("enrollment.json"); err == nil {
		t.Fatal("migration must not create a new identity")
	}
	if a.config().Devices[bcfg.device().ID].Name != "Windows 1" {
		t.Fatal("A device changed")
	}
}

func TestCollectorRefreshesLocalWhenStoreIsOffline(t *testing.T) {
	app, err := New(t.TempDir(), "b")
	if err != nil {
		t.Fatal(err)
	}
	app.account = func() string { return "" }
	app.openRepo = func(context.Context, string, string) (transport, error) {
		return nil, errors.New("offline")
	}
	collected := 0
	app.collect = func(_ context.Context, device, name string, _ []Quota, _ []AccountCorrection) ([]Day, error) {
		collected++
		return []Day{{Device: device, Name: name, Date: "2026-01-01"}}, nil
	}
	for i := 0; i < 2; i++ {
		if app.runCollector(t.Context(), app.config(), i == 0, time.Now()) == nil {
			t.Fatal("offline error was hidden")
		}
	}
	if collected != 2 || app.readAt.IsZero() {
		t.Fatalf("offline display was not refreshed twice: %d", collected)
	}
}

func TestTClashPackageEnrollsExistingUnpairedCollector(t *testing.T) {
	cloud := &memoryRepo{files: map[string][]byte{}}
	a, err := New(t.TempDir(), "a")
	if err != nil {
		t.Fatal(err)
	}
	a.openRepo = func(context.Context, string, string) (transport, error) { return cloud, nil }
	if err = a.configure(t.Context(), setup{Repo: "fixture/private", Token: "fixture-a"}); err != nil {
		t.Fatal(err)
	}
	assets := t.TempDir()
	for _, name := range []string{tclashMacTemplate, tclashLicense} {
		if err = os.WriteFile(filepath.Join(assets, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a.SetInstallerAssets(assets)
	if err = a.exportTClashDeployment(t.Context(), "Mac 2", "fixture-b"); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(a.config().DeploymentDir, "Mac 2 TClash.zip")
	z, err := zip.OpenReader(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	f, err := z.Open("enrollment.json")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	var deployment deployment
	if json.Unmarshal(body, &deployment) != nil || deployment.Name != "Mac 2" {
		t.Fatal("wrong enrollment")
	}
	b, err := New(t.TempDir(), "b")
	if err != nil {
		t.Fatal(err)
	}
	before := b.config().device().ID
	enrollment := filepath.Join(t.TempDir(), "enrollment.json")
	if err = os.WriteFile(enrollment, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = b.InstallDeployment(enrollment); err != nil {
		t.Fatal(err)
	}
	if b.config().device().ID != before {
		t.Fatal("the unpaired device identity changed")
	}
	if err = publishEnrollment(t.Context(), cloud, &b.cfg); err != nil {
		t.Fatal(err)
	}
	cfg := a.config()
	if err = receiveEnrollments(t.Context(), cloud, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Devices) != 1 || cfg.Devices[before].Name != "Mac 2" {
		t.Fatal("A did not accept TClash enrollment")
	}
}
