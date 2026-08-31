package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/server"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// 以下变量通过 ldflags 在构建时注入：
//
//	go build -ldflags="-X 'main.version=v1.1.8' -X 'main.BuildDate=2026-08-31'"
var (
	version   = "v1.1.8"
	BuildDate = "unknown"
)

func main() {
	fmt.Printf("faststrm %s (built %s)\n", version, BuildDate)

	// 1. 命令行参数：--config/-c 指定"配置目录"（defaultRoot，和 InitApp 参数语义一致）
	//    若未传，则走现有环境变量 + 默认目录探测：
	//      DEFAULT_CONFIG_DIR → CONFIG_DIR → 工作目录/.config → /app/.config
	//    在该目录下，config.InitApp 会创建/读取 config.json / account.json / tasks.json / settings.json
	var configDir string
	flag.StringVar(&configDir, "config", "", "path to config DIR (default: $DEFAULT_CONFIG_DIR or $CONFIG_DIR)")
	flag.StringVar(&configDir, "c", "", "shorthand for --config")
	flag.Parse()

	// 2. 初始化应用（建目录/拷贝默认配置/密码哈希/token）
	var cfg *config.AppConfig
	var err error
	if configDir != "" {
		cfg, err = config.InitApp(configDir)
	} else {
		defaultRoot := getDefaultRoot()
		cfg, err = config.InitApp(defaultRoot)
	}
	if err != nil {
		// 此时 logger 可能尚未初始化，用标准输出兜底
		println("InitApp failed:", err.Error())
		os.Exit(1)
	}

	// 3. 计算服务器 URL 并初始化系统托盘
	//    注意：cfg.Server.Host 可能是 0.0.0.0（监听所有网卡），但给用户展示/跳转时必须用 localhost。
	//    保持监听 Host 原样（仍接受局域网/外部访问），只在"显示层"转换 Host。
	listenAddr := fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)
	displayAddr := displayURL(cfg.Server.Host, cfg.Server.Port)
	readyCh := make(chan bool, 1)
	quitCh := make(chan struct{}, 1)

	initTray(listenAddr, displayAddr, readyCh, quitCh)

	// 4. 启动 HTTP server（在 goroutine 中，以便托盘可以接收就绪信号）
	//    cfg.Server.Host（可能 0.0.0.0）始终传给 server.Run，保证局域网绑定；
	//    启动日志在 server.Run 内部按 displayAddr 打印成 http://localhost:PORT 给用户看。
	go func() {
		if err := server.Run(cfg); err != nil {
			logger.S().Fatalf("server.Run failed: %v", err)
			os.Exit(1)
		}
	}()

	// 5. 等待服务器就绪后通知托盘
	// server.Run 内部在启动时会打印日志，但我们无法直接知道它何时就绪。
	// 简单起见，等待 1 秒后假设服务器已就绪（或让托盘自行检测）
	go func() {
		// 给服务器启动一些时间 — 用 displayAddr 做健康检查（避免发 GET 到 0.0.0.0 在部分网络配置下失败）
		waitForServer(displayAddr, readyCh)
	}()

	// 6. 等待关闭信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigCh:
		logger.S().Infof("收到关闭信号，正在退出...")
	case <-quitCh:
		logger.S().Infof("托盘请求退出，正在关闭...")
	}

	logger.S().Infof("FastStrm 已关闭")
}

// waitForServer 等待服务器就绪（尝试 HTTP 请求，最多等待 30 秒）
func waitForServer(url string, readyCh chan<- bool) {
	maxAttempts := 30
	for i := 0; i < maxAttempts; i++ {
		time.Sleep(time.Second)
		if isServerUp(url) {
			readyCh <- true
			return
		}
	}
	// 超时后仍然通知（托盘会自行检测）
	readyCh <- true
}

// isServerUp 检查服务器是否响应
func isServerUp(url string) bool {
	if url == "" {
		return false
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusFound
}

func getDefaultRoot() string {
	if v := os.Getenv("DEFAULT_CONFIG_DIR"); v != "" {
		return v
	}
	// fNOS / Docker 常见拼写：CONFIG_DIR（和 docker-entrypoint.sh / cmd/main 约定对齐）
	if v := os.Getenv("CONFIG_DIR"); v != "" {
		return v
	}
	// 非 Linux（Windows/Darwin 开发机）默认使用可执行文件旁的 .config 目录，
	// 避免 Docker 的 /app/.config 在本地无法写入。
	if runtime.GOOS != "linux" {
		if exe, err := os.Executable(); err == nil {
			base := filepath.Dir(exe)
			candidate := filepath.Join(base, ".config")
			// 候选存在，或上级的 .config 存在（例如 go run 时在 repo 根目录）
			if st, err := os.Stat(candidate); err == nil && st.IsDir() {
				return candidate
			}
			// 否则尝试当前工作目录/repo 根：exe 目录的父目录/.config（当 exe 在 repo 根时即本身）
			if cwd, err := os.Getwd(); err == nil {
				cwdCfg := filepath.Join(cwd, ".config")
				if st, err := os.Stat(cwdCfg); err == nil && st.IsDir() {
					return cwdCfg
				}
				// 若均不存在，则使用工作目录下的 .config（InitApp 会自行创建）
				return cwdCfg
			}
			return candidate
		}
	}
	// Docker 镜像内默认路径
	return "/app/.config"
}

// displayURL 把监听 host（0.0.0.0/::/空）转换成用户浏览器实际可访问的地址。
// 注意：本函数仅用于"展示/跳转/健康检查"，不改变真实 HTTP 监听的 host（仍用 cfg.Server.Host 绑定）。
func displayURL(host string, port int) string {
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "localhost"
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}
