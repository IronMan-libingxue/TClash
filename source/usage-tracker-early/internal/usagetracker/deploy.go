package usagetracker

import (
	"archive/zip"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

//go:embed install/*
var installFiles embed.FS

var deploymentDevices = []struct {
	Name string
	Mac  bool
}{
	{"Mac 2", true}, {"Mac 3", true}, {"Mac 4", true}, {"Windows 1", false}, {"Windows 2", false},
}

const macTemplate = "Codex Usage Mac.zip"
const windowsAMD64 = "codex-usage-windows-amd64.exe"
const windowsARM64 = "codex-usage-windows-arm64.exe"
const magpieLicense = "Magpie-LICENSE.txt"

func (a *App) SetInstallerAssets(dir string) { a.installerAssets = dir }

func validDeploymentToken(token string) bool {
	return token != "" && len(token) <= 1024 && !strings.ContainsAny(token, " \t\r\n")
}

// exportOneDeployment allows an owner to add devices one at a time, without
// manufacturing credentials for four machines that are not being installed.
func (a *App) exportOneDeployment(ctx context.Context, name, token string) error {
	cfg := a.config()
	if cfg.Role != "a" || cfg.Repo == "" || cfg.Token == "" {
		return errors.New("请先在 A 端连接专用私有仓库")
	}
	var target *struct {
		Name string
		Mac  bool
	}
	for i := range deploymentDevices {
		if deploymentDevices[i].Name == name {
			target = &deploymentDevices[i]
			break
		}
	}
	if target == nil {
		return errors.New("请选择预设的设备名称")
	}
	token = strings.TrimSpace(token)
	if !validDeploymentToken(token) || token == cfg.Token {
		return errors.New("请为该设备提供独立的私库访问令牌")
	}
	for _, hash := range cfg.DeploymentTokenHashes {
		if hash == digest("deployment-token", token) {
			return errors.New("请为每台设备使用不同的令牌")
		}
	}
	if _, err := a.openRepo(ctx, cfg.Repo, token); err != nil {
		return errors.New("设备的私库访问令牌无法验证")
	}
	for _, paired := range cfg.Devices {
		if paired.Name == name {
			return errors.New("这个设备已经接入")
		}
	}
	for _, invite := range cfg.Invites {
		if invite.Name == name {
			return errors.New("这个设备已有安装授权")
		}
	}
	templates := []string{windowsAMD64, windowsARM64, magpieLicense}
	if target.Mac {
		templates = []string{macTemplate}
	}
	for _, file := range templates {
		if info, err := os.Stat(filepath.Join(a.installerAssets, file)); err != nil || !info.Mode().IsRegular() {
			return errors.New("这个 A 端版本缺少安装包模板")
		}
	}
	pair, err := cfg.pair()
	if err != nil {
		return err
	}
	invite, err := newInvitation(name)
	if err != nil {
		return err
	}
	dir := cfg.DeploymentDir
	newDir := dir == ""
	if newDir {
		dir, err = os.MkdirTemp(a.dir, "deployments-")
		if err != nil {
			return err
		}
	}
	path := filepath.Join(dir, name+".zip")
	created := false
	written := false
	defer func() {
		if !created {
			if written {
				_ = os.Remove(path)
			}
			if newDir {
				_ = os.Remove(dir)
			}
		}
	}()
	if err := writeDeployment(path, a.installerAssets, deployment{pair, name, token, invite.Secret}, target.Mac); err != nil {
		return err
	}
	written = true
	cfg.Invites = maps.Clone(cfg.Invites)
	if cfg.Invites == nil {
		cfg.Invites = map[string]invitation{}
	}
	cfg.Invites[invitationID(invite.Secret)] = invite
	cfg.DeploymentTokenHashes = maps.Clone(cfg.DeploymentTokenHashes)
	if cfg.DeploymentTokenHashes == nil {
		cfg.DeploymentTokenHashes = map[string]string{}
	}
	cfg.DeploymentTokenHashes[name] = digest("deployment-token", token)
	cfg.DeploymentDir = dir
	if err := a.save(cfg); err != nil {
		return err
	}
	created = true
	return nil
}

func (a *App) exportDeployments(ctx context.Context, tokens []string) error {
	cfg := a.config()
	if cfg.Role != "a" || cfg.Repo == "" || cfg.Token == "" {
		return errors.New("请先在 A 端连接专用私有仓库")
	}
	if cfg.DeploymentDir != "" {
		return errors.New("五份安装包已生成")
	}
	if len(tokens) != len(deploymentDevices) {
		return errors.New("请为五台设备填写各自的仓库访问令牌")
	}
	pair, err := cfg.pair()
	if err != nil {
		return err
	}
	for _, template := range []string{macTemplate, windowsAMD64, windowsARM64, magpieLicense} {
		if info, err := os.Stat(filepath.Join(a.installerAssets, template)); err != nil || !info.Mode().IsRegular() {
			return errors.New("这个 A 端版本缺少安装包模板")
		}
	}
	seen := map[string]bool{cfg.Token: true}
	for i, token := range tokens {
		token = strings.TrimSpace(token)
		if !validDeploymentToken(token) || seen[token] {
			return fmt.Errorf("%s 需要独立的仓库访问令牌", deploymentDevices[i].Name)
		}
		if _, err := a.openRepo(ctx, cfg.Repo, token); err != nil {
			return fmt.Errorf("%s 的私库访问令牌无法验证", deploymentDevices[i].Name)
		}
		seen[token] = true
	}
	for _, paired := range cfg.Devices {
		for _, target := range deploymentDevices {
			if paired.Name == target.Name {
				return fmt.Errorf("%s 已登记，请先处理现有设备记录", paired.Name)
			}
		}
	}
	dir, err := os.MkdirTemp(a.dir, "deployments-")
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(dir)
		}
	}()
	cfg.Invites = make(map[string]invitation, len(tokens))
	cfg.DeploymentTokenHashes = make(map[string]string, len(tokens))
	for i, target := range deploymentDevices {
		if err := ctx.Err(); err != nil {
			return err
		}
		invite, err := newInvitation(target.Name)
		if err != nil {
			return err
		}
		cfg.Invites[invitationID(invite.Secret)] = invite
		cfg.DeploymentTokenHashes[target.Name] = digest("deployment-token", strings.TrimSpace(tokens[i]))
		d := deployment{pair, target.Name, strings.TrimSpace(tokens[i]), invite.Secret}
		path := filepath.Join(dir, target.Name+".zip")
		if err := writeDeployment(path, a.installerAssets, d, target.Mac); err != nil {
			return err
		}
	}
	cfg.DeploymentDir = dir
	if err := a.save(cfg); err != nil {
		return err
	}
	complete = true
	return nil
}

func writeDeployment(path, assets string, d deployment, mac bool) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	defer func() { err = errors.Join(err, f.Close()) }()
	z := zip.NewWriter(f)
	defer func() { err = errors.Join(err, z.Close()) }()
	script, binaries := "Install.cmd", []string{windowsAMD64, windowsARM64, magpieLicense}
	if mac {
		script, binaries = "Install.command", []string{macTemplate}
	}
	body, err := installFiles.ReadFile("install/" + script)
	if err != nil {
		return err
	}
	h := &zip.FileHeader{Name: script}
	h.SetMode(0o700)
	w, err := z.CreateHeader(h)
	if err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	if !mac {
		readme, err := installFiles.ReadFile("install/README.windows.txt")
		if err != nil {
			return err
		}
		w, err := z.Create("使用说明.txt")
		if err != nil {
			return err
		}
		if _, err := w.Write(readme); err != nil {
			return err
		}
	}
	h = &zip.FileHeader{Name: "enrollment.json"}
	h.SetMode(0o600)
	w, err = z.CreateHeader(h)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(w).Encode(d); err != nil {
		return err
	}
	for _, binary := range binaries {
		f, err := os.Open(filepath.Join(assets, binary))
		if err != nil {
			return err
		}
		w, err := z.CreateHeader(&zip.FileHeader{Name: binary, Method: zip.Store})
		if err == nil {
			_, err = io.Copy(w, f)
		}
		if err = errors.Join(err, f.Close()); err != nil {
			return err
		}
	}
	return nil
}
