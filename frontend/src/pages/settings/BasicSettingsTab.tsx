import { useState, type Dispatch, type ReactNode, type SetStateAction } from "react";
import { Link } from "react-router-dom";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ChevronDown, Settings, Filter } from "lucide-react";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { Settings as SettingsType } from "./types";

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

// 与 internal/model/settings.go 的 DefaultSettings().Strm.ForceProxyUaTokens 保持一致。
// 只用于「填入默认值」按钮与 placeholder，不参与保存逻辑（保存以输入框内容为准）。
const DEFAULT_USER_AGENT =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/116.0.5845.89 Mobile/15E148 Safari/604.1";
const DEFAULT_FORCE_PROXY_UA_TOKENS = ["Infuse", "VidHub", "SenPlayer", "SenPlayerHD"];
const DEFAULT_FORCE_PROXY_FORMATS = ["iso", "bdmv", "bdav", "dvd", "bluray"];
const DEFAULT_STRM_EXTENSIONS = [
  "mp4", "mkv", "avi", "mov", "rmvb", "webm", "flv", "m3u8",
  "mp3", "flac", "wav", "aac", "ogg", "m4a", "ts", "m2ts", "iso",
];
const DEFAULT_DOWNLOAD_EXTENSIONS = ["srt", "ass", "sub", "nfo", "jpg", "png"];

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
            <div className="flex items-center justify-between">
              <Label>User-Agent</Label>
              <button
                type="button"
                className="text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
                onClick={() => setData({ ...data, ["user-agent"]: DEFAULT_USER_AGENT })}
              >
                填入默认值
              </button>
            </div>
            <Input
              value={data["user-agent"] || ""}
              onChange={(e) =>
                setData({ ...data, ["user-agent"]: e.target.value })
              }
              placeholder="留空使用内置默认 UA"
            />
            <p className="text-xs text-muted-foreground">
              访问 115 API 和后台任务时使用的兜底 UA；播放时优先使用播放器自己的 UA。
              <span className="font-medium">留空使用内置默认 UA</span>，不会写回配置；需要显式配置时点「填入默认值」。
            </p>
          </div>
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <Label>STRM 文件扩展名</Label>
              <button
                type="button"
                className="text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
                onClick={() => setStrmExtensionsInput(DEFAULT_STRM_EXTENSIONS.join(", "))}
              >
                填入默认值
              </button>
            </div>
            <Input
              value={strmExtensionsInput}
              onChange={(e) => setStrmExtensionsInput(e.target.value)}
              placeholder={DEFAULT_STRM_EXTENSIONS.join(", ")}
            />
            <p className="text-xs text-muted-foreground">
              逗号分隔，自动添加点号前缀；BDMV/STREAM 目录内的视频流不生成 STRM。
              <span className="font-medium">留空使用内置默认 17 项</span>，不会写回配置；需要显式配置时点「填入默认值」。
            </p>
          </div>
          <div className="space-y-3 md:col-span-2">
            <div className="flex items-center justify-between">
              <Label>附带下载的元数据扩展名</Label>
              <button
                type="button"
                className="text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
                onClick={() => setDownloadExtensionsInput(DEFAULT_DOWNLOAD_EXTENSIONS.join(", "))}
              >
                填入默认值
              </button>
            </div>
            <Input
              value={downloadExtensionsInput}
              onChange={(e) => setDownloadExtensionsInput(e.target.value)}
              placeholder={DEFAULT_DOWNLOAD_EXTENSIONS.join(", ")}
            />
            <p className="text-xs text-muted-foreground">
              逗号分隔，自动添加点号前缀。<span className="font-medium">留空 = 不下载任何附带文件</span>；
              需要时点「填入默认值」（srt, ass, sub, nfo, jpg, png）。
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
          <p className="text-xs text-amber-600 dark:text-amber-500">
            修改前缀后，已有 STRM 文件里的地址不会自动更新——需要重新跑一次任务（或生活事件触发重生成）才会生效。
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

        <details className="group space-y-3 pt-2 border-t">
          <summary className="flex cursor-pointer list-none items-center gap-2">
            <span className="text-sm font-medium">高级：自定义 URL / 文件名模板</span>
            <ChevronDown className="h-4 w-4 text-muted-foreground transition-transform group-open:rotate-180" />
          </summary>
          <div className="space-y-4 pt-2">
            <div className="space-y-1.5">
              <Label htmlFor="strm-url-template">STRM URL 模板</Label>
              <Input
                id="strm-url-template"
                value={data.strm?.strmUrlTemplate || ""}
                onChange={(e) =>
                  setData({ ...data, strm: { ...(data.strm || {}), strmUrlTemplate: e.target.value } })
                }
                placeholder="{prefix}/api/strm?account={account}&pickcode={pickcode}&file_name={filename}"
              />
              <p className="text-xs text-muted-foreground">
                留空使用默认 URL 拼接。支持 <code>{"{prefix}"}</code> <code>{"{account}"}</code>{" "}
                <code>{"{pickcode}"}</code> <code>{"{filename}"}</code> <code>{"{ext}"}</code>{" "}
                <code>{"{stem}"}</code> 等变量。
              </p>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="strm-filename-template">STRM 文件名模板</Label>
              <Input
                id="strm-filename-template"
                value={data.strm?.strmFilenameTemplate || ""}
                onChange={(e) =>
                  setData({ ...data, strm: { ...(data.strm || {}), strmFilenameTemplate: e.target.value } })
                }
                placeholder="{stem}.strm"
              />
              <p className="text-xs text-muted-foreground">
                留空使用默认文件名。支持 <code>{"{filename}"}</code> <code>{"{stem}"}</code>{" "}
                <code>{"{ext}"}</code>；.iso 会自动保留 .iso.strm。
              </p>
            </div>
            <p className="text-xs text-amber-600 dark:text-amber-500">
              模板只影响新生成或重建的 STRM；已有文件需要重跑任务或重新生成才会更新。
            </p>
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
          <div className="space-y-5 pt-2">
            <p className="text-xs text-muted-foreground">
              默认 302 直连 115 CDN；命中下方「UA 标识」或「格式列表」时才走服务端代理。
            </p>

            {/* ---- 代理触发条件 ---- */}
            <div className="space-y-3">
              <h3 className="text-sm font-medium border-t pt-4">代理触发条件</h3>
              <div className="space-y-3">
                <div className="flex items-center justify-between gap-2">
                  <Label>强制代理 UA / 客户端标识</Label>
                  {/* 这个字段的空值有语义（= 关闭），所以不能靠「清空」表达「恢复默认」，
                      单独给一个填入按钮，避免用户只能手敲默认值。 */}
                  <button
                    type="button"
                    className="text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
                    onClick={() => setForceProxyUaInput(DEFAULT_FORCE_PROXY_UA_TOKENS.join(", "))}
                  >
                    填入默认值
                  </button>
                </div>
                <Input
                  value={forceProxyUaInput}
                  onChange={(e) => setForceProxyUaInput(e.target.value)}
                  placeholder={DEFAULT_FORCE_PROXY_UA_TOKENS.join(", ")}
                />
                <p className="text-xs text-muted-foreground">
                  逗号分隔，匹配 User-Agent 或 Emby 客户端名。命中即强制走服务端代理（适合 302 播不了的客户端）。
                  <span className="font-medium">留空表示「全部 302 直连」</span>
                  （关闭该功能）；需要推荐列表时点「填入默认值」。
                  下方格式列表内的原盘不受此项影响，始终按格式代理。
                </p>
              </div>
              <div className="space-y-3 rounded-md border p-3">
                <div className="flex items-center justify-between">
                  <Label>强制代理的容器/扩展名</Label>
                  <button
                    type="button"
                    className="text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
                    onClick={() => setForceProxyFormatsInput(DEFAULT_FORCE_PROXY_FORMATS.join(", "))}
                  >
                    填入默认值
                  </button>
                </div>
                <Input
                  value={forceProxyFormatsInput}
                  onChange={(e) => setForceProxyFormatsInput(e.target.value)}
                  placeholder={DEFAULT_FORCE_PROXY_FORMATS.join(", ")}
                />
                <p className="text-xs text-muted-foreground">
                  逗号分隔，大小写不敏感、点号可省略。两层共用这一份列表；
                  <span className="font-medium">留空 = 全部 302（关闭按格式代理）</span>；
                  点「填入默认值」恢复 iso, bdmv, bdav, dvd, bluray。m2ts、ts、vob 默认不代理。
                </p>
              </div>
            </div>

            {/* ---- 代理行为 ---- */}
            <div className="space-y-3">
              <h3 className="text-sm font-medium border-t pt-4">代理行为</h3>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
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
                    默认 8。同时走代理的播放数超过该值后，非原盘自动切换为 302 重定向
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
                    默认 5000。302 前会探测 CDN 是否可达，超时或失败时回退到代理
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
                    <p className="text-xs text-muted-foreground">默认 10</p>
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
                    <p className="text-xs text-muted-foreground">默认 2</p>
                  </div>
                </div>
                <p className="text-xs text-muted-foreground">
                  单账号代理上限按播放请求计数；分片并发会额外占用 CDN 连接。115 有风控，建议 2；带宽充足且稳定可试 3。
                </p>
              </div>
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
