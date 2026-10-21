import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import axiosInstance, { setToken, setUsername, setMustChangePassword } from "@/lib/axios";

interface SetupPageProps {
  onComplete: () => void;
}

export default function SetupPage({ onComplete }: SetupPageProps) {
  const navigate = useNavigate();
  const [username, setUsernameValue] = useState("admin");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    if (!username.trim()) {
      setError("请输入用户名");
      return;
    }
    if (password.length < 8) {
      setError("密码至少 8 位");
      return;
    }
    if (password !== confirm) {
      setError("两次输入的密码不一致");
      return;
    }
    setSaving(true);
    try {
      const resp = await axiosInstance.post("/api/setup/admin", {
        username: username.trim(),
        password,
      });
      const { token, user, mustChangePassword } = resp.data || {};
      if (token) setToken(token);
      if (user?.username) setUsername(user.username);
      setMustChangePassword(!!mustChangePassword);
      onComplete();
      navigate("/", { replace: true });
    } catch (err: unknown) {
      const axiosErr = err as { response?: { status?: number; data?: { error?: string } } } | undefined;
      if (axiosErr?.response?.status === 409) {
        onComplete();
        navigate("/login", { replace: true });
        return;
      }
      setError(axiosErr?.response?.data?.error || "初始化失败，请重试");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4">
      <div className="w-full max-w-md rounded-2xl border border-border bg-card p-8 text-card-foreground shadow-lg">
        <div className="mb-6 flex flex-col items-center">
          <img src="/logo.png" alt="FastStrm Logo" width={64} height={64} className="mb-4" />
          <h1 className="text-2xl font-bold">创建管理员账号</h1>
          <p className="mt-1 text-center text-sm text-muted-foreground">
            首次启动需要创建管理员，完成后即可登录使用。
          </p>
        </div>
        <form onSubmit={submit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="setup-username">用户名</Label>
            <Input
              id="setup-username"
              value={username}
              autoComplete="username"
              onChange={(e) => setUsernameValue(e.target.value)}
              placeholder="默认 admin"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="setup-password">密码</Label>
            <Input
              id="setup-password"
              type="password"
              value={password}
              autoComplete="new-password"
              onChange={(e) => setPassword(e.target.value)}
              placeholder="至少 8 位"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="setup-confirm">确认密码</Label>
            <Input
              id="setup-confirm"
              type="password"
              value={confirm}
              autoComplete="new-password"
              onChange={(e) => setConfirm(e.target.value)}
              placeholder="再次输入密码"
            />
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <Button type="submit" className="w-full" disabled={saving}>
            {saving ? "初始化中..." : "创建管理员并进入"}
          </Button>
          <p className="text-center text-xs leading-relaxed text-muted-foreground">
            已有安装继续使用原密码；此页面仅在首次启动时出现。
          </p>
        </form>
      </div>
    </div>
  );
}
