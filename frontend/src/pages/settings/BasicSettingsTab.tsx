import type { Dispatch, SetStateAction } from "react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { Checkbox } from "@/components/ui/checkbox";
import { Settings, Filter } from "lucide-react";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type {
  Settings as SettingsType,
  MountDryRunData,
  MountSyncApplyData,
} from "./types";

// 孤儿 STRM 清理三档策略说明（与后端 cleanupOrphanStrms 的实际行为逐条对齐）
const CLEANUP_MODE_HINTS: Record<string, string> = {
  off: "不检查、不处理。网盘已删除的影片会继续保留本地 STRM，媒体库里可能出现点开就报错的条目。",
  mark_only:
    "只在任务日志里列出孤儿 STRM，不动任何文件。适合先观察一段时间，确认判定准确后再切到自动清理。",
  auto_clean:
    "直接删除孤儿 STRM。命中保护阈值时会自动停手，或转入待确认队列——可在 Telegram 按钮或清理页「待确认删除批次」中确认；超过 24 小时未确认的批次会自动作废。",
};

// ==================== STRM URL 形态 ====================
// 必要参数（account / pickcode / file_name）固定携带，不做选择；
// 唯一可选项：是否把云端路径放进 URL 的 path 段（路径式 URL）。
const STRM_URL_STANDARD_TEMPLATE =
  "{prefix}/api/strm?account={account}&pickcode={pickcode}&file_name={file_name}";

// 路径式：path 末段即文件名（.iso/.mkv 在最后），播放器识别容器更可靠
const STRM_URL_PATH_TEMPLATE =
  "{prefix}/api/strm/{file_path}?account={account}&pickcode={pickcode}";

function isStrmPathTemplate(tmpl?: string): boolean {
  return !!tmpl && tmpl.includes("/api/strm/{file_path}");
}

export interface BasicSettingsTabProps {
  data: SettingsType;
  setData: Dispatch<SetStateAction<SettingsType>>;
  strmExtensionsInput: string;
  setStrmExtensionsInput: Dispatch<SetStateAction<string>>;
  downloadExtensionsInput: string;
  setDownloadExtensionsInput: Dispatch<SetStateAction<string>>;
  forceProxyUaInput: string;
  setForceProxyUaInput: Dispatch<SetStateAction<string>>;
  globalMinFileSizeMb: string;
  setGlobalMinFileSizeMb: Dispatch<SetStateAction<string>>;
  globalBlacklistInput: string;
  setGlobalBlacklistInput: Dispatch<SetStateAction<string>>;
  globalOverwriteMode: "always" | "never";
  setGlobalOverwriteMode: Dispatch<SetStateAction<"always" | "never">>;
  mountDryRun: MountDryRunData;
  mountDryRunLoading: boolean;
  mountSyncing: boolean;
  lastSyncApply: MountSyncApplyData;
  fetchMountDryRun: () => Promise<void>;
  applyMountSync: () => Promise<void>;
  saving: boolean;
  onSave: () => Promise<void>;
}

export function BasicSettingsTab(props: BasicSettingsTabProps) {
  const {
    data,
    setData,
    strmExtensionsInput,
    setStrmExtensionsInput,
    downloadExtensionsInput,
    setDownloadExtensionsInput,
    forceProxyUaInput,
    setForceProxyUaInput,
    globalMinFileSizeMb,
    setGlobalMinFileSizeMb,
    globalBlacklistInput,
    setGlobalBlacklistInput,
    globalOverwriteMode,
    setGlobalOverwriteMode,
    mountDryRun,
    mountDryRunLoading,
    mountSyncing,
    lastSyncApply,
    fetchMountDryRun,
    applyMountSync,
    saving,
    onSave,
  } = props;

  // 孤儿 STRM 清理策略：未配置时与后端默认值保持一致（auto_clean）
  const cleanupMode = data.download?.fullSyncCleanupOrphans ?? "auto_clean";

  // STRM URL：编码开关（默认开启）+ 是否使用路径式 URL（由模板字符串反推）
  const pathEncodingEnabled = data.enablePathEncoding !== false;
  const useStrmPathUrl = isStrmPathTemplate(data.strm?.strmUrlTemplate);
  const setStrmPathUrl = (on: boolean) => {
    setData({
      ...data,
      strm: {
        ...data.strm,
        strmUrlTemplate: on ? STRM_URL_PATH_TEMPLATE : STRM_URL_STANDARD_TEMPLATE,
      },
    });
  };
  // 预览：用示例值替换模板变量，直观展示最终 URL
  const strmUrlPreview = (data.strm?.strmUrlTemplate || STRM_URL_STANDARD_TEMPLATE)
    .replace("{prefix}", (data.strmPrefix || "http://192.168.1.100:8090").replace(/\/$/, ""))
    .replace("{account}", "wabisabi926")
    .replace("{pickcode}", "csj82c47tyb01dm22")
    .replace("{file_name}", "The.Ordinary.Jackpot.S01E10.Episode.10.1080p.DSNP.WEB-DL.AAC.2.0.H.264-BlackTV.mkv")
    .replace("{filename}", "The.Ordinary.Jackpot.S01E10.Episode.10.1080p.DSNP.WEB-DL.AAC.2.0.H.264-BlackTV.mkv")
    .replace("{file_path}", "电影/科幻/示例影片.iso");

  return (
    <div className="space-y-6">
      {/* 基础设置 */}
      <section className="border rounded-md p-3 sm:p-5 space-y-5">
        <div>
          <h2 className="text-base font-medium">基础设置</h2>
          <p className="text-xs text-muted-foreground mt-1">全局 User-Agent 与文件扩展名配置</p>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
          <div className="space-y-3">
            <Label>User-Agent</Label>
            <Input
              value={data["user-agent"] || ""}
              onChange={(e) =>
                setData({ ...data, ["user-agent"]: e.target.value })
              }
              placeholder="Mozilla/5.0 ..."
            />
            <p className="text-xs text-muted-foreground">
              访问 115 API 时使用的 UA
            </p>
          </div>
          <div className="space-y-3">
            <Label>Strm 文件扩展名</Label>
            <Input
              value={strmExtensionsInput}
              onChange={(e) => setStrmExtensionsInput(e.target.value)}
              placeholder=".mkv, .mp4, .mp3"
            />
            <p className="text-xs text-muted-foreground">
              用逗号分隔，自动添加点号前缀；BDMV/STREAM 目录内的视频流不生成 STRM
            </p>
          </div>
          <div className="space-y-3 md:col-span-2">
            <Label>下载文件扩展名</Label>
            <Input
              value={downloadExtensionsInput}
              onChange={(e) => setDownloadExtensionsInput(e.target.value)}
              placeholder=".srt, .ass, .sub, .nfo"
            />
            <p className="text-xs text-muted-foreground">
              用逗号分隔，自动添加点号前缀
            </p>
          </div>
        </div>
      </section>

      {/* STRM 生成设置 */}
      <section className="border rounded-md p-3 sm:p-5 space-y-5">
        <div>
          <h2 className="text-base font-medium">STRM 生成设置（全局默认）</h2>
          <p className="text-xs text-muted-foreground mt-1">
            适用于所有账号的生活事件监控和全量扫描，任务级可单独覆盖
          </p>
        </div>

        <div className="space-y-3">
          <Label>Strm 前缀</Label>
          <Input
            value={data.strmPrefix || ""}
            onChange={(e) =>
              setData({ ...data, strmPrefix: e.target.value })
            }
            placeholder="http://服务器IP:端口 (如 http://192.168.1.100:8090)"
          />
          <p className="text-xs text-muted-foreground">
            STRM 文件内容的前缀，如 Emby/Jellyfin 的 HTTP 访问地址。系统会自动追加 <code>/api/strm</code>，无需手动添加。
          </p>
        </div>

        <div className="space-y-3 pt-2 border-t">
          <Label>URL 编码</Label>
          <div className="flex items-start gap-2">
            <Checkbox
              id="enable-path-encoding"
              checked={pathEncodingEnabled}
              onCheckedChange={(checked) =>
                setData({ ...data, enablePathEncoding: checked === true })
              }
              className="mt-0.5"
            />
            <div className="space-y-1">
              <label htmlFor="enable-path-encoding" className="text-sm cursor-pointer">
                对 URL 中的账号、文件名等参数做编码（推荐开启）
              </label>
              <p className="text-xs text-muted-foreground">
                关闭后参数原样写入 URL：文件名含 # / &amp; / ? / 空格 等字符时会破坏 URL 解析，仅在确有需要时关闭。
              </p>
            </div>
          </div>
        </div>

        <div className="space-y-3 pt-2 border-t">
          <Label>URL 形态</Label>
          <div className="flex items-start gap-2">
            <Checkbox
              id="strm-path-url"
              checked={useStrmPathUrl}
              onCheckedChange={(checked) => setStrmPathUrl(checked === true)}
              className="mt-0.5"
            />
            <div className="space-y-1">
              <label htmlFor="strm-path-url" className="text-sm cursor-pointer">
                URL 路径携带文件路径（路径式 URL）
              </label>
              <p className="text-xs text-muted-foreground">
                勾选后 URL 变为 <code>/api/strm/电影/.../xxx.iso?account=..&pickcode=..</code>，
                路径末尾带真实扩展名（如 .iso），Emby/Kodi 识别原盘容器更可靠；
                与 p115strmhelper 的 redirect_url 形态一致。播放定位仍靠 pickcode，路径仅用于显示与识别。
              </p>
            </div>
          </div>
          <div className="rounded-md bg-muted px-3 py-2 space-y-1">
            <p className="text-xs text-muted-foreground">
              当前形态预览（{useStrmPathUrl ? "路径式" : "标准"}）
            </p>
            <code className="text-xs break-all">{strmUrlPreview}</code>
          </div>
        </div>

        {/* 全局文件过滤 */}
        <div className="space-y-4 pt-4 border-t">
          <div className="flex items-center gap-2">
            <Filter className="w-4 h-4 text-muted-foreground" />
            <h3 className="text-sm font-medium">全局文件过滤</h3>
          </div>
          <p className="text-xs text-muted-foreground">
            满足以下条件的文件不会生成 STRM，对所有账号的生活事件监控和全量扫描生效，任务级可单独覆盖。
          </p>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
            <div className="space-y-3">
              <Label>最小文件大小（MB）</Label>
              <Input
                type="number"
                min="0"
                value={globalMinFileSizeMb}
                onChange={(e) => setGlobalMinFileSizeMb(e.target.value)}
                placeholder="0（不过滤）"
              />
              <p className="text-xs text-muted-foreground">
                小于该大小的视频不生成 STRM；留空或 0 表示不过滤（可用于跳过预告、花絮等小文件）。
              </p>
            </div>
            <div className="space-y-3">
              <Label>文件名黑名单关键词</Label>
              <Input
                value={globalBlacklistInput}
                onChange={(e) => setGlobalBlacklistInput(e.target.value)}
                placeholder="trailer, 预告, sample"
              />
              <p className="text-xs text-muted-foreground">
                逗号分隔，文件名包含任一关键词即跳过（大小写不敏感）。
              </p>
            </div>
          </div>
        </div>

        {/* STRM 覆盖模式 */}
        <div className="space-y-3 pt-4 border-t">
          <Label>STRM 覆盖模式</Label>
          <Select
            value={globalOverwriteMode}
            onValueChange={(value) => setGlobalOverwriteMode(value as "always" | "never")}
          >
            <SelectTrigger className="w-full md:w-64">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="always">总是覆盖（默认）</SelectItem>
              <SelectItem value="never">跳过已存在</SelectItem>
            </SelectContent>
          </Select>
          <p className="text-xs text-muted-foreground">
            决定重新生成时如何处理已存在的 STRM 文件。「总是覆盖」每次重新生成，保证与网盘一致；「跳过已存在」可加速重复生成，但源文件改名或换链时可能残留旧链接。
          </p>
        </div>

        {/* 清理策略：只按 STRM 内容里的 pickcode 对账，不会碰用户自己的文件 */}
        <div className="space-y-3 pt-4 border-t">
          <div className="space-y-1">
            <Label>孤儿 STRM 清理</Label>
            <p className="text-xs text-muted-foreground">
              按 STRM 内容里的 pickcode 与云端清单对账，判断哪些本地 STRM 已经失效。
            </p>
          </div>
          <Select
            value={cleanupMode}
            onValueChange={(value) =>
              setData({
                ...data,
                download: { ...(data.download || {}), fullSyncCleanupOrphans: value },
              })
            }
          >
            <SelectTrigger className="w-full md:w-64">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="off">关闭</SelectItem>
              <SelectItem value="mark_only">仅标记（不删除）</SelectItem>
              <SelectItem value="auto_clean">自动清理（推荐）</SelectItem>
            </SelectContent>
          </Select>
          <p className="text-xs text-muted-foreground">
            {CLEANUP_MODE_HINTS[cleanupMode] ?? CLEANUP_MODE_HINTS.auto_clean}
          </p>

          <div className="rounded-md border p-2.5 sm:p-3 space-y-1.5 bg-muted/30">
            <p className="text-xs text-muted-foreground">
              <span className="font-medium text-foreground">判定规则：</span>
              云端清单里找不到该 pickcode，或 pickcode 仍有效但本地路径与云端不一致（改名/移动残留），都算孤儿。
            </p>
            <p className="text-xs text-muted-foreground">
              <span className="font-medium text-foreground">安全保护：</span>
              没有 pickcode 的手写 STRM、视频文件、列目录失败目录下的 STRM 一律跳过；孤儿数量/占比/涉及目录数超阈值时不会直接删除。
            </p>
          </div>

          <div className={cleanupMode === "off" ? "space-y-3 opacity-50" : "space-y-3"}>
            <div className="flex items-center gap-2">
              <Checkbox
                id="cleanup-remove-related"
                disabled={cleanupMode === "off"}
                checked={data.cleanup?.removeRelatedFiles ?? true}
                onCheckedChange={(checked) =>
                  setData({
                    ...data,
                    cleanup: { ...(data.cleanup || {}), removeRelatedFiles: checked === true },
                  })
                }
              />
              <label htmlFor="cleanup-remove-related" className="text-sm cursor-pointer">
                删除孤儿 STRM 时，连带删除同名 nfo / 海报 / 字幕
              </label>
            </div>
            <p className="text-xs text-muted-foreground">
              只删 .nfo/.jpg/.png/.srt/.ass 等元数据与字幕，<span className="font-medium">绝不会删 .mkv/.mp4 等视频文件</span>。
            </p>
            <div className="flex items-center gap-2">
              <Checkbox
                id="cleanup-remove-empty-dirs"
                disabled={cleanupMode === "off"}
                checked={data.cleanup?.removeEmptyDirs ?? false}
                onCheckedChange={(checked) =>
                  setData({
                    ...data,
                    cleanup: { ...(data.cleanup || {}), removeEmptyDirs: checked === true },
                  })
                }
              />
              <label htmlFor="cleanup-remove-empty-dirs" className="text-sm cursor-pointer">
                删完后清理空目录
              </label>
            </div>
          </div>
        </div>

        {/* STRM 路由策略配置（始终生效，后端智能路由自动决定 proxy/redirect） */}
        <div className="space-y-4 pt-4 border-t">
          <div className="flex items-center gap-2">
            <Settings className="w-4 h-4 text-muted-foreground" />
            <h3 className="text-sm font-medium">STRM 路由策略</h3>
          </div>
          <p className="text-xs text-muted-foreground">
            仅作用于 STRM 端点层（直接打开 .strm 文件的场景）。默认 302 redirect 直连 CDN（不走本机带宽），
            仅当下方「强制代理 UA 标识」匹配时才强制走 proxy，其余（含 ISO 原盘）均直连。
          </p>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-5">
              <div className="space-y-3">
                <Label>强制代理 UA 标识</Label>
                <Input
                  value={forceProxyUaInput}
                  onChange={(e) => setForceProxyUaInput(e.target.value)}
                  placeholder="Infuse, VidHub"
                />
                <p className="text-xs text-muted-foreground">
                  逗号分隔。留空时 STRM 端点默认全部直连，仅在特殊播放器直接打开 .strm 且需要代理时填写。
                </p>
              </div>
              <div className="space-y-3">
                <Label>单账号代理并发上限</Label>
                <Input
                  type="number"
                  min="1"
                  max="20"
                  value={data.strm?.accountProxyConcurrencyLimit ?? 8}
                  onChange={(e) =>
                    setData({ ...data, strm: { ...data.strm, accountProxyConcurrencyLimit: parseInt(e.target.value) || 8 } })
                  }
                />
                <p className="text-xs text-muted-foreground">
                  超过自动切 redirect
                </p>
              </div>
              <div className="space-y-3">
                <Label>Redirect 检测超时（ms）</Label>
                <Input
                  type="number"
                  min="500"
                  max="10000"
                  step="500"
                  value={data.strm?.redirectCheckTimeoutMs ?? 5000}
                  onChange={(e) =>
                    setData({ ...data, strm: { ...data.strm, redirectCheckTimeoutMs: parseInt(e.target.value) || 5000 } })
                  }
                />
                <p className="text-xs text-muted-foreground">
                  失败降级 proxy
                </p>
              </div>
            </div>
          </div>
      </section>

      {/* 下载限流配置 */}
      <section className="border rounded-md p-3 sm:p-5 space-y-5">
        <div>
          <h2 className="text-base font-medium">下载限流配置</h2>
          <p className="text-xs text-muted-foreground mt-1">控制 115 API 与下载的并发上限</p>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-5">
          <div className="space-y-3">
            <Label>链接获取每秒请求数</Label>
            <Input
              type="number"
              min="1"
              max="100"
              value={data.download?.linkMaxPerSecond || 2}
              onChange={(e) =>
                setData({
                  ...data,
                  download: {
                    ...(data.download || {}),
                    linkMaxPerSecond: parseInt(e.target.value) || 2
                  },
                })
              }
              placeholder="2"
            />
            <p className="text-xs text-muted-foreground">linkMaxPerSecond</p>
          </div>
          <div className="space-y-3">
            <Label>链接获取并发数</Label>
            <Input
              type="number"
              min="1"
              max="50"
              value={data.download?.linkMaxConcurrent || 10}
              onChange={(e) =>
                setData({
                  ...data,
                  download: {
                    ...(data.download || {}),
                    linkMaxConcurrent: parseInt(e.target.value) || 10
                  },
                })
              }
              placeholder="10"
            />
            <p className="text-xs text-muted-foreground">linkMaxConcurrent</p>
          </div>
          <div className="space-y-3">
            <Label>文件下载并发数</Label>
            <Input
              type="number"
              min="1"
              max="50"
              value={data.download?.downloadMaxConcurrent || 2}
              onChange={(e) =>
                setData({
                  ...data,
                  download: {
                    ...(data.download || {}),
                    downloadMaxConcurrent: parseInt(e.target.value) || 2
                  },
                })
              }
              placeholder="2"
            />
            <p className="text-xs text-muted-foreground">downloadMaxConcurrent</p>
          </div>
        </div>

        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <Label>自动下载媒体元数据</Label>
            <button
              type="button"
              role="switch"
              aria-checked={data.download?.autoDownloadMetadata ?? true}
              onClick={() =>
                setData({
                  ...data,
                  download: {
                    ...(data.download || {}),
                    autoDownloadMetadata: !(data.download?.autoDownloadMetadata ?? true)
                  },
                })
              }
              className={`inline-flex h-5 w-9 items-center rounded-full transition-colors ${
                (data.download?.autoDownloadMetadata ?? true) ? "bg-primary" : "bg-muted"
              }`}
            >
              <span
                className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                  (data.download?.autoDownloadMetadata ?? true) ? "translate-x-4" : "translate-x-0.5"
                }`}
              />
            </button>
          </div>
          <p className="text-xs text-muted-foreground">
            全量同步时自动下载 nfo/jpg/png/srt 等媒体元数据文件。关闭后只生成 STRM 视频索引文件。
          </p>
        </div>

      </section>

      <Separator />

      <section className="space-y-4">
        <h2 className="text-base font-medium">媒体挂载路径</h2>
        <div className="grid grid-cols-1 gap-4">
          <div className="space-y-2 md:col-span-2">
            <div className="flex flex-col sm:flex-row sm:items-end justify-between gap-2">
              <div>
                <Label>媒体挂载路径 (mediaMountPath)</Label>
                <p className="text-xs text-muted-foreground mt-1">
                  由系统自动计算并维护（唯一事实来源 SSOT）：根据<span className="font-medium">全局 302 × 账号集</span>、
                  <span className="font-medium">每个任务的 STRM 设置</span>、
                  <span className="font-medium">生活事件监控</span> 全量收敛得到。
                  不建议手工修改 settings.json。
                </p>
              </div>
              <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  type="button"
                  onClick={fetchMountDryRun}
                  disabled={mountDryRunLoading || mountSyncing}
                  className="w-full sm:w-auto"
                >
                  {mountDryRunLoading ? "计算中..." : "刷新视图"}
                </Button>
                <Button
                  variant="default"
                  size="sm"
                  type="button"
                  onClick={applyMountSync}
                  disabled={mountSyncing || !mountDryRun?.diff.changed}
                  className="w-full sm:w-auto"
                >
                  {mountSyncing ? "同步中..." : "立即同步并持久化"}
                </Button>
              </div>
            </div>

            <div className="rounded-md border p-2.5 sm:p-3 space-y-3 bg-muted/30">
              {mountDryRunLoading && !mountDryRun ? (
                <p className="text-sm text-muted-foreground">正在计算期望集合...</p>
              ) : mountDryRun && mountDryRun.computed.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  暂无项。请先在上方配置 STRM 前缀和 302 选项，或创建带自定义前缀的任务。
                </p>
              ) : mountDryRun ? (
                <>
                  <div className="flex flex-wrap gap-2 text-xs">
                    <span className="px-2 py-0.5 rounded bg-background border">
                      共 <b>{mountDryRun.computed.length}</b> 条期望
                    </span>
                    {mountDryRun.diff.changed ? (
                      <>
                        {mountDryRun.diff.added.length > 0 && (
                          <span className="px-2 py-0.5 rounded border bg-green-500/20 text-green-400 border-green-500/30">
                            +{mountDryRun.diff.added.length} 待新增
                          </span>
                        )}
                        {mountDryRun.diff.removed.length > 0 && (
                          <span className="px-2 py-0.5 rounded border bg-red-500/20 text-red-400 border-red-500/30">
                            -{mountDryRun.diff.removed.length} 待删除
                          </span>
                        )}
                      </>
                    ) : (
                      <span className="px-2 py-0.5 rounded border bg-background text-muted-foreground">
                        与 settings.json 一致，无差异
                      </span>
                    )}
                    <span className="px-2 py-0.5 rounded border bg-background text-muted-foreground">
                      已持久化 {mountDryRun.persisted.length} 条
                    </span>
                  </div>

                  {mountDryRun.diff.removed.length > 0 && (
                    <details className="text-xs">
                      <summary className="cursor-pointer text-red-400">
                        以下 {mountDryRun.diff.removed.length} 条在 settings.json 中存在，但已不再被任何引用方需要
                      </summary>
                      <ul className="mt-2 space-y-1 pl-4 list-disc font-mono break-all">
                        {mountDryRun.diff.removed.map((p) => (
                          <li key={`rm-${p}`}>{p}</li>
                        ))}
                      </ul>
                    </details>
                  )}

                  <ul className="space-y-2 text-sm">
                    {mountDryRun.computed.map((row) => {
                      const added = mountDryRun.diff.added.includes(row.prefix);
                      return (
                        <li
                          key={row.prefix}
                          className="flex flex-col sm:flex-row sm:flex-wrap sm:items-center gap-1.5 sm:gap-2 rounded border bg-background px-2.5 sm:px-3 py-2"
                        >
                          <Tooltip>
                            <TooltipTrigger asChild className="min-w-0 flex-1">
                              <span
                                className="block font-mono whitespace-nowrap overflow-hidden text-ellipsis text-xs sm:text-sm"
                                title={row.prefix}
                              >
                                {row.prefix}
                              </span>
                            </TooltipTrigger>
                            <TooltipContent className="font-mono text-xs max-w-[90vw] break-all whitespace-normal">
                              {row.prefix}
                            </TooltipContent>
                          </Tooltip>
                          <span
                            className={
                              "px-1.5 py-0.5 rounded text-[11px] border " +
                              (row.source === "global_302"
                                ? "bg-indigo-500/20 text-indigo-400 border-indigo-500/30"
                                : row.source === "task"
                                  ? "bg-sky-500/20 text-sky-400 border-sky-500/30"
                                  : "bg-amber-500/20 text-amber-400 border-amber-500/30")
                            }
                          >
                            {row.sourceLabel}
                          </span>
                          {row.account && (
                            <span className="text-xs text-muted-foreground">
                              账号：<b>{row.account}</b>
                            </span>
                          )}
                          {row.taskId && (
                            <span className="text-xs text-muted-foreground font-mono">
                              task #{row.taskId.slice(0, 8)}
                            </span>
                          )}
                          {added && (
                            <span className="text-[11px] px-1.5 py-0.5 rounded border bg-green-500/20 text-green-400 border-green-500/30">
                              待新增
                            </span>
                          )}
                        </li>
                      );
                    })}
                  </ul>
                </>
              ) : (
                <p className="text-sm text-muted-foreground">未加载数据</p>
              )}

              {lastSyncApply && (
                <div
                  className={
                    "rounded border px-3 py-2 text-xs " +
                    (lastSyncApply.error || lastSyncApply.nginx?.ok === false
                      ? "bg-amber-500/10 border-amber-500/30 text-amber-400"
                      : "bg-slate-500/10 border-slate-500/30 text-slate-400")
                  }
                >
                  <div className="font-medium mb-1">最近一次同步结果</div>
                  <ul className="list-disc pl-4 space-y-0.5">
                    <li>变更：{lastSyncApply.changed ? "已写入" : "无变化"}</li>
                    {lastSyncApply.added.length > 0 && (
                      <li>新增 {lastSyncApply.added.length} 条：<span className="font-mono">{lastSyncApply.added.join(", ")}</span></li>
                    )}
                    {lastSyncApply.removed.length > 0 && (
                      <li>删除 {lastSyncApply.removed.length} 条：<span className="font-mono">{lastSyncApply.removed.join(", ")}</span></li>
                    )}
                    <li>
                      nginx：
                      {lastSyncApply.nginx.attempted
                        ? lastSyncApply.nginx.ok
                          ? "已成功 reload"
                          : `reload 失败 - ${lastSyncApply.nginx.message}`
                        : lastSyncApply.nginx.skipped
                          ? "已按设置跳过 reload"
                          : "系统未检测到 nginx"}
                    </li>
                    {lastSyncApply.error && <li>错误：{lastSyncApply.error}</li>}
                  </ul>
                </div>
              )}
            </div>
          </div>
        </div>
      </section>

      <div className="pt-2 flex gap-2 items-center sticky bottom-0 bg-background/95 backdrop-blur-sm py-3 -mx-3 sm:-mx-4 md:-mx-6 px-3 sm:px-4 md:px-6 border-t">
        <Button disabled={saving} onClick={onSave} className="flex-1 sm:flex-initial">
          {saving ? "保存中..." : "保存设置"}
        </Button>
      </div>
    </div>
  );
}
