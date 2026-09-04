import type { Dispatch, SetStateAction } from "react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { FolderOpen, Info } from "lucide-react";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type {
  PathMapping,
  DisplayMonitorState,
  VerifyResult,
} from "./types";

export interface MonitorSettingsTabProps {
  monitorEnabled: boolean;
  setMonitorEnabled: Dispatch<SetStateAction<boolean>>;
  accounts: string[];
  selectedAccounts: string[];
  toggleAccount: (accountName: string) => void;
  pollInterval: number;
  setPollInterval: Dispatch<SetStateAction<number>>;
  eventTypes: {
    create: boolean;
    remove: boolean;
    rename: boolean;
    move: boolean;
  };
  setEventTypes: Dispatch<SetStateAction<{
    create: boolean;
    remove: boolean;
    rename: boolean;
    move: boolean;
  }>>;
  removeEmptyDirs: boolean;
  setRemoveEmptyDirs: Dispatch<SetStateAction<boolean>>;
  minFileSizeMb: string;
  setMinFileSizeMb: Dispatch<SetStateAction<string>>;
  firstPullMode: "latest" | "all" | "last";
  setFirstPullMode: Dispatch<SetStateAction<"latest" | "all" | "last">>;
  moveMediaMode: "recreate" | "local_move";
  setMoveMediaMode: Dispatch<SetStateAction<"recreate" | "local_move">>;
  pathMappings: PathMapping[];
  setPathMappings: Dispatch<SetStateAction<PathMapping[]>>;
  newMappingAccount: string;
  setNewMappingAccount: Dispatch<SetStateAction<string>>;
  newCloudPath: string;
  setNewCloudPath: Dispatch<SetStateAction<string>>;
  newLocalPath: string;
  setNewLocalPath: Dispatch<SetStateAction<string>>;
  openCloudPicker: (rowIndex: number, account?: string) => void;
  openLocalPicker: (rowIndex: number) => void;
  openNewCloudPicker: () => void;
  openNewLocalPicker: () => void;
  addPathMapping: () => void;
  removePathMapping: (index: number) => void;
  verifying: boolean;
  verifyResult: VerifyResult;
  handleVerify: () => Promise<void>;
  displayMonitorStates: DisplayMonitorState;
  handleStopMonitor: (account: string) => Promise<void>;
  handleStartAccount: (account: string) => Promise<void>;
  /** 从 settings.lifeMonitor.accounts 列表里移除一个账号名（删除不存在账号时用）*/
  handleRemoveFromMonitor: (account: string) => Promise<void>;
  saving: boolean;
  onSave: () => Promise<void>;
  handleStartMonitor: () => Promise<void>;
}

export function MonitorSettingsTab(props: MonitorSettingsTabProps) {
  const {
    monitorEnabled,
    setMonitorEnabled,
    accounts,
    selectedAccounts,
    toggleAccount,
    pollInterval,
    setPollInterval,
    eventTypes,
    setEventTypes,
    removeEmptyDirs,
    setRemoveEmptyDirs,
    minFileSizeMb,
    setMinFileSizeMb,
    firstPullMode,
    setFirstPullMode,
    moveMediaMode,
    setMoveMediaMode,
    pathMappings,
    setPathMappings,
    newMappingAccount,
    setNewMappingAccount,
    newCloudPath,
    setNewCloudPath,
    newLocalPath,
    setNewLocalPath,
    openCloudPicker,
    openLocalPicker,
    openNewCloudPicker,
    openNewLocalPicker,
    addPathMapping,
    removePathMapping,
    verifying,
    verifyResult,
    handleVerify,
    displayMonitorStates,
    handleStopMonitor,
    handleStartAccount,
    handleRemoveFromMonitor,
    saving,
    onSave,
    handleStartMonitor,
  } = props;

  return (
    <div className="space-y-6 w-full min-w-0 overflow-hidden">
      <section className="w-full min-w-0 overflow-hidden border rounded-md p-3 sm:p-5 space-y-5">
        <div className="flex items-center justify-between gap-2 flex-wrap">
          <h2 className="text-base font-medium">115 生活事件监控</h2>
          <div className="flex items-center gap-2">
            <Checkbox
              id="monitor-enabled"
              checked={monitorEnabled}
              onCheckedChange={(checked) => setMonitorEnabled(checked === true)}
            />
            <label htmlFor="monitor-enabled" className="text-sm cursor-pointer">
              启用监控
            </label>
          </div>
        </div>
        <p className="text-sm text-muted-foreground">
          监控 115 网盘的文件操作事件（上传、删除、移动、重命名），自动生成或删除本地 STRM 文件
        </p>

        <Alert>
          <Info className="h-4 w-4" />
          <AlertTitle>首次使用或升级后建议先跑一次全量同步</AlertTitle>
          <AlertDescription>
            <p>
              首次使用或升级后，建议执行一次全量同步。全量同步仅扫描任务指定的 115 网盘文件夹，把文件、目录写入数据库。
              数据库记录是移动、重命名、删除事件正常生效的基础；缺少记录会导致旧 STRM 无法正确清理。
            </p>
            <p className="mt-1">
              操作：左侧菜单「任务」→新建任务→保存，点击任务▶运行。
            </p>
          </AlertDescription>
        </Alert>

        <div className={`space-y-4 ${!monitorEnabled ? "opacity-50 pointer-events-none" : ""}`}>
          {/* Account Selection */}
          <div className="space-y-3">
            <Label>监控账号</Label>
            <div className="flex flex-wrap gap-5 p-3 border rounded-md">
              {accounts.length === 0 ? (
                <p className="text-sm text-muted-foreground">暂无可用账号，请先在账号管理中添加 115 账号</p>
              ) : (
                accounts.map(account => (
                  <div key={account} className="flex items-center gap-2">
                    <Checkbox
                      id={`acc-${account}`}
                      checked={selectedAccounts.includes(account)}
                      onCheckedChange={() => toggleAccount(account)}
                    />
                    <label htmlFor={`acc-${account}`} className="text-sm cursor-pointer">
                      {account}
                    </label>
                  </div>
                ))
              )}
            </div>
          </div>

          {/* Poll Interval */}
          <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
            <div className="space-y-3">
              <Label>轮询间隔（秒）</Label>
              <Input
                type="number"
                min="5"
                max="300"
                value={pollInterval}
                onChange={(e) => setPollInterval(parseInt(e.target.value) || 10)}
              />
              <p className="text-xs text-muted-foreground">
                建议 10-30 秒，太频繁可能触发限流（默认 10 秒）
              </p>
            </div>
          </div>

          {/* Event Types */}
          <div className="space-y-3">
            <Label>处理的事件类型</Label>
            <div className="flex flex-wrap gap-5 p-3 border rounded-md">
              <div className="flex items-center gap-2">
                <Checkbox
                  id="evt-create"
                  checked={eventTypes.create}
                  onCheckedChange={(checked) =>
                    setEventTypes(prev => ({ ...prev, create: checked === true }))
                  }
                />
                <label htmlFor="evt-create" className="text-sm cursor-pointer">
                  新建/上传（生成 STRM）
                </label>
              </div>
              <div className="flex items-center gap-2">
                <Checkbox
                  id="evt-remove"
                  checked={eventTypes.remove}
                  onCheckedChange={(checked) =>
                    setEventTypes(prev => ({ ...prev, remove: checked === true }))
                  }
                />
                <label htmlFor="evt-remove" className="text-sm cursor-pointer">
                  删除（移除 STRM）
                </label>
              </div>
              <div className="flex items-center gap-2">
                <Checkbox
                  id="evt-rename"
                  checked={eventTypes.rename}
                  onCheckedChange={(checked) =>
                    setEventTypes(prev => ({ ...prev, rename: checked === true }))
                  }
                />
                <label htmlFor="evt-rename" className="text-sm cursor-pointer">
                  重命名
                </label>
              </div>
              <div className="flex items-center gap-2">
                <Checkbox
                  id="evt-move"
                  checked={eventTypes.move}
                  onCheckedChange={(checked) =>
                    setEventTypes(prev => ({ ...prev, move: checked === true }))
                  }
                />
                <label htmlFor="evt-move" className="text-sm cursor-pointer">
                  移动
                </label>
              </div>
            </div>
          </div>

          {/* Remove Empty Dirs */}
          <div className="flex items-center gap-2">
            <Checkbox
              id="remove-empty"
              checked={removeEmptyDirs}
              onCheckedChange={(checked) => setRemoveEmptyDirs(checked === true)}
            />
            <label htmlFor="remove-empty" className="text-sm cursor-pointer">
              删除文件后自动清理空父目录
            </label>
          </div>

          {/* Min File Size */}
          <div className="space-y-3">
            <Label>最小文件大小（MB）</Label>
            <Input
              type="number"
              min="0"
              step="0.1"
              placeholder="留空或 0 表示不过滤"
              value={minFileSizeMb}
              onChange={(e) => {
                setMinFileSizeMb(e.target.value);
              }}
              className="max-w-xs"
            />
            <p className="text-xs text-muted-foreground">
              小于此阈值的文件跳过 STRM 生成（如封面、NFO 等小文件）。0 表示不过滤。
            </p>
          </div>

          {/* First Pull Mode */}
          <div className="space-y-3">
            <Label>首次拉取模式</Label>
            <Select
              value={firstPullMode}
              onValueChange={(v: "latest" | "all" | "last") => setFirstPullMode(v)}
            >
              <SelectTrigger className="w-full max-w-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="latest">从当前时间开始（推荐）</SelectItem>
                <SelectItem value="all">拉取全部历史事件</SelectItem>
                <SelectItem value="last">从上次断点继续</SelectItem>
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              首次启动监控时的拉取范围。<strong>latest</strong>：只处理新事件，最轻量；
              <strong>all</strong>：拉取所有历史事件（适合首次部署补历史，耗时较长）；
              <strong>last</strong>：从上次保存的游标继续，无断点时退化为 latest。
            </p>
          </div>

          {/* Move Media Mode */}
          <div className="space-y-3">
            <Label>移动事件处理模式</Label>
            <Select
              value={moveMediaMode}
              onValueChange={(v: "recreate" | "local_move") => setMoveMediaMode(v)}
            >
              <SelectTrigger className="w-full max-w-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="local_move">本地移动 STRM（推荐）</SelectItem>
                <SelectItem value="recreate">删除旧 STRM 并重新生成</SelectItem>
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              文件被移动时的处理策略。<strong>local_move</strong>：本地直接 rename STRM 文件，速度快；
              <strong>recreate</strong>：删除旧 STRM 后用新 pickcode 重新生成，更可靠但需调用 115 API。
            </p>
          </div>

          {/* Path Mappings（严格对齐用户"移动端 4 行规格"文字图，1:1）
             ============================================================
             移动端（<640px）每行 4 行堆叠（与截图 / 文字图完全一致）：
               Line 1 :  [账号下拉 ▼]  [115 网盘路径输入 ...............]  [📂]
               Line 2 :                     ↓  (居中，单独一行)
               Line 3 :  [本地保存路径输入 ...........................]  [📂]
               Line 4 :  [删除 / 添加]  (整行宽按钮，不挤右缘)
             sm+（>=640px，桌面横排）保持传统一行：
               [账号▼  云盘📂 → 本地📂  删除/添加]
             ============================================================
             - 账号下拉：sm 下 160px shrink-0；mobile 下 1/3 宽度（"遍历全部账号" 6 字 + chevron 不溢出）
             - 所有 Input 统一 min-w-0 + w-full + truncate/break-all；SelectValue/SelectItem 都 truncate
             - 每行容器统一 gap-2（mobile 竖排）/ sm:gap-2（sm 横排 items-center）
          */}
          <div className="space-y-4 w-full min-w-0">
            <Label>路径映射（115 网盘路径 → 本地保存路径）</Label>
            <div className="space-y-4 w-full min-w-0">
              {pathMappings.map((mapping, index) => (
                <div
                  key={index}
                  className="w-full min-w-0 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-2"
                >
                  {/* ============ Line 1 / sm 左段：账号下拉 ▼ + 网盘路径输入 + 📂
                                 sm  ：拆成两个独立 shrink-0 / flex-1
                                 mobile：同一行 flex，账号 1/3 左、网盘 2/3 右
                  ============ */}
                  <div className="w-full sm:w-auto min-w-0 flex flex-row gap-1 sm:gap-0 items-center">
                    {/* 账号下拉：mobile 1/3；sm 固定 160px（"遍历全部账号" 6 字 + chevron 不截断）*/}
                    <div className="w-1/3 sm:w-[160px] sm:shrink-0 min-w-0 sm:mr-2">
                      <Select
                        value={mapping.account || "__all__"}
                        onValueChange={(val) => {
                          const updated = [...pathMappings];
                          updated[index] = { ...updated[index], account: val === "__all__" ? undefined : val };
                          setPathMappings(updated);
                        }}
                      >
                        <SelectTrigger className="h-9 w-full min-w-0">
                          <SelectValue placeholder="遍历全部账号" className="truncate" />
                        </SelectTrigger>
                        <SelectContent align="start" className="min-w-[140px]">
                          <SelectItem value="__all__" className="truncate whitespace-nowrap pr-2">遍历全部账号</SelectItem>
                          {accounts.map(acc => (
                            <SelectItem key={acc} value={acc} className="truncate whitespace-nowrap pr-2">{acc}</SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>

                    {/* 网盘路径输入 + 📂 */}
                    <div className="flex-1 flex gap-1 items-center min-w-0 sm:mr-2">
                      <Input
                        value={mapping.cloudPath}
                        onChange={(e) => {
                          const updated = [...pathMappings];
                          updated[index] = { ...updated[index], cloudPath: e.target.value };
                          setPathMappings(updated);
                        }}
                        placeholder="115 网盘路径，如 /电影"
                        className="flex-1 min-w-0 w-full truncate break-all"
                      />
                      <TooltipProvider delayDuration={100}>
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <span className="inline-flex shrink-0">
                              <Button
                                type="button"
                                variant="outline"
                                size="icon"
                                onClick={() => openCloudPicker(index, mapping.account)}
                                title={mapping.account ? "选择网盘目录" : ""}
                                disabled={!mapping.account || accounts.length === 0}
                              >
                                <FolderOpen className="w-4 h-4" />
                              </Button>
                            </span>
                          </TooltipTrigger>
                          {!mapping.account && (
                            <TooltipContent side="top" className="max-w-[240px]">
                              <p>遍历全部账号模式下，不同账号的目录结构可能不一致，请手动输入路径，或先选择具体账号再选择目录。</p>
                            </TooltipContent>
                          )}
                        </Tooltip>
                      </TooltipProvider>
                    </div>

                    {/* [删除] sm 时内联在行尾；mobile 时另起整行（Line 4） */}
                    <div className="hidden sm:flex sm:shrink-0 sm:w-auto">
                      <Button
                        variant="destructive"
                        size="sm"
                        onClick={() => removePathMapping(index)}
                        className="shrink-0"
                      >
                        删除
                      </Button>
                    </div>
                  </div>

                  {/* ============ Line 2：↓ 居中（仅 mobile；sm 已在行内用 →） ============ */}
                  <span className="text-muted-foreground hidden sm:inline shrink-0 self-center px-1 sm:order-2">→</span>
                  <span
                    className="sm:hidden w-full text-center shrink-0 select-none text-muted-foreground"
                    aria-hidden
                  >
                    ↓
                  </span>

                  {/* ============ Line 3 / sm 中段：本地路径输入 + 📂 ============ */}
                  <div className="flex-1 flex gap-1 items-center min-w-0 w-full sm:w-auto sm:order-3">
                    <Input
                      value={mapping.localPath}
                      onChange={(e) => {
                        const updated = [...pathMappings];
                        updated[index] = { ...updated[index], localPath: e.target.value };
                        setPathMappings(updated);
                      }}
                      placeholder="本地路径，如/app/data/media/电影"
                      className="flex-1 min-w-0 w-full truncate break-all"
                    />
                    <Button
                      type="button"
                      variant="outline"
                      size="icon"
                      onClick={() => openLocalPicker(index)}
                      title="选择本地目录"
                      className="shrink-0"
                    >
                      <FolderOpen className="w-4 h-4" />
                    </Button>
                  </div>

                  {/* ============ Line 4（mobile only）：删除按钮整行宽 ============ */}
                  <div className="sm:hidden w-full min-w-0">
                    <Button
                      variant="destructive"
                      size="sm"
                      onClick={() => removePathMapping(index)}
                      className="w-full shrink-0"
                    >
                      删除
                    </Button>
                  </div>
                </div>
              ))}
            </div>

            {/* 新建行：与已有映射行严格同构（账号▼+网盘 / ↓ / 本地 / 添加） */}
            <div className="w-full min-w-0 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-2 pt-1 border-t border-dashed">
              {/* Line 1：账号下拉 ▼ + 网盘路径输入 + 📂 */}
              <div className="w-full sm:w-auto min-w-0 flex flex-row gap-1 sm:gap-0 items-center">
                <div className="w-1/3 sm:w-[160px] sm:shrink-0 min-w-0 sm:mr-2">
                  <Select
                    value={newMappingAccount}
                    onValueChange={setNewMappingAccount}
                  >
                    <SelectTrigger className="h-9 w-full min-w-0">
                      <SelectValue placeholder="遍历全部账号" className="truncate" />
                    </SelectTrigger>
                    <SelectContent align="start" className="min-w-[140px]">
                      <SelectItem value="__all__" className="truncate whitespace-nowrap pr-2">遍历全部账号</SelectItem>
                      {accounts.map(acc => (
                        <SelectItem key={acc} value={acc} className="truncate whitespace-nowrap pr-2">{acc}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>

                <div className="flex-1 flex gap-1 items-center min-w-0 sm:mr-2">
                  <Input
                    value={newCloudPath}
                    onChange={(e) => setNewCloudPath(e.target.value)}
                    placeholder="115 网盘路径，如 /电影"
                    className="flex-1 min-w-0 w-full truncate break-all"
                  />
                  <TooltipProvider delayDuration={100}>
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <span className="inline-flex shrink-0">
                          <Button
                            type="button"
                            variant="outline"
                            size="icon"
                            onClick={openNewCloudPicker}
                            title={newMappingAccount !== "__all__" ? "选择网盘目录" : ""}
                            disabled={newMappingAccount === "__all__" || accounts.length === 0}
                          >
                            <FolderOpen className="w-4 h-4" />
                          </Button>
                        </span>
                      </TooltipTrigger>
                      {newMappingAccount === "__all__" && (
                        <TooltipContent side="top" className="max-w-[240px]">
                          <p>遍历全部账号模式下，不同账号的目录结构可能不一致，请手动输入路径，或先选择具体账号再选择目录。</p>
                        </TooltipContent>
                      )}
                    </Tooltip>
                  </TooltipProvider>
                </div>

                <div className="hidden sm:flex sm:shrink-0 sm:w-auto">
                  <Button size="sm" onClick={addPathMapping} className="shrink-0">添加</Button>
                </div>
              </div>

              {/* Line 2：↓ 居中 */}
              <span className="text-muted-foreground hidden sm:inline shrink-0 self-center px-1 sm:order-2">→</span>
              <span className="sm:hidden w-full text-center shrink-0 select-none text-muted-foreground" aria-hidden>↓</span>

              {/* Line 3：本地路径输入 + 📂 */}
              <div className="flex-1 flex gap-1 items-center min-w-0 w-full sm:w-auto sm:order-3">
                <Input
                  value={newLocalPath}
                  onChange={(e) => setNewLocalPath(e.target.value)}
                  placeholder="本地路径，如/app/data/media/电影"
                  className="flex-1 min-w-0 w-full truncate break-all"
                />
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  onClick={openNewLocalPicker}
                  title="选择本地目录"
                  className="shrink-0"
                >
                  <FolderOpen className="w-4 h-4" />
                </Button>
              </div>

              {/* Line 4（mobile only）：添加按钮整行宽 */}
              <div className="sm:hidden w-full min-w-0">
                <Button size="sm" onClick={addPathMapping} className="w-full shrink-0">添加</Button>
              </div>
            </div>

            <p className="text-xs text-muted-foreground break-words">
              只有匹配到网盘路径前缀的文件才会被处理。支持多个路径映射。
            </p>
          </div>

          {/* Verify Button */}
          <div className="space-y-3">
            <div className="flex gap-2 items-center">
              <Button
                variant="outline"
                onClick={handleVerify}
                disabled={verifying || selectedAccounts.length === 0}
              >
                {verifying ? "验证中..." : "验证账号的生活事件功能"}
              </Button>
              {verifyResult && (
                <span className={`text-sm font-medium ${verifyResult.success ? "text-green-500" : "text-red-500"}`}>
                  {verifyResult.message}
                </span>
              )}
            </div>
            {verifyResult && verifyResult.perAccount.length > 0 && (
              <div className="rounded-md border p-3 space-y-3 text-sm">
                {verifyResult.perAccount.map(r => (
                  <div key={r.account} className="flex items-start gap-2">
                    <span className={r.success ? "text-green-500 mt-0.5" : "text-red-500 mt-0.5 shrink-0"}>
                      {r.success ? "✓" : "✗"}
                    </span>
                    <div className="min-w-0">
                      <div className="font-medium">账号：{r.account}</div>
                      <div className={r.success ? "text-green-600" : "text-red-600"}>
                        {r.message}
                      </div>
                      {r.details && (
                        <div className="text-muted-foreground text-xs mt-1 break-all">
                          详情：{JSON.stringify(r.details)}
                        </div>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Monitor Status */}
          {displayMonitorStates.length > 0 && (
            <div className="space-y-3">
              <Label>监控状态</Label>
              <div className="p-3 border rounded-md space-y-3">
                {displayMonitorStates.map((state) => {
                  // 判断账号是否仍存在于账户管理（Accounts 存储）：
                  // 不存在意味着该条目"账号 xxx 不存在"是必然异常，停止按钮无意义，应该从列表移除。
                  const accountExists = accounts.includes(state.account);
                  const isMissing = !!state.lastError && !accountExists;
                  const hasError = !!state.lastError;

                  return (
                    <div key={state.account} className="flex items-center justify-between gap-2 flex-wrap">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="text-sm font-medium">{state.account}</span>
                        <span className={`text-xs px-2 py-0.5 rounded ${
                          state.lastError
                            ? "bg-red-500/20 text-red-400"
                            : state.running
                              ? "bg-green-500/20 text-green-400"
                              : state.pending
                                ? "bg-yellow-500/20 text-yellow-400"
                                : "bg-muted text-muted-foreground"
                        }`}>
                          {state.lastError
                            ? (isMissing ? "账号不存在" : "异常")
                            : state.running
                              ? "运行中"
                              : state.pending
                                ? "待保存配置"
                                : "已停止"}
                        </span>
                        {state.eventsProcessed > 0 && (
                          <span className="text-xs text-muted-foreground">
                            已处理 {state.eventsProcessed} 个事件
                          </span>
                        )}
                        {state.lastError && (
                          <span className="text-xs text-red-500 break-all pr-2">
                            错误: {state.lastError}
                          </span>
                        )}
                        {state.pending && (
                          <span className="text-xs text-muted-foreground">
                            点击下方「保存并启动监控」以启用此账号
                          </span>
                        )}
                      </div>
                      {/* 按钮按场景区分：
                          1. 待保存 pending: 1 个灰按钮「待保存」 disabled
                          2. 账号不存在（state.lastError + accounts 里找不到）: 1 个红色 destructive 「从监控列表移除」
                          3. 其他异常（账号仍存在，但出错）：outline「停止」 + 红色「从监控列表移除」
                             （running=true 时停止有意义，让用户先停监控再决定删不删）
                          4. 运行中：1 个红色 destructive「停止」
                          5. 已停止：1 个 outline「启动」
                      */}
                      <div className="flex items-center gap-2 shrink-0">
                        {state.pending ? (
                          <Button variant="outline" size="sm" disabled>
                            待保存
                          </Button>
                        ) : isMissing ? (
                          <Button
                            variant="destructive"
                            size="sm"
                            onClick={() => handleRemoveFromMonitor(state.account)}
                          >
                            从监控列表移除
                          </Button>
                        ) : (
                          <>
                            <Button
                              variant={state.running ? "destructive" : "outline"}
                              size="sm"
                              onClick={() => state.running ? handleStopMonitor(state.account) : handleStartAccount(state.account)}
                            >
                              {state.running ? "停止" : "启动"}
                            </Button>
                            {hasError && (
                              <Button
                                variant="outline"
                                size="sm"
                                className="border-red-300 text-red-500 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-950/40"
                                onClick={() => handleRemoveFromMonitor(state.account)}
                              >
                                从监控列表移除
                              </Button>
                            )}
                          </>
                        )}
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          )}
        </div>
      </section>

      <div className="pt-2 flex flex-wrap gap-2 items-center sticky bottom-0 bg-background/95 backdrop-blur-sm py-3 -mx-3 sm:-mx-4 md:-mx-6 px-3 sm:px-4 md:px-6 border-t">
        <Button disabled={saving} onClick={onSave} className="flex-1 sm:flex-initial">
          {saving ? "保存中..." : "保存设置"}
        </Button>
        <Button
          onClick={handleStartMonitor}
          disabled={
            !monitorEnabled ||
            selectedAccounts.length === 0 ||
            pathMappings.length === 0
          }
          className="flex-1 sm:flex-initial"
        >
          保存并启动监控
        </Button>
        {(!monitorEnabled || selectedAccounts.length === 0 || pathMappings.length === 0) && (
          <p className="text-xs text-muted-foreground">
            {!monitorEnabled && "请先勾选「启用监控」"}
            {monitorEnabled && selectedAccounts.length === 0 && "请至少选择一个监控账号"}
            {monitorEnabled && selectedAccounts.length > 0 && pathMappings.length === 0 && "请至少配置一条路径映射"}
          </p>
        )}
      </div>
    </div>
  );
}
