package usagetracker

import (
	"cmp"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type choice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type viewedRequest struct {
	Request
	Device       string `json:"device"`
	DeviceName   string `json:"deviceName"`
	Simulated    bool   `json:"simulated,omitempty"`
	OtherAccount bool   `json:"otherAccount,omitempty"`
	ActualTier   bool   `json:"actualTier,omitempty"`
	PriceReason  string `json:"priceReason,omitempty"`
}

type windowChoice struct {
	ID string `json:"id"`
	Quota
	Current bool `json:"current"`
}

type deviceCard struct {
	choice
	Total         Total          `json:"total"`
	Unassigned    Total          `json:"unassigned"`
	OtherAccount  Total          `json:"otherAccount"`
	Models        []Group        `json:"models,omitempty"`
	Updated       time.Time      `json:"updated"`
	CountSince    time.Time      `json:"countSince,omitempty"`
	Pending       bool           `json:"pending,omitempty"`
	Simulated     bool           `json:"simulated,omitempty"`
	Telemetry     *Telemetry     `json:"telemetry,omitempty"`
	ActivityStats *ActivityStats `json:"activityStats,omitempty"`
	FastTotal     Total          `json:"fastTotal"`
	FastModels    []Group        `json:"fastModels,omitempty"`
	FastSessions  []Group        `json:"fastSessions,omitempty"`
	TierUnknown   int            `json:"tierUnknown,omitempty"`
	Scaled        bool           `json:"scaled,omitempty"`
	DisplayFactor float64        `json:"displayFactor,omitempty"`
	ActivityLevel string         `json:"activityLevel,omitempty"`
}

type view struct {
	Speed               speedReading   `json:"speed"`
	Role                string         `json:"role"`
	Name                string         `json:"name"`
	Repo                string         `json:"repo,omitempty"`
	ReadAt              time.Time      `json:"readAt"`
	LastSync            time.Time      `json:"lastSync"`
	CountSince          time.Time      `json:"countSince,omitempty"`
	Status              string         `json:"status,omitempty"`
	SyncMinutes         int            `json:"syncMinutes,omitempty"`
	DeploymentDir       string         `json:"deploymentDir,omitempty"`
	Deployments         []string       `json:"deployments,omitempty"`
	Cards               []deviceCard   `json:"cards,omitempty"`
	SimulationAvailable bool           `json:"simulationAvailable,omitempty"`
	SimulationEnabled   bool           `json:"simulationEnabled,omitempty"`
	SimulationStatus    string         `json:"simulationStatus,omitempty"`
	SimulatedDevice     bool           `json:"simulatedDevice,omitempty"`
	Telemetry           *Telemetry     `json:"telemetry,omitempty"`
	ActivityStats       *ActivityStats `json:"activityStats,omitempty"`
	Pricing             *pricingStatus `json:"pricing,omitempty"`
	NoticePermission    string         `json:"noticePermission,omitempty"`
	NoticesEnabled      bool           `json:"noticesEnabled,omitempty"`

	Devices            []choice          `json:"devices,omitempty"`
	Paired             []choice          `json:"paired,omitempty"`
	DeviceTargets      []choice          `json:"deviceTargets,omitempty"`
	DeviceAliases      map[string]string `json:"deviceAliases,omitempty"`
	Accounts           []choice          `json:"accounts,omitempty"`
	AccountTargets     []choice          `json:"accountTargets,omitempty"`
	Account            string            `json:"account,omitempty"`
	AccountUnavailable bool              `json:"accountUnavailable,omitempty"`
	UnassignedCount    int               `json:"unassignedCount,omitempty"`
	Windows            []windowChoice    `json:"windows,omitempty"`
	Quota              *Quota            `json:"quota,omitempty"`
	Official           *Quota            `json:"official,omitempty"`
	Window             string            `json:"window,omitempty"`
	Device             string            `json:"device,omitempty"`
	Cache              bool              `json:"cache"`
	Rule               *PeriodRule       `json:"rule,omitempty"`
	CurrentRule        *PeriodRule       `json:"currentRule,omitempty"`
	ScaledDevices      map[string]bool   `json:"scaledDevices,omitempty"`
	Scaled             bool              `json:"scaled,omitempty"`
	DisplayFactor      float64           `json:"displayFactor,omitempty"`
	SessionQuota       *Quota            `json:"sessionQuota,omitempty"`
	SessionRule        *PeriodRule       `json:"sessionRule,omitempty"`
	SessionEfforts     []Group           `json:"sessionEfforts,omitempty"`

	Total            *Total          `json:"total,omitempty"`
	DeviceTotal      *Total          `json:"deviceTotal,omitempty"`
	DeviceUnassigned *Total          `json:"deviceUnassigned,omitempty"`
	Projects         []Group         `json:"projects,omitempty"`
	Models           []Group         `json:"models,omitempty"`
	Efforts          []Group         `json:"efforts,omitempty"`
	Sessions         []Group         `json:"sessions,omitempty"`
	Rows             []viewedRequest `json:"rows,omitempty"`
	Count            int             `json:"count"`
	Page             int             `json:"page"`
	DisplayProjects  []DisplayGroup  `json:"displayProjects,omitempty"`
	DisplayModels    []DisplayGroup  `json:"displayModels,omitempty"`
	DisplayTotal     *DisplayGroup   `json:"displayTotal,omitempty"`
}

func (a *App) view(q url.Values) view {
	a.mu.RLock()
	cfg, local, remote, readAt, status, localAccount := a.cfg, a.local, a.remote, a.readAt, a.status, a.localAccount
	simulated, simulationDevices, simulationStatus := a.simulated, a.simulationDevices, a.simulationStatus
	speed := a.speed
	pricing, priceSyncError := a.pricing, a.priceSyncError
	a.mu.RUnlock()
	v := view{Speed: speed, Role: cfg.Role, Name: cfg.Name, Repo: cfg.Repo, ReadAt: readAt, LastSync: cfg.LastSync,
		Status: status, Cache: q.Get("cache") != "0"}
	if cfg.Role == "" {
		return v
	}
	days := local
	cards := map[string]*deviceCard{}
	cardModels := map[string]map[string]*Group{}
	cardFastModels := map[string]map[string]*Group{}
	cardFastSessions := map[string]map[string]*Group{}
	simulatedIDs := map[string]bool{}
	if cfg.Role == "a" {
		prefs, status := a.controllerSettings()
		v.ScaledDevices, v.NoticesEnabled, v.NoticePermission = prefs.Scaled, !prefs.Muted, status
		price := pricing.status(slices.Concat(local, remote), time.Now())
		price.Publication, price.PublicationError = cfg.PriceRevision, priceSyncError
		v.Pricing = &price
		v.SyncMinutes = int(cfg.Policy.interval() / time.Minute)
		v.DeviceAliases = cfg.DeviceAliases
		for _, target := range deploymentDevices {
			v.DeviceTargets = append(v.DeviceTargets, choice{target.Name, target.Name})
		}
		v.DeploymentDir = cfg.DeploymentDir
		if cfg.DeploymentDir != "" {
			for _, target := range deploymentDevices {
				if info, err := os.Stat(filepath.Join(cfg.DeploymentDir, target.Name+".zip")); err == nil && info.Mode().IsRegular() {
					v.Deployments = append(v.Deployments, target.Name)
				} else if info, err := os.Stat(filepath.Join(cfg.DeploymentDir, target.Name+" TClash.exe")); err == nil && info.Mode().IsRegular() {
					v.Deployments = append(v.Deployments, target.Name+" TClash")
				} else if info, err := os.Stat(filepath.Join(cfg.DeploymentDir, target.Name+" TClash 安装.app")); err == nil && info.IsDir() {
					v.Deployments = append(v.Deployments, target.Name+" TClash")
				} else if info, err := os.Stat(filepath.Join(cfg.DeploymentDir, target.Name+" TClash.zip")); err == nil && info.Mode().IsRegular() {
					v.Deployments = append(v.Deployments, target.Name+" TClash")
				}
				if info, err := os.Stat(filepath.Join(cfg.DeploymentDir, target.Name+" TClash 迁移.zip")); err == nil && info.Mode().IsRegular() {
					v.Deployments = append(v.Deployments, target.Name+" TClash 迁移")
				}
			}
		}
		v.SimulationAvailable = cfg.SimulationRoot != ""
		v.SimulationEnabled = cfg.ShowSimulation
		v.SimulationStatus = simulationStatus
		localDevice := choice{"local", deviceDisplayName("local", "Mac 1", cfg.DeviceAliases)}
		v.Devices = []choice{{"all", "全部设备"}, localDevice}
		realNames := map[string]bool{}
		for _, d := range cfg.Devices {
			v.Paired = append(v.Paired, choice{d.ID, d.Name})
			realNames[d.Name] = true
		}
		slices.SortFunc(v.Paired, compareChoices)
		for _, item := range v.Paired {
			v.Devices = append(v.Devices, choice{item.ID, deviceDisplayName(item.ID, item.Name, cfg.DeviceAliases)})
		}
		if cfg.ShowSimulation {
			for _, d := range simulationDevices {
				if realNames[d.Name] {
					continue
				}
				simulatedIDs[d.ID] = true
				v.Devices = append(v.Devices, choice{d.ID, d.Name + "（模拟）"})
			}
		}
		v.Device = q.Get("device")
		if v.Device == "" || (strings.HasPrefix(v.Device, simulationPrefix) && !simulatedIDs[v.Device]) {
			v.Device = "all"
		}
		v.SimulatedDevice = simulatedIDs[v.Device]
		days = slices.Concat(local, remote)
		if v.Device == "all" {
			for _, item := range v.Devices[1:] {
				card := &deviceCard{choice: item, Simulated: simulatedIDs[item.ID]}
				if card.Simulated {
					card.Name = strings.TrimSuffix(item.Name, "（模拟）")
				}
				cards[item.ID] = card
				cardModels[item.ID] = map[string]*Group{}
				cardFastModels[item.ID] = map[string]*Group{}
				cardFastSessions[item.ID] = map[string]*Group{}
			}
			pendingNames := map[string]bool{}
			for id, invite := range cfg.Invites {
				pendingNames[invite.Name] = true
				if realNames[invite.Name] || simulationHasName(simulationDevices, simulatedIDs, invite.Name) {
					continue
				}
				key := "pending-" + id
				cards[key] = &deviceCard{choice: choice{key, invite.Name}, Pending: true}
			}
			for _, device := range deploymentDevices {
				if realNames[device.Name] || pendingNames[device.Name] || simulationHasName(simulationDevices, simulatedIDs, device.Name) {
					continue
				}
				key := "pending-" + device.Name
				cards[key] = &deviceCard{choice: choice{key, device.Name}, Pending: true}
			}
		}
	} else {
		v.Device = cfg.device().ID
		v.CountSince = cfg.CountSince
	}
	now := time.Now()
	windows := periods(days)
	if cfg.Role == "a" {
		windows = periods(local)
	}
	selectedAccount := localAccount
	if cfg.Role == "a" {
		known := map[string]bool{}
		if localAccount != "" {
			known[localAccount] = true
		}
		for _, day := range days {
			for _, row := range day.Rows {
				if row.Account != "" {
					known[row.Account] = true
				}
			}
			for _, reading := range day.Quotas {
				if reading.Account != "" {
					known[reading.Account] = true
				}
			}
		}
		for _, fix := range cfg.AccountCorrections {
			if fix.Account != "" {
				known[fix.Account] = true
			}
			if fix.FromAccount != "" {
				known[fix.FromAccount] = true
			}
		}
		v.Account = q.Get("account")
		if v.Account == "" || v.Account == "current" {
			v.Account = "current"
		} else if v.Account != "unassigned" && v.Account != "all" && !known[v.Account] {
			v.Account = "current"
		}
		v.AccountUnavailable = localAccount == ""
		currentName := "当前登录账号"
		if localAccount == "" {
			currentName += "（暂不可识别）"
		}
		v.Accounts = append(v.Accounts, choice{"current", currentName})
		if localAccount != "" {
			v.AccountTargets = append(v.AccountTargets, choice{localAccount, currentName})
		}
		var other []choice
		for id := range known {
			if id != localAccount {
				other = append(other, choice{id, "其他账号 · " + id[:min(8, len(id))]})
			}
		}
		slices.SortFunc(other, compareChoices)
		v.Accounts = append(v.Accounts, other...)
		v.AccountTargets = append(v.AccountTargets, other...)
		v.Accounts = append(v.Accounts, choice{"all", "全部账号 · 日志总量"})
		v.Accounts = append(v.Accounts, choice{"unassigned", "账号待确认"})
		if v.Account != "current" && v.Account != "unassigned" && v.Account != "all" {
			selectedAccount = v.Account
		}
	}
	v.Window = q.Get("window")
	// Entering a device's full detail should keep the same selected period,
	// even if that period originally belonged to a non-current account.
	if cfg.Role == "a" && v.Account == "all" && v.Window != "" && v.Window != "all" {
		for _, w := range windows {
			if w.id() == v.Window {
				selectedAccount = w.Account
				break
			}
		}
	}
	var auto, active *Quota
	for _, w := range windows {
		if cfg.Role == "a" && (selectedAccount == "" || w.Account != selectedAccount) {
			continue
		}
		if cfg.Role == "a" {
			v.Windows = append(v.Windows, windowChoice{w.id(), w, w.includes(now)})
		} else {
			// B needs the window label and identifier for filtering, not
			// account allowance readings or its unscaled usage.
			public := Quota{Start: w.Start, End: w.End, Cutoff: w.Cutoff}
			v.Windows = append(v.Windows, windowChoice{w.id(), public, w.includes(now)})
		}
		if v.Window == w.id() {
			selected := w
			v.Quota = &selected
		}
		if cfg.Role == "a" && w.includes(now) && (active == nil || w.End.After(active.End)) {
			selected := w
			active = &selected
		}
		if (w.includes(now) || (cfg.Role == "a" && v.Account != "current" && v.Account != "unassigned" && v.Account != "all")) && (auto == nil ||
			(w.Account == localAccount && auto.Account != localAccount) ||
			((w.Account == localAccount) == (auto.Account == localAccount) && w.End.After(auto.End))) {
			selected := w
			auto = &selected
		}
	}
	if v.Window == "" || (v.Window != "all" && v.Quota == nil) {
		v.Quota = auto
	}
	if v.Quota != nil {
		v.Window = v.Quota.id()
		if cfg.Role == "a" && v.Account != "unassigned" && v.Account != "all" {
			for _, reading := range cfg.Quotas {
				if reading.Direct && reading.valid() && sameReset(reading, *v.Quota) && (v.Official == nil || reading.At.After(v.Official.At)) {
					copy := reading
					v.Official = &copy
				}
			}
		}
	} else {
		v.Window = "all"
	}
	rule := PeriodRule{Factor: defaultFactor, Budget: defaultBudget}
	if v.Quota != nil {
		rule = cfg.Policy.rule(*v.Quota)
		rule.End, rule.Account = v.Quota.End, v.Quota.Account
	}
	if cfg.Role == "a" {
		v.Rule = &rule
		if active != nil {
			currentRule := cfg.Policy.rule(*active)
			currentRule.End, currentRule.Account = active.End, active.Account
			v.CurrentRule = &currentRule
		}
		if _, paired := cfg.Devices[v.Device]; paired && v.ScaledDevices[v.Device] {
			v.Scaled, v.DisplayFactor = true, rule.factor(v.Device)
		}
		// A session belongs to a single account/device scope and is always
		// summarised against its current verified period, even from a
		// historical or model-filtered request list.
		if active != nil && q.Get("session") != "" {
			v.SessionQuota, v.SessionRule = active, v.CurrentRule
		}
		if cfg.ShowSimulation {
			for _, day := range simulated {
				if simulatedIDs[day.Device] {
					days = append(days, simulationDayForWindow(day, v.Quota, now))
				}
			}
		}
	}
	var localActivity *Activity
	if cfg.Role == "b" {
		localTelemetry := a.telemetrySnapshot()
		if localTelemetry.Version > 0 {
			v.Telemetry = &localTelemetry
			v.ActivityStats = activityStats(localTelemetry.Activity, v.Quota)
			localActivity = localTelemetry.Activity
		}
	} else {
		var shared *Telemetry
		if a.localTelemetry != nil {
			shared = a.localTelemetry()
		}
		own := a.telemetrySnapshot()
		combined := mergeTelemetry(&own, shared)
		if activityFresh(own.Activity, now) && (own.Activity.Active > 0 || !own.Activity.Uncertain) {
			combined.Activity = own.Activity
		}
		localActivity = combined.Activity
		if card := cards["local"]; card != nil && combined.Version > 0 {
			card.Telemetry = combined
		}
	}
	v.Speed = visibleSpeed(speed, localActivity, now)
	var total, deviceTotal, deviceUnassigned Total
	projects, models, sessions := map[string]*Group{}, map[string]*Group{}, map[string]*Group{}
	efforts := map[string]*Group{}
	sessionEfforts := map[string]*Group{}
	project, model, session := q.Get("project"), q.Get("model"), q.Get("session")
	add := func(m map[string]*Group, id, name string, row Request, factor float64) {
		g := m[id]
		if g == nil {
			g = &Group{ID: id, Name: name}
			m[id] = g
		}
		if row.At.After(g.LastAt) {
			g.LastAt = row.At
		}
		g.Total.add(row, v.Quota, factor)
	}
	addFastSession := func(device string, row Request, factor float64) {
		id := row.FastOwner
		name := "归属未知"
		if id == "" {
			id = "unknown"
		} else {
			name = "主会话 " + id[:min(10, len(id))]
		}
		add(cardFastSessions[device], id, name, row, factor)
	}
	accountMatches := func(r Request, simulated bool) bool {
		if simulated {
			return v.Account != "unassigned" && v.Device != "all"
		}
		switch v.Account {
		case "unassigned":
			return r.Account == ""
		case "all":
			return true
		default:
			return selectedAccount != "" && r.Account == selectedAccount
		}
	}
	addEffort := func(groups map[string]*Group, r Request) {
		mode := modeOf(r.Tier)
		add(groups, r.Effort+"/"+mode, effortName(r.Effort)+" · "+modeName(mode), r, 1)
	}
	var matching []viewedRequest
	for _, day := range days {
		if v.Device != "all" && v.Device != day.Device {
			continue
		}
		// The A display factor is applied after aggregating the raw data.
		factor := 1.0
		if cfg.Role == "b" {
			factor = rule.factor(day.Device)
		}
		isSimulated := simulatedIDs[day.Device]
		card := cards[day.Device]
		if card != nil && day.Telemetry != nil {
			card.Telemetry = mergeTelemetry(card.Telemetry, day.Telemetry)
		}
		if card != nil && day.At.After(card.Updated) {
			card.Updated = day.At
		}
		if card != nil && day.CountSince.After(card.CountSince) {
			card.CountSince = day.CountSince
		}
		for _, r := range day.Rows {
			sessionMatches := accept(session, day.Device+"/"+r.Session) ||
				(r.FastOwner != "" && session == day.Device+"/"+r.FastOwner)
			if v.SessionQuota != nil && v.SessionQuota.includes(r.At) && sessionMatches && accountMatches(r, isSimulated) {
				addEffort(sessionEfforts, r)
			}
			if v.Quota != nil && !v.Quota.includes(r.At) {
				continue
			}
			if cfg.Role == "a" {
				// Device cards cover all local records in the selected time
				// window, independently of account-attribution filters.
				if card != nil {
					card.Total.add(r, v.Quota, factor)
					add(cardModels[day.Device], r.Model, r.Model, r, factor)
					if r.Account == "" && !isSimulated {
						card.Unassigned.add(r, v.Quota, factor)
						deviceUnassigned.add(r, v.Quota, factor)
					} else if r.Account != "" && selectedAccount != "" && r.Account != selectedAccount && !isSimulated {
						card.OtherAccount.add(r, v.Quota, factor)
					}
					if !isSimulated {
						deviceTotal.add(r, v.Quota, factor)
					}
					if modeOf(r.Tier) == "fast" {
						card.FastTotal.add(r, v.Quota, factor)
						add(cardFastModels[day.Device], r.Model, r.Model, r, factor)
						addFastSession(day.Device, r, factor)
					} else if modeOf(r.Tier) == "unknown" {
						card.TierUnknown++
					}
				}
				if !isSimulated && r.Account == "" {
					v.UnassignedCount++
				}
				if !accountMatches(r, isSimulated) {
					continue
				}
				if !accept(project, r.Project) || !accept(model, r.Model) ||
					!sessionMatches {
					continue
				}
				name := day.Name
				if day.Device == "local" {
					name = deviceDisplayName("local", "Mac 1", cfg.DeviceAliases)
				} else if device, ok := cfg.Devices[day.Device]; ok {
					name = deviceDisplayName(device.ID, device.Name, cfg.DeviceAliases)
				}
				if isSimulated {
					name += "（模拟）"
				}
				matching = append(matching, viewedRequest{Request: r, Device: day.Device, DeviceName: name,
					Simulated: isSimulated, ActualTier: r.actualTier || r.ObservedTier, PriceReason: r.priceReason})
				add(sessions, day.Device+"/"+r.Session, r.Session[:min(10, len(r.Session))], r, factor)
				if model != "" {
					add(efforts, r.Effort, cmp.Or(r.Effort, "未记录"), r, factor)
				}
			}
			add(projects, r.Project, r.ProjectName, r, factor)
			add(models, r.Model, r.Model, r, factor)
			total.add(r, v.Quota, factor)
		}
	}
	if cfg.Role == "b" {
		if len(cfg.Pair.Verifier) > 0 {
			v.Pricing = &pricingStatus{Source: pricing.prefs.Source, Publication: pricing.published, PublicationError: priceSyncError}
		}
		summary := display(Group{Total: total}, v.Cache, rule.factor(v.Device), rule.Budget, v.Quota != nil)
		v.DisplayTotal, v.Count = &summary, total.Count
		for _, g := range groups(projects, v.Cache) {
			v.DisplayProjects = append(v.DisplayProjects, display(g, v.Cache, rule.factor(v.Device), rule.Budget, v.Quota != nil))
		}
		for _, g := range groups(models, v.Cache) {
			v.DisplayModels = append(v.DisplayModels, display(g, v.Cache, rule.factor(v.Device), rule.Budget, v.Quota != nil))
		}
		v.Quota = nil
		return v
	}
	v.Total = &total
	if v.Device == "all" {
		v.DeviceTotal, v.DeviceUnassigned = &deviceTotal, &deviceUnassigned
	}
	for id, card := range cards {
		if card.Telemetry != nil {
			activity := card.Telemetry.Activity
			card.ActivityStats = activityStats(activity, v.Quota)
			if freshObservation(activity, now, observationLimit(id, cfg.Policy)) {
				card.ActivityLevel = activityLevel(activityFlags(activity), activity.Active > 0)
			}
		}
		if _, paired := cfg.Devices[id]; paired && v.ScaledDevices[id] {
			card.Scaled, card.DisplayFactor = true, rule.factor(id)
		}
		card.Models = groups(cardModels[id], v.Cache)
		card.FastModels = groups(cardFastModels[id], v.Cache)
		card.FastSessions = groups(cardFastSessions[id], v.Cache)
		v.Cards = append(v.Cards, *card)
	}
	slices.SortFunc(v.Cards, func(a, b deviceCard) int { return compareChoices(a.choice, b.choice) })
	v.Projects, v.Models, v.Sessions = groups(projects, v.Cache), groups(models, v.Cache), groups(sessions, v.Cache)
	v.Efforts = groups(efforts, v.Cache)
	v.SessionEfforts = groups(sessionEfforts, v.Cache)
	// Hide secret-free internal account identifiers from individual requests.
	for i := range matching {
		matching[i].OtherAccount = !matching[i].Simulated && v.Quota != nil && matching[i].Account != "" && matching[i].Account != v.Quota.Account
		if matching[i].Simulated || (v.Quota != nil && matching[i].Account != v.Quota.Account) {
			matching[i].Account = ""
		} else if matching[i].Account != "" {
			matching[i].Account = "official"
		}
	}
	slices.SortFunc(matching, func(a, b viewedRequest) int {
		return cmp.Or(b.At.Compare(a.At), strings.Compare(a.Device, b.Device), strings.Compare(a.ID, b.ID))
	})
	v.Count = len(matching)
	const pageSize = 50
	page, _ := strconv.Atoi(q.Get("page"))
	v.Page = min(max(page, 0), max(0, (v.Count-1)/pageSize))
	start := v.Page * pageSize
	v.Rows = matching[start:min(start+pageSize, v.Count)]
	return v
}

func modeName(mode string) string {
	switch mode {
	case "normal":
		return "标准"
	case "fast":
		return "⚡ Fast"
	case "ultrafast":
		return ultrafastLabel
	default:
		return "档位未知"
	}
}

func effortName(effort string) string {
	if effort == "xhigh" {
		return "X High"
	}
	return cmp.Or(effort, "未记录")
}

func accept(selected, value string) bool {
	return selected == "" || selected == value
}

func compareChoices(a, b choice) int {
	return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
}

func deviceDisplayName(id, slot string, aliases map[string]string) string {
	name := slot
	if alias := aliases[id]; alias != "" && alias != slot {
		name = alias + " · " + slot
	}
	if id == "local" {
		name += "（本机）"
	}
	return name
}

func simulationHasName(devices []choice, enabled map[string]bool, name string) bool {
	for _, device := range devices {
		if device.Name == name && enabled[device.ID] {
			return true
		}
	}
	return false
}

func simulationDayForWindow(day Day, quota *Quota, now time.Time) Day {
	day.Rows = slices.Clone(day.Rows)
	for i := range day.Rows {
		if i == 0 && (day.Name == "Mac 2" || day.Name == "Mac 4") {
			day.Rows[i].Tier, day.Rows[i].FastOwner = "fast", day.Rows[i].Session
		}
		if quota != nil {
			day.Rows[i].Account = quota.Account
			if quota.includes(now) && !quota.includes(day.Rows[i].At) {
				day.Rows[i].At = quota.Start
			}
		}
	}
	if day.Telemetry == nil {
		active, fast := 1, 0
		switch day.Name {
		case "Mac 2":
			fast = 1
		case "Mac 3":
			active = 2
		case "Mac 4":
			active, fast = 2, 1
		}
		activity := &Activity{At: now, Active: active, Fast: fast, Incomplete: true}
		if active >= 2 {
			activity.Intervals = []ActivityInterval{{Start: now.Add(-12 * time.Minute), End: now, Count: active, New: true}}
		}
		traffic := &TrafficMonth{Month: now.In(beijing).Format("2006-01"), Since: now.Add(-24 * time.Hour), At: now, Upload: 12 << 20, Download: 64 << 20, Incomplete: true,
			Groups: []TrafficGroup{{Name: "示例代理组 A", Upload: 8 << 20, Download: 40 << 20}, {Name: "示例代理组 B", Upload: 4 << 20, Download: 24 << 20}}}
		day.Telemetry = &Telemetry{Version: 1, Activity: activity, Traffic: traffic}
	}
	return day
}
