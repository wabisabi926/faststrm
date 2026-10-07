// Package embyproxy — 外部播放器链接注入（精简版）
//
// 对齐 MoviePilot embyreverseproxy external_players.py：在 Emby Web 详情页
// 为 STRM 媒体项注入 PotPlayer / VLC / Infuse / MPV 的 ExternalUrls 起播链接，
// 前端脚本据此渲染按钮，点击后经 /redirect2external 302 唤起本地播放器播放网盘直链。
//
// 默认关闭，由设置项 Emby.ExternalPlayerEnabled 控制。
package embyproxy

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/wabisabi926/faststrm/pkg/logger"
)

// externalPlayer 外部播放器定义
type externalPlayer struct {
	key  string
	name string
}

// externalPlayers 精简版支持的播放器（按展示顺序）
var externalPlayers = []externalPlayer{
	{key: "potplayer", name: "PotPlayer"},
	{key: "vlc", name: "VLC"},
	{key: "infuse", name: "Infuse"},
	{key: "mpv", name: "MPV"},
}

const (
	// externalPlayerMarker 前端脚本标记，用于判断是否已注入
	externalPlayerMarker = "[EmbyReverseProxy] externalPlayer"
	// externalRedirectPath 外部播放器唤起跳转路径
	externalRedirectPath = "/redirect2external"
)

// externalPlayerItemRouteRE 匹配 Emby 详情页 Items 请求路径：
//
//	/users/{userId}/items/{itemId}
//	/emby/users/{userId}/items/{itemId}
var externalPlayerItemRouteRE = regexp.MustCompile(`^/(?:emby/)?users/[^/]+/items/[^/]+$`)

var (
	osWindowsRE = regexp.MustCompile(`(?i)compatible|Windows`)
	osMacRE     = regexp.MustCompile(`(?i)Macintosh|MacIntel`)
	osIOSRE     = regexp.MustCompile(`(?i)iphone|Ipad`)
	osAndroidRE = regexp.MustCompile(`(?i)android`)
	osUbuntuRE  = regexp.MustCompile(`(?i)Ubuntu`)
)

// SetExternalPlayers 设置是否启用外部播放器链接注入。
func (p *Proxy) SetExternalPlayers(enabled bool) {
	p.externalPlayersEnabled.Store(enabled)
}

// externalPlayersOn 是否启用外部播放器链接注入
func (p *Proxy) externalPlayersOn() bool {
	return p.externalPlayersEnabled.Load()
}

// isExternalPlayerItemPath 判断是否为详情页 Items 路径，命中返回 itemID
func isExternalPlayerItemPath(path string) (string, bool) {
	lower := strings.ToLower(path)
	if !externalPlayerItemRouteRE.MatchString(lower) {
		return "", false
	}
	idx := strings.LastIndex(lower, "/items/")
	if idx < 0 {
		return "", false
	}
	return path[idx+len("/items/"):], true
}

// ============================================================
// 请求处理：详情页 Items 注入 + 唤起跳转
// ============================================================

// serveItemExternalURLs 代理详情页 Items 响应，并向其中注入外部播放器 ExternalUrls。
func (p *Proxy) serveItemExternalURLs(w http.ResponseWriter, r *http.Request) {
	target := p.embyHost + r.URL.RequestURI()
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	req.Header.Del("Host")
	// 去掉 Accept-Encoding，确保上游返回未压缩内容以便改写 body
	req.Header.Del("Accept-Encoding")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		logger.S().Warnf("[EmbyProxy] 外部播放器注入: 上游请求失败: %v", err)
		http.Error(w, fmt.Sprintf("Emby Error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	itemID, _ := isExternalPlayerItemPath(r.URL.Path)
	out := body
	injected := false
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if resp.StatusCode == http.StatusOK && strings.Contains(ct, "json") {
		apiKey := extractAPIKey(r)
		if apiKey != "" && itemID != "" {
			var data map[string]interface{}
			if err := json.Unmarshal(body, &data); err == nil {
				if injectExternalURLs(data, r, p.embyHost, itemID, apiKey) {
					if encoded, mErr := json.Marshal(data); mErr == nil {
						out = encoded
						injected = true
					}
				}
			}
		}
	}

	for k, vv := range resp.Header {
		lk := strings.ToLower(k)
		if hopByHopHeaders[lk] || lk == "content-encoding" || lk == "content-length" ||
			lk == "content-md5" || lk == "etag" || lk == "last-modified" {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	if injected {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		logger.S().Infof("[EmbyProxy] 已注入外部播放器链接: item=%s", itemID)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(out)
}

// serveExternalRedirect 解码 link(base64) 后 302 跳转到目标播放器协议链接
func (p *Proxy) serveExternalRedirect(w http.ResponseWriter, r *http.Request) {
	link := r.URL.Query().Get("link")
	raw := ""
	if link != "" {
		if decoded, err := decodeRedirectLink(link); err == nil {
			raw = decoded
		}
	}
	if raw == "" {
		raw = "/"
	}
	w.Header().Set("Location", raw)
	w.WriteHeader(http.StatusFound)
}

// ============================================================
// ExternalUrls 注入（对齐 MoviePilot inject_external_urls）
// ============================================================

// injectExternalURLs 向 Items 响应注入 ExternalUrls 外部播放器链接，返回是否注入。
func injectExternalURLs(data map[string]interface{}, r *http.Request, embyHost, itemID, apiKey string) bool {
	sources := mapSlice(data, "MediaSources")
	if len(sources) == 0 {
		return false
	}

	externalURLs := mapSlice(data, "ExternalUrls")
	if externalURLs == nil {
		externalURLs = []interface{}{}
	}

	var positionTicks int64
	if userData, ok := data["UserData"].(map[string]interface{}); ok {
		if v, ok := userData["PlaybackPositionTicks"].(float64); ok {
			positionTicks = int64(v)
		}
	}
	itemTitle := mapString(data, "Name")
	osType := getOSType(r.Header.Get("User-Agent"))
	ms, hhmmss := positionParts(positionTicks)

	injected := false
	for _, sv := range sources {
		source, ok := sv.(map[string]interface{})
		if !ok {
			continue
		}
		streamURL := buildStreamURLForItem(r, embyHost, itemID, source, apiKey)
		subURL := getSubURL(streamURL, source, apiKey)
		sourceName := mapString(source, "Name")
		if sourceName == "" {
			sourceName = getDisplayTitle(source)
		}

		for _, pl := range externalPlayers {
			target := buildPlayerTargetURL(r, embyHost, pl.key, streamURL, subURL, itemTitle, ms, hhmmss, osType)
			if target == "" {
				continue
			}
			label := pl.name
			if sourceName != "" {
				label = pl.name + " - " + sourceName
			}
			// Name 前缀保留 key，供前端稳定匹配对应播放器链接
			externalURLs = append(externalURLs, map[string]interface{}{
				"Name": pl.key + ":" + label,
				"Url":  target,
			})
			injected = true
		}
	}
	data["ExternalUrls"] = externalURLs
	return injected
}

// buildStreamURLForItem 为媒体项构建指向代理自身的直链流 URL（触发 302）
func buildStreamURLForItem(r *http.Request, embyHost, itemID string, source map[string]interface{}, apiKey string) string {
	baseURL := externalBaseURL(r, embyHost)
	container := mapString(source, "Container")
	if container == "" {
		container = "mp4"
	}
	prefix := "videos"
	if strings.EqualFold(mapString(source, "Type"), "audio") {
		prefix = "audio"
	}
	return fmt.Sprintf("%s/emby/%s/%s/stream.%s?Static=true&MediaSourceId=%s&api_key=%s",
		baseURL, prefix, itemID, container, pyQuote(mapString(source, "Id"), ""), apiKey)
}

// buildPlayerTargetURL 按播放器协议拼装唤起链接（经 /redirect2external 包装），未知播放器返回空串。
func buildPlayerTargetURL(
	r *http.Request, embyHost, key, streamURL, subURL, title string,
	ms int, hhmmss, osType string,
) string {
	var raw string
	switch key {
	case "potplayer":
		raw = fmt.Sprintf("potplayer://%s /sub=%s /seek=%s /title=\"%s\"",
			pyQuote(streamURL, ":/?&=%"), pyQuote(subURL, ":/?&=%"), hhmmss, title)
	case "vlc":
		switch osType {
		case "windows", "macOS":
			raw = "vlc://" + pyQuote(streamURL, ":/?&=%")
		case "ios":
			raw = fmt.Sprintf("vlc-x-callback://x-callback-url/stream?url=%s&sub=%s",
				pyQuote(streamURL, ""), pyQuote(subURL, ""))
		default:
			raw = fmt.Sprintf(
				"intent:%s#Intent;package=org.videolan.vlc;type=video/*;S.subtitles_location=%s;S.title=%s;i.position=%d;end",
				pyQuote(streamURL, ":/?&=%"), pyQuote(subURL, ""), pyQuote(title, ""), ms)
		}
	case "infuse":
		raw = fmt.Sprintf("infuse://x-callback-url/play?url=%s&sub=%s",
			pyQuote(streamURL, ""), pyQuote(subURL, ""))
	case "mpv":
		if osType == "ios" || osType == "android" {
			raw = "mpv-handler://" + pyQuote(streamURL, ":/?&=%")
		} else {
			raw = "mpv-handler://play/" + urlsafeB64(streamURL)
			if subURL != "" {
				raw += "/?subfile=" + urlsafeB64(subURL)
			}
		}
	default:
		return ""
	}
	return wrapRedirect(r, embyHost, raw)
}

// wrapRedirect 把播放器协议链接包成 /redirect2external?link=<base64> 服务端地址
func wrapRedirect(r *http.Request, embyHost, rawURL string) string {
	serverAddr := externalBaseURL(r, embyHost)
	encoded := base64.StdEncoding.EncodeToString([]byte(rawURL))
	return serverAddr + externalRedirectPath + "?link=" + pyQuote(encoded, "")
}

// decodeRedirectLink 解码 redirect2external 的 base64 链接参数（自动补齐 padding）
func decodeRedirectLink(link string) (string, error) {
	switch len(link) % 4 {
	case 2:
		link += "=="
	case 3:
		link += "="
	}
	raw, err := base64.StdEncoding.DecodeString(link)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ============================================================
// 辅助
// ============================================================

// externalBaseURL 推导代理对外地址：优先请求 Host，缺失时回退 embyHost
func externalBaseURL(r *http.Request, fallback string) string {
	host := r.Host
	if host == "" {
		return strings.TrimRight(fallback, "/")
	}
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	return scheme + "://" + host
}

// getOSType 从 User-Agent 推断客户端操作系统
func getOSType(ua string) string {
	switch {
	case osWindowsRE.MatchString(ua):
		return "windows"
	case osMacRE.MatchString(ua):
		return "macOS"
	case osIOSRE.MatchString(ua):
		return "ios"
	case osAndroidRE.MatchString(ua):
		return "android"
	case osUbuntuRE.MatchString(ua):
		return "ubuntu"
	default:
		return "other"
	}
}

// positionParts 由播放进度 ticks 换算 (毫秒, "HH:MM:SS")
func positionParts(ticks int64) (int, string) {
	if ticks <= 0 {
		return 0, "00:00:00"
	}
	ms := int(ticks / 10000)
	sec := ms / 1000
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	return ms, fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// getDisplayTitle 取首个视频流的 DisplayTitle
func getDisplayTitle(source map[string]interface{}) string {
	for _, sv := range mapSlice(source, "MediaStreams") {
		stream, ok := sv.(map[string]interface{})
		if !ok || mapString(stream, "Type") != "Video" {
			continue
		}
		return mapString(stream, "DisplayTitle")
	}
	return ""
}

// getSubURL 构建外挂字幕直链（优先中文，其次任意外挂字幕），无则返回空串
func getSubURL(streamURL string, source map[string]interface{}, apiKey string) string {
	streams := mapSlice(source, "MediaStreams")
	if streams == nil {
		return ""
	}
	base := streamURL
	if idx := strings.Index(streamURL, "/stream."); idx >= 0 {
		base = streamURL[:idx]
	}

	preferred, fallback := -1, -1
	for i, sv := range streams {
		stream, ok := sv.(map[string]interface{})
		if !ok || stream["IsExternal"] != true {
			continue
		}
		if fallback < 0 {
			fallback = i
		}
		switch strings.ToLower(mapString(stream, "Language")) {
		case "chi", "zh", "zho":
			preferred = i
		}
		if preferred >= 0 {
			break
		}
	}
	subIdx := preferred
	if subIdx < 0 {
		subIdx = fallback
	}
	if subIdx < 0 {
		return ""
	}

	codec := "srt"
	if stream, ok := streams[subIdx].(map[string]interface{}); ok {
		if c := mapString(stream, "Codec"); c != "" {
			codec = c
		}
	}
	apiPart := ""
	if apiKey != "" {
		apiPart = "?api_key=" + pyQuote(apiKey, "")
	}
	return fmt.Sprintf("%s/%s/Subtitles/%d/Stream.%s%s",
		base, pyQuote(mapString(source, "Id"), ""), subIdx, codec, apiPart)
}

// mapString 安全读取 map[string]interface{} 的字符串字段
func mapString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// mapSlice 安全读取 map[string]interface{} 的切片字段
func mapSlice(m map[string]interface{}, key string) []interface{} {
	if v, ok := m[key].([]interface{}); ok {
		return v
	}
	return nil
}

// pyQuote 模拟 Python urllib.parse.quote(s, safe)：
// 仅保留字母数字与 _.-~ 以及 safe 中的字符，其余按字节百分号编码；空格编码为 %20。
func pyQuote(s, safe string) string {
	var b strings.Builder
	b.Grow(len(s))
	const hexDigits = "0123456789ABCDEF"
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isPyQuoteSafe(c, safe) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexDigits[c>>4])
		b.WriteByte(hexDigits[c&0x0f])
	}
	return b.String()
}

// isPyQuoteSafe 判断字节是否属于 Python quote 的安全字符集
func isPyQuoteSafe(c byte, safe string) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	case c == '_' || c == '.' || c == '-' || c == '~':
		return true
	}
	return strings.IndexByte(safe, c) >= 0
}

// urlsafeB64 URL-safe base64 编码并去掉尾部 padding（对齐 urlsafe_b64encode().rstrip("=")）
func urlsafeB64(s string) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString([]byte(s)), "=")
}

// ============================================================
// 前端按钮注入脚本
// ============================================================

// buildExternalPlayerScript 构建前端按钮注入脚本（含 marker，返回非空）
func buildExternalPlayerScript() string {
	playersJSON := make([]string, 0, len(externalPlayers))
	for _, pl := range externalPlayers {
		playersJSON = append(playersJSON, fmt.Sprintf("{key:'%s',name:'%s'}", pl.key, pl.name))
	}
	return `<script>
// ` + externalPlayerMarker + `
(function(){
  var PLAYERS=[` + strings.Join(playersJSON, ",") + `];
  var ICON_BASE='https://emby-external-url.7o7o.cc/embyWebAddExternalUrl/icons';
  var ICON_MAP={potplayer:'icon-PotPlayer',vlc:'icon-VLC',infuse:'icon-infuse',mpv:'icon-MPV'};
  function makeBtn(p,url,title){
    var btn=document.createElement('button');
    btn.type='button';
    btn.className='detailButton emby-button emby-button-backdropfilter raised-backdropfilter detailButton-primary';
    btn.title=title||('使用 '+p.name+' 播放');
    var content=document.createElement('div');
    content.className='detailButton-content';
    var icon=document.createElement('i');
    icon.className='md-icon detailButton-icon button-icon button-icon-left';
    var iconFile=ICON_MAP[p.key];
    if(iconFile){
      icon.textContent='\u3000';
      icon.style.backgroundImage='url('+ICON_BASE+'/'+iconFile+'.webp)';
      icon.style.backgroundRepeat='no-repeat';
      icon.style.backgroundSize='100% 100%';
      icon.style.fontSize='1.4em';
    } else {
      icon.classList.add('material-icons');
      icon.textContent='open_in_new';
    }
    var span=document.createElement('span');
    span.className='button-text';
    span.textContent=p.name;
    content.appendChild(icon);
    content.appendChild(span);
    btn.appendChild(content);
    btn.addEventListener('click',function(){location.href=url;});
    return btn;
  }
  function getPlayerUrl(item,key){
    var arr=item&&item.ExternalUrls;
    if(!Array.isArray(arr)) return null;
    var prefix=key+':';
    for(var i=0;i<arr.length;i++){
      var it=arr[i];
      if(it&&typeof it.Name==='string'&&it.Name.indexOf(prefix)===0&&it.Url){
        return {url:it.Url,title:it.Name.substring(prefix.length)};
      }
    }
    return null;
  }
  function showFlag(){
    return !!document.querySelector("div[is='emby-scroller']:not(.hide) .mediaInfo:not(.hide)");
  }
  var _injecting=false;
  var _lastItemId=null;
  function createButtons(anchor,item){
    var wrapper=document.createElement('div');
    wrapper.id='ExternalPlayersBtns';
    wrapper.className='detailButtons flex align-items-flex-start flex-wrap-wrap detail-lineItem';
    PLAYERS.forEach(function(p){
      var v=getPlayerUrl(item,p.key);
      if(v&&v.url){
        wrapper.appendChild(makeBtn(p,v.url,v.title));
      }
    });
    if(wrapper.childElementCount>0){
      anchor.insertAdjacentElement('afterend',wrapper);
    }
  }
  function tryInject(){
    if(_injecting) return;
    var detailBtns=document.querySelector("div[is='emby-scroller']:not(.hide) .mainDetailButtons");
    if(!detailBtns||!showFlag()) return;
    var itemId=null;
    try{
      var hash=location.hash||'';
      var m=hash.match(/[?&]id=([^&]+)/i);
      if(m) itemId=m[1];
    }catch(e){}
    if(!itemId) return;
    var existing=document.getElementById('ExternalPlayersBtns');
    if(existing&&_lastItemId===itemId) return;
    if(existing) existing.remove();
    _injecting=true;
    _lastItemId=itemId;
    try{
      var userId=(ApiClient._currentUser&&ApiClient._currentUser.Id)||ApiClient.getCurrentUserId();
      ApiClient.getItem(userId,itemId).then(function(item){
        var old=document.getElementById('ExternalPlayersBtns');
        if(old) old.remove();
        if(item&&item.MediaSources&&item.MediaSources.length>0){
          var anchor=document.querySelector("div[is='emby-scroller']:not(.hide) .mainDetailButtons");
          if(anchor) createButtons(anchor,item);
        }
      }).catch(function(){}).then(function(){_injecting=false;});
    }catch(e){_injecting=false;}
  }
  function startObserver(){
    var ob=new MutationObserver(function(){
      if(showFlag()&&!document.getElementById('ExternalPlayersBtns')){
        tryInject();
      }
    });
    ob.observe(document.body,{childList:true,subtree:true});
  }
  if(document.body){startObserver();}
  else{document.addEventListener('DOMContentLoaded',startObserver);}
  document.addEventListener('viewshow',function(){
    _lastItemId=null;
    var old=document.getElementById('ExternalPlayersBtns');
    if(old) old.remove();
    setTimeout(tryInject,300);
  });
})();
</script>`
}
