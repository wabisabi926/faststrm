<div align="center">
  <img src="https://raw.githubusercontent.com/wabisabi926/faststrm/refs/heads/main/frontend/public/logo.png" alt="Fast Strm Logo" width="200" height="200">
</div>

# Fast Strm

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

## 📝 最新版本 (v0.8.2)

- **� 115 扫码登录**：新增 3 个二维码 API + 前端扫码 Tab，支持 7 种客户端类型，告别 F12 抓 Cookie
- **🔄 账户状态 TG 通知闭环**：新增 `telegram.accountAlerts` 配置块，账号异常/恢复自动推送，与「更新 Cookie」按钮形成闭环
- **🍪 更新 Cookie 对话框**：账号异常时一键扫码刷新 Cookie，无需删除重建账号
- **�� STRM 生成路径修复**：修复全量扫描误加 `../data/` 前缀导致 302 模式 pickcode 反查失败的 P0 漏洞，新增 4 层兜底保护
- **🔀 路由策略配置化**：`forceProxyUaTokens` / `accountProxyConcurrencyLimit` / `redirectCheckTimeoutMs` 迁移到 `settings.json`，Web UI 可改实时生效
- **🐛 admin 登录修复**：修复首次 Docker 部署 admin/admin 无法登录的漏洞，entrypoint 复制默认配置后自动写入 SHA-256+salt 密码哈希
- **🚀 生活监控自动启动**：服务重启后无需手动点启动，懒加载触发，进程级一次性保证
- **🎨 页面排版统一**：设置/TG/Emby 通知页面统一 section 风格，Label 行高优化解决表单拥挤
- **⚙️ 配置逻辑归位**：恢复 docker-entrypoint.sh 原有的 `.config/` → `config/` 复制链路，删除 TS 层冗余代码
- **🧹 移除 .trash 目录**：清理历史遗留临时目录，相关逻辑已迁移至 `removeExtraFiles` 安全机制
- **🌐 界面本地化**：「Dry-run」在所有用户可见界面统一改为「试运行」

查看完整变更：[GitHub Releases](https://github.com/wabisabi926/faststrm/releases)

---

## 📄 许可证

本项目采用 [MIT License](LICENSE) 许可证。

## ⚠️ 免责声明

本项目仅供学习和研究使用。请确保你遵守相关的法律法规和服务条款。
