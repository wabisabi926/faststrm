// Command migrate 把 LitePan / p115strmhelper / 旧 FastStrm 的 .strm
// 原地重写为当前 FastStrm 标准 URL。
//
// 默认 dry-run；加 -write 才写盘。不会重新扫描云端。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/config"
	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/internal/service/db"
	"github.com/wabisabi926/faststrm/internal/service/rate"
	"github.com/wabisabi926/faststrm/internal/service/store"
	"github.com/wabisabi926/faststrm/internal/service/strm"
	"github.com/wabisabi926/faststrm/internal/service/strmimport"
)

func main() {
	var (
		configDir  = flag.String("config", "", "配置目录（必填，或设置 DEFAULT_CONFIG_DIR / CONFIG_DIR）")
		root       = flag.String("root", "", "要迁移的 STRM 根目录")
		from       = flag.String("from", "auto", "来源：auto|faststrm|p115|litepan")
		target     = flag.String("account", "", "目标 FastStrm 账号（p115 必填）")
		accountMap = flag.String("account-map", "", "LitePan accountID=目标账号，逗号分隔")
		prefix     = flag.String("prefix", "", "目标 URL 前缀；空则用 settings 或自动内网 IP")
		cloudRoot  = flag.String("cloud-root", "", "本地根对应的云端路径，如 /媒体")
		write      = flag.Bool("write", false, "实际写入；默认 dry-run")
		backup     = flag.Bool("backup", true, "写入前备份旧内容")
		register   = flag.Bool("register", false, "写入后登记 managed_strms（需要 -cloud-root）")
	)
	flag.Parse()

	if strings.TrimSpace(*root) == "" {
		fatalf("-root 必填")
	}
	cfgDir := resolveConfigDir(*configDir)
	cfg, err := config.InitApp(cfgDir)
	if err != nil {
		fatalf("InitApp: %v", err)
	}
	settings := cfg.Settings
	if settings == nil {
		fatalf("settings 未加载")
	}
	rate.ApplySettings(settings)

	accountStore, err := store.NewAccountStore(cfg.Salt, cfg.Paths.ConfigDir)
	if err != nil {
		fatalf("加载账号: %v", err)
	}
	defer accountStore.Close()

	fromSource, err := parseFrom(*from)
	if err != nil {
		fatalf("%v", err)
	}
	mapRefs := parseAccountMap(*accountMap)
	if err := validateAccounts(accountStore, *target, mapRefs); err != nil {
		fatalf("%v", err)
	}

	targetPrefix := strings.TrimSpace(*prefix)
	if targetPrefix == "" {
		targetPrefix = strings.TrimSpace(settings.StrmPrefix)
	}
	if targetPrefix == "" {
		targetPrefix, _ = strm.ResolveStrmPrefix(nil, cfg.Server.Port)
	}
	if targetPrefix == "" {
		fatalf("无法确定 STRM 前缀，请使用 -prefix")
	}

	opts := strmimport.Options{
		Root:               *root,
		From:               fromSource,
		TargetAccount:      strings.TrimSpace(*target),
		AccountMap:         mapRefs,
		Prefix:             targetPrefix,
		Template:           settings.Strm.StrmUrlTemplate,
		EnablePathEncoding: settings.EnablePathEncoding,
		Write:              *write,
		CloudRoot:          strings.TrimSpace(*cloudRoot),
		Resolver:           newLifeResolver(accountStore),
	}
	if *write && *backup {
		opts.BackupDir = filepath.Join(*root, ".faststrm-migrate-backup", time.Now().Format("20060102-150405"))
	}
	if *register {
		if opts.CloudRoot == "" {
			fatalf("-register 需要同时提供 -cloud-root，否则无法写托管归属")
		}
		sqlDB, derr := db.OpenNew(cfg.Paths.DataDir)
		if derr != nil {
			fatalf("打开数据库: %v", derr)
		}
		defer sqlDB.Close()
		recorder := strm.NewManagedRecorder(sqlDB)
		opts.Register = func(ctx context.Context, in strmimport.RegisterInput) error {
			return recorder.Record(ctx, strm.ManagedInput{
				LocalPath: in.LocalPath,
				Account:   in.Account,
				CloudPath: in.CloudPath,
				Pickcode:  in.PickCode,
				FileID:    in.FileID,
				Content:   in.Content,
				Source:    "migrate",
			})
		}
	}

	report, err := strmimport.Run(context.Background(), opts)
	if err != nil {
		fatalf("迁移失败: %v", err)
	}
	printReport(report, *write)
	if report.Failed > 0 {
		os.Exit(1)
	}
}

func resolveConfigDir(flagValue string) string {
	if v := strings.TrimSpace(flagValue); v != "" {
		return v
	}
	for _, key := range []string{"DEFAULT_CONFIG_DIR", "CONFIG_DIR"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	exe, err := os.Executable()
	if err == nil {
		return filepath.Join(filepath.Dir(exe), ".config")
	}
	return ""
}

func parseFrom(raw string) (strmimport.Source, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return "", nil
	case "faststrm":
		return strmimport.SourceFastStrm, nil
	case "p115":
		return strmimport.SourceP115, nil
	case "litepan":
		return strmimport.SourceLitePan, nil
	default:
		return "", fmt.Errorf("未知 -from: %s（支持 auto|faststrm|p115|litepan）", raw)
	}
}

func parseAccountMap(raw string) map[string]string {
	out := make(map[string]string)
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' }) {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

func validateAccounts(accounts *store.AccountStore, target string, mapping map[string]string) error {
	names := make([]string, 0, len(mapping)+1)
	if target != "" {
		names = append(names, target)
	}
	for _, v := range mapping {
		names = append(names, v)
	}
	for _, name := range names {
		if accounts.Get(name) == nil {
			return fmt.Errorf("账号不存在: %s", name)
		}
	}
	return nil
}

type lifeResolver struct {
	accounts *store.AccountStore
	mu       sync.Mutex
	clients  map[string]*client115.LifeClient
}

func newLifeResolver(accounts *store.AccountStore) *lifeResolver {
	return &lifeResolver{accounts: accounts, clients: make(map[string]*client115.LifeClient)}
}

func (r *lifeResolver) ResolvePickCode(ctx context.Context, account, fileID string) (string, error) {
	acc := r.accounts.Get(account)
	if acc == nil {
		return "", fmt.Errorf("账号不存在: %s", account)
	}
	if acc.Cookie == "" {
		return "", fmt.Errorf("账号 %s 没有 Cookie", account)
	}
	r.mu.Lock()
	lc := r.clients[account]
	if lc == nil {
		lc = client115.NewLifeClientWithRateLimit(acc.Cookie)
		r.clients[account] = lc
	}
	r.mu.Unlock()

	if err := rate.GetRegistry().Acquire(ctx, rate.AccountKeyFromCookie(acc.Cookie), rate.TypeAPI115); err != nil {
		return "", err
	}
	return lc.GetPickCodeByFileID(ctx, fileID)
}

func printReport(report strmimport.Report, write bool) {
	mode := "dry-run"
	if write {
		mode = "write"
	}
	fmt.Printf("\n迁移完成（%s）：共 %d，变更 %d，写入 %d，未变 %d，跳过 %d，失败 %d\n",
		mode, report.Total, report.Changed, report.Written, report.Unchanged, report.Skipped, report.Failed)
	for _, res := range report.Results {
		if res.Status == "unchanged" {
			continue
		}
		line := fmt.Sprintf("  [%s] %s", res.Status, res.Path)
		if res.Source != "" {
			line += fmt.Sprintf(" (%s)", res.Source)
		}
		if res.Reason != "" {
			line += " - " + res.Reason
		}
		fmt.Println(line)
	}
	if !write {
		fmt.Println("\n这是 dry-run，未写盘；确认无误后加 -write。")
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
