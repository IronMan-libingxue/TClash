package usagetracker

import (
	"archive/zip"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRealOneClickTemplates(t *testing.T) {
	assets := os.Getenv("TCLASH_TEST_ASSETS")
	if assets == "" {
		t.Skip("real template integration not requested")
	}
	d := deployment{Name: "Mac 2", Token: "fixture-token", Pair: Pair{Repo: "fixture-repo", Recipient: "fixture-recipient"}, Secret: make([]byte, 32), StartFresh: true}
	mac := filepath.Join(t.TempDir(), "Fixture TClash 安装.app")
	if err := writeTClashMacOneClick(mac, assets, d); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("/usr/bin/codesign", "--verify", "--strict", mac).Run(); err != nil {
		t.Fatal("Mac installer signature verification failed")
	}
	b, err := os.ReadFile(filepath.Join(mac, "Contents", "Resources", "enrollment.json"))
	if err != nil {
		t.Fatal(err)
	}
	var actual deployment
	if json.Unmarshal(b, &actual) != nil || actual.Name != d.Name || actual.Token != d.Token {
		t.Fatal("Mac authorization mismatch")
	}
	win := filepath.Join(t.TempDir(), "Fixture TClash.exe")
	d.Name = "Windows 1"
	if err := writeTClashWindowsOneClick(win, assets, d); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(win)
	if err != nil {
		t.Fatal(err)
	}
	magic := make([]byte, 2)
	_, err = f.Read(magic)
	_ = f.Close()
	if err != nil || string(magic) != "MZ" {
		t.Fatal("Windows installer is not an executable")
	}
	z, err := zip.OpenReader(win)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	for _, name := range []string{"enrollment.json", "setup-x64.exe", "setup-arm64.exe", "app-x64.exe", "app-arm64.exe", "collector-x64.exe", "collector-arm64.exe"} {
		f, err := z.Open(name)
		if err != nil {
			t.Fatalf("missing %s", name)
		}
		if name == "enrollment.json" {
			var got deployment
			if json.NewDecoder(f).Decode(&got) != nil || got.Name != "Windows 1" || got.Token != d.Token {
				t.Fatal("Windows authorization mismatch")
			}
		}
		_ = f.Close()
	}
}
