<div align="center">
  <img src="https://raw.githubusercontent.com/wabisabi926/faststrm/refs/heads/main/frontend/public/logo.png" alt="Fast Strm Logo" width="200" height="200">
</div>

# Fast Strm

**🎉 FastStrm 是开源 STRM 生成工具，基于 openStrm、MoviePilot‑Plugins 魔改开发。新增 115 生活事件轮询能力，实时监听网盘文件新增、删除变动，自动同步生成 / 清理 STRM 文件，适配 Emby、Jellyfin、Kodi，支持通知推送，可 Docker 快速部署，实现网盘资源和本地媒体库实时联动。**

---

## 🚀 核心特性

- 开源自由，支持批量生成 `.strm` 文件
- 基于 115 目录树生成，支持自定义前缀配合媒体服务器使用
- 115 生活事件实时监控（增量同步 + 全量同步）
- **STRM 智能路由**：302 直连优先 + 静默降级代理，Infuse/VidHub/SenPlayer 自动强制代理
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
docker run -d -p 3000:3000 -v ./data:/app/data wabisabi926/faststrm:latest
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

## 📝 最新版本 (v0.8.1)

- **Emby 通知修复**：修复设置无法保存和测试连接失败，新增局部更新接口和 9 种错误分类
- **TG 通知优化**：回填从秒级到毫秒级，隐藏空卡片，明确轮询 vs Webhook 推荐方案
- **路径映射增强**：Emby 路径增加本地选择器，115 网盘路径增加云盘目录选择器，账号改为下拉
- **文档重构**：精简 Readme，详细内容迁移至 GitHub Wiki（16 个页面）

查看完整变更：[GitHub Releases](https://github.com/wabisabi926/faststrm/releases)

---

## 📄 许可证

本项目采用 [MIT License](LICENSE) 许可证。

## ⚠️ 免责声明

本项目仅供学习和研究使用。请确保你遵守相关的法律法规和服务条款。
