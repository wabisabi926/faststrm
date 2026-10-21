# 设置项默认值 + Telegram 字段评估

> 调研日期：2026-10-21　基线：`8f36f5a`
> 范围：`pages/settings/BasicSettingsTab.tsx`、`pages/settings.tsx`、`handler/emby.go`、`service/store/settings.go`、`pages/tg-notify.tsx`、`service/notify/commands.go`
> 状态：**待确认，未改代码**

---

## 一、四个字段的现状（先摆事实）

四个字段现在有**三种不同的「留空」语义**，这就是你觉得别扭的根源。

| 字段 | 默认值 | 留空时前端发送 | 后端 handler（`emby.go`） | 读取时兜底（`store/settings.go`） | 实际效果 |
|---|---|---|---|---|---|
| User-Agent | 115 iOS UA | `""` | `:603` `*string`，空串**会应用** | `:234` `==""` → 默认 | ✅ 留空 = 默认（但 settings.json 存空串） |
| STRM 文件扩展名 | 17 项（mp4/mkv/…/iso） | `[]` | `:609` `len>0` → **忽略空数组** | `:166` `len==0` → 默认 | ❌ **留空 = 静默不变**（点了保存像没反应） |
| 附带下载的元数据扩展名 | 6 项（srt/ass/sub/nfo/jpg/png） | `[]` | `:612` `len>0` → **忽略空数组** | `:244` `len==0` → 默认 | ❌ **留空 = 静默不变** |
| 强制代理 UA 标识 | 4 项（Infuse/VidHub/SenPlayer/SenPlayerHD） | `[]` | `:695` `!=nil` → **应用空** | `:252` 明确**不兜底** | ✅ 留空 = 全部直连（功能关闭） |
| （对照）强制代理格式 | 5 项（iso/bdmv/bdav/dvd/bluray） | `[]` | `:698` `!=nil` → 应用空 | `:230` `len==0` → 默认 | ✅ 留空 = 默认（文档已这么写） |

**三个问题点**：

1. **两个扩展名字段清空后保存 = 什么都没发生**，还弹"设置已保存"。这是明确的 bug（静默 no-op），不是设计选择。
2. 同样是"列表类字段"，`forceProxyFormats` 能留空恢复默认，`strmExtensions` 不能 —— 同一个页面里两套规则。
3. `forceProxyUaTokens` 的"留空 = 关闭"是对的，但和上面两个放一起看，用户分不清"留空"到底是什么意思。

---

## 二、建议：三个做，一个不做

### 2.1 做（留空 = 恢复默认）

| 字段 | 为什么可以 |
|---|---|
| User-Agent | 空 UA 请求 115 会被拒，**空是无意义的非法值** |
| STRM 文件扩展名 | 空列表 = 什么视频都不生成，**空是无意义的非法值** |
| 元数据扩展名 | 想"不下载元数据"有专门的开关 `autoDownloadMetadata`，不需要靠清空列表表达，**空是冗余表达** |

### 2.2 不做（留空 = 关闭功能）

| 字段 | 为什么不能 |
|---|---|
| 强制代理 UA 标识 | **空是合法且有意义的配置**，代表"我不要强制代理，全部走 302 直连"。`store/settings.go:252` 的注释写得很清楚：「用户主动清空表示『全部直连』，回填默认值会让这个选择失效」。 |

如果把这一项也改成"留空恢复默认"，结果是：**用户永远关不掉强制代理**——清空保存后又变回 Infuse/VidHub，而 Infuse 用户恰恰最需要关掉它（否则带宽全压在 NAS 上）。

> 注：真想关"按格式代理"有独立开关（`disableSeekFormatProxy`），所以 `forceProxyFormats` 留空=默认没有副作用；但 UA 白名单没有独立开关，所以不能动。

### 2.3 为什么不再加「恢复默认」按钮

考虑过在每个字段右侧加一个「恢复默认」按钮，结论是**不加**：

- 留空已经能表达同一件事，加按钮是同一功能的两个入口。
- 加按钮要暴露默认值接口（`GET /api/settings/defaults`），否则前端得再硬编码一份默认值——而这正是 P2-5 记的「默认值硬编码 3 份」问题，不该再增加。
- 你的项目一贯做减法，这个按钮属于"为了显式而显式"。

**替代方案（成本 0）**：把说明文字写清楚 + 保存后 toast 明确告知。

---

## 三、实施细节（约 1.5 小时）

### 3.1 后端：把"留空"变成显式语义

`internal/handler/emby.go` 三处：

```go
// :603 User-Agent
if body.UserAgent != nil {
    ua := strings.TrimSpace(*body.UserAgent)
    if ua == "" {
        ua = model.DefaultUserAgent // 留空 = 恢复默认
    }
    settings.UserAgent = ua
}

// :609 STRM 扩展名
if body.StrmExtensions != nil {
    if len(body.StrmExtensions) == 0 {
        settings.StrmExtensions = append([]string{}, model.DefaultStrmExtensions...)
    } else {
        settings.StrmExtensions = body.StrmExtensions
    }
}

// :612 元数据扩展名（同上，用 model.DefaultDownloadExtensions）
```

**为什么在 handler 里显式赋值，而不是靠 `ReadSettings` 兜底**：

- 现在的兜底发生在"读取"时，`Update()` 返回的是内存里的 `out`（`store/settings.go:59-74`），里面还是空数组 → POST 响应里的 `saved` 是空值 → 前端拿不到"恢复成了什么"。
- 在 handler 里赋值后：`settings.json` 不出现空数组、响应里的 `saved` 就是真实生效值、前端可以直接用它做提示。

### 3.2 前端：文案统一

`BasicSettingsTab.tsx`：

| 字段 | 现在 | 改为 |
|---|---|---|
| User-Agent | `placeholder="Mozilla/5.0 ..."` + "访问 115 API 时使用的 UA" | placeholder 保留；说明加一句「**留空使用内置默认 UA**（115 iOS 客户端）」 |
| STRM 文件扩展名 | placeholder `.mkv, .mp4, .mp3` + "用逗号分隔…" | 说明加「**留空使用内置默认 17 项**（mp4/mkv/avi/…/ts/m2ts/iso）」 |
| 元数据扩展名 | placeholder `.srt, .ass, .sub, .nfo` | 说明加「**留空使用内置默认 6 项**（srt/ass/sub/nfo/jpg/png）」 |
| 强制代理 UA 标识 | 已有「留空则全部 302 直链」 | **保持**，把「留空则全部 302 直链」加粗，避免和上面三个混淆 |

现在的 placeholder（`.mkv, .mp4, .mp3`）容易让人以为默认只有 3 项，是个小误导，建议改成不带默认值暗示的写法（如 `mkv, mp4, iso, ts`）或干脆保留但说明里给出完整默认。

### 3.3 保存后反馈

`settings.tsx` 的 `onSave` 里，用响应里的 `saved` 做提示：

```
扩展名输入为空 → toast: "已保存，STRM 扩展名恢复为默认 17 项"
```

比现在的"设置已保存"更有信息量，也让"我清空了它自己又回来"这件事变得可预期。

---

## 四、授权用户是否自动读取 chatId（问题 2 前半）

### 4.1 现状

两个字段是**完全不同的东西**：

| | 聊天 ID（`telegram.chatId`） | 授权用户（`telegram.allowedUsers`） |
|---|---|---|
| 存什么 | 通知发到哪个会话 | 谁有权用 Bot |
| 取值 | 个人：正数；群/频道：**负数**（`-100…`） | 永远是正数（发送者 `from.id`） |
| 用途 | 发送目标 + 按钮回调的会话白名单（`commands.go:501`） | 用户级 ACL（`commands.go:87`） |
| 空值 | 未配置 = 不限制会话 | 空列表 = **允许所有用户** |

### 4.2 不能自动写入，原因是群

`IsUserAllowed`（`commands.go:87-97`）：

```go
if len(allowedUsers) == 0 { return true }   // 空 = 放行
for _, id := range allowedUsers { if id == userID { return true } }
return false                                 // 不在列表 = 拒绝
```

如果把 `chatId` 自动塞进 `allowedUsers`：

- **个人聊天**：`chatId == userId` → 正常工作，符合直觉。
- **群组**：`chatId` 是 `-100…` → 塞进去后列表非空，但**没有任何发送者会匹配这个负数** → `IsUserAllowed` 对所有人返回 false → **Bot 直接全锁死**，比现在"允许所有"还糟，而且用户完全不知道为什么。

所以"默认自动读取"是个**会在群场景炸掉**的设计。

### 4.3 建议：条件提示 + 一键添加（不静默写）

在「授权用户」区块，当满足下面三个条件时，显示一条提示条：

```
检测到通知会话 ID 123456789，是否同时作为授权用户？  [添加]
```

条件：`allowedUsers.length === 0` && `chatId` 是纯数字 && `chatId > 0`

点「添加」走已有的 `POST /api/notify/users`，不新增接口。

**好处**：

- 群场景天然不触发（chatId 是负数），不会炸。
- 单用户场景从"去日志里找 User ID"变成"点一下"。
- 不静默改用户配置，符合"用户知情"。

### 4.4 「从日志查看 User ID」这句话现在是**假的**

界面写的是「用户 ID 可向 Bot 发送任意消息后从日志查看」。实测查证：

- `commands.go:118` 打印 from.id 用的是 **`Debugf`**；
- 而日志级别在 `server.go:38` **硬编码 `zapcore.InfoLevel`**，且没有配置项可调。

→ **Debug 行永远不会被写出**，日志里根本找不到 User ID。加上 `allowedUsers` 为空时会直接放行（走不到 `:121` 的拒绝分支），用户在欢迎消息里也拿不到 ID。

所以这条指引目前**完全走不通**。

**建议改为「聊天里直接回 ID」（5 分钟，替代原来的按钮方案）**

`allowedUsers` 为空时的欢迎消息里追加一行：

```go
reply := fmt.Sprintf("👋 你好 %s！\n\n..."+
    "\n\n你的 User ID: <code>%d</code>", username, userID)
```

这样两种状态都能拿到 ID：

| allowedUsers | 发任意消息后 Bot 回复 | 能拿到 ID 吗 |
|---|---|---|
| 空（允许所有） | 欢迎菜单 + **你的 User ID: xxx** | ✅ |
| 非空且未授权 | 访问被拒绝 + **你的 User ID: xxx**（`:122` 已有） | ✅ |

**为什么不加「从最近消息获取」按钮**

- 需要额外存"最近一条 update 的 from.id"这个隐藏状态 + 新开一个查询接口，40 分钟；
- 而聊天里回 ID 是**零状态、零接口、5 行代码**，且覆盖群场景（群成员各发一条就能各自拿到 ID）；
- 按钮解决的是"填表时不用切窗口"，但用户本来就要先给 Bot 发消息验证能否连通，顺手就看到了 ID。

---

## 五、「聊天 ID」这个翻译错了吗（问题 2 后半）

### 5.1 没错

Telegram Bot API 的参数就叫 **`chat_id`**，官方文档对它的定义是「Unique identifier for the target chat or username of the target channel」，中文社区一致译作「聊天 ID」或「会话 ID」。

它和 `user_id`（`from.id`）是**两个不同的字段**，不是同一个词的两种译法：

- `chat_id` = 消息发到哪（可以是群、频道）
- `user_id` = 谁发的

所以「聊天 ID」对应 `chatId` 是准确的翻译，**改成「用户 ID」反而是错的**——群组场景下它不是用户 ID，会让用户以为填群 ID 能当用户 ID 用。

### 5.2 但你的困惑是真实的

因为**在个人聊天里 `chatId == userId`**，两个数字长得一样，用户自然会觉得"这不就是我的用户 ID 吗"。问题不在翻译，在于**没说清什么时候不一样**。

### 5.3 建议（二选一）

**方案 A（推荐，成本 5 分钟）**：标签保留「聊天 ID」，把说明改清楚

| 现在 | 改为 |
|---|---|
| `聊天 ID` / placeholder「你的 Chat ID（个人/群）」/ 说明「向 Bot 发消息后查看日志获取」 | `聊天 ID（chat_id）` / placeholder「个人填你自己的 User ID；群填 -100 开头的群 ID」/ 说明「通知发到这个会话；个人聊天时与你的用户 ID 相同」 |

**方案 B**：改成「通知会话 ID（chat_id）」

更技术准确，但"会话 ID"对普通用户比"聊天 ID"更陌生。不推荐。

---

## 六、工作量与建议顺序

| 顺序 | 内容 | 预估 | 风险 |
|---|---|---|---|
| 1 | 后端三处「留空 = 默认」（`emby.go:603/609/612`） | 20min | 低，纯补语义 |
| 2 | 前端三处说明文字 + 强制代理 UA 文案加粗 | 20min | 无 |
| 3 | 保存后 toast 用 `saved` 提示恢复了几项 | 20min | 低 |
| 4 | 聊天 ID 标签/说明改写（方案 A） | 10min | 无 |
| 5 | 欢迎消息里回「你的 User ID」（`commands.go:127`） | 5min | 无 |
| 6 | 授权用户「一键添加 chatId」提示条 | 40min | 低，需判正数 |
| 7 | 前端 `npx tsc -b && npm run build` 同步 spa | 10min | - |

**1-6 合计约 2 小时**，做完后：

- 四个字段的"留空"语义统一且可预期（三个=默认，一个=关闭且文案写明）
- 两个扩展名字段不再静默 no-op
- 授权用户不必再去找 User ID（聊天里直接有，或一键添加）
- 「从日志查看 User ID」这句假指引被彻底去掉

---

## 七、明确不做

| 不做 | 理由 |
|---|---|
| 强制代理 UA 也做「留空=默认」 | 会让"关闭强制代理"这个合法选择永久失效 |
| 加「恢复默认」按钮 | 与留空重复，且要新增默认值接口，违背减法原则 |
| 授权用户自动写入 chatId | 群组场景会把 Bot 全锁死 |
| 把「聊天 ID」改名为「用户 ID」 | 群组场景下语义错误 |
| 合并 chatId 与 allowedUsers 为一个字段 | 一个管"发到哪"，一个管"谁能用"，合并后群组场景无法表达 |
