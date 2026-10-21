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

// metadataWorkerLimit 同时进行的辅助文件下载数；不要让字幕下载占满事件轮询线程。
const metadataWorkerLimit = 2

// enqueueMetadataSync 异步同步一个辅助文件。失败会做少量重试；队列满时丢弃并记录，
// 避免拖慢生活事件主链路。
func (m *Monitor) enqueueMetadataSync(
	ctx context.Context,
	account string,
	event client115.LifeEventItem,
	localDir string,
	lifeClient *client115.LifeClient,
) {
	m.metadataOnce.Do(func() {
		m.metadataSem = make(chan struct{}, metadataWorkerLimit)
	})
	select {
	case m.metadataSem <- struct{}{}:
	default:
		logger.S().Warnf("[Monitor] 辅助文件同步队列已满，跳过: %s", event.FileName)
		return
	}
	go func() {
		defer func() { <-m.metadataSem }()
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				delay := time.Duration(attempt*attempt) * time.Second
				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
				}
			}
			// 使用独立 context：不能跟随本轮轮询结束而取消已开始的下载。
			if _, err := m.syncMetadataFile(context.Background(), account, event, localDir, lifeClient); err == nil {
				return
			} else {
				logger.S().Warnf("[Monitor] 辅助文件同步失败（第 %d 次）: %s: %v", attempt+1, event.FileName, err)
			}
		}
	}()
}

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
	for _, p := range []string{localPath, localPath + ".part"} {
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("拒绝写入符号链接: %s", p)
		}
	}
	if metadataFileUpToDate(localPath, event.FileSize) {
		logger.S().Debugf("[Monitor] 辅助文件已存在，跳过: %s", localPath)
		return localPath, nil
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

	written, err := downloadMetadataFile(ctx, meta.URL, acct.Cookie, localDir, localPath, meta.FileSize)
	if err != nil {
		return "", err
	}
	logger.S().Infof("[Monitor] 辅助文件已同步: %s (%d 字节)", localPath, written)
	return localPath, nil
}

// metadataFileUpToDate 判断本地文件是否已是最新（大小一致或云端未给大小）。
func metadataFileUpToDate(localPath string, wantSize int64) bool {
	fi, err := os.Stat(localPath)
	if err != nil {
		return false
	}
	return wantSize <= 0 || fi.Size() == wantSize
}

// downloadMetadataFile 下载到 localDir，先写 .part 再原子改名。
func downloadMetadataFile(ctx context.Context, url, cookie, localDir, localPath string, expectedSize int64) (int64, error) {
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return 0, fmt.Errorf("mkdir: %w", err)
	}
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", client115.DefaultUA)
	req.Header.Set("Referer", "https://115.com/")
	req.Header.Set("Cookie", cookie)

	client := &http.Client{Timeout: 3 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}

	tmp := localPath + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, fmt.Errorf("创建临时文件失败: %w", err)
	}
	written, copyErr := io.Copy(f, io.LimitReader(resp.Body, metadataMaxBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("写入失败: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("关闭文件失败: %w", closeErr)
	}
	if written > metadataMaxBytes {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("文件超过辅助文件上限")
	}
	if expectedSize > 0 && written != expectedSize {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("文件长度不一致: 期望 %d，实际 %d", expectedSize, written)
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("响应长度不一致: 期望 %d，实际 %d", resp.ContentLength, written)
	}
	// 覆盖已有文件前保留一份 .bak，避免用户手改的字幕/元数据被静默覆盖。
	if _, statErr := os.Stat(localPath); statErr == nil {
		bak := localPath + ".bak"
		_ = os.Remove(bak)
		if err := os.Rename(localPath, bak); err != nil {
			_ = os.Remove(tmp)
			return 0, fmt.Errorf("备份原文件失败: %w", err)
		}
		if err := os.Rename(tmp, localPath); err != nil {
			_ = os.Rename(bak, localPath)
			_ = os.Remove(tmp)
			return 0, fmt.Errorf("重命名失败: %w", err)
		}
		return written, nil
	}
	if err := os.Rename(tmp, localPath); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("重命名失败: %w", err)
	}
	return written, nil
}
