# 发布

## 版本一致性

以下必须同时更新：

- `cmd/server/main.go` 的 `version`
- `frontend/package.json` 的 `version`
- `.changes/vX.Y.Z.md`
- `wiki_drafts/版本更新日志.md`（live Wiki 为准）

## 发布说明

采用「亮点速览 + emoji 分区 + 升级说明」，只写最终行为，不写反复修改过程。

## 打包

1. 前端 `npm run build` 更新 embed 产物。
2. Windows 测试包放 `dist/`。
3. 飞牛包使用 `FASTSTRM_VERSION` 覆盖，生成 amd64/arm64 fpk。
4. 本地包与最终 HEAD 必须一致；改完文案后要重打。

## tag 与 CI

- annotated tag：`git tag -a vX.Y.Z -m "FastStrm vX.Y.Z"`
- 推送 tag 触发 Release workflow（CI 校验 + 多平台资产）。
- **tag/Release 必须得到用户明确同意**；不要自动打 tag。
- 发布后回填 Wiki 更新日志和 Release 说明。

## 验证

- Release 资产数量与平台齐全（Linux/Windows/fnOS/Kodi）。
- 用最终 tag 的二进制做一次隔离启动和 Setup/登录检查。
