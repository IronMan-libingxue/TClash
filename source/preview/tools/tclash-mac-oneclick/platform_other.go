//go:build !darwin

package main

import "time"

type unsupportedPlatform struct{}

func newPlatform() platform                                   { return unsupportedPlatform{} }
func notify(string)                                           {}
func (unsupportedPlatform) running(string) (bool, error)      { return false, nil }
func (unsupportedPlatform) askExit(string) bool               { return false }
func (unsupportedPlatform) disableLegacyStartup(string) error { return nil }
func (unsupportedPlatform) extract(string, string) error      { return nil }
func (unsupportedPlatform) verify(string) error               { return nil }
func (unsupportedPlatform) start(string) error                { return nil }
func (unsupportedPlatform) sleep(time.Duration)               {}
