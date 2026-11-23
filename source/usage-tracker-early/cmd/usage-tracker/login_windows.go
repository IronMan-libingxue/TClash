package main

import (
	"syscall"

	"golang.org/x/sys/windows/registry"
)

func registerLogin(exe, dir string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	command := syscall.EscapeArg(exe) + " -data-dir " + syscall.EscapeArg(dir) + " -background"
	return key.SetStringValue("Codex Usage Tracker", command)
}
