//go:build !windows

package main

import "time"

type unsupportedPlatform struct{}

func newPlatform() platform                                   { return unsupportedPlatform{} }
func notify(string)                                           {}
func acquireInstallerLock() (func(), bool, error)             { return func() {}, true, nil }
func (unsupportedPlatform) running(string) (bool, error)      { return false, nil }
func (unsupportedPlatform) askExit(string) bool               { return false }
func (unsupportedPlatform) runInstaller(string, bool) error   { return nil }
func (unsupportedPlatform) registeredDirs() ([]string, error) { return nil, nil }
func (unsupportedPlatform) prepareHandlers(string) error      { return nil }
func (unsupportedPlatform) collectorOK(string) error          { return nil }
func (unsupportedPlatform) disableLegacyStartup(string) error { return nil }
func (unsupportedPlatform) start(string) error                { return nil }
func (unsupportedPlatform) sleep(time.Duration)               {}
