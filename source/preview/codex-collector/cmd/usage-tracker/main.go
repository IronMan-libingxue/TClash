package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/yetone/magpie/internal/filelock"
	"github.com/yetone/magpie/internal/usagetracker"
)

const name = "Codex 用量"

//go:embed assets/*
var embedded embed.FS

func main() {
	dataDir := flag.String("data-dir", "", "independent app data directory")
	role := flag.String("role", "", "initial role for a new data directory (a or b)")
	web := flag.Bool("web", false, "open a password-protected loopback web view")
	port := flag.Int("port", 4321, "loopback web port")
	enroll := flag.String("enroll", "", "device installation authorization")
	installLogin := flag.Bool("install-login", false, "register this collector to start at user login")
	background := flag.Bool("background", false, "start the collector in the menu bar or notification area")
	simulationRoot := flag.String("simulation-root", "", "load local demonstration B devices")
	flag.Parse()
	if *enroll != "" && *role != "" {
		log.Fatal("安装授权已经指定设备角色")
	}
	if *dataDir == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			log.Fatal(err)
		}
		*dataDir = filepath.Join(dir, "Codex Usage Tracker")
	}
	dir, err := filepath.Abs(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		log.Fatal(err)
	}
	defer lock.Close()
	held, err := filelock.TryLock(lock)
	if err != nil {
		log.Fatal(err)
	}
	if !held {
		log.Fatal("这个数据目录已被另一个 Codex 用量窗口打开")
	}
	if err := usagetracker.UseLocalCodex(dir); err != nil {
		log.Fatal(err)
	}
	tracker, err := usagetracker.New(dir, *role)
	if err != nil {
		log.Fatal(err)
	}
	stagedEnrollment := filepath.Join(dir, "install-enrollment.json")
	if *installLogin && *enroll == "" {
		*enroll = stagedEnrollment
	}
	if *enroll != "" {
		if err := tracker.InstallDeployment(*enroll); err != nil {
			log.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	bin := filepath.Dir(exe)
	assets := filepath.Join(bin, "installers")
	if filepath.Base(bin) == "MacOS" {
		assets = filepath.Join(bin, "..", "Resources", "installers")
	}
	tracker.SetInstallerAssets(assets)
	if *simulationRoot != "" {
		if err := tracker.ConfigureSimulation(*simulationRoot); err != nil {
			log.Fatal(err)
		}
	}
	collector := tracker.IsCollector()
	if *installLogin {
		if !collector {
			log.Fatal("只有 B 端可以注册登录后自动启动")
		}
		if err := registerLogin(exe, dir); err != nil {
			log.Fatal(err)
		}
		if *enroll == stagedEnrollment {
			if err := os.Remove(stagedEnrollment); err != nil {
				log.Print("无法清除临时安装授权：", err)
			}
		}
	}
	files, err := fs.Sub(embedded, "assets")
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	handler := tracker.Handler(files)
	if *web {
		tracker.Start(ctx)
		if err := serveWeb(ctx, handler, dir, *port); err != nil {
			log.Fatal(err)
		}
		return
	}
	app := application.New(application.Options{
		Name: name, Description: "查看 Codex 用量和订阅周期",
		Assets:     application.AssetOptions{Handler: handler},
		Mac:        application.MacOptions{ActivationPolicy: application.ActivationPolicyRegular},
		OnShutdown: cancel,
	})
	if !collector {
		if desktop := nativeNotices(); desktop.Show != nil {
			tracker.AttachDesktopNotices(desktop)
			app.Event.OnApplicationEvent(events.Mac.ApplicationDidBecomeActive, func(*application.ApplicationEvent) {
				tracker.RequestDesktopNotices()
			})
		}
	}
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: name, URL: "/", Width: 1210, Height: 820, MinWidth: 640, MinHeight: 520,
		Hidden: collector && *background,
	})
	if collector {
		window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
			event.Cancel()
			window.Hide()
		})
		show := func() { window.Show(); window.Focus() }
		menu := application.NewMenu()
		menu.Add("查看用量").OnClick(func(*application.Context) { show() })
		menu.Add("退出采集端").OnClick(func(*application.Context) { app.Quit() })
		tray := app.SystemTray.New()
		if icon, err := fs.ReadFile(files, "tray.png"); err == nil {
			tray.SetIcon(icon)
		}
		tray.SetTooltip(name)
		tray.SetMenu(menu)
		tray.OnClick(show)
	}
	tracker.Start(ctx)
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

var unlockPage = template.Must(template.New("unlock").Parse(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Codex 用量</title><body style="background:#15191e;color:#e7ece9;font:16px system-ui;margin:12vh auto;max-width:28rem;padding:1rem"><h1>Codex 用量</h1><form method="post" action="/unlock"><label for="key">本机访问码</label><br><input id="key" type="password" name="key" required autofocus><button>打开</button>{{if .}}<p role="alert">访问码无效</p>{{end}}</form></body></html>`))

func serveWeb(ctx context.Context, app http.Handler, dir string, port int) error {
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	defer listener.Close()
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	key := hex.EncodeToString(random[:])
	keyPath := filepath.Join(dir, "browser-key")
	if err := os.WriteFile(keyPath, []byte(key), 0o600); err != nil {
		return err
	}
	defer os.Remove(keyPath)
	equal := func(s string) bool {
		return subtle.ConstantTimeCompare([]byte(key), []byte(s)) == 1
	}
	guard := http.NewCrossOriginProtection()
	server := &http.Server{
		ReadHeaderTimeout: 8 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != listener.Addr().String() {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			if r.URL.Path == "/unlock" && r.Method == http.MethodPost {
				if guard.Check(r) != nil {
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, 4096)
				if equal(r.FormValue("key")) {
					http.SetCookie(w, &http.Cookie{Name: "codex_usage_session", Value: key,
						Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
					http.Redirect(w, r, "/", http.StatusSeeOther)
					return
				}
				w.WriteHeader(http.StatusUnauthorized)
				_ = unlockPage.Execute(w, true)
				return
			}
			if cookie, err := r.Cookie("codex_usage_session"); err == nil && equal(cookie.Value) {
				app.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			if r.URL.Path == "/" && r.Method == http.MethodGet {
				_ = unlockPage.Execute(w, false)
				return
			}
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}),
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Printf("http://%s/\n", listener.Addr())
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
