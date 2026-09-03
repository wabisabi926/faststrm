import { useState, useEffect } from "react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import { UserCog } from "lucide-react";
import { toast } from "sonner";
import axiosInstance, { getUsername, setUsername, clearToken, clearUsername } from "@/lib/axios";

export default function AccountSecurityPage() {
  const [currentPwd, setCurrentPwd] = useState("");
  const [newUsername, setNewUsername] = useState("");
  const [newPwd, setNewPwd] = useState("");
  const [confirmPwd, setConfirmPwd] = useState("");
  const [saving, setSaving] = useState(false);
  const [currentUsername, setCurrentUsername] = useState("admin");

  useEffect(() => {
    const savedUsername = getUsername();
    if (savedUsername) {
      setCurrentUsername(savedUsername);
    }
  }, []);

  const handleSave = async () => {
    const trimmedUsername = newUsername.trim();
    const trimmedPwd = newPwd.trim();
    const trimmedConfirm = confirmPwd.trim();

    if (!currentPwd) {
      toast.error("请输入当前密码");
      return;
    }

    const hasUsernameChange = trimmedUsername.length > 0;
    const hasPasswordChange = trimmedPwd.length > 0;

    if (!hasUsernameChange && !hasPasswordChange) {
      toast.error("请至少填写一项修改");
      return;
    }

    if (hasUsernameChange) {
      if (trimmedUsername.length < 3 || trimmedUsername.length > 32) {
        toast.error("用户名长度需在 3-32 位之间");
        return;
      }
      if (!/^[a-zA-Z_][a-zA-Z0-9_]*$/.test(trimmedUsername)) {
        toast.error("用户名只能包含字母、数字和下划线，且以字母或下划线开头");
        return;
      }
      if (/^\d+$/.test(trimmedUsername)) {
        toast.error("用户名不能为纯数字");
        return;
      }
      if (trimmedUsername === currentUsername) {
        toast.error("新用户名不能与当前用户名相同");
        return;
      }
    }

    if (hasPasswordChange) {
      if (trimmedPwd.length < 6) {
        toast.error("密码至少 6 位");
        return;
      }
      if (trimmedPwd !== trimmedConfirm) {
        toast.error("两次输入的新密码不一致");
        return;
      }
    }

    setSaving(true);
    try {
      await axiosInstance.post("/api/auth/change-credentials", {
        currentPassword: currentPwd,
        newUsername: trimmedUsername || undefined,
        newPassword: trimmedPwd || undefined,
        confirmPassword: trimmedConfirm || undefined,
      });

      toast.success("保存成功");

      if (hasUsernameChange) {
        setUsername(trimmedUsername);
        clearToken();
        clearUsername();
        window.location.href = "/login";
        return;
      }

      setCurrentPwd("");
      setNewUsername("");
      setNewPwd("");
      setConfirmPwd("");
    } catch (err: unknown) {
      const axiosErr = err as { response?: { data?: { error?: string } } } | undefined;
      const msg = axiosErr?.response?.data?.error || "保存失败";
      toast.error(msg);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="mx-auto max-w-6xl space-y-4 sm:space-y-6 pb-8">
      {/* Page Title */}
      <div className="px-1">
        <h1 className="text-xl sm:text-2xl font-semibold">账号安全</h1>
        <p className="text-sm text-muted-foreground mt-1">
          修改管理员账户的用户名和密码
        </p>
      </div>

      <section className="border rounded-md p-3 sm:p-5 space-y-5">
        <div className="flex items-center gap-2">
          <UserCog className="h-5 w-5" />
          <h2 className="text-base font-medium">修改用户名和密码</h2>
        </div>
        <p className="text-xs text-muted-foreground">
          当前用户：<span className="font-medium text-foreground">{currentUsername}</span>
        </p>
        <div className="grid gap-4 max-w-sm sm:max-w-sm">
          <div className="space-y-3">
            <Label htmlFor="currentPassword">当前密码</Label>
            <Input
              id="currentPassword"
              type="password"
              value={currentPwd}
              onChange={(e) => setCurrentPwd(e.target.value)}
              placeholder="输入当前密码"
            />
          </div>
          <div className="space-y-3">
            <Label htmlFor="newUsername">
              新用户名 <span className="text-muted-foreground font-normal text-xs">（如不修改请留空）</span>
            </Label>
            <Input
              id="newUsername"
              value={newUsername}
              onChange={(e) => setNewUsername(e.target.value)}
              placeholder="3-32 位，字母/数字/下划线"
            />
          </div>
          <div className="space-y-3">
            <Label htmlFor="newPassword">
              新密码 <span className="text-muted-foreground font-normal text-xs">（如不修改请留空）</span>
            </Label>
            <Input
              id="newPassword"
              type="password"
              value={newPwd}
              onChange={(e) => setNewPwd(e.target.value)}
              placeholder="至少 6 位"
            />
          </div>
          <div className="space-y-3">
            <Label htmlFor="confirmPassword">确认新密码</Label>
            <Input
              id="confirmPassword"
              type="password"
              value={confirmPwd}
              onChange={(e) => setConfirmPwd(e.target.value)}
              placeholder="再次输入新密码"
              disabled={!newPwd.trim()}
            />
          </div>
          <Button
            disabled={saving}
            onClick={handleSave}
            className="mt-2 w-full sm:w-auto"
          >
            {saving ? "保存中..." : "保存"}
          </Button>
        </div>
      </section>
    </div>
  );
}
