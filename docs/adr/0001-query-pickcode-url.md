# ADR 0001：STRM 使用 query + pickcode，不使用路径式 URL

- 状态：已接受
- 日期：2026-10-20（2026-10-22 复核）

## 背景

参考项目使用过路径式 STRM URL。路径式更易读，但 FastStrm 的生活事件默认 `MoveMediaMode=local_move`：

- 文件 move/rename 会重写 STRM 内容（仅当事件带 pickcode）。
- **文件夹 move/rename 只做目录 `os.Rename` + DB 前缀更新，不递归重写内部 STRM**。

## 决策

保持：

```text
/api/strm?account=..&pickcode=..&file_name=..
```

不增加路径解析路由，不增加“路径式 URL”设置项。

## 后果

- 文件夹移动后，内部 STRM 的 pickcode 仍然有效，无需递归重写。
- 事件缺 pickcode 时，旧 URL 仍能播放；路径式会立即失效。
- 可读性通过日志、诊断页、`file_name` 元数据解决，而不是 URL 语义。
- 如果未来要做路径式，必须先实现文件夹 move/rename 的递归内容重写、失败回滚和 Emby 刷库，成本高。
