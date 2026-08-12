# FastStrm Wiki

**开源、简洁、可改造的 115 网盘 STRM 生成与同步工具**

---

## 📌 版本公告

> **🎉 v0.8.2 已发布**
>
> - ✅ **统一页面排版**：设置 / TG 通知 / Emby 通知页面全部改为 border/section 风格，Label 行高优化为 leading-6
> - ✅ **默认设置 + admin 登录修复**：docker-entrypoint.sh 复制 `.config/.config.json` 后自动补 `admin/admin` 密码哈希，修复首次部署 admin 登录失败；去掉了 TS 层重复的配置复制逻辑（Docker 原本就有）
> - ✅ **生活监控自动启动**：服务重启后，若监控配置已启用且账号 Cookie/凭据正常，首次访问监控页面或接口时会自动拉起，无需手动点击启动
> - ✅ **清理 .trash 临时目录**：旧代码残留的 `data/.trash` 目录已完全移除，相关代码路径清零
> - 完整变更说明：[GitHub Releases](https://github.com/wabisabi926/faststrm/releases)

---

## 🚩 目录导航

### 🚀 入门必读

| 序号 | 页面 | 适合人群 |
|:----:|------|---------|
| 1 | **[快速开始](快速开始)** | 新用户 — 从部署到播放的完整流程 |
| 2 | **[配置说明](配置说明)** | 高级用户 — settings.json 全字段参考 |

### ⚙️ 核心功能

| 模块 | 页面 | 说明 |
|------|------|------|
| 路由播放 | **[STRM 路由策略](STRM-路由策略)** | 302 vs 代理决策逻辑、规则引擎、降级机制 |
| 删除同步 | **[删除同步](删除同步)** | Emby library.deleted 事件处理、路径映射、试运行模式 |
| 实时监控 | **[生活事件监控](生活事件监控)** | 115 网盘变动增量同步、四类事件处理 |
| 清理对账 | **[STRM 清理对账](STRM-清理对账)** | 孤儿扫描、全量对账、回收站管理 |
| 消息通知 | **[Telegram 通知](Telegram-通知)** | Bot 配置、通知类型、轮询 vs Webhook |
| 分享转存 | **[分享链接转存](分享链接转存)** | 115 链接解析、目录浏览、一键转存 |

### 📚 参考资料

| 页面 | 说明 |
|------|------|
| **[安装部署](安装部署)** | Docker/手动/生产环境部署，端口、目录、反向代理 |
| **[功能详解 - STRM 生成](功能详解-STRM生成)** | 任务配置、路径映射、302 模式详解 |
| **[功能详解 - Emby 集成](功能详解-Emby集成)** | 媒体库刷新、Webhook 通知、删除同步 |
| **[FAQ](FAQ)** | 常见问题、排错思路、已知限制 |
| **[版本更新日志](版本更新日志)** | 历史版本完整变更记录 |
| **[开发指南](开发指南)** | 本地开发环境搭建、项目结构、构建流程 |
| **[致谢](致谢)** | 参考项目与依赖列表 |

---

## 🛠️ 技术架构

| 层级 | 技术选型 |
|------|---------|
| 前端 / 后端一体化 | **Next.js 15** (App Router + TypeScript + Turbopack) |
| UI 组件 | **shadcn/ui** + **Tailwind CSS** + **Radix UI** |
| 数据库 | **SQLite** (better-sqlite3) |
| 115 协议 | p115client 理念自研 SDK（Web + iOS API 双通道） |
| 任务调度 | 原生 async 调度器 + cron 表达式 |
| 速率控制 | 账号级令牌桶 + 指数退避重试 |
| 部署 | **Docker**（linux/amd64 + linux/arm64） |

---

## 🤝 参与贡献

欢迎提交 [Issue](https://github.com/wabisabi926/faststrm/issues) 和 [Pull Request](https://github.com/wabisabi926/faststrm/pulls) 来改进这个项目。
