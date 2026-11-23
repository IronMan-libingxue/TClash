package usagetracker

import (
	"cmp"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/yetone/magpie/internal/davsync"
	"github.com/yetone/magpie/internal/steady"

	"filippo.io/age"
)

type transport interface {
	List(context.Context, string) (map[string]string, error)
	Read(context.Context, string, string) ([]byte, error)
	Write(context.Context, string, string, []byte) error
}

type config struct {
	Role                  string                `json:"role,omitempty"`
	Name                  string                `json:"name,omitempty"`
	Repo                  string                `json:"repo,omitempty"`
	Token                 string                `json:"token,omitempty"`
	Identity              string                `json:"identity,omitempty"`
	Signer                ed25519.PrivateKey    `json:"signer,omitempty"`
	Pair                  Pair                  `json:"pair,omitempty"`
	Devices               map[string]Device     `json:"devices,omitempty"`
	Policy                Policy                `json:"policy"`
	Published             int64                 `json:"published,omitempty"`
	Uploaded              map[string]string     `json:"uploaded,omitempty"`
	Quotas                []Quota               `json:"quotas,omitempty"`
	LastSync              time.Time             `json:"lastSync,omitempty"`
	Invites               map[string]invitation `json:"invites,omitempty"`
	Invite                []byte                `json:"invite,omitempty"`
	DeploymentDir         string                `json:"deploymentDir,omitempty"`
	DeploymentTokenHashes map[string]string     `json:"deploymentTokenHashes,omitempty"`
	SimulationRoot        string                `json:"simulationRoot,omitempty"`
	ShowSimulation        bool                  `json:"showSimulation,omitempty"`
	AccountCorrections    []AccountCorrection   `json:"accountCorrections,omitempty"`
	AccountObservations   []AccountObservation  `json:"accountObservations,omitempty"`
}

type App struct {
	dir string

	mu                sync.RWMutex
	cfg               config
	local             []Day
	remote            []Day
	simulated         []Day
	simulationDevices []choice
	simulationStatus  string
	readAt            time.Time
	localAccount      string
	status            string
	speed             speedReading
	observationOpen   bool

	work            sync.Mutex
	collect         func(context.Context, string, string, []Quota, []AccountCorrection) ([]Day, error)
	quota           func(context.Context) ([]Quota, error)
	openRepo        func(context.Context, string, string) (transport, error)
	installerAssets string
	account         func() string
	measureSpeed    func() speedReading
}

func New(dataDir, role string) (*App, error) {
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	cfg, err := loadConfig(dir)
	if err != nil {
		return nil, err
	}
	c := &Collector{}
	app := &App{dir: dir, cfg: cfg, collect: c.Days, quota: ReadOfficial, account: currentAccount, measureSpeed: localSpeed,
		openRepo: func(ctx context.Context, repo, token string) (transport, error) {
			return davsync.OpenUsageRepository(ctx, repo, token)
		}}
	if role != "" {
		if err := app.setRole(role); err != nil {
			return nil, err
		}
	}
	if app.cfg.Role == "a" {
		app.remote = app.cachedDays(app.cfg)
	}
	return app, nil
}

func loadConfig(dir string) (config, error) {
	var cfg config
	data, err := os.ReadFile(filepath.Join(dir, "tracker.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if json.Unmarshal(data, &cfg) != nil || (cfg.Role != "a" && cfg.Role != "b") ||
		len(cfg.Signer) != ed25519.PrivateKeySize {
		return cfg, errors.New("统计工具配置无法读取")
	}
	if cfg.Role == "a" {
		if _, err := age.ParseX25519Identity(cfg.Identity); err != nil {
			return cfg, errors.New("A 端解密钥匙无法读取")
		}
	}
	return cfg, nil
}

func (a *App) IsCollector() bool { return a.config().Role == "b" }

// ConnectedCollectorDir is used only for an explicitly requested migration.
// The old collector directory remains the source of its existing identity.
func ConnectedCollectorDir(dir string) bool {
	cfg, err := loadConfig(dir)
	return err == nil && cfg.Role == "b" && cfg.Repo != "" && cfg.Token != "" && cfg.Pair.valid()
}

func ConnectedCollectorNamed(dir, name string) bool {
	cfg, err := loadConfig(dir)
	return err == nil && cfg.Role == "b" && cfg.Name == name && cfg.Repo != "" && cfg.Token != "" && cfg.Pair.valid()
}

func (a *App) config() config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg
}

func (a *App) save(cfg config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(a.dir, "tracker.json")
	if err := writePrivate(path, data); err != nil {
		return err
	}
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()
	return nil
}

func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return steady.WriteFile(path, data, 0o600)
}

func (a *App) setRole(role string) error {
	cfg := a.config()
	if role != "a" && role != "b" {
		return errors.New("请选择 A 端或 B 端")
	}
	if cfg.Role != "" {
		if cfg.Role == role {
			return nil
		}
		return errors.New("这个数据目录已经设置了角色")
	}
	identity, key, err := makeKeys(role)
	if err != nil {
		return err
	}
	name, _ := os.Hostname()
	name = strings.TrimSuffix(name, ".local")
	if name = label(name); name == "" {
		name = "本机"
	}
	cfg.Role, cfg.Signer, cfg.Identity, cfg.Name = role, key, identity, name
	return a.save(cfg)
}

func label(value string) string {
	var out strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsControl(r) {
			continue
		}
		if out.Len()+4 > 128 {
			break
		}
		out.WriteRune(r)
	}
	return out.String()
}

func (c config) device() Device {
	public := c.Signer.Public().(ed25519.PublicKey)
	return Device{ID: digest("device", string(public)), Name: c.Name, Verifier: public}
}

func (c config) pair() (Pair, error) {
	identity, err := age.ParseX25519Identity(c.Identity)
	if err != nil {
		return Pair{}, err
	}
	return Pair{Repo: c.Repo, Recipient: identity.Recipient().String(), Verifier: c.Signer.Public().(ed25519.PublicKey)}, nil
}

func sortedDays(m map[string]*Day) []Day {
	out := make([]Day, 0, len(m))
	for _, day := range m {
		slices.SortFunc(day.Rows, func(a, b Request) int {
			return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.ID, b.ID))
		})
		slices.SortFunc(day.Quotas, func(a, b Quota) int { return cmp.Compare(a.id(), b.id()) })
		out = append(out, *day)
	}
	slices.SortFunc(out, func(a, b Day) int { return cmp.Compare(a.Date, b.Date) })
	return out
}

func (a *App) refreshLocal(ctx context.Context, cfg config) error {
	if cfg.Role == "" {
		return nil
	}
	id, name := "local", "Mac 1（本机）"
	if cfg.Role == "b" {
		id, name = cfg.device().ID, cfg.Name
	}
	account := a.account()
	if cfg.Role == "a" {
		var err error
		cfg, err = a.observeAccount(cfg, account, time.Now().UTC())
		if err != nil {
			return err
		}
	}
	days, err := a.collect(ctx, id, name, cfg.Quotas, cfg.AccountCorrections)
	if err != nil {
		return err
	}
	after := a.account()
	if cfg.Role == "a" {
		cfg, err = a.observeAccount(cfg, after, time.Now().UTC())
		if err != nil {
			return err
		}
		classifyLocal(days, cfg)
	}
	a.mu.Lock()
	a.local, a.readAt = days, time.Now()
	a.localAccount = after
	a.mu.Unlock()
	return nil
}

func (a *App) observeAccount(cfg config, account string, at time.Time) (config, error) {
	previous := slices.Clone(cfg.AccountObservations)
	cutoff := at.AddDate(0, 0, -retention)
	kept := previous[:0]
	for _, observation := range previous {
		if observation.End.After(cutoff) {
			kept = append(kept, observation)
		}
	}
	if account == "" {
		a.observationOpen = false
	} else if a.observationOpen && len(kept) > 0 && kept[len(kept)-1].Account == account &&
		!at.Before(kept[len(kept)-1].End) && at.Sub(kept[len(kept)-1].End) <= 2*time.Minute {
		kept[len(kept)-1].End = at
	} else {
		kept = append(kept, AccountObservation{Account: account, Start: at, End: at})
		a.observationOpen = true
	}
	cfg.AccountObservations = kept
	if err := a.save(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (a *App) Start(ctx context.Context) {
	go a.sampleSpeed(ctx)
	go func() {
		start := time.Now()
		timer := time.NewTicker(time.Minute)
		defer timer.Stop()
		a.runBackground(ctx, true, start)
		for {
			select {
			case <-ctx.Done():
				return
			case tick := <-timer.C:
				a.runBackground(ctx, false, tick)
			}
		}
	}()
}

// StartLocalOnly keeps the B preview useful on the controller's own machine,
// while never opening a repository or uploading a second copy of its logs.
func (a *App) StartLocalOnly(ctx context.Context, reason string) {
	go a.sampleSpeed(ctx)
	go func() {
		refresh := func() {
			if !a.work.TryLock() {
				return
			}
			defer a.work.Unlock()
			if err := a.refreshLocal(ctx, a.config()); err != nil {
				a.setStatus(reason + " · " + err.Error())
			} else {
				a.setStatus(reason)
			}
		}
		refresh()
		timer := time.NewTicker(time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				refresh()
			}
		}
	}()
}

func (a *App) runBackground(ctx context.Context, first bool, now time.Time) {
	if !a.work.TryLock() {
		return
	}
	defer a.work.Unlock()
	cfg := a.config()
	if cfg.Role == "" {
		return
	}
	if cfg.Role == "b" && cfg.Repo != "" && cfg.Token != "" {
		if err := a.runCollector(ctx, cfg, first, now); err != nil {
			a.setStatus("同步失败 · " + err.Error())
			return
		}
		a.setStatus("")
		return
	}
	a.mu.RLock()
	previousAccount := a.localAccount
	a.mu.RUnlock()
	err := a.refreshLocal(ctx, cfg)
	if cfg.Role == "a" && err == nil {
		a.mu.RLock()
		current := a.localAccount
		a.mu.RUnlock()
		if current != "" && (first || current != previousAccount) {
			// A read is bounded and never refreshes or writes Codex credentials.
			if readErr := a.updateQuota(ctx); readErr != nil {
				err = errors.Join(err, readErr)
			}
		}
	}
	cfg = a.config()
	if cfg.Repo != "" && cfg.Token != "" {
		err = errors.Join(err, a.syncNow(ctx, cfg))
	}
	if cfg.Role == "a" && cfg.ShowSimulation {
		a.refreshSimulation(cfg)
	}
	if err != nil {
		a.setStatus(err.Error())
		return
	}
	a.setStatus("")
}

func (a *App) setStatus(s string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.status = s
}

func (a *App) updateQuota(ctx context.Context) error {
	cfg := a.config()
	if cfg.Role != "a" {
		return errors.New("仅 A 端可手动读取账号额度")
	}
	before := a.account()
	got, err := a.quota(ctx)
	if err != nil {
		return err
	}
	after := a.account()
	if before == "" || before != after {
		return errors.New("读取额度时账号发生变化或不可识别，请重试")
	}
	for _, q := range got {
		if q.Account != before {
			return errors.New("额度读数与当前账号不一致，请重试")
		}
	}
	next := make([]Quota, 0, len(cfg.Quotas)+len(got))
	for _, q := range cfg.Quotas {
		if q.At.After(time.Now().AddDate(0, 0, -retention)) {
			next = append(next, q)
		}
	}
	cfg.Quotas = append(next, got...)
	if err := a.save(cfg); err != nil {
		return err
	}
	return a.refreshLocal(ctx, cfg)
}

func (a *App) register(input string) error {
	cfg := a.config()
	if cfg.Role != "a" {
		return errors.New("仅 A 端可添加设备")
	}
	var device Device
	if err := parseCode(input, devicePrefix, &device); err != nil {
		return err
	}
	if !device.valid() {
		return errors.New("B 端设备码无效")
	}
	next := make(map[string]Device, len(cfg.Devices)+1)
	for id, d := range cfg.Devices {
		next[id] = d
	}
	device.Name = label(device.Name)
	next[device.ID] = device
	cfg.Devices = next
	return a.save(cfg)
}

type setup struct {
	Repo  string `json:"repo"`
	Name  string `json:"name"`
	Token string `json:"token"`
	Pair  string `json:"pair"`
}

func (a *App) configure(ctx context.Context, change setup) error {
	cfg := a.config()
	if cfg.Role == "" {
		return errors.New("请先选择设备角色")
	}
	if name := label(change.Name); name != "" {
		cfg.Name = name
	}
	repo := strings.TrimSpace(change.Repo)
	if cfg.Role == "b" {
		if change.Pair != "" {
			var pair Pair
			if err := parseCode(change.Pair, pairPrefix, &pair); err != nil {
				return err
			}
			if !pair.valid() {
				return errors.New("A 端配对码无效")
			}
			if cfg.Pair.Recipient != "" && cfg.Pair.Recipient != pair.Recipient {
				return errors.New("这个 B 端已配对。更换 A 端时请使用新的数据目录")
			}
			cfg.Pair = pair
		}
		repo = cfg.Pair.Repo
	}
	if repo == "" && cfg.Repo == "" && change.Token == "" {
		return a.save(cfg)
	}
	if !repoName.MatchString(repo) {
		return errors.New("请输入 GitHub 私有仓库，格式为 用户名/仓库名")
	}
	if cfg.Repo != "" && !strings.EqualFold(cfg.Repo, repo) {
		return errors.New("这个设备已绑定仓库。更换仓库时请使用新的数据目录")
	}
	if token := strings.TrimSpace(change.Token); token != "" {
		cfg.Token = token
	}
	if cfg.Token == "" {
		return errors.New("请在本机输入仅限该私有仓库的 GitHub 访问令牌")
	}
	if _, err := a.openRepo(ctx, repo, cfg.Token); err != nil {
		return fmt.Errorf("无法验证 GitHub 私有仓库 · %w", err)
	}
	cfg.Repo = repo
	return a.save(cfg)
}
