# FastStrm 代码审查报告

- 审查日期：2026-10-23（Asia/Shanghai）
- 项目：[wabisabi926/faststrm](https://github.com/wabisabi926/faststrm)
- 分支与版本：`main` / `v1.5.6`
- 审查提交：[`c415ee726caa3e03e964ccd68f50c5e5aaca006e`](https://github.com/wabisabi926/faststrm/tree/c415ee726caa3e03e964ccd68f50c5e5aaca006e)
- 方法：源码审查、现有自动化测试、隔离环境中的最小复现；未修改产品实现，未连接真实网盘或媒体服务。

## 审查结论

项目具备适合个人 NAS 的完整结构：115 事件监控、STRM 生成、Emby 反代、清理与通知由单个 Go 服务承载，React 前端嵌入二进制。托管索引、指纹校验、回收站和不完整云端清单保护，为删除操作提供了多层防护。当前提交的现有 Go 短测试（含 race detector）、前端 85 项测试以及 Linux 后端、前端构建均通过。

本轮补充验证确认 **3 项问题：1 项 P1、2 项 P2**。优先修复 Emby 认证头向外部 STRM 源外发的问题，再处理路径前缀匹配和 SSE 并发访问。原有测试没有覆盖这些场景。

本报告的 **P1** 表示应优先修复的安全问题；**P2** 表示应纳入近期修复的功能或可靠性问题。分级用于工程优先级，不代表正式 CVSS 评分。

| 编号 | 级别 | 问题 | 验证状态 |
| --- | --- | --- | --- |
| F1 | P1 | 解析外部 STRM 重定向时外发 Emby 认证头 | 使用真实解析函数和自定义 HTTP Transport 复现，并验证流代理对照路径 |
| F2 | P2 | SQL LIKE 将目录名中的 `_`、`%` 当通配符，误改、误删其他子树索引 | 分别调用真实 Go 更新、删除函数，2 项补充回归均暴露缺陷 |
| F3 | P2 | SSE 订阅表在解锁后读取长度，发生数据竞争 | 4 个 goroutine 各执行 100 次订阅/退订，race detector 实际检出 |

## 1. 逻辑与功能正确性

### F2：目录前缀匹配没有转义 LIKE 元字符，影响其他子树索引

**位置：** [`filepath_repo.go:158–165`](https://github.com/wabisabi926/faststrm/blob/c415ee726caa3e03e964ccd68f50c5e5aaca006e/internal/service/db/filepath_repo.go#L158-L165)、[`filepath_repo.go:184–195`](https://github.com/wabisabi926/faststrm/blob/c415ee726caa3e03e964ccd68f50c5e5aaca006e/internal/service/db/filepath_repo.go#L184-L195)。folders 表的对应前缀更新、删除也采用相同写法，见 [`filepath_repo.go:416–443`](https://github.com/wabisabi926/faststrm/blob/c415ee726caa3e03e964ccd68f50c5e5aaca006e/internal/service/db/filepath_repo.go#L416-L443)。

**原因：** `UpdatePathPrefixBatch` 将真实目录路径直接拼成 `oldP + "/%"`，再用于 `path LIKE ?`。参数化 SQL 不会转义 LIKE 语义；目录名中的 `_` 匹配任意一个字符，`%` 匹配任意长度字符串。`DeleteByPathPrefix` 使用相同模式。

**触发条件：** 同一账号下存在带 `_` 或 `%` 的目录，同时存在可被该模式匹配的其他目录。例如数据库包含：

```text
movies/a_b/one.mkv
movies/axb/two.mkv
```

本轮对临时数据库调用真实 Go 函数，分别验证了以下结果：

1. `UpdatePathPrefixBatch(db, "acc", "movies/a_b", "movies/new")` 把两条记录都改为 `movies/new/...`；第二条应保持 `movies/axb/two.mkv`。
2. `DeleteByPathPrefix(db, "acc", "movies/a_b")` 同时删除了 `movies/axb/two.mkv` 的索引；第二条应保留。

两项补充回归均以“其他子树应保持不变”为断言，实际失败；另用 SQLite 原样 SQL 得到相同更新结果。

**影响：** 正常目录重命名、移动、移出映射或其他按前缀清理的操作，可能损坏其他子树的 files/folders 索引，进而影响旧路径反查和后续同步。这里确认的是**数据库索引误改、误删**，没有证据表明该 SQL 会直接删除其他子树的实体文件。

**修复建议：** 统一字面路径前缀匹配函数，转义 `\`、`%`、`_`，并显式设置 `ESCAPE`；也可使用语义明确的字面字符串前缀比较。更新、删除、快照和统计查询应使用同一规则。补充上述双目录用例，并分别覆盖 files 与 folders。

### 生命周期行为与历史问题复核

本次提交已修复或移除了旧版本报告中的五类问题。下表是对**当前源码**的核对，不沿用旧提交的缺陷结论。

| 旧问题 | 当前实现与结论 |
| --- | --- |
| 查询参数令牌未纳入播放缓存 | [`computeHeaderHash/authCacheHash`](https://github.com/wabisabi926/faststrm/blob/c415ee726caa3e03e964ccd68f50c5e5aaca006e/internal/service/embyproxy/proxy.go#L1927-L1963) 纳入提取出的认证身份；[`HandleMediaStream`](https://github.com/wabisabi926/faststrm/blob/c415ee726caa3e03e964ccd68f50c5e5aaca006e/internal/service/embyproxy/proxy.go#L1248-L1281) 禁止匿名请求复用受保护缓存。旧匿名缓存问题不再作为当前发现。 |
| 并发初始化覆盖刚创建的管理员 | [`InitializeAdmin`](https://github.com/wabisabi926/faststrm/blob/c415ee726caa3e03e964ccd68f50c5e5aaca006e/internal/config/config.go#L211-L235) 在同一写锁内检查初始化状态，先持久化、再替换内存。 |
| rename/move 后丢失托管归属 | 目录及单文件分支加入托管路径迁移；例如 [`event_rename.go:172`](https://github.com/wabisabi926/faststrm/blob/c415ee726caa3e03e964ccd68f50c5e5aaca006e/internal/service/monitor/event_rename.go#L172)、[`event_move.go:535`](https://github.com/wabisabi926/faststrm/blob/c415ee726caa3e03e964ccd68f50c5e5aaca006e/internal/service/monitor/event_move.go#L535)，并有生命周期回归用例。 |
| 文件名模板与改名、清理不一致 | 产品已下线模板；[`SettingsStore`](https://github.com/wabisabi926/faststrm/blob/c415ee726caa3e03e964ccd68f50c5e5aaca006e/internal/service/store/settings.go#L167-L177) 对再次写回的旧字段也清空，更新和保存入口同样清洗。应理解为“移除模板功能”，不能描述为继续支持完整模板生命周期。 |
| AccountStore 向外返回共享可变指针 | [`cloneAccountInfo/Get/List`](https://github.com/wabisabi926/faststrm/blob/c415ee726caa3e03e964ccd68f50c5e5aaca006e/internal/service/store/account.go#L212-L247) 返回快照并单独复制 `CookieValid`；Upsert 也复制输入。 |

## 2. 设计与架构

**优点。** handler、service、store、db 与通用 `pkg/strmutil` 职责相对明确；嵌入 SPA 与 SQLite 降低部署复杂度。播放链路将直链解析、缓存、302 路由和 Range 代理分别建模。清理链路对托管归属、文件指纹、扫描凭据和回收站进行约束，符合媒体库同步工具应有的保守删除原则。

**主要不足。** create、move、rename、recreate 在不同分支分别维护文件系统、files、folders、managed_strms 和关联文件，跨模块一致性需要调用方手工保证。F2 表明同一类路径规则尚未统一；F1 则说明媒体请求头与 Emby 认证请求头的信任边界仍需明确划分。

**失败恢复风险。** 目录已经完成 `os.Rename` 后才迁移托管索引；[`migrateManagedPaths`](https://github.com/wabisabi926/faststrm/blob/c415ee726caa3e03e964ccd68f50c5e5aaca006e/internal/service/monitor/event_helpers.go#L336-L350) 迁移失败时仅记录警告，没有向调用者返回错误，数据库事务无法补偿已经发生的文件移动。这是本轮源码审查识别的架构风险，未作为新增动态复现缺陷计数。建议明确移动前预检、迁移失败补偿和重启后对账策略，并提供可重试的统一生命周期操作。

## 3. 代码质量与可读性

通用文件名、路径与原子写入逻辑已收敛到 `pkg/strmutil`，账号快照的实现与注释也比较清楚。安全删除和云端失败保护有针对性的测试，能够帮助维护关键不变量。

部分事件处理函数仍然庞大：[`handleRenameEvent`](https://github.com/wabisabi926/faststrm/blob/c415ee726caa3e03e964ccd68f50c5e5aaca006e/internal/service/monitor/event_rename.go#L18-L34) 标注复杂度 90，嵌套分支中重复推导路径、同步内容、更新数据库和发送通知。建议先抽取共享操作和状态转换，再增加功能分支。注释中大量参考项目行号与历史 `P0-x` 标记，宜逐步替换为当前规则及其原因。

### F3：SSE 订阅表长度在锁外读取，发生数据竞争

**位置：** [`server.go:154–157`](https://github.com/wabisabi926/faststrm/blob/c415ee726caa3e03e964ccd68f50c5e5aaca006e/internal/service/sse/server.go#L154-L157)、[`server.go:179–185`](https://github.com/wabisabi926/faststrm/blob/c415ee726caa3e03e964ccd68f50c5e5aaca006e/internal/service/sse/server.go#L179-L185)。

**原因：** `Subscribe` 和 `Unsubscribe` 在持锁期间修改 `s.subscribers`，释放锁后，日志参数中的 `len(s.subscribers)` 又直接读取同一个 map。其他连接可以同时新增或删除订阅。日志级别关闭也不能保护参数求值时发生的读取。

**触发与验证：** 补充测试创建一个独立 SSE Server，4 个 goroutine 同时启动，每个循环 100 次 `Subscribe` / `Unsubscribe`。启用 `go test -race` 后，实际报告 `WARNING: DATA RACE`，对应锁外长度读取与订阅表写入。

**影响：** 多个浏览器标签页或客户端并发连接、重连、断开时，会执行未同步的 map 读写。本轮确认了数据竞争，**没有观察或证明进程崩溃**。

**修复建议：** 在持有同一把锁时保存 `total := len(s.subscribers)`，解锁后只把局部变量传给日志。将并发订阅/退订用例加入长期 race 回归。

## 4. 安全性

### F1：外部 STRM 重定向解析会外发 Emby 认证头

**位置：** [`buildForwardHeaders`](https://github.com/wabisabi926/faststrm/blob/c415ee726caa3e03e964ccd68f50c5e5aaca006e/internal/service/embyproxy/proxy.go#L1984-L1997)、[`resolveRedirectChain:1502–1537`](https://github.com/wabisabi926/faststrm/blob/c415ee726caa3e03e964ccd68f50c5e5aaca006e/internal/service/embyproxy/proxy.go#L1502-L1537)。

**原因：** `buildForwardHeaders` 仅过滤 hop-by-hop 头与 Host，将客户端的 `X-Emby-Token`、`Authorization`、`Cookie` 等原样纳入转发集合。`resolveRedirectChain` 随后把这些头用于访问外部 STRM URL 的 HEAD 请求，并自动跟随重定向。媒体源提供方与 Emby 认证服务拥有不同的信任边界，不应共享这些凭据。

**触发条件：** Emby 媒体源使用外部 HTTP/HTTPS STRM 地址，播放请求带有上述认证头，并走到外部重定向解析分支。正常启用的内置 `/api/strm` 进程内解析有提前返回路径，本项复现覆盖的是外部源，不能泛化为所有播放请求都会泄漏。

**本轮验证：** 使用真实 `resolveRedirectChain`、真实 `http.Client` 重定向行为和自定义 Transport 捕获请求，全程使用虚构域名与假凭据，不进行网络访问：

```text
Emby 播放请求
  → HEAD http://external-strm-origin.invalid/source
  → 302 http://different-cdn.invalid/movie.mkv
```

捕获结果：

- 外部首跳收到原始 `X-Emby-Token`、`Authorization`、`Cookie`，以及加入的 `X-Emby-UserId`。
- 跳转到不同主机后，Go 正确移除了 Cookie 和 Authorization，但 `X-Emby-Token` 仍被发送。
- 对照测试调用真实 `proxyStreamToStrm`：未向外部目标发送上述三种认证头，Range 正常透传。该流代理路径不属于本项缺陷。

两项行为验证测试均通过，表示成功确认了认证头外发和对照路径隔离的实际行为。

**影响：** 外部 STRM 服务或重定向链目标可以获得客户端的 Emby 认证凭据；凭据尚有效且目标可访问对应 Emby 服务时，可能被用于该会话授权范围内的访问。本轮没有使用真实凭据，也没有验证账户接管或扩大权限。

**修复建议：** 对媒体源 HEAD 请求采用最小请求头白名单；Emby 会话 token、Cookie、Authorization、用户身份信息仅发送给明确的 Emby 上游。外部源如需独立鉴权，应使用独立配置。重定向每一跳均重新应用目标域与凭据规则，不能只依赖 Go 对标准敏感头的默认处理。回归需覆盖首跳、跨域跳转和正常媒体 Range 行为。

### 已有安全控制

当前提交对管理员初始化、JWT、认证缓存、账号快照、托管归属和删除指纹已有明确保护。这些措施值得保留；F1 需要在出站 HTTP 请求的边界补齐。公开 STRM 播放入口是当前明确的产品设计，本报告没有将其本身认定为新增漏洞。

## 5. 测试与验证

环境：Linux amd64；Go 1.25.0；Node 25.8.2。源码、依赖复用及复现均位于临时副本；危险文件操作仅涉及临时目录和临时 SQLite。

| 检查 | 本轮结果 | 范围说明 |
| --- | --- | --- |
| `go test ./... -race -short -count=1 -timeout=4m` | 全部通过 | 使用项目现有测试；允许本机模拟 HTTP 服务监听后完成。`-short` 会跳过部分长用例。 |
| Linux 后端构建 | 通过 | 验证当前源码可构建为 Linux 二进制。 |
| `NODE_OPTIONS=--no-experimental-webstorage npm test -- --reporter=dot` | 通过，11 个文件、85 项测试 | Node 25 使用兼容参数；没有把环境差异认定为产品缺陷。 |
| `npm run build` | 通过 | 包含 TypeScript 检查和 Vite 生产构建。 |
| 外部认证头行为验证 | 2 项通过 | 真实重定向解析确认外发；真实流代理对照确认认证头不外发。 |
| 路径前缀补充回归 | 2 项暴露缺陷 | 真实 Go 更新、删除函数均错误影响相邻子树索引。 |
| SSE 并发补充回归 | race detector 检出缺陷 | 4 个 goroutine 各 100 次订阅/退订，无真实服务依赖。 |

前端验证在当前源码副本中复用已有 node_modules；已比较 package.json 和 package-lock.json，差异仅为项目版本号 `1.5.5 → 1.5.6`，依赖内容一致。Vite 给出主 JS 大于 500 KB 的体积提示，构建仍成功；可以后续按页面拆包改善首次加载。

项目 CI 已包含 Go race/short 测试、覆盖率、lint、Vitest、前端构建及多平台编译，但本报告只对上表实际执行项作结论。本轮**未执行**真实 115、Emby、Telegram 联调、浏览器 E2E、Windows 交叉编译或运行验证、Docker/fNOS 部署、性能压测及完整依赖漏洞扫描。

建议将本轮认证边界、字面目录前缀和并发订阅用例加入仓库；生命周期测试还应覆盖文件系统移动成功而索引迁移失败时的恢复行为。优先修复顺序为 **F1 → F2 → F3**。

## 修复方案（2026-10-26 复核）

**修复状态**：F1、F2、F3 均已修复并推送到 `origin/main`，并入 v1.5.6 补充修复（提交 `e40d0b1`、`65c5743`、`9499a53`）；真机结果见 `docs/dev/archive/handoff-2026-10-26.md`。v1.5.6 tag 尚未创建。

复核确认 F1、F2、F3 在 `c415ee7` 上均成立，按三个独立提交实施。补充两点：

- F2 另有两处只读前缀查询未转义：`CountFilesByPrefix`（`filepath_repo.go:221`）与 `ListSnapshotByAccount`（`filepath_repo.go:255`）。前者导致计数偏大，后者会把相邻子树 pickcode 纳入快照，使孤儿清理更保守；不会多删文件。
- `db` 包已有 `escapeLike`（`managed_strms.go:290`）并已配合 `ESCAPE '\'` 使用，修复时上提复用，不再另写转义。
- `/api/strm` 不校验 Emby 认证头（`internal/handler/strm.go:110`），因此 F1 收敛外发头不影响自身 STRM 回退路径。

### 提交 1 · F1 收敛媒体源外发请求头

- `resolveRedirectChain` 改用最小请求头白名单：仅 `User-Agent`、`Accept`；删除 `X-Emby-UserId` 注入与全量 `buildForwardHeaders`。
- `followRedirectClient.CheckRedirect` 在跨主机跳转时清除 `Authorization`、`Cookie`、`X-Emby-Token`、`X-MediaBrowser-Token`、`X-Emby-UserId`，作为防止未来回归的兜底；同主机跳转不清，避免影响 Emby API。
- 回归用例：首跳 302 → 跨主机，断言两跳均收不到三类凭据且 UA 保留；跨主机清理函数单测；现有 `/api/strm` 与 `proxyStreamToStrm` 用例保持通过。
- 若外部源确需独立鉴权，后续另加显式配置，默认不透传 Emby 凭据。

### 提交 2 · F2 字面路径前缀统一

- 新建 `internal/service/db/like.go`：迁入 `escapeLike`，新增 `literalPrefixPattern(p) = escapeLike(p) + "/%"`。
- `filepath_repo.go` 六处统一 `LIKE ? ESCAPE '\'`：`UpdatePathPrefixBatch`、`DeleteByPathPrefix`（files + folders）、`UpdateFolderPathPrefixBatch`、`DeleteFolderByPathPrefix`、`CountFilesByPrefix`、`ListSnapshotByAccount`（并补 `normalizeDbPath`，与存储路径一致）。
- `substr` 偏移继续使用原始 `oldP` 的 rune 长度，只转义 LIKE 参数。
- 回归用例：`movies/a_b` 与 `movies/axb`、`movies/a%b` 与 `movies/axxb`、含 `\` 目录与相邻同名目录，覆盖 update/delete/count/snapshot 及 files/folders 两表，断言相邻子树零变化。
- 历史错误行无需数据迁移，后续同步或事件会自然覆盖。

### 提交 3 · F3 SSE 锁外读取

- `Subscribe`/`Unsubscribe` 在持锁期间保存 `total := len(s.subscribers)`，日志使用局部变量。
- 新增 4 goroutine × 100 次并发订阅/退订用例，由 Linux CI 的 `-race` 验证。

### 验收与范围

- 每提交跑 `gofmt`、`go build ./...`、相关包 `go test -short -count=1`；最后跑全量 `go test ./... -short -count=1`。
- `-race` 需 Linux/CGO 环境，Windows 本地无法执行，交由 CI。
- 本轮不做：`migrateManagedPaths` 吞错补偿、`handleRenameEvent` 拆解、前端拆包。
