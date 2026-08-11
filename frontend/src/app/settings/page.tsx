"use client";
import { useEffect, useState } from "react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { toast } from "sonner";
import { Settings, LifeBuoy, Shield, User, Lock, UserCog } from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import axiosInstance, { getUsername, setUsername, clearToken, clearUsername } from "@/lib/axios";
import { StrmCleanupCard } from "./StrmCleanupCard";

type PathMapping = {
  account?: string;
  cloudPath: string;
  localPath: string;
};

type LifeMonitorConfig = {
  enabled: boolean;
  accounts: string[];
  pollInterval: number;
  pathMappings: PathMapping[];
  removeEmptyDirs: boolean;
  eventTypes: {
    create: boolean;
    remove: boolean;
    rename: boolean;
    move: boolean;
  };
  strmPrefix?: string;
  enablePathEncoding?: boolean;
  enable302?: boolean;
  minFileSize?: number;
  firstPullMode?: "latest" | "all" | "last";
  moveMediaMode?: "recreate" | "local_move";
};

type Settings = {
  "user-agent"?: string;
  strmExtensions?: string[];
  downloadExtensions?: string[];
  mediaMountPath?: string[];
  // 全局 STRM 生成设置
  strmPrefix?: string;
  enablePathEncoding?: boolean;
  enable302?: boolean;
  removeExtraFiles?: boolean;
  emby?: {
    url?: string;
    apiKey?: string;
    notifyMediaAdded?: boolean;
    notifyMediaRemoved?: boolean;
    notifyPlayback?: boolean;
    playbackShowProgress?: boolean;
    playbackShowOverview?: boolean;
    webhookAuth?: string;
    libraryId?: string;
  };
  download?: {
    linkMaxPerSecond?: number;
    linkMaxConcurrent?: number;
    downloadMaxConcurrent?: number;
  };
  lifeMonitor?: LifeMonitorConfig;
} & Record<string, unknown>;

type MonitorState = {
  account: string;
  running: boolean;
  status: string;
  eventsProcessed: number;
  lastError?: string;
};

const DEFAULT_MONITOR_CONFIG: LifeMonitorConfig = {
  enabled: false,
  accounts: [],
  pollInterval: 30,
  pathMappings: [],
  removeEmptyDirs: true,
  eventTypes: {
    create: true,
    remove: true,
    rename: true,
    move: true,
  },
  minFileSize: 0,
  firstPullMode: "latest",
  moveMediaMode: "local_move",
};

export default function SettingsPage() {
  const [activeTab, setActiveTab] = useState<"basic" | "monitor" | "security">("basic");
  const [data, setData] = useState<Settings>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [strmExtensionsInput, setStrmExtensionsInput] = useState("");
  const [downloadExtensionsInput, setDownloadExtensionsInput] = useState("");

  // 媒体挂载路径：SSOT 管理，不再手动编辑
  type MountSourceTag = "global_302" | "task" | "life_monitor";
  type MountEntryRow = {
    prefix: string;
    source: MountSourceTag;
    sourceLabel: string;
    account?: string;
    taskId?: string;
  };
  type MountDryRunData = {
    persisted: string[];
    computed: MountEntryRow[];
    final: string[];
    diff: {
      added: string[];
      removed: string[];
      kept: string[];
      changed: boolean;
    };
  } | null;
  type MountSyncApplyData = {
    changed: boolean;
    added: string[];
    removed: string[];
    final: string[];
    nginx: { attempted: boolean; available: boolean; ok: boolean; message: string };
    error?: string;
  } | null;
  const [mountDryRun, setMountDryRun] = useState<MountDryRunData>(null);
  const [mountDryRunLoading, setMountDryRunLoading] = useState(false);
  const [mountSyncing, setMountSyncing] = useState(false);
  const [lastSyncApply, setLastSyncApply] = useState<MountSyncApplyData>(null);

  // Change password states
  const [currentPwd, setCurrentPwd] = useState("");
  const [newPwd, setNewPwd] = useState("");
  const [confirmPwd, setConfirmPwd] = useState("");
  const [changingPwd, setChangingPwd] = useState(false);

  // Change username states
  const [usernameCurrentPwd, setUsernameCurrentPwd] = useState("");
  const [newUsername, setNewUsername] = useState("");
  const [confirmUsername, setConfirmUsername] = useState("");
  const [changingUsername, setChangingUsername] = useState(false);
  const [currentUsername, setCurrentUsername] = useState("admin");

  // Life monitor states
  const [accounts, setAccounts] = useState<string[]>([]);
  const [monitorStates, setMonitorStates] = useState<MonitorState[]>([]);
  const [verifying, setVerifying] = useState(false);
  const [verifyResult, setVerifyResult] = useState<{
    success: boolean;
    message: string;
    perAccount: { account: string; success: boolean; message: string; details?: Record<string, unknown> }[];
  } | null>(null);

  // Life monitor form state
  const [monitorEnabled, setMonitorEnabled] = useState(false);
  const [selectedAccounts, setSelectedAccounts] = useState<string[]>([]);
  const [pollInterval, setPollInterval] = useState(30);
  const [pathMappings, setPathMappings] = useState<PathMapping[]>([]);
  const [newMappingAccount, setNewMappingAccount] = useState<string>("__all__");
  const [removeEmptyDirs, setRemoveEmptyDirs] = useState(true);
  const [eventTypes, setEventTypes] = useState({
    create: true,
    remove: true,
    rename: true,
    move: true,
  });
  const [minFileSizeMb, setMinFileSizeMb] = useState(""); // MB 输入框显示用
  const [firstPullMode, setFirstPullMode] = useState<"latest" | "all" | "last">("latest");
  const [moveMediaMode, setMoveMediaMode] = useState<"recreate" | "local_move">("local_move");

  // New path mapping input
  const [newCloudPath, setNewCloudPath] = useState("");
  const [newLocalPath, setNewLocalPath] = useState("");

  // Merge saved monitor states + selected but not-yet-saved accounts for display
  const displayMonitorStates: (MonitorState & { pending?: boolean })[] = (() => {
    const byAccount = new Map<string, MonitorState>();
    for (const s of monitorStates) byAccount.set(s.account, s);
    const result: (MonitorState & { pending?: boolean })[] = [];
    const seen = new Set<string>();
    for (const acc of selectedAccounts) {
      seen.add(acc);
      const saved = byAccount.get(acc);
      if (saved) {
        result.push(saved);
      } else {
        result.push({
          account: acc,
          running: false,
          status: "待保存配置",
          eventsProcessed: 0,
          pending: true,
        });
      }
    }
    for (const s of monitorStates) {
      if (!seen.has(s.account)) {
        result.push(s);
      }
    }
    return result;
  })();

  useEffect(() => {
    const loadData = async () => {
      try {
        const settingsResp = await axiosInstance.get("/api/settings");
        const settings = settingsResp.data || {};
        setData(settings);
        setStrmExtensionsInput((settings.strmExtensions || []).join(", "));
        setDownloadExtensionsInput((settings.downloadExtensions || []).join(", "));

        // Load life monitor config
        const monitor = settings.lifeMonitor || DEFAULT_MONITOR_CONFIG;
        setMonitorEnabled(monitor.enabled);
        setSelectedAccounts(monitor.accounts || []);
        setPollInterval(monitor.pollInterval || 30);
        setPathMappings(monitor.pathMappings || []);
        setRemoveEmptyDirs(monitor.removeEmptyDirs ?? true);
        setEventTypes(monitor.eventTypes || DEFAULT_MONITOR_CONFIG.eventTypes);
        const loadedMinSize = typeof monitor.minFileSize === "number" ? monitor.minFileSize : 0;
        setMinFileSizeMb(loadedMinSize > 0 ? (loadedMinSize / (1024 * 1024)).toString() : "");
        setFirstPullMode(monitor.firstPullMode || "latest");
        setMoveMediaMode(monitor.moveMediaMode || "local_move");

        // Load accounts
        const accountsResp = await axiosInstance.get("/api/account");
        setAccounts(accountsResp.data?.map?.((a: { name: string }) => a.name) || []);

        // Load monitor states
        const monitorResp = await axiosInstance.get("/api/lifeMonitor");
        setMonitorStates(monitorResp.data?.states || []);

        // 加载当前用户名
        const savedUsername = getUsername();
        if (savedUsername) {
          setCurrentUsername(savedUsername);
        }

        // 加载媒体挂载路径 dry-run 快照
        await fetchMountDryRun();
      } catch (err) {
        console.error("Failed to load settings:", err);
        toast.error("加载设置失败");
      } finally {
        setLoading(false);
      }
    };

    loadData();
  }, []);

  const fetchMountDryRun = async () => {
    setMountDryRunLoading(true);
    try {
      const resp = await axiosInstance.get("/api/mediaMountSync");
      setMountDryRun(resp.data || null);
    } catch (e) {
      console.error("Failed to fetch media mount dry-run:", e);
    } finally {
      setMountDryRunLoading(false);
    }
  };

  const applyMountSync = async () => {
    setMountSyncing(true);
    setLastSyncApply(null);
    try {
      const resp = await axiosInstance.post("/api/mediaMountSync", {});
      setLastSyncApply(resp.data || null);
      // 同步成功后刷新 dry-run 视图 + 刷新 settings（因为 mediaMountPath 被写回了）
      await Promise.all([
        fetchMountDryRun(),
        (async () => {
          try {
            const r = await axiosInstance.get("/api/settings");
            setData(r.data || {});
          } catch {
            // ignore
          }
        })(),
      ]);
      const ok = resp.data && resp.data.nginx?.ok !== false;
      toast.success(
        ok
          ? `媒体挂载路径已同步${resp.data?.changed ? "（有变更）" : "（无变化）"}`
          : `同步完成但 nginx reload 失败：${String(resp.data?.nginx?.message || "未知错误")}`
      );
    } catch (e) {
      toast.error("同步媒体挂载路径失败");
      console.error("applyMountSync failed:", e);
    } finally {
      setMountSyncing(false);
    }
  };

  const handleChangePassword = async () => {
    if (!currentPwd || !newPwd || !confirmPwd) {
      toast.error("请填写所有密码字段");
      return;
    }
    if (newPwd !== confirmPwd) {
      toast.error("两次输入的新密码不一致");
      return;
    }
    if (newPwd.length < 6) {
      toast.error("新密码至少 6 位");
      return;
    }
    setChangingPwd(true);
    try {
      await axiosInstance.post("/api/auth/change-password", {
        currentPassword: currentPwd,
        newPassword: newPwd,
      });
      toast.success("密码修改成功");
      setCurrentPwd("");
      setNewPwd("");
      setConfirmPwd("");
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : "密码修改失败";
      toast.error(msg);
    } finally {
      setChangingPwd(false);
    }
  };

  const handleChangeUsername = async () => {
    const trimmedUsername = newUsername.trim();
    if (!usernameCurrentPwd || !trimmedUsername || !confirmUsername) {
      toast.error("请填写所有字段");
      return;
    }
    if (trimmedUsername !== confirmUsername.trim()) {
      toast.error("两次输入的新用户名不一致");
      return;
    }
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
    setChangingUsername(true);
    try {
      await axiosInstance.post("/api/auth/change-username", {
        currentPassword: usernameCurrentPwd,
        newUsername: trimmedUsername,
      });
      toast.success("用户名修改成功，请重新登录");
      setUsername(trimmedUsername);
      clearToken();
      clearUsername();
      window.location.href = "/login";
    } catch (err: unknown) {
      const axiosErr = err as { response?: { data?: { error?: string } } } | undefined;
      const msg = axiosErr?.response?.data?.error || "用户名修改失败";
      toast.error(msg);
    } finally {
      setChangingUsername(false);
    }
  };

  const onSave = async () => {
    setSaving(true);
    try {
      const strmExtensions = strmExtensionsInput
        .split(",")
        .map(ext => ext.trim())
        .filter(ext => ext.length > 0)
        .map(ext => ext.startsWith(".") ? ext : `.${ext}`)
        .map(ext => ext.toLowerCase());

      const downloadExtensions = downloadExtensionsInput
        .split(",")
        .map(ext => ext.trim())
        .filter(ext => ext.length > 0)
        .map(ext => ext.startsWith(".") ? ext : `.${ext}`)
        .map(ext => ext.toLowerCase());

      // 解析 MB 输入为字节；空值或非法值视为 0（不过滤）
      const parsedMb = parseFloat(minFileSizeMb);
      const minBytes = Number.isFinite(parsedMb) && parsedMb > 0
        ? Math.floor(parsedMb * 1024 * 1024)
        : 0;

      // 注意：mediaMountPath 不在此处手工写入，由 SSOT 的 syncMediaMountPaths() 统一维护
      //       （PUT /api/settings 内部会自动触发 sync，并返回同步详情）
      const saveData = {
        ...data,
        strmExtensions,
        downloadExtensions,
        lifeMonitor: {
          enabled: monitorEnabled,
          accounts: selectedAccounts,
          pollInterval,
          pathMappings,
          removeEmptyDirs,
          eventTypes,
          minFileSize: minBytes,
          firstPullMode,
          moveMediaMode,
        },
      };

      const saveResp = await axiosInstance.put("/api/settings", saveData);
      setData(saveData);
      // 保存后自动刷新 dry-run 视图（因为全局 strmPrefix/enable302/任务/生活事件 都可能影响）
      await fetchMountDryRun();
      const syncInfo = (saveResp.data as { mediaMountSync?: { changed?: boolean; nginx?: { attempted?: boolean; ok?: boolean; message?: string } } } | undefined)?.mediaMountSync;
      const syncSummaries: string[] = [];
      if (syncInfo) {
        syncSummaries.push(syncInfo.changed ? "挂载路径：已更新" : "挂载路径：无变化");
        if (syncInfo.nginx?.attempted) {
          syncSummaries.push(syncInfo.nginx.ok ? "nginx：重载成功" : `nginx：重载失败 - ${syncInfo.nginx.message}`);
        }
      }
      toast.success(syncSummaries.length ? `保存成功（${syncSummaries.join("；")}）` : "保存成功");
    } catch (error: unknown) {
      if (error && typeof error === 'object' && 'response' in error) {
        const apiError = error as { response?: { status?: number; data?: { message?: string } } };
        if (apiError.response?.status === 409) {
          toast.error(apiError.response.data?.message || "有任务正在执行中，无法保存设置。请等待任务完成后再试。");
        } else if (apiError.response?.status === 400) {
          toast.error("保存失败：参数错误");
        } else {
          toast.error("保存失败");
        }
      } else {
        toast.error("保存失败");
      }
    } finally {
      setSaving(false);
    }
  };

  const toggleAccount = (accountName: string) => {
    setSelectedAccounts(prev =>
      prev.includes(accountName)
        ? prev.filter(a => a !== accountName)
        : [...prev, accountName]
    );
  };

  const addPathMapping = () => {
    if (!newCloudPath.trim() || !newLocalPath.trim()) {
      toast.error("请填写完整的路径映射");
      return;
    }
    setPathMappings(prev => [...prev, {
      account: newMappingAccount !== "__all__" ? newMappingAccount : undefined,
      cloudPath: newCloudPath.trim(),
      localPath: newLocalPath.trim(),
    }]);
    setNewCloudPath("");
    setNewLocalPath("");
  };

  const removePathMapping = (index: number) => {
    setPathMappings(prev => prev.filter((_, i) => i !== index));
  };

  const handleVerify = async () => {
    if (selectedAccounts.length === 0) {
      toast.error("请先选择账号");
      return;
    }
    setVerifying(true);
    setVerifyResult(null);
    try {
      const perAccount: { account: string; success: boolean; message: string; details?: Record<string, unknown> }[] = [];
      for (const account of selectedAccounts) {
        try {
          const resp = await axiosInstance.post("/api/lifeMonitor", {
            action: "verify",
            account,
          });
          perAccount.push({ account, ...resp.data });
        } catch (apiErr: unknown) {
          const msg = apiErr instanceof Error ? apiErr.message : "请求失败";
          perAccount.push({ account, success: false, message: msg });
        }
      }
      const allSuccess = perAccount.every(r => r.success);
      const successCount = perAccount.filter(r => r.success).length;
      setVerifyResult({
        success: allSuccess,
        message: allSuccess
          ? `所有 ${perAccount.length} 个账号验证通过`
          : `${successCount}/${perAccount.length} 个账号通过`,
        perAccount,
      });
      if (allSuccess) {
        toast.success("账号验证通过，生活事件已开启");
      } else {
        toast.error(`${perAccount.length - successCount} 个账号验证失败`);
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : "验证失败";
      toast.error(msg);
      setVerifyResult({
        success: false,
        message: msg,
        perAccount: [],
      });
    } finally {
      setVerifying(false);
    }
  };

  const handleStartMonitor = async () => {
    try {
      const parsedMb = parseFloat(minFileSizeMb);
      const minBytes = Number.isFinite(parsedMb) && parsedMb > 0
        ? Math.floor(parsedMb * 1024 * 1024)
        : 0;
      const resp = await axiosInstance.post("/api/lifeMonitor", {
        action: "updateConfig",
        updates: {
          enabled: monitorEnabled,
          accounts: selectedAccounts,
          pollInterval,
          pathMappings,
          removeEmptyDirs,
          eventTypes,
          minFileSize: minBytes,
          firstPullMode,
          moveMediaMode,
        },
      });
      toast.success(resp.data?.message || "监控已更新");
      const monitorResp = await axiosInstance.get("/api/lifeMonitor");
      setMonitorStates(monitorResp.data?.states || []);
    } catch {
      toast.error("启动监控失败");
      const monitorResp = await axiosInstance.get("/api/lifeMonitor");
      setMonitorStates(monitorResp.data?.states || []);
    }
  };

  const handleStopMonitor = async (account: string) => {
    try {
      await axiosInstance.post("/api/lifeMonitor", {
        action: "stop",
        account,
      });
      toast.success(`监控已停止: ${account}`);
      const monitorResp = await axiosInstance.get("/api/lifeMonitor");
      setMonitorStates(monitorResp.data?.states || []);
    } catch {
      toast.error("停止监控失败");
      const monitorResp = await axiosInstance.get("/api/lifeMonitor");
      setMonitorStates(monitorResp.data?.states || []);
    }
  };

  const handleStartAccount = async (account: string) => {
    try {
      const resp = await axiosInstance.post("/api/lifeMonitor", {
        action: "start",
        account,
      });
      toast.success(resp.data?.message || `监控已启动: ${account}`);
      const monitorResp = await axiosInstance.get("/api/lifeMonitor");
      setMonitorStates(monitorResp.data?.states || []);
    } catch (err) {
      const axiosErr = err as { response?: { data?: { error?: string }; message?: string } };
      const msg = axiosErr?.response?.data?.error || axiosErr?.message || "启动监控失败";
      toast.error(msg);
      const monitorResp = await axiosInstance.get("/api/lifeMonitor");
      setMonitorStates(monitorResp.data?.states || []);
    }
  };

  if (loading) return <div>Loading...</div>;

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      {/* Page Title */}
      <div>
        <h1 className="text-2xl font-semibold">设置</h1>
        <p className="text-sm text-muted-foreground mt-1">
          配置全局选项、生活事件监控及安全
        </p>
      </div>

      {/* Tab Bar */}
      <div className="flex gap-1 border-b border-border">
        <button
          onClick={() => setActiveTab("basic")}
          className={`px-4 py-2 text-sm font-medium transition-colors relative ${
            activeTab === "basic"
              ? "text-foreground"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          <Settings className="inline-block h-4 w-4 mr-1" />
          基础设置
          {activeTab === "basic" && (
            <span className="absolute bottom-0 left-0 right-0 h-0.5 bg-foreground" />
          )}
        </button>
        <button
          onClick={() => setActiveTab("monitor")}
          className={`px-4 py-2 text-sm font-medium transition-colors relative ${
            activeTab === "monitor"
              ? "text-foreground"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          <LifeBuoy className="inline-block h-4 w-4 mr-1" />
          生活事件
          {activeTab === "monitor" && (
            <span className="absolute bottom-0 left-0 right-0 h-0.5 bg-foreground" />
          )}
        </button>
        <button
          onClick={() => setActiveTab("security")}
          className={`px-4 py-2 text-sm font-medium transition-colors relative ${
            activeTab === "security"
              ? "text-foreground"
              : "text-muted-foreground hover:text-foreground"
          }`}
        >
          <Shield className="inline-block h-4 w-4 mr-1" />
          清理与安全
          {activeTab === "security" && (
            <span className="absolute bottom-0 left-0 right-0 h-0.5 bg-foreground" />
          )}
        </button>
      </div>

      {/* Tab 1: 基础设置 */}
      {activeTab === "basic" && (
        <div className="space-y-6">
          <section className="space-y-4">
            <h2 className="text-base font-medium">基础设置</h2>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div className="space-y-2">
                <Label>User-Agent</Label>
                <Input
                  value={data["user-agent"] || ""}
                  onChange={(e) =>
                    setData({ ...data, ["user-agent"]: e.target.value })
                  }
                  placeholder="Mozilla/5.0 ..."
                />
              </div>
              <div className="space-y-2">
                <Label>Strm文件扩展名</Label>
                <Input
                  value={strmExtensionsInput}
                  onChange={(e) => setStrmExtensionsInput(e.target.value)}
                  placeholder="请输入 例如：.mkv, .mp4, .mp3"
                />
                <p className="text-xs text-muted-foreground">
                  用逗号分隔，自动添加点号前缀
                </p>
              </div>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div className="space-y-2">
                <Label>下载文件扩展名</Label>
                <Input
                  value={downloadExtensionsInput}
                  onChange={(e) => setDownloadExtensionsInput(e.target.value)}
                  placeholder="请输入 例如：.srt, .ass, .sub, .nfo"
                />
                <p className="text-xs text-muted-foreground">
                  用逗号分隔，自动添加点号前缀
                </p>
              </div>
            </div>
          </section>

          <Separator />

          <section className="space-y-4">
            <h2 className="text-base font-medium">STRM 生成设置（全局默认）</h2>
            <div className="grid grid-cols-1 gap-4">
              <div className="space-y-2">
                <Label>Strm 前缀</Label>
                <Input
                  value={data.strmPrefix || ""}
                  onChange={(e) =>
                    setData({ ...data, strmPrefix: e.target.value })
                  }
                  placeholder="http://localhost:3000"
                />
                <p className="text-xs text-muted-foreground">
                  STRM 文件内容的前缀，如 Emby/Jellyfin 的 HTTP 访问地址。302 模式下自动追加 <code>/api/strm</code>，无需手动添加。
                </p>
              </div>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              <div className="flex items-center gap-2">
                <Checkbox
                  id="global-enable-302"
                  checked={!!data.enable302}
                  onCheckedChange={(checked) =>
                    setData({ ...data, enable302: checked === true })
                  }
                />
                <label htmlFor="global-enable-302" className="text-sm cursor-pointer leading-tight">
                  302 重定向<span className="text-xs text-muted-foreground">（生成带 pickcode 的 STRM）</span>
                </label>
              </div>
              <div className="flex items-center gap-2">
                <Checkbox
                  id="global-enable-path-encoding"
                  checked={!!data.enablePathEncoding}
                  onCheckedChange={(checked) =>
                    setData({ ...data, enablePathEncoding: checked === true })
                  }
                />
                <label htmlFor="global-enable-path-encoding" className="text-sm cursor-pointer">
                  URL 路径编码
                </label>
              </div>
              <div className="flex items-center gap-2">
                <Checkbox
                  id="global-remove-extra"
                  checked={!!data.removeExtraFiles}
                  onCheckedChange={(checked) =>
                    setData({ ...data, removeExtraFiles: checked === true })
                  }
                />
                <label htmlFor="global-remove-extra" className="text-sm cursor-pointer">
                  删除多余本地 STRM 文件
                </label>
              </div>
            </div>
            <p className="text-xs text-muted-foreground">
              以上为全局默认值，适用于所有账号的生活事件监控和全量扫描。任务级可单独覆盖 302 和前缀，路径编码统一受全局控制。
            </p>
          </section>

          <Separator />

          <section className="space-y-4">
            <h2 className="text-base font-medium">下载限流配置</h2>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div className="space-y-2">
                <Label>链接获取每秒请求数 (linkMaxPerSecond)</Label>
                <Input
                  type="number"
                  min="1"
                  max="100"
                  value={data.download?.linkMaxPerSecond || 2}
                  onChange={(e) =>
                    setData({
                      ...data,
                      download: { 
                        ...(data.download || {}), 
                        linkMaxPerSecond: parseInt(e.target.value) || 2 
                      },
                    })
                  }
                  placeholder="2"
                />
                <p className="text-xs text-muted-foreground">
                  控制获取下载链接的每秒请求数
                </p>
              </div>
              <div className="space-y-2">
                <Label>链接获取并发数 (linkMaxConcurrent)</Label>
                <Input
                  type="number"
                  min="1"
                  max="50"
                  value={data.download?.linkMaxConcurrent || 10}
                  onChange={(e) =>
                    setData({
                      ...data,
                      download: { 
                        ...(data.download || {}), 
                        linkMaxConcurrent: parseInt(e.target.value) || 10 
                      },
                    })
                  }
                  placeholder="10"
                />
                <p className="text-xs text-muted-foreground">
                  控制同时获取下载链接的数量
                </p>
              </div>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div className="space-y-2">
                <Label>文件下载并发数 (downloadMaxConcurrent)</Label>
                <Input
                  type="number"
                  min="1"
                  max="50"
                  value={data.download?.downloadMaxConcurrent || 2}
                  onChange={(e) =>
                    setData({
                      ...data,
                      download: { 
                        ...(data.download || {}), 
                        downloadMaxConcurrent: parseInt(e.target.value) || 2 
                      },
                    })
                  }
                  placeholder="2"
                />
                <p className="text-xs text-muted-foreground">
                  控制同时下载文件的数量
                </p>
              </div>
            </div>
          </section>

          <Separator />

          <section className="space-y-4">
            <h2 className="text-base font-medium">媒体挂载路径</h2>
            <div className="grid grid-cols-1 gap-4">
              <div className="space-y-2 md:col-span-2">
                <div className="flex flex-wrap items-end justify-between gap-2">
                  <div>
                    <Label>媒体挂载路径 (mediaMountPath)</Label>
                    <p className="text-xs text-muted-foreground mt-1">
                      由系统自动计算并维护（唯一事实来源 SSOT）：根据<span className="font-medium">全局 302 × 账号集</span>、
                      <span className="font-medium">每个任务的 STRM 设置</span>、
                      <span className="font-medium">生活事件监控</span> 全量收敛得到。
                      不建议手工修改 settings.json。
                    </p>
                  </div>
                  <div className="flex items-center gap-2">
                    <Button
                      variant="outline"
                      size="sm"
                      type="button"
                      onClick={fetchMountDryRun}
                      disabled={mountDryRunLoading || mountSyncing}
                    >
                      {mountDryRunLoading ? "计算中..." : "刷新视图"}
                    </Button>
                    <Button
                      variant="default"
                      size="sm"
                      type="button"
                      onClick={applyMountSync}
                      disabled={mountSyncing || !mountDryRun?.diff.changed}
                    >
                      {mountSyncing ? "同步中..." : "立即同步并持久化"}
                    </Button>
                  </div>
                </div>

                <div className="rounded-md border p-3 space-y-3 bg-muted/30">
                  {mountDryRunLoading && !mountDryRun ? (
                    <p className="text-sm text-muted-foreground">正在计算期望集合...</p>
                  ) : mountDryRun && mountDryRun.computed.length === 0 ? (
                    <p className="text-sm text-muted-foreground">
                      暂无项。请先在上方配置 STRM 前缀和 302 选项，或创建带自定义前缀的任务。
                    </p>
                  ) : mountDryRun ? (
                    <>
                      <div className="flex flex-wrap gap-2 text-xs">
                        <span className="px-2 py-0.5 rounded bg-background border">
                          共 <b>{mountDryRun.computed.length}</b> 条期望
                        </span>
                        {mountDryRun.diff.changed ? (
                          <>
                            {mountDryRun.diff.added.length > 0 && (
                              <span className="px-2 py-0.5 rounded border bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-400">
                                +{mountDryRun.diff.added.length} 待新增
                              </span>
                            )}
                            {mountDryRun.diff.removed.length > 0 && (
                              <span className="px-2 py-0.5 rounded border bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-400">
                                -{mountDryRun.diff.removed.length} 待删除
                              </span>
                            )}
                          </>
                        ) : (
                          <span className="px-2 py-0.5 rounded border bg-background text-muted-foreground">
                            与 settings.json 一致，无差异
                          </span>
                        )}
                        <span className="px-2 py-0.5 rounded border bg-background text-muted-foreground">
                          已持久化 {mountDryRun.persisted.length} 条
                        </span>
                      </div>

                      {mountDryRun.diff.removed.length > 0 && (
                        <details className="text-xs">
                          <summary className="cursor-pointer text-red-600 dark:text-red-400">
                            以下 {mountDryRun.diff.removed.length} 条在 settings.json 中存在，但已不再被任何引用方需要
                          </summary>
                          <ul className="mt-2 space-y-1 pl-4 list-disc font-mono break-all">
                            {mountDryRun.diff.removed.map((p) => (
                              <li key={`rm-${p}`}>{p}</li>
                            ))}
                          </ul>
                        </details>
                      )}

                      <ul className="space-y-2 text-sm">
                        {mountDryRun.computed.map((row) => {
                          const added = mountDryRun.diff.added.includes(row.prefix);
                          return (
                            <li
                              key={row.prefix}
                              className="flex flex-wrap items-center gap-2 rounded border bg-background px-3 py-2"
                            >
                              <span className="font-mono break-all flex-1 min-w-0">{row.prefix}</span>
                              <span
                                className={
                                  "px-1.5 py-0.5 rounded text-[11px] border " +
                                  (row.source === "global_302"
                                    ? "bg-indigo-50 text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-300 border-indigo-200"
                                    : row.source === "task"
                                      ? "bg-sky-50 text-sky-700 dark:bg-sky-900/30 dark:text-sky-300 border-sky-200"
                                      : "bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300 border-amber-200")
                                }
                              >
                                {row.sourceLabel}
                              </span>
                              {row.account && (
                                <span className="text-xs text-muted-foreground">
                                  账号：<b>{row.account}</b>
                                </span>
                              )}
                              {row.taskId && (
                                <span className="text-xs text-muted-foreground font-mono">
                                  task #{row.taskId.slice(0, 8)}
                                </span>
                              )}
                              {added && (
                                <span className="text-[11px] px-1.5 py-0.5 rounded border bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-300 border-green-200">
                                  待新增
                                </span>
                              )}
                            </li>
                          );
                        })}
                      </ul>
                    </>
                  ) : (
                    <p className="text-sm text-muted-foreground">未加载数据</p>
                  )}

                  {lastSyncApply && (
                    <div
                      className={
                        "rounded border px-3 py-2 text-xs " +
                        (lastSyncApply.error || lastSyncApply.nginx?.ok === false
                          ? "bg-amber-50 border-amber-200 text-amber-800 dark:bg-amber-900/20 dark:text-amber-300"
                          : "bg-slate-50 border-slate-200 text-slate-700 dark:bg-slate-900/30 dark:text-slate-300")
                      }
                    >
                      <div className="font-medium mb-1">最近一次同步结果</div>
                      <ul className="list-disc pl-4 space-y-0.5">
                        <li>变更：{lastSyncApply.changed ? "已写入" : "无变化"}</li>
                        {lastSyncApply.added.length > 0 && (
                          <li>新增 {lastSyncApply.added.length} 条：<span className="font-mono">{lastSyncApply.added.join(", ")}</span></li>
                        )}
                        {lastSyncApply.removed.length > 0 && (
                          <li>删除 {lastSyncApply.removed.length} 条：<span className="font-mono">{lastSyncApply.removed.join(", ")}</span></li>
                        )}
                        <li>
                          nginx：
                          {lastSyncApply.nginx.attempted
                            ? lastSyncApply.nginx.ok
                              ? "已成功 reload"
                              : `reload 失败 - ${lastSyncApply.nginx.message}`
                            : lastSyncApply.nginx.available
                              ? "skipNginxReload=true（跳过）"
                              : "系统未检测到 nginx"}
                        </li>
                        {lastSyncApply.error && <li>错误：{lastSyncApply.error}</li>}
                      </ul>
                    </div>
                  )}
                </div>
              </div>
            </div>
          </section>

          <div className="pt-2 flex gap-2 items-center">
            <Button disabled={saving} onClick={onSave}>
              {saving ? "保存中..." : "保存设置"}
            </Button>
          </div>
        </div>
      )}

      {/* Tab 2: 生活事件 */}
      {activeTab === "monitor" && (
        <div className="space-y-6">
          <section className="space-y-4">
            <div className="flex items-center justify-between">
              <h2 className="text-base font-medium">115 生活事件监控</h2>
              <div className="flex items-center gap-2">
                <Checkbox
                  id="monitor-enabled"
                  checked={monitorEnabled}
                  onCheckedChange={(checked) => setMonitorEnabled(checked === true)}
                />
                <label htmlFor="monitor-enabled" className="text-sm cursor-pointer">
                  启用监控
                </label>
              </div>
            </div>
            <p className="text-sm text-muted-foreground">
              监控 115 网盘的文件操作事件（上传、删除、移动、重命名），自动生成或删除本地 STRM 文件
            </p>

            <div className={`space-y-4 ${!monitorEnabled ? "opacity-50 pointer-events-none" : ""}`}>
              {/* Account Selection */}
              <div className="space-y-2">
                <Label>监控账号</Label>
                <div className="flex flex-wrap gap-4 p-3 border rounded-md">
                  {accounts.length === 0 ? (
                    <p className="text-sm text-muted-foreground">暂无可用账号，请先在账号管理中添加 115 账号</p>
                  ) : (
                    accounts.map(account => (
                      <div key={account} className="flex items-center gap-2">
                        <Checkbox
                          id={`acc-${account}`}
                          checked={selectedAccounts.includes(account)}
                          onCheckedChange={() => toggleAccount(account)}
                        />
                        <label htmlFor={`acc-${account}`} className="text-sm cursor-pointer">
                          {account}
                        </label>
                      </div>
                    ))
                  )}
                </div>
              </div>

              {/* Poll Interval */}
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <div className="space-y-2">
                  <Label>轮询间隔（秒）</Label>
                  <Input
                    type="number"
                    min="5"
                    max="300"
                    value={pollInterval}
                    onChange={(e) => setPollInterval(parseInt(e.target.value) || 30)}
                  />
                  <p className="text-xs text-muted-foreground">
                    建议 30-60 秒，太频繁可能触发限流
                  </p>
                </div>
              </div>

              {/* Event Types */}
              <div className="space-y-2">
                <Label>处理的事件类型</Label>
                <div className="flex flex-wrap gap-4 p-3 border rounded-md">
                  <div className="flex items-center gap-2">
                    <Checkbox
                      id="evt-create"
                      checked={eventTypes.create}
                      onCheckedChange={(checked) =>
                        setEventTypes(prev => ({ ...prev, create: checked === true }))
                      }
                    />
                    <label htmlFor="evt-create" className="text-sm cursor-pointer">
                      新建/上传（生成 STRM）
                    </label>
                  </div>
                  <div className="flex items-center gap-2">
                    <Checkbox
                      id="evt-remove"
                      checked={eventTypes.remove}
                      onCheckedChange={(checked) =>
                        setEventTypes(prev => ({ ...prev, remove: checked === true }))
                      }
                    />
                    <label htmlFor="evt-remove" className="text-sm cursor-pointer">
                      删除（移除 STRM）
                    </label>
                  </div>
                  <div className="flex items-center gap-2">
                    <Checkbox
                      id="evt-rename"
                      checked={eventTypes.rename}
                      onCheckedChange={(checked) =>
                        setEventTypes(prev => ({ ...prev, rename: checked === true }))
                      }
                    />
                    <label htmlFor="evt-rename" className="text-sm cursor-pointer">
                      重命名
                    </label>
                  </div>
                  <div className="flex items-center gap-2">
                    <Checkbox
                      id="evt-move"
                      checked={eventTypes.move}
                      onCheckedChange={(checked) =>
                        setEventTypes(prev => ({ ...prev, move: checked === true }))
                      }
                    />
                    <label htmlFor="evt-move" className="text-sm cursor-pointer">
                      移动
                    </label>
                  </div>
                </div>
              </div>

              {/* Remove Empty Dirs */}
              <div className="flex items-center gap-2">
                <Checkbox
                  id="remove-empty"
                  checked={removeEmptyDirs}
                  onCheckedChange={(checked) => setRemoveEmptyDirs(checked === true)}
                />
                <label htmlFor="remove-empty" className="text-sm cursor-pointer">
                  删除文件后自动清理空父目录
                </label>
              </div>

              {/* Min File Size */}
              <div className="space-y-2">
                <Label>最小文件大小（MB）</Label>
                <Input
                  type="number"
                  min="0"
                  step="0.1"
                  placeholder="留空或 0 表示不过滤"
                  value={minFileSizeMb}
                  onChange={(e) => {
                    setMinFileSizeMb(e.target.value);
                  }}
                  className="max-w-xs"
                />
                <p className="text-xs text-muted-foreground">
                  小于此阈值的文件跳过 STRM 生成（如封面、NFO 等小文件）。0 表示不过滤。
                </p>
              </div>

              {/* First Pull Mode */}
              <div className="space-y-2">
                <Label>首次拉取模式</Label>
                <Select
                  value={firstPullMode}
                  onValueChange={(v: "latest" | "all" | "last") => setFirstPullMode(v)}
                >
                  <SelectTrigger className="w-full max-w-xs">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="latest">从当前时间开始（推荐）</SelectItem>
                    <SelectItem value="all">拉取全部历史事件</SelectItem>
                    <SelectItem value="last">从上次断点继续</SelectItem>
                  </SelectContent>
                </Select>
                <p className="text-xs text-muted-foreground">
                  首次启动监控时的拉取范围。<strong>latest</strong>：只处理新事件，最轻量；
                  <strong>all</strong>：拉取所有历史事件（适合首次部署补历史，耗时较长）；
                  <strong>last</strong>：从上次保存的游标继续，无断点时退化为 latest。
                </p>
              </div>

              {/* Move Media Mode */}
              <div className="space-y-2">
                <Label>移动事件处理模式</Label>
                <Select
                  value={moveMediaMode}
                  onValueChange={(v: "recreate" | "local_move") => setMoveMediaMode(v)}
                >
                  <SelectTrigger className="w-full max-w-xs">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="local_move">本地移动 STRM（推荐）</SelectItem>
                    <SelectItem value="recreate">删除旧 STRM 并重新生成</SelectItem>
                  </SelectContent>
                </Select>
                <p className="text-xs text-muted-foreground">
                  文件被移动时的处理策略。<strong>local_move</strong>：本地直接 rename STRM 文件，速度快；
                  <strong>recreate</strong>：删除旧 STRM 后用新 pickcode 重新生成，更可靠但需调用 115 API。
                </p>
              </div>

              {/* Path Mappings */}
              <div className="space-y-2">
                <Label>路径映射（115 网盘路径 → 本地保存路径）</Label>
                <div className="space-y-2">
                  {pathMappings.map((mapping, index) => (
                    <div key={index} className="flex gap-2 items-center">
                      <Select
                        value={mapping.account || "__all__"}
                        onValueChange={(val) => {
                          const updated = [...pathMappings];
                          updated[index] = { ...updated[index], account: val === "__all__" ? undefined : val };
                          setPathMappings(updated);
                        }}
                      >
                        <SelectTrigger className="w-[140px]">
                          <SelectValue placeholder="全部账号" />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="__all__">全部账号</SelectItem>
                          {accounts.map(acc => (
                            <SelectItem key={acc} value={acc}>{acc}</SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                      <Input
                        value={mapping.cloudPath}
                        onChange={(e) => {
                          const updated = [...pathMappings];
                          updated[index] = { ...updated[index], cloudPath: e.target.value };
                          setPathMappings(updated);
                        }}
                        placeholder="115 网盘路径，如 /电影"
                        className="flex-1"
                      />
                      <span className="text-muted-foreground">→</span>
                      <Input
                        value={mapping.localPath}
                        onChange={(e) => {
                          const updated = [...pathMappings];
                          updated[index] = { ...updated[index], localPath: e.target.value };
                          setPathMappings(updated);
                        }}
                        placeholder="本地路径，如/app/data/media/电影"
                        className="flex-1"
                      />
                      <Button
                        variant="destructive"
                        size="sm"
                        onClick={() => removePathMapping(index)}
                      >
                        删除
                      </Button>
                    </div>
                  ))}
                </div>
                <div className="flex gap-2 items-center">
                  <Select
                    value={newMappingAccount}
                    onValueChange={setNewMappingAccount}
                  >
                    <SelectTrigger className="w-[140px]">
                      <SelectValue placeholder="全部账号" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="__all__">全部账号</SelectItem>
                      {accounts.map(acc => (
                        <SelectItem key={acc} value={acc}>{acc}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <Input
                    value={newCloudPath}
                    onChange={(e) => setNewCloudPath(e.target.value)}
                    placeholder="115 网盘路径，如 /电影"
                    className="flex-1"
                  />
                  <span className="text-muted-foreground">→</span>
                  <Input
                    value={newLocalPath}
                    onChange={(e) => setNewLocalPath(e.target.value)}
                    placeholder="本地路径，如/app/data/media/电影"
                    className="flex-1"
                  />
                  <Button size="sm" onClick={addPathMapping}>
                    添加
                  </Button>
                </div>
                <p className="text-xs text-muted-foreground">
                  只有匹配到网盘路径前缀的文件才会被处理。支持多个路径映射。
                </p>
              </div>

              {/* Verify Button */}
              <div className="space-y-2">
                <div className="flex gap-2 items-center">
                  <Button
                    variant="outline"
                    onClick={handleVerify}
                    disabled={verifying || selectedAccounts.length === 0}
                  >
                    {verifying ? "验证中..." : "验证账号的生活事件功能"}
                  </Button>
                  {verifyResult && (
                    <span className={`text-sm font-medium ${verifyResult.success ? "text-green-500" : "text-red-500"}`}>
                      {verifyResult.message}
                    </span>
                  )}
                </div>
                {verifyResult && verifyResult.perAccount.length > 0 && (
                  <div className="rounded-md border p-3 space-y-2 text-sm">
                    {verifyResult.perAccount.map(r => (
                      <div key={r.account} className="flex items-start gap-2">
                        <span className={r.success ? "text-green-500 mt-0.5" : "text-red-500 mt-0.5 shrink-0"}>
                          {r.success ? "✓" : "✗"}
                        </span>
                        <div className="min-w-0">
                          <div className="font-medium">账号：{r.account}</div>
                          <div className={r.success ? "text-green-600" : "text-red-600"}>
                            {r.message}
                          </div>
                          {r.details && (
                            <div className="text-muted-foreground text-xs mt-1 break-all">
                              详情：{JSON.stringify(r.details)}
                            </div>
                          )}
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>

              {/* Monitor Status */}
              {displayMonitorStates.length > 0 && (
                <div className="space-y-2">
                  <Label>监控状态</Label>
                  <div className="p-3 border rounded-md space-y-2">
                    {displayMonitorStates.map((state) => (
                      <div key={state.account} className="flex items-center justify-between">
                        <div className="flex items-center gap-2 flex-wrap">
                          <span className="text-sm font-medium">{state.account}</span>
                          <span className={`text-xs px-2 py-0.5 rounded ${
                            state.running
                              ? "bg-green-100 text-green-800"
                              : state.pending
                                ? "bg-yellow-100 text-yellow-800"
                                : "bg-gray-100 text-gray-800"
                          }`}>
                            {state.running ? "运行中" : state.pending ? "待保存配置" : "已停止"}
                          </span>
                          {state.eventsProcessed > 0 && (
                            <span className="text-xs text-muted-foreground">
                              已处理 {state.eventsProcessed} 个事件
                            </span>
                          )}
                          {state.lastError && (
                            <span className="text-xs text-red-500">
                              错误: {state.lastError}
                            </span>
                          )}
                          {state.pending && (
                            <span className="text-xs text-muted-foreground">
                              点击下方「保存并启动监控」以启用此账号
                            </span>
                          )}
                        </div>
                        <Button
                          variant={state.running ? "destructive" : "outline"}
                          size="sm"
                          disabled={state.pending}
                          onClick={() => state.running ? handleStopMonitor(state.account) : handleStartAccount(state.account)}
                        >
                          {state.pending ? "待保存" : state.running ? "停止" : "启动"}
                        </Button>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          </section>

          <div className="pt-2 flex flex-wrap gap-2 items-center">
            <Button disabled={saving} onClick={onSave}>
              {saving ? "保存中..." : "保存设置"}
            </Button>
            <Button
              onClick={handleStartMonitor}
              disabled={
                !monitorEnabled ||
                selectedAccounts.length === 0 ||
                pathMappings.length === 0
              }
            >
              保存并启动监控
            </Button>
            {(!monitorEnabled || selectedAccounts.length === 0 || pathMappings.length === 0) && (
              <p className="text-xs text-muted-foreground">
                {!monitorEnabled && "请先勾选「启用监控」"}
                {monitorEnabled && selectedAccounts.length === 0 && "请至少选择一个监控账号"}
                {monitorEnabled && selectedAccounts.length > 0 && pathMappings.length === 0 && "请至少配置一条路径映射"}
              </p>
            )}
          </div>
        </div>
      )}

      {/* Tab 3: 清理与安全 */}
      {activeTab === "security" && (
        <div className="space-y-6">
          <section className="space-y-4">
            <h2 className="text-base font-medium">STRM 清理</h2>
            <StrmCleanupCard />
          </section>

          <Card>
            <CardHeader>
              <div className="flex items-center gap-2">
                <UserCog className="h-5 w-5" />
                <CardTitle>账户安全</CardTitle>
              </div>
              <CardDescription>
                当前用户：<span className="font-medium text-foreground">{currentUsername}</span>
              </CardDescription>
            </CardHeader>
            <CardContent>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                <div className="space-y-3">
                  <div className="flex items-center gap-2 text-sm font-medium">
                    <User className="h-4 w-4" />
                    修改用户名
                  </div>
                  <p className="text-xs text-muted-foreground">
                    修改后需重新登录。规则：3-32 位，字母/数字/下划线。
                  </p>
                  <div className="grid gap-2">
                    <Label htmlFor="usernameCurrentPassword">当前密码</Label>
                    <Input
                      id="usernameCurrentPassword"
                      type="password"
                      value={usernameCurrentPwd}
                      onChange={(e) => setUsernameCurrentPwd(e.target.value)}
                      placeholder="输入当前密码"
                    />
                    <Label htmlFor="newUsername">新用户名</Label>
                    <Input
                      id="newUsername"
                      value={newUsername}
                      onChange={(e) => setNewUsername(e.target.value)}
                      placeholder="3-32 位，字母/数字/下划线"
                    />
                    <Label htmlFor="confirmUsername">确认新用户名</Label>
                    <Input
                      id="confirmUsername"
                      value={confirmUsername}
                      onChange={(e) => setConfirmUsername(e.target.value)}
                      placeholder="再次输入新用户名"
                    />
                    <Button
                      disabled={changingUsername}
                      onClick={handleChangeUsername}
                      className="mt-1"
                    >
                      {changingUsername ? "修改中..." : "修改用户名"}
                    </Button>
                  </div>
                </div>

                <div className="space-y-3">
                  <div className="flex items-center gap-2 text-sm font-medium">
                    <Lock className="h-4 w-4" />
                    修改密码
                  </div>
                  <p className="text-xs text-muted-foreground">
                    定期修改密码有助于提升账户安全性。
                  </p>
                  <div className="grid gap-2">
                    <Label htmlFor="currentPassword">当前密码</Label>
                    <Input
                      id="currentPassword"
                      type="password"
                      value={currentPwd}
                      onChange={(e) => setCurrentPwd(e.target.value)}
                      placeholder="输入当前密码"
                    />
                    <Label htmlFor="newPassword">新密码</Label>
                    <Input
                      id="newPassword"
                      type="password"
                      value={newPwd}
                      onChange={(e) => setNewPwd(e.target.value)}
                      placeholder="至少 6 位"
                    />
                    <Label htmlFor="confirmPassword">确认新密码</Label>
                    <Input
                      id="confirmPassword"
                      type="password"
                      value={confirmPwd}
                      onChange={(e) => setConfirmPwd(e.target.value)}
                      placeholder="再次输入新密码"
                    />
                    <Button
                      disabled={changingPwd}
                      onClick={handleChangePassword}
                      className="mt-1"
                    >
                      {changingPwd ? "修改中..." : "修改密码"}
                    </Button>
                  </div>
                </div>
              </div>
            </CardContent>
          </Card>
        </div>
      )}
    </div>
  );
}
