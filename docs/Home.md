# Fast Strm

> 开源、简洁、可改造的 115 网盘 STRM 生成与同步工具

<div align="center">
  <img src="https://raw.githubusercontent.com/wabisabi926/faststrm/refs/heads/main/frontend/public/logo.png" alt="Fast Strm Logo" width="200" height="200">
</div>

## 这是什么？

Fast Strm 帮你把 115 网盘里的媒体文件生成本地 `.strm` 文件，配合 Emby/Jellyfin/Kodi 实现「云盘资源本地化播放」——媒体库看起来像本地文件，实际播放时直连 115 CDN，不占部署设备存储。

## 核心亮点

### 🚀 STRM 路由策略（方案 B）
不是「强制 302」或「强制代理」二选一，而是**规则引擎自动决策**：
- Infuse/VidHub/SenPlayer 等 seek 兼容性差的客户端 → 强制代理
- 其他客户端（Emby/Kodi/浏览器）→ 默认 302 直连 115 CDN，部署设备零中转
- 302 前做 HEAD 预检，115 CDN 不可达时静默降级代理，用户无感
- 115 单账号并发代理限流（阈值 8），避免触发 115 约 10 进程上限

### 📡 生活事件监控
30 秒轮询 115 生活事件，4 类事件（创建/删除/移动/重命名）自动同步 STRM：
- 3 层路径解析缓存，性能稳定
- 删除事件熔断（单次 >100 条或占比 >50% 跳过），防误删
- iOS API 405 自动降级 Web API

### 🗑️ Emby 删除同步
监听 Emby `library.deleted` 事件，自动删除本地 STRM + 关联字幕/nfo/图片 + 清理 DB：
- 三道防误删：STRM 存在检查 + 标题匹配 + 目录文件数 ≤100
- 60 秒去重窗口，避免与生活监控重复处理
- Dry-run 模式，首次配置只记日志不删除
- 7 天回收站，误删可恢复

### 🔒 凭据安全
- cookie/apiKey 用 AES-256-GCM 加密存储
- 密码 SHA-256 哈希
- JWT 登录态

### 📱 Telegram 通知
- 任务开始/完成/失败
- Emby 入库/删除/播放
- 剧集缓冲合并（10 秒窗口，避免刷屏）

## 快速导航

| 文档 | 内容 |
|------|------|
| [安装部署](安装部署) | Docker 部署、配置目录、端口说明 |
| [快速开始](快速开始) | 首次使用全流程：账号→任务→STRM→Emby |
| [STRM 生成](功能详解-STRM生成) | 任务配置、路径映射、302 模式 |
| [生活事件监控](功能详解-生活事件监控) | 实时同步、路径映射、事件类型 |
| [STRM 路由策略](功能详解-STRM路由策略) | proxy vs redirect、规则引擎 |
| [STRM 清理对账](功能详解-STRM清理对账) | 孤儿扫描、全量对账、回收站 |
| [Emby 集成](功能详解-Emby集成) | 刷新、Webhook、删除同步 |
| [Telegram 通知](功能详解-Telegram通知) | Bot 配置、通知类型 |
| [分享链接转存](功能详解-分享链接转存) | 解析、浏览、转存 |
| [配置项参考](配置项参考) | AppSettings 全字段说明 |
| [FAQ](FAQ) | 常见问题 |

## 技术栈

- **前端/后端**：Next.js 15 (App Router + Turbopack)
- **数据库**：SQLite (better-sqlite3)
- **115 SDK**：p115client
- **部署**：Docker（linux/amd64 + linux/arm64）

## 开源协议

[MIT License](https://github.com/wabisabi926/faststrm/blob/main/LICENSE)

## 相关项目

- [p115client](https://github.com/ChenyangGao/p115client) - 115 网盘 Python 客户端
- [embyExternalUrl](https://github.com/bpking1/embyExternalUrl) - Emby 302 反代参考
- [MoviePilot-Plugins](https://github.com/DDSRem/MoviePilot-Plugins) - samediasyncdel 移植参考
