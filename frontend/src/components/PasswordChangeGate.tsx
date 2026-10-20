import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { ShieldAlert, LogOut } from "lucide-react";
import { toast } from "sonner";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import axiosInstance, {
  clearToken,
  clearUsername,
  getMustChangePassword,
  setMustChangePassword,
} from "@/lib/axios";

interface PasswordChangeGateProps {
  children: React.ReactNode;
}

/**
 * 默认口令强制改密门禁。
 * 后端在所有受保护接口上返回 PASSWORD_CHANGE_REQUIRED 时，这里展示不可关闭的改密页。
 */
export default function PasswordChangeGate({ children }: PasswordChangeGateProps) {
  const location = useLocation();
  const navigate = useNavigate();
  const pathname = location.pathname;
  const isLogin = pathname === "/login";
  const hasToken = !!localStorage.getItem("auth-token");

  const [mustChange, setMustChange] = useState(() => getMustChangePassword());
  const [checking, setChecking] = useState(!isLogin && hasToken);
  const [username, setUsername] = useState("admin");
  const [currentPwd, setCurrentPwd] = useState("");
  const [newPwd, setNewPwd] = useState("");
  const [confirmPwd, setConfirmPwd] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (isLogin || !localStorage.getItem("auth-token")) {
      setChecking(false);
      return;
    }
    let cancelled = false;
    setChecking(true);
    axiosInstance
      .get("/api/auth/security-status")
      .then((res) => {
        if (cancelled) return;
        const required = !!res.data?.mustChangePassword;
        setMustChange(required);
        setMustChangePassword(required);
        if (res.data?.username) setUsername(res.data.username);
      })
      .catch(() => {
        // 状态接口失败不阻塞页面；真正的门禁由受保护接口的 403 + 拦截器兜底
      })
      .finally(() => {
        if (!cancelled) setChecking(false);
      });

    const onRequired = () => {
      setMustChange(true);
      setChecking(false);
    };
    window.addEventListener("auth:password-change-required", onRequired);
    return () => {
      cancelled = true;
      window.removeEventListener("auth:password-change-required", onRequired);
    };
  }, [isLogin, pathname]);

  if (isLogin || !hasToken) {
    return <>{children}</>;
  }

  if (checking) {
    return (
      <div className="flex items-center justify-center min-h-screen bg-background">
        <div className="text-center">
          <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary mx-auto" />
          <p className="mt-2 text-sm text-muted-foreground">检查账号安全状态...</p>
        </div>
      </div>
    );
  }

  if (!mustChange) {
    return <>{children}</>;
  }

  const handleLogout = async () => {
    try {
      await axiosInstance.post("/api/auth/logout");
    } catch {
      // 忽略登出接口错误，本地清理即可
    }
    clearToken();
    clearUsername();
    setMustChangePassword(false);
    navigate("/login");
  };

  const handleSubmit = async () => {
    setError("");
    if (!currentPwd) {
      setError("请输入当前密码");
      return;
    }
    if (newPwd.length < 6) {
      setError("新密码至少 6 位");
      return;
    }
    if (newPwd === currentPwd) {
      setError("新密码不能与当前密码相同");
      return;
    }
    if (newPwd !== confirmPwd) {
      setError("两次输入的新密码不一致");
      return;
    }

    setSaving(true);
    try {
      await axiosInstance.post("/api/auth/change-credentials", {
        currentPassword: currentPwd,
        newPassword: newPwd,
        confirmPassword: confirmPwd,
      });
      setMustChangePassword(false);
      toast.success("密码已修改，正在重新加载");
      window.location.reload();
    } catch (err: unknown) {
      const axiosErr = err as { response?: { data?: { error?: string } } } | undefined;
      setError(axiosErr?.response?.data?.error || "修改失败，请重试");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-4">
      <div className="w-full max-w-lg rounded-2xl border border-border bg-card p-6 sm:p-8 shadow-lg">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-full bg-amber-500/15 text-amber-600 dark:text-amber-400">
            <ShieldAlert className="h-5 w-5" />
          </div>
          <div>
            <h1 className="text-xl font-semibold">请先修改默认管理员密码</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              当前账号 <span className="font-medium text-foreground">{username}</span> 仍在使用出厂默认口令。
              为了避免局域网/公网被直接登录，修改密码后才能使用管理功能。
            </p>
          </div>
        </div>

        <div className="mt-6 space-y-4">
          <div className="space-y-2">
            <Label htmlFor="force-current-password">当前密码</Label>
            <Input
              id="force-current-password"
              type="password"
              value={currentPwd}
              onChange={(e) => setCurrentPwd(e.target.value)}
              placeholder="请输入当前密码"
              autoComplete="current-password"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="force-new-password">新密码</Label>
            <Input
              id="force-new-password"
              type="password"
              value={newPwd}
              onChange={(e) => setNewPwd(e.target.value)}
              placeholder="至少 6 位，不能与当前密码相同"
              autoComplete="new-password"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="force-confirm-password">确认新密码</Label>
            <Input
              id="force-confirm-password"
              type="password"
              value={confirmPwd}
              onChange={(e) => setConfirmPwd(e.target.value)}
              placeholder="再次输入新密码"
              autoComplete="new-password"
              onKeyDown={(e) => {
                if (e.key === "Enter") void handleSubmit();
              }}
            />
          </div>

          {error && (
            <p className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {error}
            </p>
          )}

          <div className="flex flex-col gap-2 sm:flex-row">
            <Button className="flex-1" disabled={saving} onClick={() => void handleSubmit()}>
              {saving ? "保存中..." : "修改密码并继续"}
            </Button>
            <Button variant="outline" disabled={saving} onClick={() => void handleLogout()}>
              <LogOut className="mr-2 h-4 w-4" />
              退出登录
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">
            修改密码后可到「账号安全」页继续修改用户名。默认口令未修改前，所有管理接口都会被服务端拒绝。
          </p>
        </div>
      </div>
    </div>
  );
}
