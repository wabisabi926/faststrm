//go:build !windows

package main

// initTray 在非 Windows 平台为空实现，签名与 Windows 端保持一致。
//   - listenAddr / displayAddr 在非 Windows 未使用，保留参数仅为跨平台编译。
func initTray(listenAddr, displayAddr string, readyCh <-chan bool, quitCh chan struct{}) {
	// 非 Windows 平台不启用系统托盘
	_, _ = listenAddr, displayAddr
}
