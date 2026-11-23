package usagetracker

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/sessions"
)

func TestDeploymentEnrollmentAndCollectorSchedule(t *testing.T) {
	ctx := t.Context()
	cloud := &memoryRepo{files: map[string][]byte{}}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	quota := Quota{Account: "fixture", Start: now.Add(-72 * time.Hour), End: now.Add(96 * time.Hour), At: now, Used: 36}
	var callCount int
	newApp := func(role string) *App {
		app, err := New(t.TempDir(), role)
		check(err)
		app.account = func() string { return quota.Account }
		app.openRepo = func(context.Context, string, string) (transport, error) { return cloud, nil }
		app.collect = func(_ context.Context, id, name string, _ []Quota, _ []AccountCorrection) ([]Day, error) {
			callCount++
			return []Day{{Device: id, Name: name, Date: now.Format(time.DateOnly), At: now,
				Quotas: []Quota{quota}, Rows: []Request{{ID: "one", At: now, Project: "p", ProjectName: "Project",
					Session: "session", Model: "model", Account: quota.Account, Tokens: Tokens{Input: 6, Output: 5, Cache: 12}, Priced: true, USD: 3, confirmed: true}}}}, nil
		}
		return app
	}
	a := newApp("a")
	check(a.configure(ctx, setup{Repo: "fixture/private", Token: "fixture-a"}))
	check(a.refreshLocal(ctx, a.config()))
	assets := t.TempDir()
	a.SetInstallerAssets(assets)
	for _, name := range []string{macTemplate, windowsAMD64, windowsARM64, magpieLicense} {
		check(os.WriteFile(filepath.Join(assets, name), []byte(name), 0600))
	}
	check(a.exportDeployments(ctx, []string{"fixture-2", "fixture-3", "fixture-4", "fixture-win1", "fixture-win2"}))
	if v := a.view(nil); len(v.Cards) != 6 || len(v.Paired) != 0 || v.SyncMinutes != 5 {
		t.Fatalf("wrong initial deployment overview: %d cards, %d paired, %d minutes", len(v.Cards), len(v.Paired), v.SyncMinutes)
	}
	install := func(name string) *App {
		z, err := zip.OpenReader(filepath.Join(a.config().DeploymentDir, name+".zip"))
		check(err)
		defer z.Close()
		f, err := z.Open("enrollment.json")
		check(err)
		defer f.Close()
		body, err := io.ReadAll(f)
		check(err)
		b := newApp("")
		path := filepath.Join(t.TempDir(), "enrollment.json")
		check(os.WriteFile(path, body, 0600))
		check(b.InstallDeployment(path))
		return b
	}
	var bs []*App
	for _, d := range deploymentDevices {
		b := install(d.Name)
		check(b.runCollector(ctx, b.config(), true, now))
		bs = append(bs, b)
	}
	check(a.syncNow(ctx, a.config()))
	first := a.view(nil)
	if len(first.Paired) != 5 || first.Count != 6 || first.Total.total(true) != 6*23 || first.Quota.Used != 36 {
		t.Fatal("A did not automatically register and aggregate five independent B devices and its own usage")
	}
	for _, card := range first.Cards {
		if card.Pending || card.Total.total(true) != 23 || len(card.Models) != 1 || card.Models[0].total(true) != 23 {
			t.Fatalf("card for %s did not reconcile", card.Name)
		}
	}
	b := bs[0]
	before := callCount
	check(b.runCollector(ctx, b.config(), false, now.Add(4*time.Minute)))
	if callCount != before {
		t.Fatal("B collected before its default interval")
	}
	check(b.runCollector(ctx, b.config(), false, now.Add(5*time.Minute)))
	if callCount != before+1 {
		t.Fatal("B missed the five-minute sync")
	}
	check(a.setFrequency(2))
	check(a.syncNow(ctx, a.config()))
	check(b.runCollector(ctx, b.config(), false, now.Add(6*time.Minute)))
	if callCount != before+1 || b.config().Policy.interval() != 2*time.Minute {
		t.Fatal("B did not receive the new interval ahead of its next upload")
	}
	check(b.runCollector(ctx, b.config(), false, now.Add(7*time.Minute)))
	if callCount != before+2 {
		t.Fatal("B missed its adjusted interval")
	}
	if err := b.action(ctx, "sync", json.NewDecoder(strings.NewReader("{}"))); err == nil {
		t.Fatal("B exposed manual synchronization")
	}

	replayed := install(deploymentDevices[0].Name)
	check(replayed.runCollector(ctx, replayed.config(), true, now))
	check(a.syncNow(ctx, a.config()))
	if v := a.view(url.Values{"cache": {"0"}}); len(v.Paired) != 5 || v.Total.total(false) != 6*11 || v.Count != 6 {
		t.Fatal("an already consumed invitation enrolled a second identity")
	}
}

func TestLocalSpeedOnlyUsesSelectedModel(t *testing.T) {
	tail := []byte("{\"type\":\"turn_context\",\"payload\":{\"model\":\"old\"}}\n" +
		"{\"type\":\"event_msg\",\"payload\":{\"type\":\"thread_settings_applied\",\"settings\":{\"model\":\"new\"}}}\n" +
		"{\"type\":\"turn_context\"")
	model := modelFromTail(tail)
	if model != "new" {
		t.Fatalf("selected %q", model)
	}
	now := time.Now()
	calls := []sessions.Call{{Time: now, Model: "old", Millis: 100, Tokens: sessions.Tokens{Output: 40}},
		{Time: now.Add(-time.Second), Model: "new", Millis: 5000, TTFT: 1000, Tokens: sessions.Tokens{Output: 120}}}
	speed := speedFromCalls(model, calls, now)
	if speed.Rate == nil || *speed.Rate != 30 || speed.Whole || speed.Model != "new" {
		t.Fatalf("bad speed result: %+v", speed)
	}
	calls[1].TTFT = 0
	speed = speedFromCalls(model, calls, now)
	if speed.Rate == nil || *speed.Rate != 24 || !speed.Whole {
		t.Fatal("whole-request estimate is mislabeled")
	}
	if speed := speedFromCalls("missing", calls, now); speed.Rate != nil || speed.Model != "missing" {
		t.Fatal("borrowed the speed of another model")
	}
}
