package client115

import (
	"context"
	"fmt"
	"strconv"
)

// FsListPageSize 115 目录接口默认分页大小。
const FsListPageSize = 1000

// FsPageFetcher 拉取一页目录条目，返回条目与接口声明的总数（未知时为 0）。
type FsPageFetcher func(ctx context.Context, offset, limit int) ([]FsFileEntry, int, error)

// ListAllFsFiles 拉取目录下的全部条目，返回条目与接口声明的总数（未知时为 0）。
func (c *Client) ListAllFsFiles(ctx context.Context, cid, cookie string, pageSize int) ([]FsFileEntry, int, error) {
	if cid == "" {
		cid = "0"
	}
	return FetchAllFsPages(ctx, pageSize, func(ctx context.Context, offset, limit int) ([]FsFileEntry, int, error) {
		resp, err := c.FsFiles(ctx, cid, limit, offset, cookie)
		if err != nil {
			return nil, 0, err
		}
		if resp == nil || !resp.State {
			return nil, 0, fmt.Errorf("目录 %s 未返回成功状态", cid)
		}
		return resp.Data, resp.Count, nil
	})
}

// FetchAllFsPages 分页拉全目录，供 task/清理/删除前复核共用。
//
// 分页终止规则（清理/删除链路必须使用本函数，不能只看短页）：
//   - Count > 0：offset 达到 Count 才结束；实得 < Count 返回错误；
//   - Count 缺失：继续拉取直到空页；出现重复页或超过 maxPages 返回错误。
//
// 115 单页可能小于请求的 limit；短页不能证明目录已经列完，否则后半段文件
// 会被清理逻辑误判为“云端已删除”。
func FetchAllFsPages(ctx context.Context, pageSize int, fetch FsPageFetcher) ([]FsFileEntry, int, error) {
	if pageSize <= 0 {
		pageSize = FsListPageSize
	}
	const maxPages = 10000
	var out []FsFileEntry
	expected := 0
	offset := 0
	lastFirst := ""
	lastLen := -1
	for page := 0; page < maxPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, expected, err
		}
		data, count, err := fetch(ctx, offset, pageSize)
		if err != nil {
			return nil, expected, fmt.Errorf("第 %d 页拉取失败: %w", page, err)
		}
		if count > expected {
			expected = count
		}
		if len(data) == 0 {
			if expected > 0 && offset < expected {
				return nil, expected, fmt.Errorf("分页不完整: 实得 %d / count=%d", offset, expected)
			}
			break
		}
		first := entryIdentity(data[0])
		if first != "" && first == lastFirst && len(data) == lastLen {
			return nil, expected, fmt.Errorf("分页重复（offset=%d），已中止", offset)
		}
		lastFirst, lastLen = first, len(data)
		out = append(out, data...)
		offset += len(data)
		if expected > 0 && offset >= expected {
			break
		}
		if page == maxPages-1 {
			return nil, expected, fmt.Errorf("分页超过上限 %d 页", maxPages)
		}
	}
	if expected > 0 && offset < expected {
		return nil, expected, fmt.Errorf("分页不完整: 实得 %d / count=%d", offset, expected)
	}
	return out, expected, nil
}

func entryIdentity(e FsFileEntry) string {
	if e.FID != nil {
		switch v := e.FID.(type) {
		case string:
			if v != "" && v != "0" {
				return v
			}
		case float64:
			return strconv.FormatInt(int64(v), 10)
		case int64:
			return strconv.FormatInt(v, 10)
		case int:
			return strconv.Itoa(v)
		}
	}
	return e.Name
}
