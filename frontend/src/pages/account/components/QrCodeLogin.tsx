import * as React from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { RefreshCw, QrCode, CheckCircle, AlertCircle, Loader2, Info } from "lucide-react";
import axiosInstance from "@/lib/axios";
import { toast } from "sonner";

// 客户端类型选项（对齐 p115client APP_TO_SSOENT）
const CLIENT_OPTIONS = [
  { value: "alipaymini", label: "支付宝小程序", recommended: true },
  { value: "wechatmini", label: "微信小程序" },
  { value: "115android", label: "115 安卓" },
  { value: "android", label: "安卓原生" },
  { value: "115ios", label: "115 iOS" },
  { value: "ios", label: "iOS 原生" },
  { value: "115ipad", label: "115 iPad" },
  { value: "tv", label: "115 TV" },
  { value: "qandroid", label: "115 管理端" },
  { value: "os_windows", label: "Windows 客户端" },
  { value: "os_mac", label: "Mac 客户端" },
  { value: "os_linux", label: "Linux 客户端" },
  { value: "harmony", label: "鸿蒙" },
  { value: "web", label: "115 网页（会踢掉网页登录）" },
];

type QrStatus = "idle" | "loading" | "waiting" | "scanned" | "success" | "expired" | "cancelled" | "error";

interface QrCodeLoginProps {
  /** 扫码成功后的回调，返回 cookie 字符串 */
  onSuccess: (cookie: string) => void;
  /** 是否自动开始（默认 false，需用户点击按钮） */
  autoStart?: boolean;
}

export function QrCodeLogin({ onSuccess, autoStart = false }: QrCodeLoginProps) {
  const [clientType, setClientType] = React.useState("alipaymini");
  const [status, setStatus] = React.useState<QrStatus>("idle");
  const [qrcodeBase64, setQrcodeBase64] = React.useState<string>("");
  const [uid, setUid] = React.useState<string>("");
  const [time, setTime] = React.useState<string>("");
  const [sign, setSign] = React.useState<string>("");
  const [errorMsg, setErrorMsg] = React.useState<string>("");
  const [countdown, setCountdown] = React.useState<number>(60);

  const pollTimerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);
  const countdownRef = React.useRef<ReturnType<typeof setInterval> | null>(null);
  const expiredRef = React.useRef<boolean>(false);

  // 清理定时器
  const clearTimers = React.useCallback(() => {
    if (pollTimerRef.current) {
      clearTimeout(pollTimerRef.current);
      pollTimerRef.current = null;
    }
    if (countdownRef.current) {
      clearInterval(countdownRef.current);
      countdownRef.current = null;
    }
  }, []);

  React.useEffect(() => {
    return () => clearTimers();
  }, [clearTimers]);

  // 获取二维码
  const fetchQrcode = React.useCallback(async (ct: string) => {
    clearTimers();
    setStatus("loading");
    setErrorMsg("");
    setQrcodeBase64("");
    expiredRef.current = false;

    try {
      const resp = await axiosInstance.get("/api/account/qrcode/token", {
        params: { clientType: ct },
      });
      if (resp.data?.success) {
        setUid(resp.data.uid);
        setTime(resp.data.time);
        setSign(resp.data.sign);
        setQrcodeBase64(resp.data.qrcodeBase64);
        setStatus("waiting");
        setCountdown(60);

        // 启动倒计时
        countdownRef.current = setInterval(() => {
          setCountdown((prev) => {
            if (prev <= 1) {
              if (countdownRef.current) clearInterval(countdownRef.current);
              if (!expiredRef.current) {
                expiredRef.current = true;
                setStatus("expired");
                setErrorMsg("二维码已过期，请刷新");
              }
              return 0;
            }
            return prev - 1;
          });
        }, 1000);

        // 开始轮询状态
        pollStatus(resp.data.uid, resp.data.time, resp.data.sign, ct);
      } else {
        throw new Error(resp.data?.error || "获取二维码失败");
      }
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : "获取二维码失败";
      setStatus("error");
      setErrorMsg(msg);
    }
  }, [clearTimers]);

  // 轮询扫码状态
  const pollStatus = React.useCallback(
    async (u: string, t: string, s: string, ct: string) => {
      if (expiredRef.current) return;

      try {
        const resp = await axiosInstance.get("/api/account/qrcode/status", {
          params: { uid: u, time: t, sign: s, clientType: ct },
        });

        if (!resp.data?.success) {
          throw new Error(resp.data?.error || "查询状态失败");
        }

        const { status: st, msg, cookie } = resp.data;

        switch (st) {
          case "waiting":
            setStatus("waiting");
            // 继续轮询（1.5 秒间隔）
            pollTimerRef.current = setTimeout(() => pollStatus(u, t, s, ct), 1500);
            break;
          case "scanned":
            setStatus("scanned");
            // 继续轮询等待确认
            pollTimerRef.current = setTimeout(() => pollStatus(u, t, s, ct), 1500);
            break;
          case "success":
            clearTimers();
            setStatus("success");
            if (cookie) {
              // 后端 status 接口已直接返回 cookie
              onSuccess(cookie);
            } else {
              // fallback：后端未返回 cookie，主动调 cookie 接口获取（不传 accountName，只拿 cookie）
              try {
                const cookieResp = await axiosInstance.post("/api/account/qrcode/cookie", {
                  uid: u,
                  clientType: ct,
                });
                if (cookieResp.data?.success && cookieResp.data.cookie) {
                  onSuccess(cookieResp.data.cookie);
                } else {
                  throw new Error(cookieResp.data?.error || "获取 Cookie 失败");
                }
              } catch (err) {
                setStatus("error");
                setErrorMsg(err instanceof Error ? err.message : "获取 Cookie 失败");
              }
            }
            break;
          case "expired":
            clearTimers();
            setStatus("expired");
            setErrorMsg("二维码已过期，请刷新");
            break;
          case "cancelled":
            clearTimers();
            setStatus("cancelled");
            setErrorMsg("用户取消登录");
            break;
          default:
            // 未知状态，继续轮询
            pollTimerRef.current = setTimeout(() => pollStatus(u, t, s, ct), 1500);
            break;
        }
      } catch (err: unknown) {
        // 网络错误时继续轮询，不立即失败
        console.error("[QrCodeLogin] Poll error:", err);
        pollTimerRef.current = setTimeout(() => pollStatus(u, t, s, ct), 3000);
      }
    },
    [clearTimers, onSuccess]
  );

  // 切换客户端类型时重新获取二维码
  const handleClientChange = (value: string) => {
    setClientType(value);
    if (status !== "idle" && status !== "loading") {
      fetchQrcode(value);
    }
  };

  // 自动开始
  React.useEffect(() => {
    if (autoStart && status === "idle") {
      fetchQrcode(clientType);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoStart]);

  return (
    <div className="space-y-4">
      {/* 客户端类型选择 */}
      <div className="space-y-2">
        <label className="text-sm font-medium">登录客户端</label>
        <Select value={clientType} onValueChange={handleClientChange} disabled={status === "loading"}>
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {CLIENT_OPTIONS.map((opt) => (
              <SelectItem key={opt.value} value={opt.value}>
                {opt.label}
                {opt.recommended && <span className="ml-2 text-xs text-green-600">推荐</span>}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <p className="text-xs text-muted-foreground">
          推荐使用支付宝/微信小程序（不会踢掉现有设备）；Web 端会踢掉已有网页登录
        </p>
      </div>

      {/* 二维码区域 */}
      {status !== "idle" && (
        <div className="flex flex-col items-center space-y-3 p-4 border rounded-lg bg-gray-50">
          {status === "loading" && (
            <div className="w-[240px] h-[240px] flex items-center justify-center">
              <Loader2 className="w-8 h-8 animate-spin text-gray-400" />
            </div>
          )}

          {(status === "waiting" || status === "scanned") && qrcodeBase64 && (
            <>
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img
                src={qrcodeBase64}
                alt="登录二维码"
                className={`w-[240px] h-[240px] ${status === "scanned" ? "opacity-50" : ""}`}
              />
              <div className="flex items-center gap-2 text-sm">
                {status === "waiting" ? (
                  <>
                    <QrCode className="w-4 h-4 text-blue-500" />
                    <span className="text-blue-600">等待扫码... {countdown}s</span>
                  </>
                ) : (
                  <>
                    <CheckCircle className="w-4 h-4 text-green-500" />
                    <span className="text-green-600">已扫码，请在手机上确认</span>
                  </>
                )}
              </div>
            </>
          )}

          {status === "success" && (
            <div className="w-[240px] h-[240px] flex flex-col items-center justify-center space-y-2">
              <CheckCircle className="w-12 h-12 text-green-500" />
              <p className="text-sm font-medium text-green-600">登录成功</p>
              <p className="text-xs text-muted-foreground">正在获取 Cookie...</p>
            </div>
          )}

          {status === "expired" && (
            <div className="w-[240px] h-[240px] flex flex-col items-center justify-center space-y-2">
              <AlertCircle className="w-12 h-12 text-orange-500" />
              <p className="text-sm font-medium text-orange-600">二维码已过期</p>
              <Button size="sm" variant="outline" onClick={() => fetchQrcode(clientType)}>
                <RefreshCw className="w-3.5 h-3.5 mr-1" />
                刷新二维码
              </Button>
            </div>
          )}

          {status === "cancelled" && (
            <div className="w-[240px] h-[240px] flex flex-col items-center justify-center space-y-2">
              <AlertCircle className="w-12 h-12 text-gray-500" />
              <p className="text-sm font-medium text-gray-600">已取消登录</p>
              <Button size="sm" variant="outline" onClick={() => fetchQrcode(clientType)}>
                <RefreshCw className="w-3.5 h-3.5 mr-1" />
                重新获取二维码
              </Button>
            </div>
          )}

          {status === "error" && (
            <div className="w-[240px] h-[240px] flex flex-col items-center justify-center space-y-2">
              <AlertCircle className="w-12 h-12 text-red-500" />
              <p className="text-sm font-medium text-red-600">获取失败</p>
              <p className="text-xs text-muted-foreground text-center max-w-[200px]">{errorMsg}</p>
              <Button size="sm" variant="outline" onClick={() => fetchQrcode(clientType)}>
                <RefreshCw className="w-3.5 h-3.5 mr-1" />
                重试
              </Button>
            </div>
          )}
        </div>
      )}

      {/* 刷新按钮（waiting 状态下显示） */}
      {(status === "waiting" || status === "scanned") && (
        <Button
          variant="outline"
          size="sm"
          className="w-full"
          onClick={() => fetchQrcode(clientType)}
        >
          <RefreshCw className="w-3.5 h-3.5 mr-2" />
          刷新二维码
        </Button>
      )}

      {/* 开始按钮（idle 状态） */}
      {status === "idle" && (
        <Button className="w-full" onClick={() => fetchQrcode(clientType)}>
          <QrCode className="w-4 h-4 mr-2" />
          获取二维码
        </Button>
      )}

      {/* 提示信息 */}
      <Alert>
        <Info className="h-4 w-4" />
        <AlertDescription className="text-xs">
          请使用 115 手机客户端扫描二维码登录。二维码 60 秒内有效，过期后可点击刷新。
        </AlertDescription>
      </Alert>
    </div>
  );
}
