# 测试

## 必跑

```bash
go test ./...
go vet ./...
gofmt -l .
cd frontend && npm run build && npm test
```

涉及前端交互的改动，必须启动实际二进制，在浏览器里走一遍主路径；不能只凭 `tsc` 通过。

## 原盘/Range 验证

至少覆盖：

1. 真实 115 ISO 的 15MB Range，确认 `206` 与 `Content-Range` 精确。
2. 1MB Range / seek 后再次 Range。
3. 分片代理首片、后续分片顺序与总长度。
4. 代理未运行时 ISO 播放失败的提示（预期行为）。
5. 重复扫描/清理 token 不能执行两次。

测试时使用隔离配置目录，避免读写线上 8090 实例的真实配置。

## 测试数据

- 单测优先 `httptest.Server` 模拟 CDN/Emby，不访问真实 115。
- 迁移工具测试使用固定 URL fixtures，覆盖 FastStrm/p115/LitePan 方言。
- 破坏性清理只做临时目录，不在真实库执行。
