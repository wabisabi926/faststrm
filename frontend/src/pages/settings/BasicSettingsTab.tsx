import type { Dispatch, SetStateAction } from "react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { Checkbox } from "@/components/ui/checkbox";
import { Settings } from "lucide-react";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type {
  Settings as SettingsType,
  MountDryRunData,
  MountSyncApplyData,
} from "./types";

export interface BasicSettingsTabProps {
  data: SettingsType;
  setData: Dispatch<SetStateAction<SettingsType>>;
  strmExtensionsInput: string;
  setStrmExtensionsInput: Dispatch<SetStateAction<string>>;
  downloadExtensionsInput: string;
  setDownloadExtensionsInput: Dispatch<SetStateAction<string>>;
  forceProxyUaInput: string;
  setForceProxyUaInput: Dispatch<SetStateAction<string>>;
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
    mountDryRun,
    mountDryRunLoading,
    mountSyncing,
    lastSyncApply,
    fetchMountDryRun,
    applyMountSync,
    saving,
    onSave,
  } = props;

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
              用逗号分隔，自动添加点号前缀
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

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 pt-1">
          <div className="flex items-center gap-2">
            <Checkbox
              id="global-enable-path-encoding"
              checked={!!data.enablePathEncoding}
              onCheckedChange={(checked) =>
                setData({ ...data, enablePathEncoding: checked === true })
              }
            />
            <label htmlFor="global-enable-path-encoding" className="text-sm cursor-pointer">
              URL 路径编码
            </label>
          </div>
          <div className="flex items-center gap-2">
            <Checkbox
              id="global-remove-extra"
              checked={!!data.removeExtraFiles}
              onCheckedChange={(checked) =>
                setData({ ...data, removeExtraFiles: checked === true })
              }
            />
            <label htmlFor="global-remove-extra" className="text-sm cursor-pointer">
              删除多余 STRM 文件
            </label>
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
            以下两种情况强制走 proxy：
            <span className="ml-1">
              ① UA 匹配下方标识 &nbsp; ② <b>.iso / .bdmv / .m2ts / .ts / .vob</b> 等需要精确 seek 的原盘格式
            </span>
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

            {/* T9: STRM URL 签名开关 */}
            <div className="pt-4 border-t space-y-3">
              <div className="flex items-center gap-2">
                <Checkbox
                  id="enable-token-signing"
                  checked={!!data.strm?.enableTokenSigning}
                  onCheckedChange={(checked) =>
                    setData({
                      ...data,
                      strm: { ...data.strm, enableTokenSigning: checked === true },
                    })
                  }
                />
                <label htmlFor="enable-token-signing" className="text-sm cursor-pointer">
                  启用 STRM URL 签名（HMAC-SHA256）
                </label>
              </div>
              <p className="text-xs text-muted-foreground ml-6">
                开启后，STRM 代理 URL 会带 HMAC 签名 token，防止被扫。
                首次开启时后端自动生成 secret。保持关闭 = 老 STRM 不受影响。
                {data.strm?.tokenSecret && (
                  <>
                    {" "}
                    <span className="text-emerald-600">✓ secret 已生成 ({data.strm.tokenSecret.length}字符)</span>
                  </>
                )}
              </p>
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
                        : lastSyncApply.nginx.available
                          ? "skipNginxReload=true（跳过）"
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
