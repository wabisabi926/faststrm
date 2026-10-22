import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import {
  Activity,
  AlertTriangle,
  Info,
  RefreshCw,
  ScrollText,
  Server,
  ShieldCheck,
  Users,
  XCircle,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import axiosInstance from "@/lib/axios";

interface ProxyStatus {
  running?: boolean;
  listenAddr?: string;
  externalPlayerEnabled?: boolean;
}

interface RuntimeStatus {
  version?: string;
  startedAt?: string;
  uptimeSeconds?: number;
  proxy?: ProxyStatus;
}

interface JournalEvent {
  id: number;
  time: string;
  level: "info" | "warn" | "error" | string;
  category: string;
  message: string;
}

interface AccountInfo {
  name: string;
  accountType: string;
  cookieStatus?: string;
  cookieValid?: boolean;
  url?: string;
}

const LEVEL_STYLE: Record<string, string> = {
  info: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
  warn: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  error: "bg-red-500/15 text-red-600 dark:text-red-400",
};

const CATEGORY_LABELS: Record<string, string> = {
  server: "服务",
  task: "任务",
  cleanup: "清理",
  account: "账号",
  notify: "通知",
  rate: "限流",
  proxy: "反代",
  settings: "设置",
};

const CATEGORY_OPTIONS = [
  { value: "all", label: "全部" },
  { value: "task", label: "任务" },
  { value: "cleanup", label: "清理" },
  { value: "account", label: "账号" },
  { value: "notify", label: "通知" },
  { value: "rate", label: "限流" },
  { value: "proxy", label: "反代" },
  { value: "settings", label: "设置" },
  { value: "server", label: "服务" },
];

function formatUptime(seconds?: number): string {
  if (!seconds || seconds < 0) return "-";
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d} 天 ${h} 小时`;
  if (h > 0) return `${h} 小时 ${m} 分钟`;
  return `${m} 分钟`;
}

function formatTime(value?: string): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export default function RuntimePage() {
  const [status, setStatus] = useState<RuntimeStatus | null>(null);
  const [journal, setJournal] = useState<JournalEvent[]>([]);
  const [accounts, setAccounts] = useState<AccountInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [category, setCategory] = useState("all");

  const load = async (isRefresh = false) => {
    if (isRefresh) setRefreshing(true);
    else setLoading(true);
    const results = await Promise.allSettled([
      axiosInstance.get("/api/runtime/status"),
      axiosInstance.get("/api/runtime/journal?limit=100"),
      axiosInstance.get("/api/account"),
    ]);
    if (results[0].status === "fulfilled") setStatus(results[0].value.data);
    if (results[1].status === "fulfilled") {
      setJournal(Array.isArray(results[1].value.data?.items) ? results[1].value.data.items : []);
    }
    if (results[2].status === "fulfilled") {
      setAccounts(Array.isArray(results[2].value.data) ? results[2].value.data : []);
    }
    setLastUpdated(new Date());
    if (isRefresh) setRefreshing(false);
    else setLoading(false);
  };

  useEffect(() => {
    void load();
    const timer = window.setInterval(() => void load(true), 10000);
    return () => window.clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const accountStats = useMemo(() => {
    let valid = 0;
    let invalid = 0;
    let unknown = 0;
    for (const account of accounts) {
      if (account.accountType === "openlist") {
        if (account.url) valid += 1;
        else invalid += 1;
        continue;
      }
      const state = account.cookieStatus || (account.cookieValid === true ? "valid" : account.cookieValid === false ? "invalid" : "unknown");
      if (state === "valid") valid += 1;
      else if (state === "invalid") invalid += 1;
      else unknown += 1;
    }
    return { valid, invalid, unknown, total: accounts.length };
  }, [accounts]);

  const proxy = status?.proxy;
  const visibleJournal = useMemo(
    () => (category === "all" ? journal : journal.filter((event) => event.category === category)),
    [journal, category],
  );

  return (
    <div className="mx-auto max-w-5xl space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold sm:text-2xl break-words">系统状态</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            当前进程、Emby 反代、账号健康与最近系统事件；任务历史请查看
            <Link to="/history" className="mx-1 underline underline-offset-4">任务记录</Link>。
          </p>
        </div>
        <div className="flex items-center gap-2">
          {lastUpdated && (
            <span className="text-xs text-muted-foreground">更新于 {lastUpdated.toLocaleTimeString("zh-CN")}</span>
          )}
          <Button variant="outline" size="sm" onClick={() => void load(true)} disabled={refreshing}>
            <RefreshCw className={`mr-2 h-4 w-4 ${refreshing ? "animate-spin" : ""}`} />
            刷新
          </Button>
        </div>
      </div>

      <div className="grid gap-4 sm:grid-cols-3">
        <Card className="h-full">
          <CardHeader className="pb-2">
            <CardDescription className="flex items-center gap-2">
              <Server className="h-4 w-4" /> 服务
            </CardDescription>
            <CardTitle className="flex items-center gap-2 text-2xl">
              {loading ? "…" : status?.version || "-"}
              <Badge className="bg-green-500/15 text-green-600 dark:text-green-400">运行中</Badge>
            </CardTitle>
          </CardHeader>
          <CardContent className="text-xs text-muted-foreground break-words">
            已运行 {formatUptime(status?.uptimeSeconds)} · 启动于 {formatTime(status?.startedAt)}
          </CardContent>
        </Card>

        <Card className="h-full">
          <CardHeader className="pb-2">
            <CardDescription className="flex items-center gap-2">
              <Activity className="h-4 w-4" /> Emby 反代
            </CardDescription>
            <CardTitle className="flex items-center gap-2 text-lg">
              <span className={`h-2.5 w-2.5 rounded-full ${proxy?.running ? "bg-green-500" : "bg-red-500"}`} />
              {proxy?.running ? "运行中" : "未运行"}
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-1 text-xs text-muted-foreground">
            <div className="break-all">监听：{proxy?.listenAddr || "-"}</div>
            <div>外部播放器：{proxy?.externalPlayerEnabled ? "已启用" : "未启用"}</div>
          </CardContent>
        </Card>

        <Card className="h-full">
          <CardHeader className="pb-2">
            <CardDescription className="flex items-center gap-2">
              <Users className="h-4 w-4" /> 网盘账号
            </CardDescription>
            <CardTitle className="text-2xl">{accountStats.total}</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
            <span className="inline-flex items-center gap-1">
              <ShieldCheck className="h-3.5 w-3.5 text-green-500" /> 正常 {accountStats.valid}
            </span>
            <span className="inline-flex items-center gap-1">
              <XCircle className="h-3.5 w-3.5 text-red-500" /> 失效 {accountStats.invalid}
            </span>
            {accountStats.unknown > 0 && (
              <span className="text-muted-foreground">未知 {accountStats.unknown}</span>
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <ScrollText className="h-4 w-4" /> 系统事件
          </CardTitle>
          <CardDescription>
            启动、任务、清理、账号、通知、限流与反代等状态事件；保留当前进程最近 500 条，重启后清空。
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-wrap gap-1.5">
            {CATEGORY_OPTIONS.map((option) => (
              <Button
                key={option.value}
                type="button"
                size="sm"
                variant={category === option.value ? "default" : "outline"}
                className="h-7 rounded-full px-3 text-xs"
                onClick={() => setCategory(option.value)}
              >
                {option.label}
              </Button>
            ))}
          </div>
          {visibleJournal.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">
              {journal.length === 0 ? "暂无系统事件" : "当前分类暂无系统事件"}
            </p>
          ) : (
            <div className="space-y-2">
              {visibleJournal.map((event) => (
                <div key={event.id} className="flex flex-col gap-1 rounded-md border px-3 py-2 text-sm sm:flex-row sm:items-center sm:gap-3">
                  <Badge className={LEVEL_STYLE[event.level] || LEVEL_STYLE.info}>
                    {event.level === "error" ? "错误" : event.level === "warn" ? "警告" : "信息"}
                  </Badge>
                  <span className="shrink-0 font-mono text-xs text-muted-foreground">{formatTime(event.time)}</span>
                  <span className="shrink-0 text-xs text-muted-foreground">
                    [{CATEGORY_LABELS[event.category] || event.category}]
                  </span>
                  <span className="min-w-0 flex-1 break-words">{event.message}</span>
                  {event.level === "error" ? (
                    <XCircle className="hidden h-4 w-4 shrink-0 text-red-500 sm:block" />
                  ) : event.level === "warn" ? (
                    <AlertTriangle className="hidden h-4 w-4 shrink-0 text-amber-500 sm:block" />
                  ) : (
                    <Info className="hidden h-4 w-4 shrink-0 text-blue-500 sm:block" />
                  )}
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
