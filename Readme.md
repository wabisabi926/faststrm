<div align="center">
  <img src="https://raw.githubusercontent.com/wabisabi926/faststrm/refs/heads/main/frontend/public/logo.png" alt="Fast Strm Logo" width="200" height="200">
</div>

# Fast Strm

<div align="left">

**FastStrm — 让 115 网盘和你的播放器真正「同步」**

> 想象一下：你在 115 网盘存了一部电影，打开 Emby/Kodi 就能直接看；你在网盘删了，播放器里自动消失——FastStrm 就是实现这件事的工具。
>
> ✅ **网盘增删改，本地秒同步** — 网盘里新传的电影，播放器里立刻出现；删了的也自动消失，不用每次手动扫描
>
> ✅ **扫码登录，零配置** — 手机扫一扫就搞定，不用 F12 抓 Cookie，过期了再扫一下就好
>
> ✅ **看片不卡，零带宽消耗** — 直接走 115 原链接播放，不经过你的服务器中转，4K 秒开
>
> ✅ **Emby 深度联动** — 在 Emby 里删了片，网盘对应的本地文件自动清理，不占磁盘
>
> ✅ **异常主动通知** — Cookie 过期、账号异常，Telegram 第一时间推送，不用自己盯着看
>
> ✅ **Docker 一键启动** — 一行命令搞定，5 分钟内看完第一部电影

</div>

---

## 🚀 核心特性

- 开源自由，支持批量生成 `.strm` 文件
- 基于 115 目录树生成，支持自定义前缀配合媒体服务器使用
- 115 生活事件实时监控（增量同步 + 全量同步）
- **📱 115 扫码登录**：手机 App 扫码自动获取 Cookie，无需 F12 抓包；Cookie 失效后一键扫码刷新
- **🔄 账户状态 TG 通知闭环**：账号异常自动推送，扫码恢复后自动推送恢复通知
- **STRM 智能路由**：302 直连优先 + 静默降级代理，Infuse/VidHub/SenPlayer 自动强制代理，路由参数可在线配置
- **Emby 删除同步**：监听 `library.deleted` 事件自动清理 STRM，三道防误删保护
- 支持 Telegram Bot 通知与交互
- 轻量，易于二次开发

tg交流群：
https://t.me/+J6csNlBG6q1iYjBl

## 📦 快速开始

```bash
# 克隆并启动
git clone https://github.com/wabisabi926/faststrm.git
cd faststrm
docker-compose up -d

# 或使用 Docker 镜像
docker pull wabisabi926/faststrm:latest
docker run -d \
  --name faststrm \
  -p 3000:3000 \
  -v ./config:/app/config \
  -v ./data:/app/data \
  -v ./logs:/app/logs \
  -v /path/to/your/strm:/app/data/strm \
  -e TZ=Asia/Shanghai \
  --restart unless-stopped \
  wabisabi926/faststrm:latest
```

启动后访问 `http://localhost:3000`，默认账号密码：**admin / admin**

> ⚠️ 首次登录请立即修改默认密码！

---

## 🔗 详细文档

> **完整配置说明、功能详解、路由策略、排错指南请查看 [GitHub Wiki](https://github.com/wabisabi926/faststrm/wiki)**

| 主题 | 说明 |
|------|------|
| [🚀 快速开始](https://github.com/wabisabi926/faststrm/wiki/快速开始) | 首次使用全流程 |
| [⚙️ 配置说明](https://github.com/wabisabi926/faststrm/wiki/配置说明) | settings.json 全字段参考 |
| [🔀 STRM 路由策略](https://github.com/wabisabi926/faststrm/wiki/STRM-路由策略) | 302 vs 代理决策逻辑 |
| [🗑️ 删除同步](https://github.com/wabisabi926/faststrm/wiki/删除同步) | Emby 删除事件自动清理 |
| [📝 版本日志](https://github.com/wabisabi926/faststrm/wiki/版本更新日志) | 历史版本更新记录 |

---

## 📝 最新版本 (v0.8.5)

- **� P0 安全修复**：修复 `/api/strmCleanup/*` 认证绕过漏洞，middleware 路径匹配从 `startsWith("/api/strm")` 改为精确匹配 `=== "/api/strm"`
- **🔒 JWT 密钥安全**：移除硬编码默认密钥，改为三级解析（环境变量 → 持久化文件 → 自动生成随机密钥），生产环境拒绝默认密钥启动
- **🚀 服务自启动**：定时任务调度器和 Telegram 轮询在服务启动时自动初始化（layout.tsx），重启后无需手动触发
- **🔧 TG Bot 优化**：`/scan`、`/cleanup` 命令改为直接调用服务层 `runReconcile()`/`runScan()`，不再通过 HTTP 绕过认证；Webhook 支持 `secret_token` 验证
- **📦 依赖升级**：Next.js 15.4.8 → 15.5.23，axios 等安全漏洞修复

查看完整变更：[GitHub Releases](https://github.com/wabisabi926/faststrm/releases)

---

## 📄 许可证

本项目采用 [MIT License](LICENSE) 许可证。

## ⚠️ 免责声明

本项目仅供学习和研究使用。请确保你遵守相关的法律法规和服务条款。
