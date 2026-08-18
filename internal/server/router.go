package server

import "net/http"

// 为了简化 router，所有路由注册已放在 server.go 的 RegisterRoutes 中。
// 本文件作为路由拆分保留位：后续模块多时按 handler 拆分。

// 占位：避免 "net/http" 导入未使用告警（如需要在本文件注册路由时移除下面的 _）
var _ = http.MethodGet
