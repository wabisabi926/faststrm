import { DataTable } from "@/components/data-table";
import { AddAccountDialog } from "./components/AddAccountDialog";
import { UpdateCookieDialog } from "./components/UpdateCookieDialog";
import { AccountHealthPanel } from "./AccountHealthPanel";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { useIsMobile } from "@/hooks/use-mobile";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ColumnDef } from "@tanstack/react-table";
import { useEffect, useState, useCallback } from "react";
import { toast } from "sonner";
import axiosInstance, { axios } from "@/lib/axios";
import { 
  Edit, 
  Trash2, 
  Plus,
  User,
  Key,
  AlertCircle,
  CheckCircle,
  AlertTriangle,
  HelpCircle,
  RefreshCw,
  Loader2,
  Copy,
  Eye,
  EyeOff,
  ShieldCheck
} from "lucide-react";

export type Account = {
  accountType: string;
  name: string;
  cookie?: string;
  account?: string;
  password?: string;
  url?: string;
  token?: string;
  expiresAt?: number;
  lastCookieCheck?: number;  // 上次 cookie 检查时间（unix 毫秒）
  cookieValid?: boolean;      // cookie 是否有效
};

type AccountStatus = {
  name: string;
  status: "ok" | "error" | "unknown";
  message?: string;
};

// 后端 DeleteAccount 引用检查返回的引用详情（与 handler.AccountRefs JSON 对应）
type AccountRefs = {
  tasks?: string[];
  monitorAccounts?: boolean;
  monitorMappings?: number;
  dbFileRecords?: number;
  dbFolderRecords?: number;
};

type PendingDeleteRefs = {
  name: string;
  refs: AccountRefs;
};

export default function AccountPage() {
  const [data, setData] = useState<Account[]>([]);
  const [statusMap, setStatusMap] = useState<Record<string, AccountStatus>>({});
  const [isLoading, setIsLoading] = useState(true);
  const [isCheckingStatus, setIsCheckingStatus] = useState(false);
  const [deleteDialogOpen, setDeleteDialogOpen] = useState<string | null>(null);
  // 删除被引用账号时，后端返回 409 + 引用详情；非空表示当前弹窗处于「二次确认强制删除」状态
  const [pendingDeleteRefs, setPendingDeleteRefs] = useState<PendingDeleteRefs | null>(null);
  const [revealed, setRevealed] = useState<Set<string>>(new Set());
  const isMobile = useIsMobile();

  const toggleReveal = (key: string) => {
    setRevealed((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  const maskSecret = (value?: string) => {
    if (!value) return "-";
    if (value.length <= 16) return "••••••••";
    return `${value.slice(0, 8)}…${value.slice(-6)}`;
  };

  const copyText = async (text: string, label: string) => {
    if (!window.confirm(`复制${label}到剪贴板？`)) return;
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text);
      } else {
        const ta = document.createElement("textarea");
        ta.value = text;
        ta.style.position = "fixed";
        ta.style.left = "-9999px";
        document.body.appendChild(ta);
        ta.select();
        document.execCommand("copy");
        document.body.removeChild(ta);
      }
      toast.success(`${label}已复制`);
    } catch {
      toast.error(`${label}复制失败`);
    }
  };

  const fetchAccounts = useCallback(async () => {
    try {
      setIsLoading(true);
      const res = await axiosInstance.get("/api/account");
      setData(res.data);
    } catch {
      toast.error("获取账号列表失败");
    } finally {
      setIsLoading(false);
    }
  }, []);

  const checkStatus = useCallback(async (accountNames?: string[]) => {
    try {
      setIsCheckingStatus(true);
      const params = accountNames
        ? `?deep=true&names=${accountNames.join(",")}`
        : `?deep=true`;
      const res = await axiosInstance.get(`/api/account/status${params}`);
      const map: Record<string, AccountStatus> = {};
      for (const s of res.data.results) {
        map[s.name] = s;
      }
      setStatusMap(map);
    } catch {
      toast.error("检测账号状态失败");
    } finally {
      setIsCheckingStatus(false);
    }
  }, []);

  const refreshAll = useCallback(async () => {
    await fetchAccounts();
  }, [fetchAccounts]);

  useEffect(() => {
    fetchAccounts();
    // 依赖为 setState + axios 单例，引用稳定；故意不写依赖避免重复加载
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (data.length > 0) {
      checkStatus();
    }
  }, [data.length]);

  const handleDelete = async (name: string, force = false) => {
    try {
      await axiosInstance.delete(
        `/api/account?name=${encodeURIComponent(name)}${force ? "&force=1" : ""}`
      );
      toast.success("删除成功");
      setDeleteDialogOpen(null);
      setPendingDeleteRefs(null);
      fetchAccounts();
    } catch (err) {
      const data = axios.isAxiosError(err)
        ? (err.response?.data as { code?: string; refs?: AccountRefs } | undefined)
        : undefined;
      // 后端默认拒绝删除仍被引用的账号：展示引用详情，二次确认后才强制删除。
      if (data?.code === "account_in_use" && !force) {
        setPendingDeleteRefs({ name, refs: data.refs ?? {} });
        return;
      }
      toast.error("删除失败");
    }
  };

  const columns: ColumnDef<Account>[] = [
    {
      accessorKey: "name",
      header: "账号名称",
      cell: ({ row }) => (
        <div className="flex items-center gap-2">
          <User className="w-4 h-4 text-muted-foreground" />
          <span className="font-medium">{row.original.name}</span>
        </div>
      ),
    },
    {
      accessorKey: "accountType",
      header: "账号类型",
      cell: ({ row }) => (
        <Badge variant="outline" className="text-xs">
          {row.original.accountType}
        </Badge>
      ),
    },
    {
      id: "credentials",
      header: "认证信息",
      cell: ({ row }) => {
        const account = row.original;
        
        if (account.accountType === "115") {
          const cookie = account.cookie ?? "";
          const revealKey = `${account.name}:cookie`;
          const isRevealed = revealed.has(revealKey);
          return (
            <div className="flex items-center gap-1.5 min-w-0">
              <Key className="w-4 h-4 text-muted-foreground shrink-0" />
              <code className="text-xs bg-muted px-2 py-1 rounded max-w-[190px] truncate block">
                {isRevealed ? cookie || "-" : maskSecret(cookie)}
              </code>
              {cookie && (
                <>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="h-6 w-6 shrink-0"
                    onClick={() => toggleReveal(revealKey)}
                    title={isRevealed ? "隐藏 Cookie" : "显示 Cookie"}
                    aria-label={isRevealed ? "隐藏 Cookie" : "显示 Cookie"}
                  >
                    {isRevealed ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="h-6 w-6 shrink-0"
                    onClick={() => void copyText(cookie, "Cookie")}
                    title="复制 Cookie"
                    aria-label="复制 Cookie"
                  >
                    <Copy className="h-3.5 w-3.5" />
                  </Button>
                </>
              )}
            </div>
          );
        } else if (account.accountType === "openlist") {
          const revealKey = `${account.name}:password`;
          const isRevealed = revealed.has(revealKey);
          return (
            <div className="flex flex-col items-center gap-1">
              <div className="flex items-center justify-center gap-2 text-xs">
                <User className="w-3 h-3 text-muted-foreground" />
                <span className="text-muted-foreground">用户:</span>
                <code className="bg-muted px-1 rounded">{account.account}</code>
              </div>
              <div className="flex items-center justify-center gap-2 text-xs">
                <Key className="w-3 h-3 text-muted-foreground" />
                <span className="text-muted-foreground">密码:</span>
                <code className="bg-muted px-1 rounded">{isRevealed ? account.password || "-" : maskSecret(account.password)}</code>
                {account.password && (
                  <>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      className="h-6 w-6 shrink-0"
                      onClick={() => toggleReveal(revealKey)}
                      title={isRevealed ? "隐藏密码" : "显示密码"}
                      aria-label={isRevealed ? "隐藏密码" : "显示密码"}
                    >
                      {isRevealed ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      className="h-6 w-6 shrink-0"
                      onClick={() => void copyText(account.password || "", "密码")}
                      title="复制密码"
                      aria-label="复制密码"
                    >
                      <Copy className="h-3.5 w-3.5" />
                    </Button>
                  </>
                )}
              </div>
              <div className="flex items-center justify-center gap-2 text-xs">
                <span className="text-muted-foreground">URL:</span>
                <code className="bg-muted px-1 rounded text-blue-500">{account.url}</code>
                {account.url && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="h-6 w-6 shrink-0"
                    onClick={() => void copyText(account.url || "", "URL")}
                    title="复制 URL"
                    aria-label="复制 URL"
                  >
                    <Copy className="h-3.5 w-3.5" />
                  </Button>
                )}
              </div>
            </div>
          );
        }
        
        return <span className="text-muted-foreground">-</span>;
      },
    },
    {
      id: "status",
      header: () => (
        <div className="flex items-center gap-1">
          <span>状态</span>
          <Button
            variant="ghost"
            size="sm"
            className="h-6 w-6 p-0"
            onClick={() => checkStatus()}
            title="刷新状态"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${isCheckingStatus ? "animate-spin" : ""}`} />
          </Button>
        </div>
      ),
      cell: ({ row }) => {
        const account = row.original;
        const status = statusMap[account.name];

        // 更新 Cookie 入口始终可见（115 账户）：换号/重新登录后要沿用同一账户名，
        // 否则已生成的 STRM 会因「账号不存在」而无法播放。
        const updateCookieBtn =
          account.accountType === "115" ? (
            <UpdateCookieDialog
              accountName={account.name}
              onSuccess={() => {
                fetchAccounts();
                setTimeout(() => checkStatus([account.name]), 1000);
              }}
              trigger={
                <Button variant="outline" size="sm" className="h-7 text-xs">
                  <RefreshCw className="w-3 h-3 mr-1" />
                  更新 Cookie
                </Button>
              }
            />
          ) : null;

        if (!status) {
          return (
            <div className="flex flex-col gap-1">
              <div className="flex items-center gap-1.5 text-muted-foreground">
                <Loader2 className="w-3.5 h-3.5 animate-spin" />
                <span className="text-xs">检测中...</span>
              </div>
              {updateCookieBtn}
            </div>
          );
        }

        const config = {
          ok: {
            icon: <CheckCircle className="w-3.5 h-3.5 text-green-500" />,
            label: "正常",
            cls: "text-green-500",
          },
          error: {
            icon: <AlertTriangle className="w-3.5 h-3.5 text-red-500" />,
            label: "异常",
            cls: "text-red-500",
          },
          unknown: {
            icon: <HelpCircle className="w-3.5 h-3.5 text-muted-foreground" />,
            label: "未知",
            cls: "text-muted-foreground",
          },
        };

        const c = config[status.status];
        return (
          <div className="flex flex-col gap-1">
            <div className="flex items-center gap-1.5" title={status.message || c.label}>
              {c.icon}
              <span className={`text-xs font-medium ${c.cls}`}>{c.label}</span>
              {status.message && status.status === "error" && (
                <span className="text-xs text-red-400" title={status.message}>
                  ({status.message})
                </span>
              )}
            </div>
            {/* 更新 Cookie 入口始终可见（仅 115）：异常时可换 Cookie，正常时也可换号重登 */}
            {updateCookieBtn}
          </div>
        );
      },
    },
    {
      id: "actions",
      header: "操作",
      cell: ({ row }) => {
        const account = row.original;
        return (
          <div className="flex gap-1">
            <AddAccountDialog
              account={account}
              trigger={
                <Button 
                  variant="ghost" 
                  size="sm"
                  className="h-8 w-8 p-0"
                  title="编辑账号"
                >
                  <Edit className="w-4 h-4" />
                </Button>
              }
              onSuccess={fetchAccounts}
            />
            <Button
              variant="ghost"
              size="sm"
              className="h-8 w-8 p-0 text-red-500 hover:text-red-600 hover:bg-red-500/10"
              title="删除账号"
              onClick={() => {
                setPendingDeleteRefs(null);
                setDeleteDialogOpen(account.name);
              }}
            >
              <Trash2 className="w-4 h-4" />
            </Button>
          </div>
        );
      },
    },
  ];

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-600"></div>
      </div>
    );
  }

  const renderStatus = (account: Account) => {
    const status = statusMap[account.name];
    if (!status) {
      return (
        <span className="inline-flex items-center gap-1 text-muted-foreground text-xs">
          <Loader2 className="w-3 h-3 animate-spin" /> 检测中
        </span>
      );
    }
    const map = {
      ok: { icon: <CheckCircle className="w-3.5 h-3.5 text-green-500" />, label: "正常", cls: "text-green-500" },
      error: { icon: <AlertTriangle className="w-3.5 h-3.5 text-red-500" />, label: "异常", cls: "text-red-500" },
      unknown: { icon: <HelpCircle className="w-3.5 h-3.5 text-muted-foreground" />, label: "未知", cls: "text-muted-foreground" },
    } as const;
    const c = map[status.status];
    return (
      <span className={`inline-flex items-center gap-1 text-xs font-medium ${c.cls}`} title={status.message || c.label}>
        {c.icon} {c.label}
      </span>
    );
  };

  const renderCredentials = (account: Account) => {
    if (account.accountType === "115") {
      const cookie = account.cookie ?? "";
      const revealKey = `${account.name}:cookie`;
      const isRevealed = revealed.has(revealKey);
      return (
        <div className="flex items-center gap-1.5 min-w-0">
          <Key className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
          <code className="text-xs bg-muted px-1.5 py-0.5 rounded truncate max-w-[180px]">
            {isRevealed ? cookie || "-" : maskSecret(cookie)}
          </code>
          {cookie && (
            <>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="h-6 w-6 shrink-0"
                onClick={() => toggleReveal(revealKey)}
                title={isRevealed ? "隐藏 Cookie" : "显示 Cookie"}
                aria-label={isRevealed ? "隐藏 Cookie" : "显示 Cookie"}
              >
                {isRevealed ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="h-6 w-6 shrink-0"
                onClick={() => void copyText(cookie, "Cookie")}
                title="复制 Cookie"
                aria-label="复制 Cookie"
              >
                <Copy className="h-3.5 w-3.5" />
              </Button>
            </>
          )}
        </div>
      );
    }
    return null;
  };

  const renderMobileCard = (account: Account) => {
    return (
      <Card key={account.name} className="py-0 shadow-sm">
        <CardContent className="p-3 space-y-3">
          {/* Header: name + type + status */}
          <div className="flex items-start justify-between gap-2">
            <div className="flex items-center gap-2 min-w-0">
              <User className="w-4 h-4 text-muted-foreground shrink-0" />
              <span className="font-medium text-sm truncate">{account.name}</span>
            </div>
            <Badge variant="outline" className="text-xs shrink-0">
              {account.accountType}
            </Badge>
          </div>

          {/* Credentials */}
          {renderCredentials(account)}

          {/* Footer: status + actions */}
          <div className="flex items-center justify-between gap-2 pt-1 border-t border-border/50">
            <div className="flex items-center gap-2 flex-wrap">
              {renderStatus(account)}
              {account.accountType === "115" && (
                <UpdateCookieDialog
                  accountName={account.name}
                  onSuccess={() => {
                    fetchAccounts();
                    setTimeout(() => checkStatus([account.name]), 1000);
                  }}
                  trigger={
                    <Button variant="outline" size="sm" className="h-6 text-xs px-2">
                      <RefreshCw className="w-3 h-3 mr-1" />
                      更新 Cookie
                    </Button>
                  }
                />
              )}
            </div>
            <div className="flex gap-1 shrink-0">
              <AddAccountDialog
                account={account}
                trigger={
                  <Button variant="ghost" size="sm" className="h-7 w-7 p-0">
                    <Edit className="w-3.5 h-3.5" />
                  </Button>
                }
                onSuccess={fetchAccounts}
              />
              <Button
                variant="ghost"
                size="sm"
                className="h-7 w-7 p-0 text-red-500 hover:text-red-600 hover:bg-red-500/10"
                onClick={() => {
                  setPendingDeleteRefs(null);
                  setDeleteDialogOpen(account.name);
                }}
              >
                <Trash2 className="w-3.5 h-3.5" />
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>
    );
  };

  return (
    <div className="mx-auto max-w-5xl space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-xl sm:text-2xl font-semibold">账号管理</h1>
          <p className="text-muted-foreground mt-1 text-sm">管理你的网盘账号信息</p>
        </div>
        <div className="flex gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => checkStatus()}
            disabled={isCheckingStatus}
          >
            <RefreshCw className={`w-4 h-4 mr-1 ${isCheckingStatus ? "animate-spin" : ""}`} />
            {isCheckingStatus ? "检测中" : "检测状态"}
          </Button>
          <AddAccountDialog
            onSuccess={refreshAll}
            trigger={
              <Button size="sm">
                <Plus className="w-4 h-4 mr-1" />
                新增账号
              </Button>
            }
          />
        </div>
      </div>

      <div className="flex items-start gap-2 rounded-md border bg-muted/30 px-3 py-2.5 text-xs text-muted-foreground">
        <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-green-600" />
        <span>凭据默认脱敏展示；点击眼睛临时显示，复制前会二次确认，避免误粘贴到聊天或日志。</span>
      </div>

      {data.length > 0 && (
        <AccountHealthPanel
          accountNames={data.map((account) => account.name)}
          statusMap={statusMap}
        />
      )}

      {data.length === 0 ? (
        <div className="text-center py-12 bg-muted/30 rounded-lg border border-border">
          <AlertCircle className="mx-auto h-12 w-12 text-muted-foreground" />
          <h3 className="mt-4 text-base font-medium">暂无账号</h3>
          <p className="mt-2 text-muted-foreground">点击上方按钮添加你的第一个账号</p>
        </div>
      ) : isMobile ? (
        <div className="space-y-2.5">
          {data.map((account) => renderMobileCard(account))}
        </div>
      ) : (
        <DataTable columns={columns} data={data} align="center" />
      )}

      {/* Delete confirmation dialog（被引用时二次确认强制删除） */}
      <Dialog
        open={!!deleteDialogOpen}
        onOpenChange={(open) => {
          setDeleteDialogOpen(open ? deleteDialogOpen : null);
          if (!open) setPendingDeleteRefs(null);
        }}
      >
        <DialogContent className="max-w-[95vw] sm:max-w-[425px]">
          <DialogHeader>
            <DialogTitle>{pendingDeleteRefs ? "账号仍被引用" : "确认删除"}</DialogTitle>
            <DialogDescription>
              {pendingDeleteRefs
                ? `账号 "${pendingDeleteRefs.name}" 仍被以下配置引用。删除后相关 STRM 将无法播放、生活监控会持续报错。`
                : `你确定要删除账号 "${deleteDialogOpen}" 吗？此操作无法撤销。`}
              {!pendingDeleteRefs && (
                <>
                  <br />
                  <span className="text-sm text-gray-500 mt-2 block">
                    账号类型: {data.find((a) => a.name === deleteDialogOpen)?.accountType}
                  </span>
                </>
              )}
            </DialogDescription>
          </DialogHeader>
          {pendingDeleteRefs && (
            <ul className="list-disc space-y-1 pl-5 text-sm text-muted-foreground">
              {(pendingDeleteRefs.refs.tasks?.length ?? 0) > 0 && (
                <li>
                  任务 {pendingDeleteRefs.refs.tasks?.length} 个：
                  {pendingDeleteRefs.refs.tasks?.slice(0, 3).join("、")}
                  {(pendingDeleteRefs.refs.tasks?.length ?? 0) > 3 ? " 等" : ""}
                </li>
              )}
              {pendingDeleteRefs.refs.monitorAccounts && <li>生活监控账号列表</li>}
              {(pendingDeleteRefs.refs.monitorMappings ?? 0) > 0 && (
                <li>生活监控路径映射 {pendingDeleteRefs.refs.monitorMappings} 条</li>
              )}
              {(pendingDeleteRefs.refs.dbFileRecords ?? 0) > 0 && (
                <li>索引库文件记录 {pendingDeleteRefs.refs.dbFileRecords} 条</li>
              )}
              {(pendingDeleteRefs.refs.dbFolderRecords ?? 0) > 0 && (
                <li>索引库目录记录 {pendingDeleteRefs.refs.dbFolderRecords} 条</li>
              )}
            </ul>
          )}
          <DialogFooter className="gap-2">
            <Button
              variant="outline"
              onClick={() => {
                setDeleteDialogOpen(null);
                setPendingDeleteRefs(null);
              }}
            >
              取消
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                if (deleteDialogOpen) {
                  void handleDelete(deleteDialogOpen, Boolean(pendingDeleteRefs));
                }
              }}
            >
              {pendingDeleteRefs ? "仍要强制删除" : "删除"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
