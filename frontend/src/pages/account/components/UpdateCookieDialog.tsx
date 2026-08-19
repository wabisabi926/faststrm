import * as React from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { RefreshCw, AlertCircle, CheckCircle } from "lucide-react";
import axiosInstance from "@/lib/axios";
import { toast } from "sonner";
import { QrCodeLogin } from "./QrCodeLogin";

interface UpdateCookieDialogProps {
  /** 要更新 Cookie 的账户名 */
  accountName: string;
  /** 触发按钮 */
  trigger?: React.ReactNode;
  /** 更新成功后的回调（通常用于刷新状态） */
  onSuccess?: () => void;
}

/**
 * 更新 Cookie 对话框（用于账户异常恢复）
 * 扫码成功后通过 PUT /api/account 更新指定账户的 Cookie 字段
 * 后端 qrcode/cookie 路由会异步触发状态重检，让 TG 恢复通知能立即发送
 */
export function UpdateCookieDialog({ accountName, trigger, onSuccess }: UpdateCookieDialogProps) {
  const [open, setOpen] = React.useState(false);
  const [updating, setUpdating] = React.useState(false);
  const [done, setDone] = React.useState(false);

  // 扫码成功回调：直接调 PUT /api/account 更新该账户的 Cookie
  const handleQrcodeSuccess = React.useCallback(
    async (cookie: string) => {
      setUpdating(true);
      try {
        // 通过 PUT /api/account 更新 Cookie 字段
        await axiosInstance.put("/api/account", {
          name: accountName,
          cookie,
        });

        toast.success("Cookie 更新成功，正在重新检测状态");
        setDone(true);

        // 延迟关闭，让用户看到成功提示
        setTimeout(() => {
          onSuccess?.();
          setOpen(false);
          setDone(false);
        }, 1500);
      } catch (err: unknown) {
        const msg = err instanceof Error ? err.message : "更新 Cookie 失败";
        toast.error(msg);
      } finally {
        setUpdating(false);
      }
    },
    [accountName, onSuccess]
  );

  return (
    <Dialog open={open} onOpenChange={(v) => {
      setOpen(v);
      if (!v) setDone(false);
    }}>
      <DialogTrigger asChild>
        {trigger ?? (
          <Button variant="outline" size="sm" className="h-7 text-xs">
            <RefreshCw className="w-3 h-3 mr-1" />
            更新Cookie
          </Button>
        )}
      </DialogTrigger>

      <DialogContent className="max-w-[95vw] sm:max-w-[500px]">
        <DialogHeader>
          <DialogTitle>更新 Cookie - {accountName}</DialogTitle>
          <DialogDescription>
            账户 Cookie 已失效，请使用 115 客户端扫码重新登录以获取新 Cookie
          </DialogDescription>
        </DialogHeader>

        {updating && (
          <Alert>
            <AlertCircle className="h-4 w-4 animate-pulse" />
            <AlertDescription>正在更新 Cookie 并重新检测状态...</AlertDescription>
          </Alert>
        )}

        {done && (
          <Alert className="border-green-200 bg-green-50">
            <CheckCircle className="h-4 w-4 text-green-600" />
            <AlertDescription className="text-green-800">
              Cookie 更新成功！正在重新检测账户状态...
            </AlertDescription>
          </Alert>
        )}

        {!done && !updating && (
          <QrCodeLogin onSuccess={handleQrcodeSuccess} autoStart={true} />
        )}
      </DialogContent>
    </Dialog>
  );
}
