package handler

import "net/http"

// HandleFsGet 兼容旧 STRM 文件，内部转发到 HandleStrm
// 历史：/api/fs/get 原本是"纯 302 简化端点"，v1.2.0 后也加了智能路由。
// enable302 开关废弃后，两个端点逻辑 100% 一致，直接合并。
// TODO: 未来版本可彻底废弃此路由
func HandleFsGet(opts StrmOptions) http.HandlerFunc {
	return HandleStrm(opts)
}
