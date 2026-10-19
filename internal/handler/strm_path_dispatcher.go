package handler

import (
	"net/http"
	"strings"
	"sync/atomic"
)

// StrmPathDispatcher 404 分发器：把「路径式 STRM URL」交给 STRM 处理器，其余回落到 SPA。
//
// 背景：go-zero 路由只支持 :param（单段），不支持 * 通配，无法直接注册
// /api/strm/{任意深度路径}。而路径式 STRM URL（对齐 p115strmhelper 的
// redirect_url/电影/.../xxx.iso?pickcode=...）需要让路径末尾的 .iso/.mkv
// 出现在 URL 的 path 段，播放器才能可靠识别容器类型。
//
// 实现：注册为 rest.WithNotFoundHandler；命中 /api/strm/ 前缀的 GET/HEAD
// 转给 STRM 处理器，其余请求（含前端 SPA 路由、静态资源）原样回落。
// STRM 处理器在路由注册阶段通过 SetStrmHandler 注入（依赖就绪晚于 server 创建）。
type StrmPathDispatcher struct {
	spa  http.Handler
	strm atomic.Pointer[http.Handler]
}

// NewStrmPathDispatcher 创建分发器；spa 为未命中时的回落处理器（通常为前端 SPA）。
func NewStrmPathDispatcher(spa http.Handler) *StrmPathDispatcher {
	return &StrmPathDispatcher{spa: spa}
}

// SetStrmHandler 注入路径式 STRM 处理器（须在 server.Start 前调用）。
func (d *StrmPathDispatcher) SetStrmHandler(h http.Handler) {
	d.strm.Store(&h)
}

// ServeHTTP 实现 http.Handler。
func (d *StrmPathDispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isStrmPathRequest(r) {
		if h := d.strm.Load(); h != nil {
			(*h).ServeHTTP(w, r)
			return
		}
	}
	d.spa.ServeHTTP(w, r)
}

// isStrmPathRequest 判断是否为路径式 STRM 请求。
// 仅 GET/HEAD（播放器取流），且路径必须以 /api/strm/ 开头；
// /api/strm 本身由正常路由处理，/api/strm/cache/clear 是 POST，不会进 404 分发。
func isStrmPathRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return strings.HasPrefix(r.URL.Path, "/api/strm/")
}
