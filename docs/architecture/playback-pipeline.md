# 播放链路

## STRM 内容

标准格式由 `internal/service/strm/build.go` 统一生成：

```text
{prefix}/api/strm?account={account}&pickcode={pickcode}&file_name={file_name}
```

- `pickcode` 必须是 17 位字母数字。
- ISO 文件名为 `xxx.iso.strm`，内容里的 `file_name` 保留 `.iso`。
- 自定义 URL/文件名模板已下线：`SettingsStore` 在读取、Update、保存时都会清空旧字段，运行时只使用标准格式。

## 路由决策

`internal/service/strm/router.go` 的 `DecideRoute` 顺序：

1. 私网请求的 `?mode=proxy|redirect`（调试用，最高优先级）。
2. UA 命中 `strm.forceProxyUaTokens` → 代理。
3. 文件名/容器命中 `strm.forceProxyFormats` → 代理。
4. 默认 302。

代理时：

- 普通格式并发超限 → 降级 302。
- 原盘格式（默认 `iso/bdmv/bdav/dvd/bluray`）允许少量软超限，超出后排队，不降级 302。
- `/api/strm` 对 302 做 HEAD 预检，预检失败自动回退代理。

## 直链缓存与刷新

- 缓存 key：`account:pickcode|ua`；不同 UA 可能拿到不同 CDN URL。
- 缓存 TTL 默认 5 分钟，并按 CDN URL 的 `t` 参数提前过期。
- 并发相同 pickcode 用 singleflight 合并。
- 失败短时负面缓存，避免 115 API 雪崩。
- 代理流遇到 401/403/404/408/429/5xx 会换链重试一次；Range 分片换链用 singleflight 合并。
- 代理 HTTP 客户端连接/TLS 握手超时 10s，单主机连接上限 16；CDN 黑洞节点不会长期占用代理并发额度。

## Range 分片代理

`internal/handler/range_proxy.go`：

- 仅对 GET + 上游 206 生效；首片直写，剩余分片有界并发、按序输出。
- 严格校验 `Content-Range` 起点/终点和 `Content-Length`。
- 默认 10MB × 2，单流并发上限 4；设置页可调。
- 同一账号的 Range 请求共享一个并发池（默认跟随分片并发数，上限 8），避免多路原盘叠加触发 115 风控。
- 客户端断开时取消所有在途分片并关闭响应体。

## Emby 反向代理

客户端必须连接 FastStrm 反代端口（默认 8097），不能连 Emby 本体端口（默认 8096）：

- PlaybackInfo 被改写：`SupportsDirectPlay=true`、`SupportsTranscoding=false`、删除转码字段。
- 媒体流请求按源格式决定：
  - 原盘格式 → `proxyStreamToStrm`，由 STRM 端点走 Range 代理。
  - 普通格式 → 解析 302 链，客户端直连 CDN；解析不到自身端点时回退代理。
  - 自身 `/api/strm` 的 seek 代理和 302 解析走进程内 Handler，不经过域名/DNS/NAT 回环；外部 STRM 保留 HTTP fallback。
  - 解析外部 STRM 时只发送 `User-Agent` / `Accept`，不向第三方源外发 `X-Emby-Token`、`Authorization`、`Cookie`；跨主机重定向再兜底清除这些凭据。
- Web HTML/JS 注入 crossOrigin 补丁，避免浏览器跨域拉 CDN 失败。
- 外部播放器按钮只在通过 8097 访问 Emby 网页端时注入。

## 默认格式策略

| 格式 | 默认 |
|---|---|
| `iso`、`bdmv`、`bdav`、`dvd`、`bluray` | 服务端代理 |
| `mkv`、`mp4`、`ts`、`m2ts` 等 | 302 直连 |
| `forceProxyUaTokens` 命中的客户端 | 全部代理 |
| BDMV 目录 | 生成阶段跳过 |

设置项：`strm.forceProxyFormats`、`strm.forceProxyUaTokens`、`strm.disableRangeProxy`、`strm.proxyChunkSizeMB`、`strm.proxyConcurrency`。
