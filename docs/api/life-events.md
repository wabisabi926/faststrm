# 生活事件日志 API

## GET `/api/lifeEvents`

列出生活事件处理日志（按时间倒序）。需要 JWT。

| 参数 | 类型 | 说明 |
|---|---|---|
| `account` | string | 账号名精确过滤 |
| `eventType` | string | 事件类型：`create` / `delete` / `move` / `rename` / `folder-sync` / `move-out` / `move-recreate` / `new_folder` / `poll` / `stall-timeout` |
| `success` | `true` / `false` | 成功或失败 |
| `since` | unix ms | 开始时间（含） |
| `until` | unix ms | 结束时间（不含） |
| `limit` | int | 返回条数，默认 200，最大 5000 |
| `mapped` | `true` / `1` | 只返回命中生活监控路径映射的事件 |

### `mapped=true` 过滤语义

- 依据「设置 → 生活事件 → 路径映射」：
  - `account` 非空时只匹配该账号；
  - `cloudPath` 匹配日志的 `filePath`（精确或子树）；
  - `localPath` 匹配日志的 `localPath`（精确或子树）。
- 任一路径命中即保留，因此即使事件没有完整 `filePath`，只要 `localPath` 在映射根内也会显示。
- 没有配置任何路径映射时返回空列表，不会退化成「显示全部」。

### 示例

```http
GET /api/lifeEvents?mapped=true&limit=200
Authorization: Bearer <token>
```

```json
{
  "total": 1,
  "items": [
    {
      "id": 123,
      "timestamp": 1793000000000,
      "account": "alice",
      "eventType": "create",
      "success": true,
      "filePath": "Service/movie/a.mkv",
      "localPath": "/app/data/strm/Service/movie/a.strm",
      "message": "STRM 已创建"
    }
  ]
}
```

## DELETE `/api/lifeEvents`

| 参数 | 说明 |
|---|---|
| `id` | 删除单条日志 |
| `action=cleanup` | 清理 30 天前日志 |
| `action=clear` | 清空全部日志 |
| `eventType=poll` | 按类型清理（例如清理历史轮询日志） |
