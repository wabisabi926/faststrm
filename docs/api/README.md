# API 约定

## 认证

| 类型 | 端点 | 说明 |
|---|---|---|
| 公开 | `/api/health`、`/api/setup/status`、`/api/auth/login`、`/api/strm`、`/api/emby/webhook`、`/api/notify/webhook` | 播放端/回调无法携带 JWT |
| JWT | 其余 `/api/*` | `Authorization: Bearer <token>` |
| 初始化 | `/api/setup/admin` | 仅在 `config.admin.password` 为空时可用 |

首次启动必须完成 `/setup`；未完成时业务 API 返回 `409 SETUP_REQUIRED`。

## 错误响应

统一 JSON：

```json
{"error":"...","code":"OPTIONAL_CODE"}
```

常见状态码：`400` 参数错误、`401` 未认证、`403` 无权限、`409` 状态冲突、`429` 限流、`502` 上游失败。

## SSE

`GET /api/events/stream` 提供任务进度/清理事件；前端自动降级为轮询。

## 专题

- [STRM 播放与诊断](strm.md)
- [Emby 反向代理](emby-proxy.md)
- [Emby 通知与 Webhook](emby-notify.md)
- [系统状态与系统事件](runtime.md)

## OpenAPI

生成的 Swagger 在 `internal/server/swagger.json`；`scripts/swagger.json` 为脚本副本。接口变更时同步更新，不要只改一份。
