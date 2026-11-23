//go:build !darwin && !windows

package main

import "errors"

func registerLogin(exe, dir string) error {
	return errors.New("此系统暂不支持采集端登录启动")
}
