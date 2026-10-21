import { useState, type Dispatch, type ReactNode, type SetStateAction } from "react";
import { Link } from "react-router-dom";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { Checkbox } from "@/components/ui/checkbox";
import { ChevronDown, Settings, Filter } from "lucide-react";
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

/** 低频/进阶配置默认折叠，减少基础设置页的纵向噪音。 */
function CollapsibleSection({
  title,
  description,
  defaultOpen = false,
  children,
}: {
  title: string;
  description?: string;
  defaultOpen?: boolean;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <section className="border rounded-md">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-start gap-3 p-3 text-left sm:p-5"
      >
        <div className="min-w-0 flex-1">
          <h2 className="text-base font-medium">{title}</h2>
          {description && (
            <p className="mt-1 text-xs text-muted-foreground">{description}</p>
          )}
        </div>
        <ChevronDown
          className={`mt-0.5 h-4 w-4 shrink-0 text-muted-foreground transition-transform ${
            open ? "rotate-180" : ""
          }`}
        />
      </button>
      {open && <div className="space-y-5 border-t p-3 sm:p-5">{children}</div>}
    </section>
  );
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
  forceProxyFormatsInput: string;
  setForceProxyFormatsInput: Dispatch<SetStateAction<string>>;
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
  fetchMountDryRun: () => Promise<MountDryRunData>;
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
    forceProxyFormatsInput,
    setForceProxyFormatsInput,
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

  // STRM URL：编码开关（默认开启）
  const pathEncodingEnabled = data.enablePathEncoding !== false;

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
            <Label>STRM 文件扩展名</Label>
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
            <Label>附带下载的元数据扩展名</Label>
            <Input
              value={downloadExtensionsInput}
              onChange={(e) => setDownloadExtensionsInput(e.target.value)}
              placeholder=".srt, .ass, .sub, .nfo"
            />
            <p className="text-xs text-muted-foreground">
              用逗号分隔，自动添加点号前缀
            </p>
          </div>
          <div className="space-y-2 md:col-span-2">
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
              全量同步时自动下载上方扩展名对应的 nfo/jpg/png/srt 等元数据。关闭后只生成 STRM 视频索引文件。
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
          <Label>STRM 前缀</Label>
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

        <details className="group space-y-3 pt-2 border-t">
          <summary className="flex cursor-pointer list-none items-center gap-2">
            <span className="text-sm font-medium">高级：URL 编码</span>
            <ChevronDown className="h-4 w-4 text-muted-foreground transition-transform group-open:rotate-180" />
          </summary>
          <div className="flex items-start gap-2 pt-2">
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
        </details>

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

        {/* STRM 路由策略配置（始终生效，后端智能路由自动决定 proxy/redirect） */}
        <details className="group space-y-3 pt-4 border-t">
          <summary className="flex cursor-pointer list-none items-center gap-2">
            <Settings className="w-4 h-4 text-muted-foreground" />
            <h3 className="text-sm font-medium">STRM 路由策略</h3>
            <span className="hidden text-xs text-muted-foreground sm:inline">
              高级：强制代理 UA / 并发上限 / 重定向检测
            </span>
            <ChevronDown className="ml-auto h-4 w-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-180" />
          </summary>
          <div className="space-y-4 pt-2">
            <p className="text-xs text-muted-foreground">
              默认 302 直连 115 CDN；仅当下方「强制代理 UA 标识」匹配时走代理。
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
                  逗号分隔。命中即强制走服务端代理（适合 302 播不了的客户端）；留空则全部 302 直链。
                  原盘格式不受此项影响，始终走代理。
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
                  超过后自动切换为 302 重定向
                </p>
              </div>
              <div className="space-y-3">
                <Label>重定向检测超时（毫秒）</Label>
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
                  检测失败时回退到代理
                </p>
              </div>
            </div>
            <div className="space-y-3 rounded-md border p-3">
              <div className="flex items-center gap-2">
                <Checkbox
                  id="disableSeekFormatProxy"
                  checked={!(data.strm?.disableSeekFormatProxy ?? false)}
                  onCheckedChange={(checked) =>
                    setData({ ...data, strm: { ...data.strm, disableSeekFormatProxy: checked !== true } })
                  }
                />
                <Label htmlFor="disableSeekFormatProxy" className="cursor-pointer">
                  按格式强制代理（推荐开启）
                </Label>
              </div>
              <p className="text-xs text-muted-foreground">
                开启后，下方列表内的格式在 <b>Emby 反代</b> 和 <b>STRM 端点</b> 两层都走服务端代理——
                ISO / BDMV / M2TS 等原盘直接 302 到 CDN 常因 UA/签名绑定失败（卡住或无法播放）。
                关闭后两层都不再按格式代理，原盘也走 302（可能播放失败）。
              </p>
              <div className="space-y-2 pt-1">
                <Label>强制代理的容器/扩展名</Label>
                <Input
                  value={forceProxyFormatsInput}
                  onChange={(e) => setForceProxyFormatsInput(e.target.value)}
                  placeholder="iso, bdmv, m2ts, vob"
                />
                <p className="text-xs text-muted-foreground">
                  逗号分隔，大小写不敏感、点号可省略。两层共用这一份列表；留空恢复默认值
                  （iso/bdmv/bdav/dvd/bluray/m2ts/vob/ifo/bup，不含 ts）。
                </p>
              </div>
            </div>
            <div className="space-y-3 rounded-md border p-3">
              <div className="flex items-center gap-2">
                <Checkbox
                  id="disableRangeProxy"
                  checked={!(data.strm?.disableRangeProxy ?? false)}
                  onCheckedChange={(checked) =>
                    setData({ ...data, strm: { ...data.strm, disableRangeProxy: checked !== true } })
                  }
                />
                <Label htmlFor="disableRangeProxy" className="cursor-pointer">
                  代理流分片并发（推荐开启）
                </Label>
              </div>
              <p className="text-xs text-muted-foreground">
                按 Range 分片并行拉取再按序输出，解决大原盘（50GB+）单连接带宽不足导致的卡顿、超时、播不出来。
              </p>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <Label>分片大小（MB）</Label>
                  <Input
                    type="number"
                    min="1"
                    max="64"
                    value={data.strm?.proxyChunkSizeMB ?? 10}
                    onChange={(e) =>
                      setData({ ...data, strm: { ...data.strm, proxyChunkSizeMB: parseInt(e.target.value) || 10 } })
                    }
                  />
                </div>
                <div className="space-y-1.5">
                  <Label>分片并发数</Label>
                  <Input
                    type="number"
                    min="1"
                    max="4"
                    value={data.strm?.proxyConcurrency ?? 2}
                    onChange={(e) =>
                      setData({ ...data, strm: { ...data.strm, proxyConcurrency: parseInt(e.target.value) || 2 } })
                    }
                  />
                </div>
              </div>
              <p className="text-xs text-muted-foreground">
                单账号代理上限按播放请求计数；分片并发会额外占用 CDN 连接。115 有风控，建议 2；带宽充足且稳定可试 3。
              </p>
            </div>
          </div>
        </details>
      </section>


      {/* 115 请求控制：QPM 分桶 + 并发 */}
      <CollapsibleSection
        title="115 请求控制"
        description="按「账号 × 用途」分桶限流，避免扫描与播放互相抢占配额。保存后立即生效。"
      >
        <div className="space-y-5">
          <div className="space-y-3">
            <h3 className="text-sm font-medium">每账号配额（每分钟）</h3>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div className="space-y-2">
                <Label>扫描与元数据</Label>
                <Input
                  type="number"
                  min="1"
                  value={data.rateLimit?.api115Qpm ?? 60}
                  onChange={(e) =>
                    setData({
                      ...data,
                      rateLimit: { ...(data.rateLimit || {}), api115Qpm: Math.max(1, parseInt(e.target.value) || 60) },
                    })
                  }
                />
                <p className="text-xs text-muted-foreground">列目录、元数据读取，默认 60</p>
              </div>
              <div className="space-y-2">
                <Label>播放取直链</Label>
                <Input
                  type="number"
                  min="1"
                  value={data.rateLimit?.playbackQpm ?? 120}
                  onChange={(e) =>
                    setData({
                      ...data,
                      rateLimit: { ...(data.rateLimit || {}), playbackQpm: Math.max(1, parseInt(e.target.value) || 120) },
                    })
                  }
                />
                <p className="text-xs text-muted-foreground">起播与 seek 取链，默认 120</p>
              </div>
              <div className="space-y-2">
                <Label>生活事件轮询</Label>
                <Input
                  type="number"
                  min="1"
                  value={data.rateLimit?.lifeQpm ?? 60}
                  onChange={(e) =>
                    setData({
                      ...data,
                      rateLimit: { ...(data.rateLimit || {}), lifeQpm: Math.max(1, parseInt(e.target.value) || 60) },
                    })
                  }
                />
                <p className="text-xs text-muted-foreground">
                  与监控页「轮询间隔」联动：10s 间隔约 6 次/分，默认 60 足够
                </p>
              </div>
            </div>
          </div>
          <div className="space-y-3 border-t pt-4">
            <h3 className="text-sm font-medium">全局与并发</h3>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div className="space-y-2">
                <Label>全局兜底上限（每分钟）</Label>
                <Input
                  type="number"
                  min="1"
                  value={data.rateLimit?.globalCapQpm ?? 300}
                  onChange={(e) =>
                    setData({
                      ...data,
                      rateLimit: { ...(data.rateLimit || {}), globalCapQpm: Math.max(1, parseInt(e.target.value) || 300) },
                    })
                  }
                />
                <p className="text-xs text-muted-foreground">多账号叠加时的风控保护，默认 300</p>
              </div>
              <div className="space-y-2">
                <Label>文件下载并发</Label>
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
                        downloadMaxConcurrent: parseInt(e.target.value) || 2,
                      },
                    })
                  }
                />
                <p className="text-xs text-muted-foreground">同时下载元数据/关联文件的并发上限，默认 2</p>
              </div>
            </div>
          </div>
        </div>
      </CollapsibleSection>

      <Separator />

      <CollapsibleSection
        title="高级维护：媒体挂载路径"
        description="由系统自动计算并维护，通常无需手动修改；需要核对时展开。"
      >
        <div className="grid grid-cols-1 gap-4">
          <div className="space-y-2 md:col-span-2">
            <div className="flex flex-col sm:flex-row sm:items-end justify-between gap-2">
              <div>
                <Label>媒体挂载路径</Label>
                <p className="text-xs text-muted-foreground mt-1">
                  根据<span className="font-medium">全局 STRM 前缀</span>、
                  <span className="font-medium">任务设置</span>和
                  <span className="font-medium">生活事件监控</span>自动收敛得到，通常无需手动修改。
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
                  暂无项。请先配置全局 STRM 前缀，或创建带自定义前缀的任务。
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
                              "px-1.5 py-0.5 rounded text-[10px] border " +
                              (row.source === "global" || row.source === "global_302"
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
                            <span className="text-[10px] px-1.5 py-0.5 rounded border bg-green-500/20 text-green-400 border-green-500/30">
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
      </CollapsibleSection>

      {/* 其他配置入口：这些配置按业务归属在其他页面，这里只做指引 */}
      <div className="text-xs text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 pt-1">
        <span>其他配置入口：</span>
        <Link to="/cleanup" className="text-primary hover:underline">清理策略</Link>
        <Link to="/notify" className="text-primary hover:underline">Emby / Telegram 通知</Link>
        <Link to="/account" className="text-primary hover:underline">Cookie 巡检</Link>
      </div>

      <div className="pt-2 flex gap-2 items-center sticky bottom-0 bg-background/95 backdrop-blur-sm py-3 -mx-3 sm:-mx-4 md:-mx-6 px-3 sm:px-4 md:px-6 border-t">
        <Button disabled={saving} onClick={onSave} className="flex-1 sm:flex-initial">
          {saving ? "保存中..." : "保存设置"}
        </Button>
      </div>
    </div>
  );
}
