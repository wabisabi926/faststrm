//go:build !windows

package main

// initTray 在非 Windows 平台为空实现
func initTray(url string, readyCh <-chan bool, quitCh chan struct{}) {
	// 非 Windows 平台不启用系统托盘
}
