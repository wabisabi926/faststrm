# FastStrm 开发者文档

本目录面向开发者与维护者。用户安装、设置、FAQ 以 [GitHub Wiki](https://github.com/wabisabi926/faststrm/wiki) 为准；发布说明以 `.changes/` 和 Wiki 的「版本更新日志」为准。

## 文档地图

| 目录 | 内容 |
|---|---|
| `architecture/` | 当前架构、播放链路、STRM 生命周期、清理安全边界 |
| `adr/` | 架构决策记录（ADR），编号一旦发布不再修改 |
| `api/` | STRM、Emby 反代、系统状态等接口契约 |
| `dev/` | 构建、测试、发布、迁移工具 |
| `dev/archive/` | 已归档的一次性文档（会话交接、临时记录），不随代码更新 |
| `deploy/` | Docker / 飞牛 / Windows 的维护者说明 |
| `assets/` | 截图等二进制资源；流程图优先用 Mermaid |

## 文档边界

- `wiki_drafts/`：用户向 Wiki 的源文件，不是开发者文档。
- `.changes/`：单个版本的发布说明源文件。
- `docs/`：当前有效的开发者文档。
- `dev/`：历史实施计划/测试报告，仅作存档，不要当作当前行为依据。
- `docs/dev/archive/`：已结束的一次性文档，同样只作存档。

## 更新约定

- 架构文档描述**当前代码行为**；行为变更时同步更新。
- ADR 只追加，不重写历史；新决策另开编号。
- 文档中的路径使用仓库相对路径，便于在 GitHub 和本地编辑器里跳转。
- 不在文档中写 Cookie、Bot Token、完整签名 CDN URL 等敏感信息。
