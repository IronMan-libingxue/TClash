package main

import (
	"bytes"
	"encoding/xml"
	"os"
	"path/filepath"

	"github.com/yetone/magpie/internal/steady"
)

const loginLabel = "local.codex-usage-tracker.collector"

func registerLogin(exe, dir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	agents := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agents, 0o700); err != nil {
		return err
	}
	var plist bytes.Buffer
	plist.WriteString(xml.Header)
	plist.WriteString("<plist version=\"1.0\"><dict><key>Label</key><string>" + loginLabel + "</string><key>ProgramArguments</key><array>")
	for _, arg := range []string{exe, "-data-dir", dir, "-background"} {
		plist.WriteString("<string>")
		if err := xml.EscapeText(&plist, []byte(arg)); err != nil {
			return err
		}
		plist.WriteString("</string>")
	}
	plist.WriteString("</array><key>RunAtLoad</key><true/></dict></plist>")
	return steady.WriteFile(filepath.Join(agents, loginLabel+".plist"), plist.Bytes(), 0o600)
}
