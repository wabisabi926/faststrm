import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Activity, ArrowLeft, Loader2, Play, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import axiosInstance from "@/lib/axios";

interface AccountInfo {
  name: string;
  accountType: string;
  cookie?: string;
}

interface DiagnoseResult {
  account?: string;
  pickcode?: string;
  fileName?: string;
  fileSize?: number;
  userAgent?: string;
  clientIp?: string;
  privateNetwork?: boolean;
  explicitMode?: string;
  decision?: string;
  reason?: string;
  redirectCheck?: {
    attempted?: boolean;
    ok?: boolean;
    status?: number;
  };
  cdn?: {
    status?: number;
    contentType?: string;
    contentLength?: string;
    contentRange?: string;
    acceptRanges?: string;
    elapsedMs?: number;
    error?: string;
  };
}

function formatBytes(bytes?: number): string {
  if (!bytes || bytes <= 0) return "-";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let index = 0;
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024;
    index += 1;
  }
  return `${value.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

function decisionLabel(decision?: string): { label: string; className: string } {
  if (decision === "proxy") {
    return { label: "Proxy 代理流", className: "bg-amber-500/15 text-amber-600 dark:text-amber-400" };
  }
  if (decision === "redirect") {
    return { label: "Redirect 302", className: "bg-blue-500/15 text-blue-600 dark:text-blue-400" };
  }
  return { label: decision || "未判定", className: "bg-muted text-muted-foreground" };
}

export default function DiagnosePage() {
  const [accounts, setAccounts] = useState<AccountInfo[]>([]);
  const [account, setAccount] = useState("");
  const [pickcode, setPickcode] = useState("");
  const [fileName, setFileName] = useState("");
  const [ua, setUa] = useState("");
  const [mode, setMode] = useState("auto");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [details, setDetails] = useState("");
  const [result, setResult] = useState<DiagnoseResult | null>(null);

  useEffect(() => {
    const load = async () => {
      try {
        const response = await axiosInstance.get("/api/account");
        const list: AccountInfo[] = Array.isArray(response.data) ? response.data : [];
        const playable = list.filter((item) => item.accountType === "115" && item.cookie);
        setAccounts(playable);
        if (playable.length > 0) setAccount(playable[0].name);
      } catch {
        setError("加载 115 账号失败，请先确认账号已配置。");
      }
    };
    void load();
  }, []);

  const runDiagnose = async () => {
    setError("");
    setDetails("");
    setResult(null);
    if (!account) {
      setError("请选择 115 账号");
      return;
    }
    if (!/^[A-Za-z0-9]{17}$/.test(pickcode.trim())) {
      setError("pickcode 应为 17 位字母数字");
      return;
    }
    setLoading(true);
    try {
      const response = await axiosInstance.get("/api/strm/diagnose", {
        params: {
          account,
          pickcode: pickcode.trim(),
          file_name: fileName.trim() || undefined,
          ua: ua.trim() || undefined,
          mode: mode === "auto" ? undefined : mode,
        },
        timeout: 30000,
      });
      setResult(response.data as DiagnoseResult);
    } catch (err) {
      const axiosErr = err as {
        response?: { data?: { error?: string; details?: string } };
        message?: string;
      };
      setError(axiosErr.response?.data?.error || axiosErr.message || "诊断失败");
      setDetails(axiosErr.response?.data?.details || "");
    } finally {
      setLoading(false);
    }
  };

  const decision = decisionLabel(result?.decision);
  const cdnOk = typeof result?.cdn?.status === "number" && result.cdn.status >= 200 && result.cdn.status < 400;

  return (
    <div className="mx-auto max-w-5xl space-y-5">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="min-w-0">
          <h1 className="text-xl sm:text-2xl font-semibold break-words">播放诊断</h1>
          <p className="text-sm text-muted-foreground mt-1 break-words">
            检查 STRM 的实际路由、CDN 可达性与代理回退原因。
          </p>
        </div>
        <Button variant="outline" size="sm" asChild>
          <Link to="/">
            <ArrowLeft className="mr-1 h-4 w-4" />
            返回概览
          </Link>
        </Button>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="flex items-center gap-2 text-base">
              <Activity className="h-4 w-4 text-primary" />
              诊断目标
            </CardTitle>
            <p className="text-xs text-muted-foreground">
              用真实账号获取直链并复现路由决策；不会播放或下载完整文件。
            </p>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="space-y-1.5">
              <Label htmlFor="diagnose-account">115 账号</Label>
              {accounts.length === 0 ? (
                <div className="rounded-md border border-dashed p-3 text-xs text-muted-foreground">
                  暂无带 Cookie 的 115 账号，请先到{" "}
                  <Link to="/account" className="text-primary hover:underline">
                    账户
                  </Link>{" "}
                  添加。
                </div>
              ) : (
                <Select value={account} onValueChange={setAccount}>
                  <SelectTrigger id="diagnose-account">
                    <SelectValue placeholder="选择账号" />
                  </SelectTrigger>
                  <SelectContent>
                    {accounts.map((item) => (
                      <SelectItem key={item.name} value={item.name}>
                        {item.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="diagnose-pickcode">Pickcode</Label>
              <Input
                id="diagnose-pickcode"
                value={pickcode}
                onChange={(e) => setPickcode(e.target.value)}
                placeholder="17 位 pickcode"
                className="font-mono"
                maxLength={17}
              />
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="diagnose-filename">文件名（可选）</Label>
              <Input
                id="diagnose-filename"
                value={fileName}
                onChange={(e) => setFileName(e.target.value)}
                placeholder="沙丘.2024.iso"
              />
              <p className="text-xs text-muted-foreground">
                用于检查 Content-Type 与下载文件名；留空则使用云端文件名。
              </p>
            </div>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="diagnose-ua">User-Agent（可选）</Label>
                <Input
                  id="diagnose-ua"
                  value={ua}
                  onChange={(e) => setUa(e.target.value)}
                  placeholder="留空使用全局 UA"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="diagnose-mode">路由模式</Label>
                <Select value={mode} onValueChange={setMode}>
                  <SelectTrigger id="diagnose-mode">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="auto">按策略自动</SelectItem>
                    <SelectItem value="redirect">强制 Redirect</SelectItem>
                    <SelectItem value="proxy">强制 Proxy</SelectItem>
                  </SelectContent>
                </Select>
                <p className="text-xs text-muted-foreground">显式模式仅对私网客户端生效。</p>
              </div>
            </div>

            {error && (
              <div className="rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-xs text-destructive">
                <p>{error}</p>
                {details && <p className="mt-1 break-all opacity-80">{details}</p>}
              </div>
            )}

            <Button
              className="w-full"
              onClick={() => void runDiagnose()}
              disabled={loading || accounts.length === 0}
            >
              {loading ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Play className="mr-2 h-4 w-4" />}
              {loading ? "诊断中..." : "开始诊断"}
            </Button>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-3">
            <div className="flex items-center justify-between gap-2">
              <CardTitle className="text-base">诊断结果</CardTitle>
              {result && (
                <Badge className={decision.className}>{decision.label}</Badge>
              )}
            </div>
            <p className="text-xs text-muted-foreground">按当前 UA 与路由策略计算</p>
          </CardHeader>
          <CardContent className="space-y-3 text-xs">
            {!result ? (
              <div className="flex flex-col items-center justify-center py-10 text-center text-muted-foreground">
                <ShieldCheck className="mb-2 h-8 w-8 opacity-50" />
                <p>填写账号与 pickcode 后开始诊断</p>
              </div>
            ) : (
              <>
                <div className="rounded-md border bg-muted/30 p-3 space-y-1.5">
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">文件名</span>
                    <span className="min-w-0 truncate font-medium" title={result.fileName}>{result.fileName || "-"}</span>
                  </div>
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">文件大小</span>
                    <span className="font-mono">{formatBytes(result.fileSize)}</span>
                  </div>
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">客户端</span>
                    <span className="font-mono">
                      {result.clientIp || "-"} {result.privateNetwork ? "（私网）" : "（公网）"}
                    </span>
                  </div>
                </div>

                <div className="rounded-md border p-3 space-y-1.5">
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">路由原因</span>
                    <span className="text-right break-all">{result.reason || "-"}</span>
                  </div>
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">Redirect 预检</span>
                    <span className="font-mono">
                      {result.redirectCheck?.attempted
                        ? `${result.redirectCheck.ok ? "通过" : "失败"} · HTTP ${result.redirectCheck.status ?? "-"}`
                        : "未触发（proxy 或显式模式）"}
                    </span>
                  </div>
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">显式模式</span>
                    <span className="font-mono">{result.explicitMode || "未应用"}</span>
                  </div>
                </div>

                <div className="rounded-md border p-3 space-y-1.5">
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-medium">CDN 探测</span>
                    <Badge variant={cdnOk ? "default" : "destructive"} className="text-[10px]">
                      {cdnOk ? "可达" : "异常"}
                    </Badge>
                  </div>
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">HTTP 状态</span>
                    <span className="font-mono">{result.cdn?.status ?? "-"}</span>
                  </div>
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">Content-Type</span>
                    <span className="font-mono truncate max-w-[220px]" title={result.cdn?.contentType}>
                      {result.cdn?.contentType || "-"}
                    </span>
                  </div>
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">Content-Length</span>
                    <span className="font-mono">{result.cdn?.contentLength || "-"}</span>
                  </div>
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">Accept-Ranges</span>
                    <span className="font-mono">{result.cdn?.acceptRanges || "-"}</span>
                  </div>
                  <div className="flex justify-between gap-3">
                    <span className="text-muted-foreground">耗时</span>
                    <span className="font-mono">{result.cdn?.elapsedMs ?? "-"} ms</span>
                  </div>
                  {result.cdn?.error && (
                    <p className="break-all text-destructive">{result.cdn.error}</p>
                  )}
                </div>

                <div className="flex items-start gap-2 rounded-md border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
                  <ShieldCheck className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                  <span>诊断只读取 2 字节并立即关闭连接；不会返回 CDN URL 或 Cookie。</span>
                </div>
              </>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
