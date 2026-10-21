import { useEffect, useState } from "react";
import { Navigate, useLocation } from "react-router-dom";
import axiosInstance from "@/lib/axios";
import SetupPage from "@/pages/setup";

interface SetupGateProps {
  children: React.ReactNode;
}

type SetupState = "loading" | "required" | "ready" | "error";

/**
 * 首次启动初始化门禁：后端 config 密码为空时，所有页面先进入 Setup。
 * 初始化完成后自动放行到正常登录/页面。
 */
export default function SetupGate({ children }: SetupGateProps) {
  const location = useLocation();
  const [state, setState] = useState<SetupState>("loading");
  const [reload, setReload] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setState("loading");
    axiosInstance
      .get("/api/setup/status")
      .then((resp) => {
        if (!cancelled) setState(resp.data?.required ? "required" : "ready");
      })
      .catch(() => {
        if (!cancelled) setState("error");
      });
    return () => {
      cancelled = true;
    };
  }, [reload]);

  if (state === "loading") {
    return (
      <div className="flex min-h-screen items-center justify-center bg-background">
        <div className="text-center">
          <div className="mx-auto h-8 w-8 animate-spin rounded-full border-b-2 border-primary" />
          <p className="mt-2 text-sm text-muted-foreground">检查初始化状态...</p>
        </div>
      </div>
    );
  }

  if (state === "error") {
    return (
      <div className="flex min-h-screen items-center justify-center bg-background px-4">
        <div className="w-full max-w-md rounded-2xl border border-border bg-card p-8 text-center">
          <h1 className="text-lg font-semibold">无法连接服务</h1>
          <p className="mt-2 text-sm text-muted-foreground">
            请确认服务已启动后重试。
          </p>
          <button
            type="button"
            className="mt-4 rounded-md bg-primary px-4 py-2 text-sm text-primary-foreground"
            onClick={() => setReload((v) => v + 1)}
          >
            重试
          </button>
        </div>
      </div>
    );
  }

  if (state === "required") {
    return <SetupPage onComplete={() => setState("ready")} />;
  }

  if (location.pathname === "/setup") {
    return <Navigate to="/login" replace />;
  }

  return <>{children}</>;
}
