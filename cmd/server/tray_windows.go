//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/getlantern/systray"
)

var (
	trayServerURL   string
	trayServerReady bool
	trayOnQuit      chan struct{}
)

// initTray 初始化系统托盘（Windows 专用）
func initTray(url string, readyCh <-chan bool, quitCh chan struct{}) {
	trayServerURL = url
	trayOnQuit = quitCh

	// 启动托盘
	go systray.Run(onTrayReady, onTrayExit)

	// 等待服务器就绪
	go func() {
		<-readyCh
		trayServerReady = true
		systray.SetTooltip(fmt.Sprintf("FastStrm %s - 运行中", version))
		systray.SetTitle("FastStrm")
	}()
}

func onTrayReady() {
	// 设置图标（从嵌入的 favicon.ico）
	systray.SetIcon(iconData)
	systray.SetTitle("FastStrm")
	systray.SetTooltip("FastStrm - 正在启动...")

	// 主菜单项
	mOpen := systray.AddMenuItem("打开 Web 界面", "在浏览器中打开 FastStrm 管理界面")
	mOpen.SetIcon(iconData)

	// 状态显示
	mStatus := systray.AddMenuItem("状态: 启动中...", "服务器启动状态")
	mStatus.Disable()

	systray.AddSeparator()

	// 服务器状态
	mServer := systray.AddMenuItem("服务器: 未就绪", "检查服务器连接状态")
	mServer.Disable()

	systray.AddSeparator()

	// 关于
	mAbout := systray.AddMenuItem("关于 FastStrm", "查看版本信息")

	systray.AddSeparator()

	// 退出按钮
	mQuit := systray.AddMenuItem("退出", "关闭 FastStrm 服务")
	mQuit.SetIcon(iconData)

	// 轮询更新状态
	go updateTrayStatus(mStatus, mServer)

	// 处理菜单事件
	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				openBrowser(trayServerURL)
			case <-mAbout.ClickedCh:
				// 显示关于信息
				fmt.Printf("关于 FastStrm: 版本=%s, 架构=%s/%s, 服务器=%s\n",
					version, runtime.GOOS, runtime.GOARCH, trayServerURL)
				systray.SetTooltip(fmt.Sprintf("FastStrm %s - 关于信息已记录", version))
			case <-mQuit.ClickedCh:
				if trayOnQuit != nil {
					select {
					case trayOnQuit <- struct{}{}:
					default:
					}
				}
				systray.Quit()
				return
			}
		}
	}()
}

func onTrayExit() {
	if trayOnQuit != nil {
		select {
		case trayOnQuit <- struct{}{}:
		default:
		}
	}
}

func updateTrayStatus(mStatus, mServer *systray.MenuItem) {
	for {
		time.Sleep(2 * time.Second)

		status := "未就绪"
		serverStatus := "未就绪"
		tooltip := "FastStrm - 启动中..."

		if trayServerReady {
			if isServerUp(trayServerURL) {
				status = "运行中"
				serverStatus = "已就绪"
				tooltip = fmt.Sprintf("FastStrm %s - 运行中", version)
			} else {
				status = "已连接"
				serverStatus = "响应延迟"
				tooltip = "FastStrm - 连接中..."
			}
		}

		mStatus.SetTitle(fmt.Sprintf("状态: %s", status))
		mServer.SetTitle(fmt.Sprintf("服务器: %s", serverStatus))
		systray.SetTooltip(tooltip)
	}
}

func openBrowser(url string) {
	if url == "" {
		return
	}
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
