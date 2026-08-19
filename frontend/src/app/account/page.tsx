"use client";
import { DataTable } from "@/components/data-table";
import { AddAccountDialog } from "./components/AddAccountDialog";
import { UpdateCookieDialog } from "./components/UpdateCookieDialog";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
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
          const shortCookie = cookie.length > 30 ? cookie.slice(0, 30) + "..." : cookie;
          
          return (
            <div className="flex items-center gap-2">
              <Key className="w-4 h-4 text-muted-foreground" />
              <code 
                title={cookie} 
                className="text-xs bg-muted px-2 py-1 rounded max-w-xs truncate block"
              >
                {shortCookie}
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
                  {"*".repeat(Math.min(account.password?.length ?? 0, 8))}
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
            <Dialog 
              open={deleteDialogOpen === account.name} 
              onOpenChange={(open) => setDeleteDialogOpen(open ? account.name : null)}
            >
              <DialogTrigger asChild>
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-8 w-8 p-0 text-red-500 hover:text-red-600 hover:bg-red-500/10"
                  title="删除账户"
                >
                  <Trash2 className="w-4 h-4" />
                </Button>
              </DialogTrigger>
              <DialogContent className="max-w-[95vw] sm:max-w-[425px]">
                <DialogHeader>
                  <DialogTitle>确认删除</DialogTitle>
                  <DialogDescription>
                    你确定要删除账户 &ldquo;{account.name}&rdquo; 吗？此操作无法撤销。
                    <br />
                    <span className="text-sm text-gray-500 mt-2 block">
                      账户类型: {account.accountType}
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
                      handleDelete(account.name);
                      setDeleteDialogOpen(null);
                    }}
                  >
                    删除
                  </Button>
                </DialogFooter>
              </DialogContent>
            </Dialog>
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

  return (
    <div className="mx-auto max-w-7xl space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-2xl font-semibold">账户管理</h1>
          <p className="text-muted-foreground mt-1">管理你的网盘账户信息</p>
        </div>
        <div className="flex gap-2">
          <Button
            variant="outline"
            onClick={() => checkStatus()}
            disabled={isCheckingStatus}
          >
            <RefreshCw className={`w-4 h-4 mr-2 ${isCheckingStatus ? "animate-spin" : ""}`} />
            {isCheckingStatus ? "检测中..." : "检测状态"}
          </Button>
          <AddAccountDialog
            onSuccess={refreshAll}
            trigger={
              <Button>
                <Plus className="w-4 h-4 mr-2" />
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
      ) : (
        <DataTable columns={columns} data={data} />
      )}
    </div>
  );
}
