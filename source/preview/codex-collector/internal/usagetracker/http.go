package usagetracker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

func (a *App) Handler(assets fs.FS) http.Handler {
	files := http.FileServerFS(assets)
	guard := http.NewCrossOriginProtection()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/view", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, a.view(r.URL.Query()))
	})
	mux.HandleFunc("POST /api/action/{action}", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			jsonResponse(w, http.StatusUnsupportedMediaType, map[string]string{"error": "请使用应用内操作"})
			return
		}
		if !a.work.TryLock() {
			jsonResponse(w, http.StatusConflict, map[string]string{"error": "正在读取或同步，请稍后重试"})
			return
		}
		defer a.work.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
		defer cancel()
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
		err := a.action(ctx, r.PathValue("action"), decoder)
		if err != nil {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonResponse(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.Handle("/", files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			if err := guard.Check(r); err != nil || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (a *App) action(ctx context.Context, action string, decoder *json.Decoder) error {
	if a.config().Role == "b" {
		return errors.New("B 端由 A 端管理并自动同步")
	}
	switch action {
	case "simulation":
		var input struct{ Enabled bool }
		if err := decode(decoder, &input); err != nil {
			return err
		}
		return a.setSimulationEnabled(input.Enabled)
	case "frequency":
		var input struct{ Minutes int }
		if err := decode(decoder, &input); err != nil {
			return err
		}
		return a.setFrequency(input.Minutes)
	case "deploy":
		var input struct{ Tokens []string }
		if err := decode(decoder, &input); err != nil {
			return err
		}
		return a.exportDeployments(ctx, input.Tokens)
	case "deploy-one":
		var input struct {
			Name  string `json:"name"`
			Token string `json:"token"`
		}
		if err := decode(decoder, &input); err != nil {
			return err
		}
		return a.exportOneDeployment(ctx, input.Name, input.Token)
	case "deploy-tclash-one":
		var input struct {
			Name  string `json:"name"`
			Token string `json:"token"`
		}
		if err := decode(decoder, &input); err != nil {
			return err
		}
		return a.exportTClashDeployment(ctx, input.Name, input.Token)
	case "deploy-tclash-migration":
		var input struct{ Name string }
		if err := decode(decoder, &input); err != nil {
			return err
		}
		return a.exportTClashMigration(input.Name)
	case "role":
		var input struct{ Role string }
		if err := decode(decoder, &input); err != nil {
			return err
		}
		if input.Role != "a" {
			return errors.New("请从 A 端生成的安装包安装 B 端")
		}
		if err := a.setRole(input.Role); err != nil {
			return err
		}
		return a.refreshLocal(ctx, a.config())
	case "setup":
		var change setup
		if err := decode(decoder, &change); err != nil {
			return err
		}
		if err := a.configure(ctx, change); err != nil {
			return err
		}
		return a.refreshLocal(ctx, a.config())
	case "register":
		var input struct{ Code string }
		if err := decode(decoder, &input); err != nil {
			return err
		}
		return a.register(input.Code)
	case "rule":
		var rule PeriodRule
		if err := decode(decoder, &rule); err != nil {
			return err
		}
		return a.setRule(rule)
	case "refresh":
		return a.refreshLocal(ctx, a.config())
	case "confirm-account":
		var input struct {
			ID      string `json:"id"`
			Account string `json:"account"`
		}
		if err := decode(decoder, &input); err != nil {
			return err
		}
		return a.confirmAccount(ctx, input.ID, input.Account)
	case "quota":
		return a.updateQuota(ctx)
	case "sync":
		cfg := a.config()
		if err := a.syncNow(ctx, cfg); err != nil {
			a.setStatus(err.Error())
			return err
		}
		a.setStatus("")
		return nil
	default:
		return errors.New("无法识别此操作")
	}
}

func decode(d *json.Decoder, out any) error {
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || !errors.Is(d.Decode(new(any)), io.EOF) {
		return errors.New("输入格式不正确")
	}
	return nil
}

func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
