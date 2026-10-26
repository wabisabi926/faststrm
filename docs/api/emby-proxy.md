# Emby 反向代理

FastStrm 在独立端口（默认 8097）运行 Emby 反向代理；客户端必须连接该端口，连接 8096 不会触发 PlaybackInfo 改写。

## 拦截范围

| 路径 | 行为 |
|---|---|
| `/Items/{id}/PlaybackInfo` | 识别 HTTP/STRM 源，强制 DirectPlay，删除转码字段 |
| `/videos/{id}/stream`、`/audio/{id}/stream` | Static=true 的直链流：按格式 302 或代理 |
| `/videos/{id}/{name}`、`/items/{id}/download|file` | 媒体流路由 |
| `/web/...`、HTML 壳、`htmlvideoplayer.js` | 注入 crossOrigin 补丁 |
| `/system/info` | 端口改写为反代端口 |
| WebSocket | 双向转发 |
| 其他 | 透传 Emby |

## PlaybackInfo

对 STRM/HTTP 源：

- `SupportsDirectPlay=true`
- `SupportsDirectStream=true`
- `SupportsTranscoding=false`
- 删除 `TranscodingUrl`/`TranscodingContainer`/`TranscodingSubProtocol`
- 生成 `DirectStreamUrl=/videos/{id}/stream?Static=true&MediaSourceId=...`

原盘格式（`iso/bdmv/bdav/dvd/bluray`）走服务端代理；普通格式解析 302 链后直连 CDN。

## 外部 STRM 请求头边界

解析外部 STRM 的 HEAD/重定向请求只携带 `User-Agent` / `Accept`：

- 不发送 `X-Emby-Token`、`Authorization`、`Cookie` 及用户身份头；
- 跨主机重定向时再兜底清除敏感头；
- 若外部源依赖 Emby 认证头鉴权，需要在源侧配置独立凭据。

内置 `/api/strm` 不受影响：自身端点走进程内 Handler，不经过外部 HTTP 请求。

## 状态接口

`GET /api/emby/proxy/status`（JWT）返回：

```json
{
  "running": true,
  "listenAddr": "0.0.0.0:8097",
  "clientAddr": "http://192.168.1.10:8097",
  "embyURL": "http://192.168.1.10:8096",
  "externalPlayerEnabled": true,
  "startedAt": "2026-10-22T09:00:00+08:00",
  "forceProxyFormats": ["iso","bdmv","bdav","dvd","bluray"],
  "forceProxyUaTokens": ["Infuse","VidHub","SenPlayer","SenPlayerHD"],
  "rangeProxy": "10MBx2"
}
```

`0.0.0.0` 是监听地址，不是客户端地址；客户端应使用 `clientAddr`。

## 外部播放器

开启后，通过反代访问 Emby 详情页会注入 PotPlayer / VLC / Infuse / MPV 按钮。仅反代端口生效；直连 8096 不显示。
