package monitor

// 字幕 / nfo / 海报等辅助文件的自动同步。
//
// 背景：任务执行时会把 DownloadExtensions（srt/ass/nfo/jpg…）下载到本地，
// 但生活事件只维护 STRM，网盘后续补字幕时不会同步，用户只能重跑任务。
// 这里在 create 事件里识别辅助文件，直接下载到对应本地目录。
//
// 约束：
//   - 只在扩展名命中 DownloadExtensions、且不属于 StrmExtensions 时触发
//   - 单文件上限 200MB，防止误配把大文件当元数据下载
//   - 已存在且大小一致则跳过；下载先写 .part 再原子改名
//   - 任何失败只记录日志，不影响其他事件处理

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wabisabi926/faststrm/internal/service/client115"
	"github.com/wabisabi926/faststrm/pkg/logger"
	"github.com/wabisabi926/faststrm/pkg/strmutil"
)

// metadataMaxBytes 辅助文件大小上限（200MB）。
const metadataMaxBytes = 200 << 20

// isDownloadExtFile 判断文件名是否属于「辅助文件」（下载到本地而非生成 STRM）。
// 同时命中 StrmExtensions 时按 STRM 处理（优先）。
func (m *Monitor) isDownloadExtFile(fileName string) bool {
	if m.appSettingsFn == nil {
		return false
	}
	s := m.appSettingsFn()
	if s == nil {
		return false
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == "" {
		return false
	}
	for _, e := range s.StrmExtensions {
		if strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(e), "."), strings.TrimPrefix(ext, ".")) {
			return false
		}
	}
	for _, e := range s.DownloadExtensions {
		if strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(e), "."), strings.TrimPrefix(ext, ".")) {
			return true
		}
	}
	return false
}

// syncMetadataFile 下载单个辅助文件到 localDir，返回本地路径。
func (m *Monitor) syncMetadataFile(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	localDir string,
	lifeClient *client115.LifeClient,
) (string, error) {
	if lifeClient == nil {
		return "", fmt.Errorf("lifeClient 为空")
	}
	if strings.TrimSpace(localDir) == "" {
		return "", fmt.Errorf("本地目录为空")
	}
	acct := m.accountReader.Get(account)
	if acct == nil || acct.Cookie == "" {
		return "", fmt.Errorf("账号 %s 缺少 Cookie", account)
	}

	name := strmutil.SanitizeFileName(event.FileName)
	if name == "" {
		return "", fmt.Errorf("文件名无效: %q", event.FileName)
	}
	localPath := filepath.Join(localDir, name)

	// 已存在且大小一致 → 跳过
	if fi, err := os.Stat(localPath); err == nil {
		if event.FileSize <= 0 || fi.Size() == event.FileSize {
			logger.S().Debugf("[Monitor] 辅助文件已存在，跳过: %s", localPath)
			return localPath, nil
		}
	}

	pickcode := m.resolveEventPickCodeWithRetry(ctx, account, lifeClient, event)
	if pickcode == "" {
		return "", fmt.Errorf("无法解析 pickcode: %s", event.FileName)
	}
	meta, err := lifeClient.FsClient().GetDownloadUrlWebFull(ctx, pickcode, acct.Cookie, client115.DefaultUA)
	if err != nil {
		return "", fmt.Errorf("获取直链失败: %w", err)
	}
	if meta == nil || meta.URL == "" {
		return "", fmt.Errorf("直链为空")
	}
	if meta.FileSize > metadataMaxBytes {
		return "", fmt.Errorf("文件过大（%d 字节），超过辅助文件上限", meta.FileSize)
	}

	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, meta.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", client115.DefaultUA)
	req.Header.Set("Referer", "https://115.com/")
	req.Header.Set("Cookie", acct.Cookie)

	client := &http.Client{Timeout: 3 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return "", fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}

	tmp := localPath + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	written, copyErr := io.Copy(f, io.LimitReader(resp.Body, metadataMaxBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("写入失败: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("关闭文件失败: %w", closeErr)
	}
	if written > metadataMaxBytes {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("文件超过辅助文件上限")
	}
	if err := os.Rename(tmp, localPath); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("重命名失败: %w", err)
	}
	logger.S().Infof("[Monitor] 辅助文件已同步: %s (%d 字节)", localPath, written)
	return localPath, nil
}
