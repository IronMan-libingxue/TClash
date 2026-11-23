package usagetracker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/sessions"
	"github.com/yetone/magpie/internal/settings"
)

const (
	sharedPriceFile   = "prices.tclash"
	sharedPriceFormat = "tclash-pricing-v1\x00"
	sharedPriceLimit  = 2 << 20
)

type sharedPriceTable struct {
	Revision int64                    `json:"revision"`
	Source   string                   `json:"source"`
	Agent    json.RawMessage          `json:"agent,omitempty"`
	Magpie   map[string]catalog.Price `json:"magpie,omitempty"`
}

func magpiePriceSnapshot(agent *agentPriceTable) map[string]catalog.Price {
	models := catalog.PriceModelIDs()
	for _, model := range agent.Models {
		models = append(models, model.ID)
	}
	prices := map[string]catalog.Price{}
	for _, id := range models {
		if _, exists := prices[id]; exists {
			continue
		}
		if price, known := sessions.PriceOf(settings.Settings{}, id); known {
			prices[id] = price
		}
	}
	return prices
}

func (p controllerPricing) sharedTable() sharedPriceTable {
	table := sharedPriceTable{Source: p.prefs.Source}
	if table.Source == agentSpeedSource {
		table.Agent = bundledAgentPrices
		if p.agentCached {
			table.Agent = p.prefs.AgentCache
		}
	} else {
		table.Magpie = p.magpie
	}
	return table
}

func openPublishedPricing(data, verifier []byte) (controllerPricing, error) {
	if len(data) > sharedPriceLimit {
		return controllerPricing{}, errors.New("A 端价表超出大小限制")
	}
	body, err := verify(sharedPriceFormat, data, verifier)
	if err != nil {
		return controllerPricing{}, err
	}
	var table sharedPriceTable
	if json.Unmarshal(body, &table) != nil || table.Revision < 1 {
		return controllerPricing{}, errors.New("A 端价表版本无效")
	}
	p := controllerPricing{
		prefs:     pricePreferences{Source: table.Source},
		published: table.Revision,
	}
	switch table.Source {
	case agentSpeedSource:
		p.agent, err = parseAgentPrices(table.Agent, time.Time{})
		if err != nil {
			return controllerPricing{}, errors.New("A 端 Agent Speed 价表无效")
		}
		p.agentCached, p.prefs.AgentCache = true, table.Agent
	case magpieSource:
		if len(table.Magpie) == 0 {
			return controllerPricing{}, errors.New("A 端 Magpie 价表无有效模型")
		}
		p.magpie = table.Magpie
	default:
		return controllerPricing{}, errors.New("无法识别 A 端价表来源")
	}
	return p, nil
}

func readPublishedPricing(dir string, verifier []byte) (controllerPricing, error) {
	data, err := os.ReadFile(filepath.Join(dir, sharedPriceFile))
	if err != nil {
		return controllerPricing{}, err
	}
	return openPublishedPricing(data, verifier)
}

func (a *App) publishPricing(ctx context.Context, store transport, cfg *config, now time.Time) error {
	a.mu.RLock()
	table := a.pricing.sharedTable()
	a.mu.RUnlock()
	if len(table.Magpie) == 0 && len(table.Agent) == 0 {
		return errors.New("本机有效价表尚未就绪")
	}
	payload, err := json.Marshal(table)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	if cfg.PriceDigest == digest {
		return nil
	}
	table.Revision = max(now.UnixMilli(), cfg.PriceRevision+1)
	payload, err = json.Marshal(table)
	if err != nil {
		return err
	}
	packet := sign(sharedPriceFormat, payload, cfg.Signer)
	if len(packet) > sharedPriceLimit {
		return errors.New("本机有效价表超出大小限制")
	}
	if err = store.Write(ctx, controls, sharedPriceFile, packet); err != nil {
		return err
	}
	cfg.PriceRevision, cfg.PriceDigest = table.Revision, digest
	return nil
}

func (a *App) receivePricing(ctx context.Context, store transport, cfg config) error {
	packet, err := store.Read(ctx, controls, sharedPriceFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	pricing, err := openPublishedPricing(packet, cfg.Pair.Verifier)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if pricing.published < a.pricing.published {
		return errors.New("A 端价表版本早于本机有效版本")
	}
	if pricing.published == a.pricing.published {
		return nil
	}
	if err = writePrivate(filepath.Join(a.dir, sharedPriceFile), packet); err != nil {
		return err
	}
	a.pricing = pricing
	a.local = repricedDays(a.local, pricing, true)
	return nil
}

func (a *App) setPriceSyncError(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.priceSyncError = ""
	if err != nil {
		a.priceSyncError = err.Error()
	}
}
