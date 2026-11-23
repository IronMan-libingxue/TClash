package usagetracker

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const enrollments = "enrollments"
const enrollmentFormat = "UCT-E1\x00"
const enrollmentSecretSize = 32

type invitation struct {
	Name   string `json:"name"`
	Secret []byte `json:"secret"`
}

type deployment struct {
	Pair       Pair   `json:"pair"`
	Name       string `json:"name"`
	Token      string `json:"token"`
	Secret     []byte `json:"secret"`
	StartFresh bool   `json:"startFresh,omitempty"`
}

type enrollment struct {
	Verifier []byte `json:"verifier"`
	Proof    []byte `json:"proof"`
}

func newInvitation(name string) (invitation, error) {
	secret := make([]byte, enrollmentSecretSize)
	_, err := rand.Read(secret)
	return invitation{Name: name, Secret: secret}, err
}

func invitationID(secret []byte) string {
	return digest("invite", string(secret))
}

func enrollmentProof(secret, verifier []byte) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(enrollmentFormat))
	h.Write(verifier)
	return h.Sum(nil)
}

// InstallDeployment stores the B role and creates its device identity locally.
// The first network access is done by the background worker.
func (a *App) InstallDeployment(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var d deployment
	if len(data) > 16<<10 || json.Unmarshal(data, &d) != nil || !d.Pair.valid() ||
		len(d.Secret) != enrollmentSecretSize || label(d.Name) == "" || !validDeploymentToken(d.Token) {
		return errors.New("设备安装授权无效")
	}
	cfg := a.config()
	if cfg.Role != "" {
		if cfg.Role == "b" && cfg.Name == d.Name && cfg.Repo == d.Pair.Repo &&
			cfg.Pair.Recipient == d.Pair.Recipient {
			if d.StartFresh && cfg.Token != d.Token {
				return errors.New("这个设备已有旧连接，需先清理旧 B 后再使用新授权")
			}
			return nil
		}
		if cfg.Role != "b" || cfg.Repo != "" || cfg.Pair.Recipient != "" {
			return errors.New("这个目录已有设备配置")
		}
	}
	if cfg.Role == "" {
		_, key, err := makeKeys("b")
		if err != nil {
			return err
		}
		cfg.Signer = key
	}
	cfg.Role, cfg.Name = "b", label(d.Name)
	cfg.Repo, cfg.Pair, cfg.Token, cfg.Invite = d.Pair.Repo, d.Pair, d.Token, d.Secret
	if !d.StartFresh {
		return a.save(cfg)
	}
	cfg.CountSince = time.Now().UTC()
	var moved []string
	restore := func() {
		for _, name := range moved {
			_ = os.Rename(filepath.Join(a.dir, name+".fresh-staging"), filepath.Join(a.dir, name))
		}
	}
	for _, name := range []string{"activity-history.json", "traffic-ledger.json", "shared-telemetry.json"} {
		path := filepath.Join(a.dir, name)
		if _, err := os.Stat(path + ".fresh-staging"); err == nil {
			restore()
			return errors.New("发现未完成的旧数据清理，请先检查")
		} else if !errors.Is(err, os.ErrNotExist) {
			restore()
			return err
		}
		if err := os.Rename(path, path+".fresh-staging"); err == nil {
			moved = append(moved, name)
		} else if !errors.Is(err, os.ErrNotExist) {
			restore()
			return err
		}
	}
	if err := a.save(cfg); err != nil {
		restore()
		return err
	}
	for _, name := range moved {
		if err := os.Remove(filepath.Join(a.dir, name+".fresh-staging")); err != nil {
			return errors.New("新授权已保存，但旧设备统计文件未能清理")
		}
	}
	a.telemetryMu.Lock()
	a.telemetry = Telemetry{}
	a.traffic = newTrafficLedger(a.dir)
	a.telemetryMu.Unlock()
	return nil
}

func publishEnrollment(ctx context.Context, store transport, cfg *config) error {
	if len(cfg.Invite) == 0 {
		return nil
	}
	d := cfg.device()
	body, err := json.Marshal(enrollment{d.Verifier, enrollmentProof(cfg.Invite, d.Verifier)})
	if err != nil {
		return err
	}
	name := invitationID(cfg.Invite) + "-" + d.ID + daySuffix
	if err := store.Write(ctx, enrollments, name, sign(enrollmentFormat, body, cfg.Signer)); err != nil {
		return err
	}
	cfg.Invite = nil
	return nil
}

func receiveEnrollments(ctx context.Context, store transport, cfg *config) error {
	if len(cfg.Invites) == 0 {
		return nil
	}
	files, err := store.List(ctx, enrollments)
	if err != nil {
		return err
	}
	cfg.Invites = maps.Clone(cfg.Invites)
	cfg.Devices = maps.Clone(cfg.Devices)
	if cfg.Devices == nil {
		cfg.Devices = map[string]Device{}
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		base, ok := strings.CutSuffix(name, daySuffix)
		inviteID, id, split := strings.Cut(base, "-")
		invite, pending := cfg.Invites[inviteID]
		if !ok || !split || !pending || !deviceID.MatchString(id) {
			continue
		}
		blob, err := store.Read(ctx, enrollments, name)
		if err != nil {
			return err
		}
		offset := ed25519.SignatureSize + len(enrollmentFormat)
		var claim enrollment
		if len(blob) <= offset || len(blob) > 4096 || json.Unmarshal(blob[offset:], &claim) != nil {
			continue
		}
		device := Device{id, invite.Name, claim.Verifier}
		if !device.valid() || !hmac.Equal(claim.Proof, enrollmentProof(invite.Secret, claim.Verifier)) {
			continue
		}
		if _, err := verify(enrollmentFormat, blob, claim.Verifier); err != nil {
			continue
		}
		cfg.Devices[id] = device
		delete(cfg.Invites, inviteID)
	}
	return nil
}
