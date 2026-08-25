import { DataTable } from "@/components/data-table";
import { AddAccountDialog } from "./components/AddAccountDialog";
import { UpdateCookieDialog } from "./components/UpdateCookieDialog";
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
import axiosInstance from "@/lib/axios";
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
  Loader2
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

export default function AccountPage() {
  const [data, setData] = useState<Account[]>([]);
  const [statusMap, setStatusMap] = useState<Record<string, AccountStatus>>({});
  const [isLoading, setIsLoading] = useState(true);
  const [isCheckingStatus, setIsCheckingStatus] = useState(false);
  const [deleteDialogOpen, setDeleteDialogOpen] = useState<string | null>(null);
  const isMobile = useIsMobile();

  const fetchAccounts = useCallback(async () => {
    try {
      setIsLoading(true);
      const res = await axiosInstance.get("/api/account");
      setData(res.data);
    } catch {
      toast.error("获取账户列表失败");
    } finally {
      setIsLoading(false);
    }
  }, []);

  const checkStatus = useCallback(async (accountNames?: string[]) => {
    try {
      setIsCheckingStatus(true);
      const params = accountNames ? `?names=${accountNames.join(",")}` : "";
      const res = await axiosInstance.get(`/api/account/status${params}`);
      const map: Record<string, AccountStatus> = {};
      for (const s of res.data.results) {
        map[s.name] = s;
      }
      setStatusMap(map);
    } catch {
      toast.error("检测账户状态失败");
    } finally {
      setIsCheckingStatus(false);
    }
  }, []);

  const refreshAll = useCallback(async () => {
    await fetchAccounts();
  }, [fetchAccounts]);

  useEffect(() => {
    fetchAccounts();
  }, []);

  useEffect(() => {
    if (data.length > 0) {
      checkStatus();
    }
  }, [data.length]);

  const handleDelete = async (name: string) => {
    try {
      await axiosInstance.delete(`/api/account?name=${name}`);
      toast.success("删除成功");
      fetchAccounts();
    } catch {
      toast.error("删除失败");
    }
  };

  const columns: ColumnDef<Account>[] = [
    {
      accessorKey: "name",
      header: "账户名称",
      cell: ({ row }) => (
        <div className="flex items-center gap-2">
          <User className="w-4 h-4 text-muted-foreground" />
          <span className="font-medium">{row.original.name}</span>
        </div>
      ),
    },
    {
      accessorKey: "accountType",
      header: "账户类型",
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
          return (
            <div className="flex items-center gap-2">
              <Key className="w-4 h-4 text-muted-foreground" />
              <code 
                className="text-xs bg-muted px-2 py-1 rounded max-w-xs truncate block"
              >
                {cookie || "-"}
              </code>
            </div>
          );
        } else if (account.accountType === "openlist") {
          return (
            <div className="space-y-1">
              <div className="flex items-center gap-2 text-xs">
                <User className="w-3 h-3 text-muted-foreground" />
                <span className="text-muted-foreground">用户:</span>
                <code className="bg-muted px-1 rounded">{account.account}</code>
              </div>
              <div className="flex items-center gap-2 text-xs">
                <Key className="w-3 h-3 text-muted-foreground" />
                <span className="text-muted-foreground">密码:</span>
                <code className="bg-muted px-1 rounded">
                  {account.password || "********"}
                </code>
              </div>
              <div className="flex items-center gap-2 text-xs">
                <span className="text-muted-foreground">URL:</span>
                <code className="bg-muted px-1 rounded text-blue-500">
                  {account.url}
                </code>
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
        
        if (!status) {
          return (
            <div className="flex items-center gap-1.5 text-muted-foreground">
              <Loader2 className="w-3.5 h-3.5 animate-spin" />
              <span className="text-xs">检测中...</span>
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
            {/* 异常且为 115 类型时显示"更新Cookie"按钮 */}
            {status.status === "error" && account.accountType === "115" && (
              <UpdateCookieDialog
                accountName={account.name}
                onSuccess={() => {
                  fetchAccounts();
                  setTimeout(() => checkStatus([account.name]), 1000);
                }}
                trigger={
                  <Button 
                    variant="outline" 
                    size="sm" 
                    className="h-7 text-xs"
                  >
                    <RefreshCw className="w-3 h-3 mr-1" />
                    更新Cookie
                  </Button>
                }
              />
            )}
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
                  title="编辑账户"
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
              title="删除账户"
              onClick={() => setDeleteDialogOpen(account.name)}
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
      return (
        <div className="flex items-center gap-1.5 min-w-0">
          <Key className="w-3.5 h-3.5 text-muted-foreground shrink-0" />
          <code className="text-xs bg-muted px-1.5 py-0.5 rounded truncate max-w-[180px]">
            {account.cookie ? account.cookie.substring(0, 40) + "..." : "-"}
          </code>
        </div>
      );
    }
    return null;
  };

  const renderMobileCard = (account: Account) => {
    const status = statusMap[account.name];
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
              {status?.status === "error" && account.accountType === "115" && (
                <UpdateCookieDialog
                  accountName={account.name}
                  onSuccess={() => {
                    fetchAccounts();
                    setTimeout(() => checkStatus([account.name]), 1000);
                  }}
                  trigger={
                    <Button variant="outline" size="sm" className="h-6 text-xs px-2">
                      <RefreshCw className="w-3 h-3 mr-1" />
                      更新Cookie
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
                onClick={() => setDeleteDialogOpen(account.name)}
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
    <div className="mx-auto max-w-7xl space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-xl sm:text-2xl font-semibold">账户管理</h1>
          <p className="text-muted-foreground mt-1 text-sm">管理你的网盘账户信息</p>
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
                新增账户
              </Button>
            }
          />
        </div>
      </div>
      
      {data.length === 0 ? (
        <div className="text-center py-12 bg-muted/30 rounded-lg border border-border">
          <AlertCircle className="mx-auto h-12 w-12 text-muted-foreground" />
          <h3 className="mt-4 text-lg font-medium">暂无账户</h3>
          <p className="mt-2 text-muted-foreground">点击上方按钮添加你的第一个账户</p>
        </div>
      ) : isMobile ? (
        <div className="space-y-2.5">
          {data.map((account) => renderMobileCard(account))}
        </div>
      ) : (
        <DataTable columns={columns} data={data} />
      )}

      {/* Delete confirmation dialog */}
      <Dialog 
        open={!!deleteDialogOpen} 
        onOpenChange={(open) => setDeleteDialogOpen(open ? deleteDialogOpen : null)}
      >
        <DialogContent className="max-w-[95vw] sm:max-w-[425px]">
          <DialogHeader>
            <DialogTitle>确认删除</DialogTitle>
            <DialogDescription>
              你确定要删除账户 &ldquo;{deleteDialogOpen}&rdquo; 吗？此操作无法撤销。
              <br />
              <span className="text-sm text-gray-500 mt-2 block">
                账户类型: {data.find(a => a.name === deleteDialogOpen)?.accountType}
              </span>
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="gap-2">
            <Button 
              variant="outline"
              onClick={() => setDeleteDialogOpen(null)}
            >
              取消
            </Button>
            <Button
              variant="destructive"
              onClick={() => {
                if (deleteDialogOpen) {
                  handleDelete(deleteDialogOpen);
                  setDeleteDialogOpen(null);
                }
              }}
            >
              删除
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
