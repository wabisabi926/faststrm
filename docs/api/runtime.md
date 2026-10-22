# 系统状态与系统事件

## `GET /api/runtime/status`

JWT 保护。返回当前进程与反代的聚合状态：

```json
{
  "status": "ok",
  "version": "v1.5.4",
  "startedAt": "2026-10-22T09:59:57+08:00",
  "uptimeSeconds": 120,
  "proxy": {
    "running": true,
    "listenAddr": "0.0.0.0:8097",
    "clientAddr": "http://192.168.1.10:8097",
    "externalPlayerEnabled": true,
    "forceProxyFormats": ["iso","bdmv","bdav","dvd","bluray"],
    "forceProxyUaTokens": ["Infuse","VidHub","SenPlayer","SenPlayerHD"],
    "rangeProxy": "10MBx2"
  },
  "journal": []
}
```

`0.0.0.0` 是监听地址；客户端使用 `clientAddr`。

## `GET /api/runtime/journal`

JWT 保护。参数：

| 参数 | 说明 |
|---|---|
| `limit` | 默认 100，最大 500 |
| `level` | `info` / `warn` / `error` |
| `category` | `server` / `task` / `cleanup` / `account` / `notify` / `rate` / `proxy` / `settings` |

返回：

```json
{"count": 1, "items": [{"id":1,"time":"...","level":"info","category":"proxy","message":"Emby 反代已启动"}]}
```

## 边界

- 这是**当前进程**的事件日志，内存环形缓冲，重启清空。
- 不记录每次播放；任务执行明细看 `/api/taskHistory` 和任务日志。
- 不记录 Cookie、Bot Token、完整签名 CDN URL。
- 当前记录点：服务启动/关闭、反代启动/停止/端口不可用、Emby 设置保存、115 Cookie 失效、任务开始/完成/失败、STRM 扫描与清理结果、账号巡检、Telegram 轮询启动/停止/409 冲突、115 限流与生活事件风控冷却。
