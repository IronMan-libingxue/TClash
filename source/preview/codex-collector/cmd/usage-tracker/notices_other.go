//go:build !darwin || !cgo

package main

import "github.com/yetone/magpie/internal/usagetracker"

func nativeNotices() usagetracker.DesktopNotices { return usagetracker.DesktopNotices{} }
