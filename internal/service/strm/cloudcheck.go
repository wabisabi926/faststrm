package strm

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/internal/model"
	"github.com/wabisabi926/faststrm/internal/service/client115"
)

const (
	cloudCheckPageSize = 1000
	cloudCheckMaxPages = 100
	// cloudCheckCacheTTL 目录列表缓存时长：只覆盖同一次删除循环，
	// 避免跨请求复用旧列表把“刚恢复的文件”误判为不存在。
	cloudCheckCacheTTL = 15 * time.Second
)

// DirLister 列出云端目录的依赖（*client115.Client 实现）。
type DirLister interface {
	FsDirGetID(ctx context.Context, p, cookie string) (int64, error)
	FsFiles(ctx context.Context, cid string, limit, offset int, cookie string) (*client115.FsFilesResp, error)
}

// AccountCookieReader 读取账号 Cookie（*store.AccountStore 实现）。
type AccountCookieReader interface {
	Get(name string) *model.AccountInfo
}

type dirCheckResult struct {
	names     map[string]struct{}
	pickcodes map[string]struct{}
	at        time.Time
	err       error
}

// CloudChecker 在删除前复核「云端文件仍然不存在」。
//
// 以托管记录里的 cloud_path 的父目录为单位列目录（同一目录只列一次），
// 只有目录列表成功且 pickcode 不在其中时才返回 stillAbsent=true。
// 目录不存在、网络/鉴权失败、cloud_path 缺失一律返回 error，调用方必须保留文件。
type CloudChecker struct {
	lister   DirLister
	accounts AccountCookieReader

	mu    sync.Mutex
	cache map[string]*dirCheckResult
}

func NewCloudChecker(lister DirLister, accounts AccountCookieReader) *CloudChecker {
	return &CloudChecker{
		lister:   lister,
		accounts: accounts,
		cache:    make(map[string]*dirCheckResult),
	}
}

// StillAbsent 返回 true 表示已确认云端目录中不存在该文件。
// 同时比较原路径文件名与 pickcode：文件名可能因非法字符被转义（如 '/' → '_'），
// 只比名字会漏判；pickcode 命中时一律保留（保守，先不删）。
func (c *CloudChecker) StillAbsent(ctx context.Context, account, cloudPath, pickcode string) (bool, error) {
	if c == nil || c.lister == nil || c.accounts == nil {
		return false, fmt.Errorf("cloud checker not configured")
	}
	cloudPath = strings.TrimSpace(strings.ReplaceAll(cloudPath, "\\", "/"))
	if cloudPath == "" {
		return false, fmt.Errorf("云端路径缺失，无法复核")
	}
	pickcode = strings.ToLower(strings.TrimSpace(pickcode))
	if pickcode == "" {
		return false, fmt.Errorf("pickcode 缺失，无法复核")
	}
	parent := path.Dir(cloudPath)
	if parent == "." || parent == "" {
		parent = "/"
	}
	key := account + "\x00" + parent
	base := path.Base(cloudPath)

	c.mu.Lock()
	if cached, ok := c.cache[key]; ok && time.Since(cached.at) <= cloudCheckCacheTTL {
		c.mu.Unlock()
		if cached.err != nil {
			return false, cached.err
		}
		if _, present := cached.names[base]; present {
			return false, nil
		}
		_, present := cached.pickcodes[pickcode]
		return !present, nil
	}
	c.mu.Unlock()

	result := c.loadDir(ctx, account, parent)
	c.mu.Lock()
	c.cache[key] = result
	c.mu.Unlock()
	if result.err != nil {
		return false, result.err
	}
	if _, present := result.names[base]; present {
		return false, nil
	}
	_, present := result.pickcodes[pickcode]
	return !present, nil
}

func (c *CloudChecker) loadDir(ctx context.Context, account, parent string) *dirCheckResult {
	acc := c.accounts.Get(account)
	if acc == nil || acc.Cookie == "" {
		return &dirCheckResult{err: fmt.Errorf("账号 %q 不存在或无 Cookie，无法复核云端", account)}
	}
	cid, err := c.lister.FsDirGetID(ctx, parent, acc.Cookie)
	if err != nil {
		return &dirCheckResult{err: fmt.Errorf("复核云端目录 %s 失败: %w", parent, err)}
	}
	set := make(map[string]struct{})
	names := make(map[string]struct{})
	offset := 0
	for page := 0; page < cloudCheckMaxPages; page++ {
		resp, lerr := c.lister.FsFiles(ctx, strconv.FormatInt(cid, 10), cloudCheckPageSize, offset, acc.Cookie)
		if lerr != nil {
			return &dirCheckResult{err: fmt.Errorf("复核云端目录 %s 列表失败: %w", parent, lerr)}
		}
		for _, item := range resp.Data {
			if item.PickCode != "" {
				set[strings.ToLower(item.PickCode)] = struct{}{}
			}
			if item.Name != "" {
				names[item.Name] = struct{}{}
			}
		}
		if len(resp.Data) < cloudCheckPageSize {
			return &dirCheckResult{pickcodes: set, names: names, at: time.Now()}
		}
		offset += len(resp.Data)
	}
	return &dirCheckResult{err: fmt.Errorf("复核云端目录 %s 超过 %d 页，未完成确认", parent, cloudCheckMaxPages)}
}
