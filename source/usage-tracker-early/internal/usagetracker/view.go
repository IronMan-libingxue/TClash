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
}

type windowChoice struct {
	ID string `json:"id"`
	Quota
	Current bool `json:"current"`
}

type deviceCard struct {
	choice
	Total     Total     `json:"total"`
	Models    []Group   `json:"models,omitempty"`
	Updated   time.Time `json:"updated"`
	Pending   bool      `json:"pending,omitempty"`
	Simulated bool      `json:"simulated,omitempty"`
}

type view struct {
	Speed               speedReading `json:"speed"`
	Role                string       `json:"role"`
	Name                string       `json:"name"`
	Repo                string       `json:"repo,omitempty"`
	ReadAt              time.Time    `json:"readAt"`
	LastSync            time.Time    `json:"lastSync"`
	Status              string       `json:"status,omitempty"`
	SyncMinutes         int          `json:"syncMinutes,omitempty"`
	DeploymentDir       string       `json:"deploymentDir,omitempty"`
	Deployments         []string     `json:"deployments,omitempty"`
	Cards               []deviceCard `json:"cards,omitempty"`
	SimulationAvailable bool         `json:"simulationAvailable,omitempty"`
	SimulationEnabled   bool         `json:"simulationEnabled,omitempty"`
	SimulationStatus    string       `json:"simulationStatus,omitempty"`
	SimulatedDevice     bool         `json:"simulatedDevice,omitempty"`

	Devices            []choice       `json:"devices,omitempty"`
	Paired             []choice       `json:"paired,omitempty"`
	Accounts           []choice       `json:"accounts,omitempty"`
	AccountTargets     []choice       `json:"accountTargets,omitempty"`
	Account            string         `json:"account,omitempty"`
	AccountUnavailable bool           `json:"accountUnavailable,omitempty"`
	UnassignedCount    int            `json:"unassignedCount,omitempty"`
	Windows            []windowChoice `json:"windows,omitempty"`
	Quota              *Quota         `json:"quota,omitempty"`
	Official           *Quota         `json:"official,omitempty"`
	Window             string         `json:"window,omitempty"`
	Device             string         `json:"device,omitempty"`
	Cache              bool           `json:"cache"`
	Rule               *PeriodRule    `json:"rule,omitempty"`

	Total           *Total          `json:"total,omitempty"`
	Projects        []Group         `json:"projects,omitempty"`
	Models          []Group         `json:"models,omitempty"`
	Efforts         []Group         `json:"efforts,omitempty"`
	Sessions        []Group         `json:"sessions,omitempty"`
	Rows            []viewedRequest `json:"rows,omitempty"`
	Count           int             `json:"count"`
	Page            int             `json:"page"`
	DisplayProjects []DisplayGroup  `json:"displayProjects,omitempty"`
	DisplayModels   []DisplayGroup  `json:"displayModels,omitempty"`
}

func (a *App) view(q url.Values) view {
	a.mu.RLock()
	cfg, local, remote, readAt, status, localAccount := a.cfg, a.local, a.remote, a.readAt, a.status, a.localAccount
	simulated, simulationDevices, simulationStatus := a.simulated, a.simulationDevices, a.simulationStatus
	speed := a.speed
	a.mu.RUnlock()
	v := view{Speed: speed, Role: cfg.Role, Name: cfg.Name, Repo: cfg.Repo, ReadAt: readAt, LastSync: cfg.LastSync,
		Status: status, Cache: q.Get("cache") != "0"}
	if cfg.Role == "" {
		return v
	}
	days := local
	cards := map[string]*deviceCard{}
	cardModels := map[string]map[string]*Group{}
	simulatedIDs := map[string]bool{}
	if cfg.Role == "a" {
		v.SyncMinutes = int(cfg.Policy.interval() / time.Minute)
		v.DeploymentDir = cfg.DeploymentDir
		if cfg.DeploymentDir != "" {
			for _, target := range deploymentDevices {
				if info, err := os.Stat(filepath.Join(cfg.DeploymentDir, target.Name+".zip")); err == nil && info.Mode().IsRegular() {
					v.Deployments = append(v.Deployments, target.Name)
				}
			}
		}
		v.SimulationAvailable = cfg.SimulationRoot != ""
		v.SimulationEnabled = cfg.ShowSimulation
		v.SimulationStatus = simulationStatus
		localDevice := choice{"local", "Mac 1（本机）"}
		v.Devices = []choice{{"all", "全部设备"}, localDevice}
		realNames := map[string]bool{}
		for _, d := range cfg.Devices {
			v.Paired = append(v.Paired, choice{d.ID, d.Name})
			realNames[d.Name] = true
		}
		slices.SortFunc(v.Paired, compareChoices)
		v.Devices = append(v.Devices, v.Paired...)
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
	var auto *Quota
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
		if cfg.ShowSimulation {
			for _, day := range simulated {
				if simulatedIDs[day.Device] {
					days = append(days, simulationDayForWindow(day, v.Quota, now))
				}
			}
		}
	}
	var total Total
	projects, models, sessions := map[string]*Group{}, map[string]*Group{}, map[string]*Group{}
	efforts := map[string]*Group{}
	project, model, session := q.Get("project"), q.Get("model"), q.Get("session")
	add := func(m map[string]*Group, id, name string, row Request, factor float64) {
		g := m[id]
		if g == nil {
			g = &Group{ID: id, Name: name}
			m[id] = g
		}
		g.Total.add(row, v.Quota, factor)
	}
	var matching []viewedRequest
	for _, day := range days {
		if v.Device != "all" && v.Device != day.Device {
			continue
		}
		factor := rule.factor(day.Device)
		isSimulated := simulatedIDs[day.Device]
		card := cards[day.Device]
		if card != nil && day.At.After(card.Updated) {
			card.Updated = day.At
		}
		for _, r := range day.Rows {
			if v.Quota != nil && (!v.Quota.includes(r.At) || (cfg.Role == "b" && r.Account != v.Quota.Account)) {
				continue
			}
			if cfg.Role == "a" {
				if !isSimulated && r.Account == "" {
					v.UnassignedCount++
				}
				if !isSimulated && ((v.Account == "unassigned" && r.Account != "") || (v.Account != "unassigned" && v.Account != "all" && (selectedAccount == "" || r.Account != selectedAccount))) {
					continue
				}
				if isSimulated && v.Account == "unassigned" {
					continue
				}
				if isSimulated && v.Device == "all" {
					if card != nil {
						card.Total.add(r, v.Quota, factor)
						add(cardModels[day.Device], r.Model, r.Model, r, factor)
					}
					continue
				}
				if !accept(project, r.Project) || !accept(model, r.Model) ||
					!accept(session, day.Device+"/"+r.Session) {
					continue
				}
				name := day.Name
				if isSimulated {
					name += "（模拟）"
				}
				matching = append(matching, viewedRequest{Request: r, Device: day.Device, DeviceName: name, Simulated: isSimulated})
				add(sessions, day.Device+"/"+r.Session, r.Session[:min(10, len(r.Session))], r, factor)
				if model != "" {
					add(efforts, r.Effort, cmp.Or(r.Effort, "未记录"), r, factor)
				}
			}
			add(projects, r.Project, r.ProjectName, r, factor)
			add(models, r.Model, r.Model, r, factor)
			total.add(r, v.Quota, factor)
			if card != nil {
				card.Total.add(r, v.Quota, factor)
				add(cardModels[day.Device], r.Model, r.Model, r, factor)
			}
		}
	}
	if cfg.Role == "b" {
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
	for id, card := range cards {
		card.Models = groups(cardModels[id], v.Cache)
		v.Cards = append(v.Cards, *card)
	}
	slices.SortFunc(v.Cards, func(a, b deviceCard) int { return compareChoices(a.choice, b.choice) })
	v.Projects, v.Models, v.Sessions = groups(projects, v.Cache), groups(models, v.Cache), groups(sessions, v.Cache)
	v.Efforts = groups(efforts, v.Cache)
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

func accept(selected, value string) bool {
	return selected == "" || selected == value
}

func compareChoices(a, b choice) int {
	return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
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
		if quota != nil {
			day.Rows[i].Account = quota.Account
			if quota.includes(now) && !quota.includes(day.Rows[i].At) {
				day.Rows[i].At = quota.Start
			}
		}
	}
	return day
}
