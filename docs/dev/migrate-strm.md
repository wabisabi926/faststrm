# STRM 迁移工具

把 LitePan / p115strmhelper / 旧 FastStrm 的 `.strm` 内容原地重写为当前 FastStrm 标准 URL，**不重新扫描云端、不重新生成文件**。

## 支持矩阵

| 来源 | 支持 | 说明 |
|---|---|---|
| FastStrm `/api/fs/get` | ✅ | 直接改为 `/api/strm` |
| p115 `redirect_url?pickcode=` | ✅ | 提取 pickcode，指定目标账号 |
| LitePan `/api/strm/play/{account}/{fileKey}/...` | ✅ | fileID → pickcode，需要账号映射 |
| p115 路径路由 / 分享链接 | ❌ | 需要转存或路径反查，工具只报告不修改 |
| LitePan `/api/strm/path/...` | ❌ | 二期支持路径反查 |
| 直接 CDN URL | ❌ | 没有稳定 pickcode，无法迁移 |

## 用法

```bash
# 预览（默认不写盘）
faststrm-migrate -config /path/to/.config \
  -root /mnt/media/strm -from auto \
  -account alice -account-map "1=alice,2=bob" \
  -prefix http://nas:8090

# 实际写入 + 备份 + 登记托管
faststrm-migrate -config /path/to/.config \
  -root /mnt/media/strm -account alice \
  -cloud-root /媒体 -write -backup -register
```

| 参数 | 说明 |
|---|---|
| `-from` | `auto` / `faststrm` / `p115` / `litepan` |
| `-account` | 目标账号；p115 必填 |
| `-account-map` | LitePan accountID → FastStrm 账号，如 `1=alice` |
| `-prefix` | 目标 URL 前缀；为空时用 settings 或自动内网 IP |
| `-cloud-root` | 本地根对应的云端路径，用于托管登记 |
| `-write` | 实际写入；不加为 dry-run |
| `-backup` | 写入前把旧内容备份到 `<root>/.faststrm-migrate-backup/<时间>` |
| `-register` | 写入后登记 managed_strms；需要 `-cloud-root` |

## 行为

1. 遍历 `.strm`，读取第一行并识别方言。
2. p115 直接取 pickcode；LitePan 用 115 `files/info` 反查 pickcode（限流 + 缓存）。
3. 用 `strm.BuildContent` 生成标准 URL，原子写入；内容相同则跳过。
4. 旧内容可选备份；失败文件保持原样并输出原因。
5. 退出码：有失败文件为 1，全部成功/跳过为 0。

## 注意

- 迁移前停止 FastStrm 或确认没有任务在写同一目录；`-register` 会写 SQLite。
- 自定义 URL/文件名模板已下线，迁移统一生成标准 URL；需要使用 `-register` 时提供 `-cloud-root`。
- 迁移后建议 Emby 刷新媒体库；如果托管记录缺失，先跑一次全量扫描再执行清理。
