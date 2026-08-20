package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/server"
	"github.com/wabisabi926/faststrm/pkg/logger"
)

// 以下变量通过 ldflags 在构建时注入：
//   go build -ldflags="-X 'main.version=v0.9.4' -X 'main.BuildDate=2026-08-20'"
var (
	version   = "v0.9.4"
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

	// 3. 启动 HTTP server
	if err := server.Run(cfg); err != nil {
		logger.S().Fatalf("server.Run failed: %v", err)
	}
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
