// Package client115 115 网盘 API 客户端
// life.go 实现 115 生活事件（Life）API 客户端
// 对齐 p115client life_behavior_detail / life_calendar_setoption
package client115

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// ==================== 事件类型常量 ====================

// BehaviorTypeToName 行为类型(int) -> 名称(string) 映射
// 对齐 p115client BEHAVIOR_TYPE_TO_NAME
var BehaviorTypeToName = map[int]string{
	1:  "upload_image_file",
	2:  "upload_file",
	3:  "star_image",
	4:  "star_file",
	5:  "move_image_file",
	6:  "move_file",
	7:  "browse_image",
	8:  "browse_video",
	9:  "browse_audio",
	10: "browse_document",
	14: "receive_files",
	17: "new_folder",
	18: "copy_folder",
	19: "folder_label",
	20: "folder_rename",
	22: "delete_file",
	23: "copy_file",
	24: "file_rename",
}

// BehaviorNameToType 行为类型名称(string) -> 编号(int) 映射
var BehaviorNameToType = map[string]int{
	"upload_image_file": 1,
	"upload_file":       2,
	"star_image":        3,
	"star_file":         4,
	"move_image_file":   5,
	"move_file":         6,
	"browse_image":      7,
	"browse_video":      8,
	"browse_audio":      9,
	"browse_document":   10,
	"receive_files":     14,
	"new_folder":        17,
	"copy_folder":       18,
	"folder_label":      19,
	"folder_rename":     20,
	"delete_file":       22,
	"copy_file":         23,
	"file_rename":       24,
}

// CreateEventTypes 创建类事件（上传/新建/复制/接收）
var CreateEventTypes = map[int]bool{1: true, 2: true, 14: true, 17: true, 18: true, 23: true}

// MoveEventTypes 移动事件类型
var MoveEventTypes = map[int]bool{5: true, 6: true}

// RenameEventTypes 重命名事件类型
var RenameEventTypes = map[int]bool{20: true, 24: true}

// DeleteEventTypes 删除事件类型
var DeleteEventTypes = map[int]bool{22: true}

// IgnoreBehaviorTypes 忽略的行为类型（标星/浏览/标签等无操作事件）
// 对齐 p115client IGNORE_BEHAVIOR_TYPES
var IgnoreBehaviorTypes = map[int]bool{
	3:  true, // star_image 标星图片
	4:  true, // star_file 标星文件/目录
	7:  true, // browse_image 浏览图片
	8:  true, // browse_video 浏览视频
	9:  true, // browse_audio 浏览音频
	10: true, // browse_document 浏览文档
	19: true, // folder_label 标签文件夹
}

// ==================== LifeEventItem ====================

// LifeEventItem 115 生活事件条目
// 对齐 p115client life_behavior_detail 响应字段
type LifeEventItem struct {
	ID           string `json:"id"`
	Type         int    `json:"type"`
	BehaviorType string `json:"behavior_type,omitempty"`
	FileCategory int    `json:"file_category"`
	UpdateTime   int64  `json:"update_time"`
	FileID       string `json:"file_id"`
	FileName     string `json:"file_name"`
	ParentID     string `json:"parent_id"`
	PickCode     string `json:"pick_code"`
	FileSize     int64  `json:"file_size,omitempty"`
	FilePath     string `json:"-"` // 由路径解析逻辑填充，不从 API 响应直接解析
}

// lifeEventRaw 用于灵活解析 115 life_behavior_detail API 响应
type lifeEventRaw struct {
	ID           any    `json:"id"`
	Type         any    `json:"type"`
	BehaviorType string `json:"behavior_type"`
	FileCategory any    `json:"file_category"`
	UpdateTime   any    `json:"update_time"`
	FileID       any    `json:"file_id"`
	FileName     string `json:"file_name"`
	ParentID     any    `json:"parent_id"`
	PickCode     string `json:"pick_code"`
	FileSize     any    `json:"file_size"`
	FilePath     string `json:"file_path,omitempty"`
}

// ==================== LifeClient ====================

// LifeClient 115 生活事件 API 客户端
type LifeClient struct {
	cookie     string
	httpClient *http.Client
	fsClient   *Client // 复用 Client 通用请求能力（列目录 / 取路径等）

	// 路径解析缓存
	pathCache    sync.Map // key: parentID, value: cachedPathEntry —— 存 parentDirPath（含自身名）→ 兼容旧缓存结构
	pathCacheTTL time.Duration

	// API 域名轮换
	useAlternateHost bool
}

// NewLifeClient 创建生活事件客户端
func NewLifeClient(cookie string) *LifeClient {
	return &LifeClient{
		cookie: cookie,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		fsClient:     NewClient(cookie),
		pathCacheTTL: 5 * time.Minute,
	}
}

// FsClient 暴露底层 fs client（供 monitor 层调用 FsFiles 做网盘存在性校验等）
func (c *LifeClient) FsClient() *Client {
	return c.fsClient
}

// Cookie 返回 cookie 字符串（供外部调用 FsFiles 等 API 使用）
func (c *LifeClient) Cookie() string {
	return c.cookie
}

// cachedPathEntry 路径缓存条目
type cachedPathEntry struct {
	path      string
	expiresAt time.Time
}

// anyIDMatches 比较 JSON 里任意类型的 id（int/float64/string）与目标字符串。
func anyIDMatches(a any, target string) bool {
	if target == "" {
		return false
	}
	switch v := a.(type) {
	case string:
		return v == target
	case int:
		return strconv.Itoa(v) == target
	case int64:
		return strconv.FormatInt(v, 10) == target
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64) == target
	}
	return false
}

// stripRootPrefix 去掉"根目录/"前缀（带/不带前导斜杠都处理），并清理多余斜杠空格
func stripRootPrefix(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "/")
	for strings.HasPrefix(p, "根目录/") {
		p = strings.TrimPrefix(p, "根目录/")
	}
	if p == "根目录" {
		p = ""
	}
	return p
}

// 生活事件 API 端点（对齐参考项目 _get_life_event_app 策略）
// - proapi/ios: 字段完整（含 pick_code），是 p115client life_behavior_detail_app 默认端点
// - webapi: 字段可能缺失（pick_code 经常为空），但稳定，作为风控回退
const (
	lifeApiPrimaryHost  = "https://proapi.115.com"
	lifeApiPrimaryPath  = "/ios/behavior/detail"
	lifeApiFallbackHost = "https://webapi.115.com"
	lifeApiFallbackPath = "/behavior/detail"
)

// getApiHost 返回当前使用的 API 域名
// 默认 proapi.115.com（字段完整），失败回退 webapi.115.com（稳定但字段缺失）
func (c *LifeClient) getApiHost() string {
	if c.useAlternateHost {
		return lifeApiFallbackHost
	}
	return lifeApiPrimaryHost
}

// getBehaviorDetailPath 返回 behavior/detail 的 API 路径
// proapi 需要 /{app} 前缀，webapi 不需要
func (c *LifeClient) getBehaviorDetailPath() string {
	if c.useAlternateHost {
		return lifeApiFallbackPath
	}
	return lifeApiPrimaryPath
}

// switchApiHost 切换 API 域名（用于风控规避）
func (c *LifeClient) switchApiHost() {
	c.useAlternateHost = !c.useAlternateHost
	logger.S().Warnf("[LifeClient] 切换 API 域名: useAlternate=%v (host=%s)", c.useAlternateHost, c.getApiHost())
}

// LifeShow 开启生活事件功能
// POST https://life.115.com/api/1.0/web/1.0/calendar/setoption
// 对齐 p115client life_calendar_setoption
func (c *LifeClient) LifeShow(ctx context.Context) error {
	if c.cookie == "" {
		return fmt.Errorf("cookie is empty")
	}

	endpoint := "https://life.115.com/api/1.0/web/1.0/calendar/setoption"
	form := url.Values{}
	form.Set("locus", "1")
	form.Set("open_life", "1")

	body, err := c.doRequest(ctx, http.MethodPost, endpoint, form.Encode())
	if err != nil {
		return fmt.Errorf("lifeShow request: %w", err)
	}

	var resp struct {
		State  bool   `json:"state"`
		ErrNo  int    `json:"errNo"`
		ErrNo2 int    `json:"errno"`
		Error  string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("parse lifeShow response: %w (body=%s)", err, truncateBody(body, 256))
	}

	logger.S().Infof("[LifeClient] lifeShow resp: state=%v errNo=%d err=%s",
		resp.State, resp.ErrNo, resp.Error)

	if resp.State {
		return nil
	}

	errNo := resp.ErrNo
	if errNo == 0 {
		errNo = resp.ErrNo2
	}
	if errNo == 99 || errNo == 990001 {
		return fmt.Errorf("cookie 已过期 (errno=%d): %s", errNo, resp.Error)
	}
	if errNo != 0 {
		return fmt.Errorf("lifeShow 失败 errno=%d: %s", errNo, resp.Error)
	}
	return fmt.Errorf("lifeShow 失败: state=false, body=%s", truncateBody(body, 256))
}

// PullEvents 拉取生活事件列表
// GET https://webapi.115.com/behavior/detail?limit=1000&offset=0
// 对齐 p115client life_behavior_detail
// PullEvents 拉取生活事件（游标模式：from_time + from_id）
// 对齐参考项目 iter_life_behavior_once：使用 offset 分页拉取，但用 from_time/from_id 过滤旧事件
// 首次拉取（fromTime=0 && fromID=0）从当前时间开始，只拉新事件
func (c *LifeClient) PullEvents(ctx context.Context, account string, fromTime, fromID int64) ([]LifeEventItem, error) { //nolint:cyclop // complexity: 29
	if c.cookie == "" {
		return nil, fmt.Errorf("cookie is empty")
	}

	// 每次拉取都默认尝试 proapi/ios（字段完整），仅本次失败时回退到 webapi
	// 避免上一次切换后状态泄漏导致永久走 webapi（参考项目用 web_fallback_until 时间窗口控制）
	c.useAlternateHost = false
	apiHost := c.getApiHost()

	// 对齐参考项目：首批拉 1000 条，后续也 1000 条
	// 使用 offset 分页，但用 from_time/from_id 过滤
	var allFiltered []LifeEventItem
	offset := 0
	const limit = 1000
	maxPages := 10 // 安全限制，避免无限拉取

	for page := 0; page < maxPages; page++ {
		endpoint := fmt.Sprintf(
			"%s%s?limit=%d&offset=%d",
			apiHost, c.getBehaviorDetailPath(), limit, offset,
		)

		body, err := c.doRequest(ctx, http.MethodGet, endpoint, "")
		if err != nil {
			if !c.useAlternateHost && page == 0 {
				c.switchApiHost()
				apiHost = c.getApiHost()
				continue
			}
			if len(allFiltered) > 0 {
				break // 已有数据，返回已拉取的
			}
			return nil, fmt.Errorf("pullEvents request: %w", err)
		}

		var resp struct {
			Code  int    `json:"code"`
			Error string `json:"error,omitempty"`
			Data  struct {
				List     []lifeEventRaw `json:"list"`
				Count    any            `json:"count"`
				NextPage any            `json:"next_page"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			if len(allFiltered) > 0 {
				break
			}
			return nil, fmt.Errorf("parse pullEvents response: %w (body=%s)", err, truncateBody(body, 512))
		}

		if resp.Code != 0 {
			if resp.Code == 99 || resp.Code == 990001 {
				return nil, fmt.Errorf("cookie 已过期 (code=%d): %s", resp.Code, resp.Error)
			}
			if len(allFiltered) > 0 {
				break
			}
			return nil, fmt.Errorf("pullEvents 失败 code=%d: %s", resp.Code, resp.Error)
		}

		if len(resp.Data.List) == 0 {
			break
		}

		// 调试：打印首批事件的原始数据
		if page == 0 && len(resp.Data.List) > 0 {
			first := resp.Data.List[0]
			logger.S().Infof("[LifeClient] DEBUG 首批事件 sample: list_len=%d, first.id=%v first.type=%v first.update_time=%v first.file_name=%s",
				len(resp.Data.List), first.ID, first.Type, first.UpdateTime, first.FileName)
		}

		// 过滤：只保留 id > fromID 且 update_time >= fromTime 的事件
		hitOld := false
		for _, raw := range resp.Data.List {
			item := raw.toLifeEventItem()
			eid := lifeToInt64(item.ID)
			etime := item.UpdateTime

			// 调试：打印前3条事件的游标判定
			if page == 0 {
				logger.S().Debugf("[LifeClient] DEBUG event: id=%s eid=%d update_time=%d fromID=%d fromTime=%d -> skip=%v",
					item.ID, eid, etime, fromID, fromTime,
					(fromID > 0 && eid <= fromID) || (fromTime > 0 && etime > 0 && etime < fromTime))
			}

			// 游标过滤：跳过已处理的事件
			if fromID > 0 && eid <= fromID {
				hitOld = true
				continue
			}
			if fromTime > 0 && etime > 0 && etime < fromTime {
				hitOld = true
				continue
			}

			allFiltered = append(allFiltered, item)
		}

		// 如果遇到旧事件，说明已经翻到历史数据，不需要继续翻页
		if hitOld {
			break
		}

		// 没有下一页，停止
		if !parseNextPage(resp.Data.NextPage) {
			break
		}

		offset += len(resp.Data.List)
	}

	_ = account

	logger.S().Infof("[LifeClient] pulled events: filtered=%d, from_id=%d, from_time=%d",
		len(allFiltered), fromID, fromTime)
	return allFiltered, nil
}

// parseNextPage 灵活解析 next_page 字段（API 可能返回 bool/int/string）
func parseNextPage(v any) bool {
	switch val := v.(type) {
	case bool:
		return val
	case int:
		return val != 0
	case float64:
		return val != 0
	case string:
		return val == "1" || val == "true" || val == "True"
	default:
		return false
	}
}

// doRequest 发送 HTTP 请求，返回响应体
func (c *LifeClient) doRequest(ctx context.Context, method, urlStr, body string) ([]byte, error) {
	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", DefaultUA)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", "https://115.com/")
	req.Header.Set("Origin", "https://115.com")
	req.Header.Set("Cookie", c.cookie)

	if reqBody != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := string(respBody)
		if len(snippet) > 256 {
			snippet = snippet[:256]
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}

	return respBody, nil
}

// ==================== 路径解析 ====================

// fsMediaAncestorNode 文件祖先节点
type fsMediaAncestorNode struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	ParentID int    `json:"parent_id"`
}

// FsFilesMediaAncestors 通过 fs_files_media API 获取指定目录的祖先路径链
// GET https://webapi.115.com/files/medialist?cid={cid}&limit=1&type=6&nf=1
// 对齐 p115client fs_files_media
func (c *LifeClient) FsFilesMediaAncestors(ctx context.Context, cid string) ([]fsMediaAncestorNode, error) {
	if cid == "" || cid == "0" {
		return nil, nil // 根目录无祖先
	}

	// /files/medialist 仅在 webapi 上调用，不跟随 life API 的 proapi/ios 切换
	// （proapi 上需要 /{app} 前缀，路径不同，且 fs_files_media 是 web 端口 API）
	endpoint := fmt.Sprintf("https://webapi.115.com/files/medialist?cid=%s&limit=1&type=6&nf=1", url.QueryEscape(cid))

	body, err := c.doRequest(ctx, http.MethodGet, endpoint, "")
	if err != nil {
		return nil, fmt.Errorf("fs_files_media request: %w", err)
	}

	var resp struct {
		State     bool                  `json:"state"`
		Ancestors []fsMediaAncestorNode `json:"ancestors"`
		ErrMsg    string                `json:"errmsg,omitempty"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse fs_files_media response: %w (body=%s)", err, truncateBody(body, 256))
	}
	if !resp.State {
		return nil, fmt.Errorf("fs_files_media state=false: %s", resp.ErrMsg)
	}
	return resp.Ancestors, nil
}

// ResolveDirPath 通过 cid 获取文件夹的完整云端路径（包含文件夹自身名称）。
// 三级回退：
//  1. 内存缓存 pathCache (key=cid)
//  2. FsFilesMediaAncestors(cid) 祖先链 +  在父目录中 FsFiles 回查自身 Name
//  3. 失败返回空串+error（不再伪造 /unknown/ 虚拟路径）
func (c *LifeClient) ResolveDirPath(ctx context.Context, cid string) (string, error) {
	if cid == "" || cid == "0" {
		return "", nil // 根目录没有路径
	}
	now := time.Now()
	// 1) 缓存
	if cached, ok := c.pathCache.Load(cid); ok {
		entry := cached.(cachedPathEntry)
		if now.Before(entry.expiresAt) {
			return entry.path, nil
		}
		c.pathCache.Delete(cid)
	}

	// 2) 祖先链
	ancestors, err := c.FsFilesMediaAncestors(ctx, cid)
	if err != nil {
		return "", fmt.Errorf("ResolveDirPath ancestors cid=%s: %w", cid, err)
	}

	// a) 组装祖先段路径（去掉 根目录/ 前缀）
	var ancestorNames []string
	for _, n := range ancestors {
		name := strings.TrimSpace(n.Name)
		if name == "" || name == "根目录" {
			continue
		}
		ancestorNames = append(ancestorNames, name)
	}

	// b) grandparentCid = 包含 cid 所指文件夹的那个目录（cid 的直接父目录）。
	//    - 如果 ancestors 非空，ancestors 的最后一项就是 cid 的父文件夹
	//    - 如果 ancestors 为空，说明 cid 是根目录下的一级文件夹，父就是 "0"
	grandparentCid := "0"
	if len(ancestors) > 0 {
		last := ancestors[len(ancestors)-1]
		if last.ID > 0 {
			grandparentCid = strconv.Itoa(last.ID)
		}
	}

	// c) 在 grandparentCid 目录下列表，找到 ID=cid 的文件夹项就读它的 Name
	var folderOwnName string
	if c.fsClient != nil {
		resp, listErr := c.fsClient.FsFiles(ctx, grandparentCid, 2000, 0, c.cookie)
		if listErr == nil && resp != nil && resp.State {
			for i := range resp.Data {
				e := &resp.Data[i]
				if !e.IsDir {
					continue
				}
				if anyIDMatches(e.CID, cid) {
					folderOwnName = strings.TrimSpace(e.Name)
					break
				}
			}
		} else if listErr != nil {
			logger.S().Warnf("[LifeClient] ResolveDirPath FsFiles(grandparent=%s) 失败 (将尝试仅用祖先链): %v",
				grandparentCid, listErr)
		}
	}

	// d) 如果 FsFiles 没找到名字，尝试再查一次祖先 API 的「扩展字段」：
	//    部分实现会把当前目录自身作为「最后一个 ancestor」，我们可以再尝试。
	if folderOwnName == "" && len(ancestors) > 0 {
		last := ancestors[len(ancestors)-1]
		if grandparentCid != "0" && strconv.Itoa(last.ID) != grandparentCid {
			folderOwnName = strings.TrimSpace(last.Name)
			if folderOwnName == "根目录" {
				folderOwnName = ""
			}
		}
	}
	// e) 仍找不到名 —— 返回错误，路径不完整不做臆造（参考项目也是 API 失败直接 return None）
	if folderOwnName == "" {
		return "", fmt.Errorf("ResolveDirPath: 无法获取文件夹自身名称 cid=%s grandparent=%s ancestorCount=%d",
			cid, grandparentCid, len(ancestors))
	}

	// 3) 组装完整路径：[ancestorNames...] + folderOwnName
	ancestorNames = append(ancestorNames, folderOwnName)
	fullPath := strings.Join(ancestorNames, "/")
	fullPath = stripRootPrefix(fullPath) // 再次安全清理

	// 4) 缓存（即便空串也缓存一小段时间，避免重复失败）
	c.pathCache.Store(cid, cachedPathEntry{
		path:      fullPath,
		expiresAt: now.Add(c.pathCacheTTL),
	})
	return fullPath, nil
}

// ResolvePathByFileID 通过事件 file_id 自身查询祖先链，解析出文件/文件夹的完整云路径。
// 解决「事件 parent_id=0（所有事件都显示在根目录）」的痛点：115 生活事件 API 的 parent_id 字段
// 经常不可靠（甚至全部为 0），此时直接用 file_id 作为 cid 调 medialist 祖先链仍然能拿到真实路径链。
//
// 解析策略：
//  1. ancestors 里包含所有祖先目录（不含 file_id 自身）—— 最后一个节点即 file_id 的直接父目录
//  2. 组装 ancestors(排除根目录) + "/" + fileName
//  3. 失败返回空串，不再伪造虚拟路径
func (c *LifeClient) ResolvePathByFileID(ctx context.Context, fileID, fileName string) string { //nolint:cyclop // complexity: 43
	fileID = strings.TrimSpace(fileID)
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return ""
	}
	if fileID == "" || fileID == "0" {
		// 没 file_id，退化为裸文件名（仅作为最后兜底）
		return "/" + fileName
	}

	// 先尝试缓存 key=fileID
	now := time.Now()
	if cached, ok := c.pathCache.Load("fid:" + fileID); ok {
		entry := cached.(cachedPathEntry)
		if now.Before(entry.expiresAt) {
			if entry.path != "" {
				return entry.path + "/" + fileName
			}
		} else {
			c.pathCache.Delete("fid:" + fileID)
		}
	}

	// 用 file_id 作为 cid 查祖先链
	ancestors, err := c.FsFilesMediaAncestors(ctx, fileID)
	ancestorCount := 0
	ancestorsOK := (err == nil)
	if ancestorsOK {
		ancestorCount = len(ancestors)
	}
	// 对 err / state=false / ancestors=0 三种情况，统一进入回退逻辑：
	//   1) FsFiles(cid=0) 根目录列目录核对 → 确认真在根目录 → 裸文件名
	//   2) 否则遍历根目录下每个子文件夹列子目录内容，找到 fileID 匹配 → 得出父目录名
	//   3) 最后尝试把 fileID 当 cid 调 ResolveDirPath 反查自身所在位置
	if !ancestorsOK || ancestorCount == 0 {
		logTag := "ancestors=0"
		if !ancestorsOK {
			logTag = "ancestors_FAIL"
			logger.S().Warnf("[LifeClient] ResolvePathByFileID ancestors 失败 fid=%s name=%s: %v → 转入根目录核对降级",
				fileID, fileName, err)
		}
		if ancestorCount == 0 {
			logger.S().Infof("[LifeClient] ResolvePathByFileID ancestors=0 fid=%s name=%s, 将用 FsFiles 核对是否为真根目录",
				fileID, fileName)
		}
		_ = logTag
		if c.fsClient != nil {
			resp, lerr := c.fsClient.FsFiles(ctx, "0", 2000, 0, c.cookie)
			if lerr == nil && resp != nil && resp.State {
				foundAtRoot := false
				for i := range resp.Data {
					e := &resp.Data[i]
					if strings.EqualFold(strings.TrimSpace(e.Name), fileName) && anyIDMatches(e.CID, fileID) {
						logger.S().Infof("[LifeClient] ResolvePathByFileID fid=%s 根目录核对OK (name=%s) → 裸文件名",
							fileID, fileName)
						c.pathCache.Store("fid:"+fileID, cachedPathEntry{
							path:      "",
							expiresAt: now.Add(c.pathCacheTTL),
						})
						return "/" + fileName
					}
				}
				if !foundAtRoot {
					// 不在根目录 → 遍历根目录下每个子文件夹（cid），递归列其内容，找到 cid==fileID 且名匹配时返回 parentName/fileName
					dirCount := 0
					for i := range resp.Data {
						e := &resp.Data[i]
						if !e.IsDir {
							continue
						}
						dirCount++
					}
					logger.S().Infof("[LifeClient] ResolvePathByFileID 根目录列到 %d 个子文件夹，开始遍历查找 fid=%s name=%s",
						dirCount, fileID, fileName)
					for i := range resp.Data {
						e := &resp.Data[i]
						if !e.IsDir {
							continue
						}
						subCid := ""
						switch v := e.CID.(type) {
						case string:
							subCid = v
						case int, int64, float64:
							subCid = fmt.Sprintf("%v", v)
						}
						if subCid == "" || subCid == "0" {
							continue
						}
						subResp, slerr := c.fsClient.FsFiles(ctx, subCid, 2000, 0, c.cookie)
						if slerr != nil || subResp == nil || !subResp.State {
							continue
						}
						for j := range subResp.Data {
							se := &subResp.Data[j]
							if strings.EqualFold(strings.TrimSpace(se.Name), fileName) && anyIDMatches(se.CID, fileID) {
								parentName := strings.TrimSpace(e.Name)
								if parentName == "" || parentName == "根目录" {
									continue
								}
								logger.S().Infof("[LifeClient] ResolvePathByFileID fid=%s 通过FsFiles核对: parent=%s name=%s",
									fileID, parentName, fileName)
								c.pathCache.Store("fid:"+fileID, cachedPathEntry{
									path:      parentName,
									expiresAt: now.Add(c.pathCacheTTL),
								})
								return parentName + "/" + fileName
							}
						}
					}
					// 两级都没找到：最后尝试把 fileID 当文件夹 cid 调 ResolveDirPath 反查自身所在位置
					if dirPath, derr := c.ResolveDirPath(ctx, fileID); derr == nil && dirPath != "" {
						logger.S().Infof("[LifeClient] ResolvePathByFileID fid=%s 通过ResolveDirPath回退: dirPath=%s",
							fileID, dirPath)
						c.pathCache.Store("fid:"+fileID, cachedPathEntry{
							path:      dirPath,
							expiresAt: now.Add(c.pathCacheTTL),
						})
						return dirPath + "/" + fileName
					}
				}
			} else {
				logger.S().Warnf("[LifeClient] ResolvePathByFileID FsFiles 根目录也失败 fid=%s: err=%v respOK=%v",
					fileID, lerr, resp != nil && resp.State)
			}
		}
		// 所有 API 都失败：返回 "/" + fileName 作为最后兜底（caller 会根据 mapping 判断无效）
		return "/" + fileName
	}

	// 组装父目录段（排除 根目录、空名）
	var parentNames []string
	for _, n := range ancestors {
		name := strings.TrimSpace(n.Name)
		if name == "" || name == "根目录" {
			continue
		}
		parentNames = append(parentNames, name)
	}

	parentDir := strings.Join(parentNames, "/")
	parentDir = stripRootPrefix(parentDir)

	// 缓存父目录
	c.pathCache.Store("fid:"+fileID, cachedPathEntry{
		path:      parentDir,
		expiresAt: now.Add(c.pathCacheTTL),
	})

	if parentDir == "" {
		return "/" + fileName
	}
	return parentDir + "/" + fileName
}

// ResolvePath 通过 parent_id + file_name 解析文件/文件夹在云端的完整路径
//
// 修复点（关键）：115 生活事件 parent_id 字段经常不可靠（几乎全部为 0），
// 之前 parentID=0 时直接 return "/fileName" 导致路径缺少「电影/」等父级前缀。
// 现改为多级回退：
//
//  1. parentID 合法（非空非0）→ 走 ResolveDirPath(parentID) + "/" + fileName（原有逻辑）
//  2. parentID 无效但 fileID 有值 → 调 ResolvePathByFileID(fileID, fileName) 用 file_id 自身查祖先链
//  3. 全部失败 → 返回裸文件名 "/" + fileName（保留最后一个可选项，由调用方根据 mapping 判断是否有效）
func (c *LifeClient) ResolvePath(ctx context.Context, parentID, fileID, fileName string) string {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return ""
	}

	parentID = strings.TrimSpace(parentID)
	fileID = strings.TrimSpace(fileID)

	// 情况 1：parentID 看起来合法，走原 ResolveDirPath(parentID)
	if parentID != "" && parentID != "0" {
		dirPath, err := c.ResolveDirPath(ctx, parentID)
		if err == nil && dirPath != "" {
			return dirPath + "/" + fileName
		}
		if err != nil {
			logger.S().Warnf("[LifeClient] ResolvePath via parentID 降级: parentID=%s fid=%s name=%s: %v",
				parentID, fileID, fileName, err)
		}
		// 失败时 fallback 到 file_id 祖先链（继续往下）
	}

	// 情况 2：parentID=0/无效 或上面失败 → 用 file_id 自身查祖先链
	if fileID != "" && fileID != "0" {
		if byFid := c.ResolvePathByFileID(ctx, fileID, fileName); byFid != "" {
			return byFid
		}
	}

	// 情况 3：最后兜底：返回 "/fileName"。后续 mapping 若不命中会自然 NONE 跳过。
	return "/" + fileName
}

// FsFiles 列目录（生活事件文件夹递归处理使用）
// 包装底层 Client.FsFiles，复用其请求/解析逻辑
func (c *LifeClient) FsFiles(ctx context.Context, cid string, limit, offset int) (*FsFilesResp, error) {
	if c.fsClient == nil {
		return nil, fmt.Errorf("fsClient not initialized")
	}
	return c.fsClient.FsFiles(ctx, cid, limit, offset, c.cookie)
}

// toLifeEventItem 将灵活解析的 raw 转为强类型 LifeEventItem
func (r lifeEventRaw) toLifeEventItem() LifeEventItem {
	item := LifeEventItem{
		BehaviorType: r.BehaviorType,
		FileName:     r.FileName,
		PickCode:     r.PickCode,
		FilePath:     r.FilePath,
	}
	item.ID = lifeToString(r.ID)
	item.Type = lifeToInt(r.Type)
	item.FileCategory = lifeToInt(r.FileCategory)
	item.UpdateTime = lifeToInt64(r.UpdateTime)
	item.FileID = lifeToString(r.FileID)
	item.ParentID = lifeToString(r.ParentID)
	item.FileSize = lifeToInt64(r.FileSize)

	if item.BehaviorType == "" {
		if name, ok := BehaviorTypeToName[item.Type]; ok {
			item.BehaviorType = name
		}
	}
	return item
}

// lifeToInt 将 any 转为 int
func lifeToInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	case string:
		if n, err := strconv.Atoi(x); err == nil {
			return n
		}
	}
	return 0
}

// lifeToInt64 将 any 转为 int64
func lifeToInt64(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	case string:
		if n, err := strconv.ParseInt(x, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// lifeToString 将 any 转为 string
func lifeToString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case json.Number:
		return x.String()
	default:
		return fmt.Sprintf("%v", x)
	}
}

// truncateBody 截断字节为字符串，超出长度截断
func truncateBody(b []byte, maxLen int) string {
	if len(b) <= maxLen {
		return string(b)
	}
	return string(b[:maxLen])
}

// TypeNumberToString 115 数字类型 -> 分类字符串
func TypeNumberToString(t int) string {
	switch {
	case CreateEventTypes[t]:
		return "create"
	case DeleteEventTypes[t]:
		return "delete"
	case MoveEventTypes[t]:
		return "move"
	case RenameEventTypes[t]:
		return "rename"
	default:
		return "folder-sync"
	}
}
