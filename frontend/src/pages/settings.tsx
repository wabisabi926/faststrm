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
import { Settings, LifeBuoy, Shield, UserCog, FolderOpen } from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import axiosInstance, { getUsername, setUsername, clearToken, clearUsername } from "@/lib/axios";
import type { AxiosError } from "axios";
import { StrmCleanupCard } from "./StrmCleanupCard";
import { DirectoryTreeDialog } from "@/pages/task/components/DirectoryTreeDialog";
import { LocalDirectoryTreeDialog } from "@/pages/task/components/LocalDirectoryTreeDialog";

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
  // STRM 路由策略配置（302 模式生效）
  strm?: {
    forceProxyUaTokens?: string[];
    accountProxyConcurrencyLimit?: number;
    redirectCheckTimeoutMs?: number;
  };
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
    autoDownloadMetadata?: boolean;
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
  pollInterval: 10,
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
  // STRM 路由策略配置
  const [forceProxyUaInput, setForceProxyUaInput] = useState("");

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

  // Change credentials states (merged username + password)
  const [currentPwd, setCurrentPwd] = useState("");
  const [newUsername, setNewUsername] = useState("");
  const [newPwd, setNewPwd] = useState("");
  const [confirmPwd, setConfirmPwd] = useState("");
  const [savingCredentials, setSavingCredentials] = useState(false);
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
  const [pollInterval, setPollInterval] = useState(10);
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

  // Directory picker dialog states
  // For existing row editing: index points to the mapping row
  // For new row: index = -1
  const [cloudPickerOpen, setCloudPickerOpen] = useState(false);
  const [localPickerOpen, setLocalPickerOpen] = useState(false);
  const [pickerTargetRow, setPickerTargetRow] = useState(-1); // -1 = new row, >=0 = existing row
  const [pickerAccount, setPickerAccount] = useState<string>("");

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
        setForceProxyUaInput((settings.strm?.forceProxyUaTokens || []).join(", "));

        // Load life monitor config
        const monitor = settings.lifeMonitor || DEFAULT_MONITOR_CONFIG;
        setMonitorEnabled(monitor.enabled);
        setSelectedAccounts(monitor.accounts || []);
        setPollInterval(monitor.pollInterval || 10);
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
        await refreshMonitorStates();

        // 加载当前用户名
        const savedUsername = getUsername();
        if (savedUsername) {
          setCurrentUsername(savedUsername);
        }

        // 加载媒体挂载路径试运行快照
        await fetchMountDryRun();
      } catch (err) {
        console.error("加载设置失败:", err);
        toast.error("加载设置失败");
      } finally {
        setLoading(false);
      }
    };

    loadData();
  }, []);

  // 统一刷新 lifeMonitor 状态，替代复制粘贴的 7 处 axiosInstance.get + setMonitorStates
  const refreshMonitorStates = async () => {
    try {
      const monitorResp = await axiosInstance.get("/api/lifeMonitor");
      const rawStates = monitorResp.data?.states || [];
      // 兼容 Go 字段名: eventCount→eventsProcessed, error→status
      setMonitorStates(rawStates.map((s: Record<string, unknown>) => ({
        account: s.account || "",
        running: s.running || false,
        status: s.status || s.error || "",
        eventsProcessed: s.eventsProcessed ?? s.eventCount ?? 0,
        lastError: s.lastError || s.error || "",
      })));
    } catch (e) {
      console.error("Failed to refresh monitor states:", e);
    }
  };

  const fetchMountDryRun = async () => {
    setMountDryRunLoading(true);
    try {
      const resp = await axiosInstance.get("/api/mediaMountSync");
      const d = resp.data;
      if (d) {
        // 防御性处理：确保所有数组字段非 null
        d.computed = d.computed || [];
        d.persisted = d.persisted || [];
        d.final = d.final || [];
        if (d.diff) {
          d.diff.added = d.diff.added || [];
          d.diff.removed = d.diff.removed || [];
          d.diff.kept = d.diff.kept || [];
        }
      }
      setMountDryRun(d || null);
    } catch (e) {
      console.error("获取媒体挂载试运行数据失败:", e);
    } finally {
      setMountDryRunLoading(false);
    }
  };

  const applyMountSync = async () => {
    setMountSyncing(true);
    setLastSyncApply(null);
    try {
      const resp = await axiosInstance.post("/api/mediaMountSync", {});
      const d = resp.data;
      if (d) {
        d.added = d.added || [];
        d.removed = d.removed || [];
        d.kept = d.kept || [];
        d.final = d.final || [];
      }
      setLastSyncApply(d || null);
      // 同步成功后刷新试运行视图 + 刷新 settings（因为 mediaMountPath 被写回了）
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

  const handleSaveCredentials = async () => {
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
      if (
        trimmedUsername.length < 3 ||
        trimmedUsername.length > 32
      ) {
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

    setSavingCredentials(true);
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
      const axiosErr = err as
        | { response?: { data?: { error?: string } } }
        | undefined;
      const msg = axiosErr?.response?.data?.error || "保存失败";
      toast.error(msg);
    } finally {
      setSavingCredentials(false);
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

      // 解析强制代理 UA tokens
      const forceProxyUaTokens = forceProxyUaInput
        .split(",")
        .map(token => token.trim())
        .filter(token => token.length > 0);

      // 注意：mediaMountPath 不在此处手工写入，由 SSOT 的 syncMediaMountPaths() 统一维护
      //       （PUT /api/settings 内部会自动触发 sync，并返回同步详情）
      const saveData = {
        ...data,
        strmExtensions,
        downloadExtensions,
        download: {
          ...data.download,
          autoDownloadMetadata: data.download?.autoDownloadMetadata ?? true,
        },
        strm: {
          ...data.strm,
          forceProxyUaTokens,
        },
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

      const saveResp = await axiosInstance.post("/api/settings", saveData);
      setData(saveData);
      // 保存后自动刷新试运行视图（因为全局 strmPrefix/enable302/任务/生活事件 都可能影响）
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

  // Directory picker handlers for existing rows
  const openCloudPicker = (rowIndex: number, account?: string) => {
    setPickerTargetRow(rowIndex);
    setPickerAccount(account || accounts[0] || "");
    setCloudPickerOpen(true);
  };

  const openLocalPicker = (rowIndex: number) => {
    setPickerTargetRow(rowIndex);
    setLocalPickerOpen(true);
  };

  // Directory picker handlers for new row
  const openNewCloudPicker = () => {
    setPickerTargetRow(-1);
    setPickerAccount(newMappingAccount !== "__all__" ? newMappingAccount : (accounts[0] || ""));
    setCloudPickerOpen(true);
  };

  const openNewLocalPicker = () => {
    setPickerTargetRow(-1);
    setLocalPickerOpen(true);
  };

  const handleCloudPathSelected = (path: string) => {
    if (pickerTargetRow >= 0) {
      const updated = [...pathMappings];
      updated[pickerTargetRow] = { ...updated[pickerTargetRow], cloudPath: path };
      setPathMappings(updated);
    } else {
      setNewCloudPath(path);
    }
    setCloudPickerOpen(false);
  };

  const handleLocalPathSelected = (path: string) => {
    if (pickerTargetRow >= 0) {
      const updated = [...pathMappings];
      updated[pickerTargetRow] = { ...updated[pickerTargetRow], localPath: path };
      setPathMappings(updated);
    } else {
      setNewLocalPath(path);
    }
    setLocalPickerOpen(false);
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
      await refreshMonitorStates();
    } catch {
      toast.error("启动监控失败");
      await refreshMonitorStates();
    }
  };

  const handleStopMonitor = async (account: string) => {
    try {
      await axiosInstance.post("/api/lifeMonitor", {
        action: "stop",
        account,
      });
      toast.success(`监控已停止: ${account}`);
      await refreshMonitorStates();
    } catch {
      toast.error("停止监控失败");
      await refreshMonitorStates();
    }
  };

  const handleStartAccount = async (account: string) => {
    try {
      const resp = await axiosInstance.post("/api/lifeMonitor", {
        action: "start",
        account,
      });
      toast.success(resp.data?.message || `监控已启动: ${account}`);
      await refreshMonitorStates();
    } catch (err) {
      const axiosErr = err as AxiosError<{ error?: string }>;
      const msg = axiosErr?.response?.data?.error || axiosErr?.message || "启动监控失败";
      toast.error(msg);
      await refreshMonitorStates();
    }
  };

  if (loading) return <div>加载中...</div>;

  return (
    <>
    <div className="mx-auto max-w-3xl space-y-6">
      {/* Page Title */}
      <div>
        <h1 className="text-2xl font-semibold">设置</h1>
        <p className="text-sm text-muted-foreground mt-1">
          配置全局选项、生活事件监控及安全
        </p>
      </div>

      {/* Tab Bar */}
      <div className="flex gap-1 border-b border-border overflow-x-auto scrollbar-none -mx-1 px-1">
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
          {/* 基础设置 */}
          <section className="border rounded-md p-4 sm:p-5 space-y-5">
            <div>
              <h2 className="text-base font-medium">基础设置</h2>
              <p className="text-xs text-muted-foreground mt-1">全局 User-Agent 与文件扩展名配置</p>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
              <div className="space-y-3">
                <Label>User-Agent</Label>
                <Input
                  value={data["user-agent"] || ""}
                  onChange={(e) =>
                    setData({ ...data, ["user-agent"]: e.target.value })
                  }
                  placeholder="Mozilla/5.0 ..."
                />
                <p className="text-xs text-muted-foreground">
                  访问 115 API 时使用的 UA
                </p>
              </div>
              <div className="space-y-3">
                <Label>Strm 文件扩展名</Label>
                <Input
                  value={strmExtensionsInput}
                  onChange={(e) => setStrmExtensionsInput(e.target.value)}
                  placeholder=".mkv, .mp4, .mp3"
                />
                <p className="text-xs text-muted-foreground">
                  用逗号分隔，自动添加点号前缀
                </p>
              </div>
              <div className="space-y-3 md:col-span-2">
                <Label>下载文件扩展名</Label>
                <Input
                  value={downloadExtensionsInput}
                  onChange={(e) => setDownloadExtensionsInput(e.target.value)}
                  placeholder=".srt, .ass, .sub, .nfo"
                />
                <p className="text-xs text-muted-foreground">
                  用逗号分隔，自动添加点号前缀
                </p>
              </div>
            </div>
          </section>

          {/* STRM 生成设置 */}
          <section className="border rounded-md p-4 sm:p-5 space-y-5">
            <div>
              <h2 className="text-base font-medium">STRM 生成设置（全局默认）</h2>
              <p className="text-xs text-muted-foreground mt-1">
                适用于所有账号的生活事件监控和全量扫描，任务级可单独覆盖
              </p>
            </div>

            <div className="space-y-3">
              <Label>Strm 前缀</Label>
              <Input
                value={data.strmPrefix || ""}
                onChange={(e) =>
                  setData({ ...data, strmPrefix: e.target.value })
                }
                placeholder="http://服务器IP:端口 (如 http://192.168.1.100:8090)"
              />
              <p className="text-xs text-muted-foreground">
                STRM 文件内容的前缀，如 Emby/Jellyfin 的 HTTP 访问地址。系统会自动追加对应路径（302 模式 <code>/api/fs/get</code>，其他模式 <code>/api/strm</code>），无需手动添加。
              </p>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-3 gap-4 pt-1">
              <div className="flex items-center gap-2">
                <Checkbox
                  id="global-enable-302"
                  checked={!!data.enable302}
                  onCheckedChange={(checked) =>
                    setData({ ...data, enable302: checked === true })
                  }
                />
                <label htmlFor="global-enable-302" className="text-sm cursor-pointer leading-tight">
                  302 重定向<span className="text-xs text-muted-foreground">（直链下载，不走本机代理）</span>
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
                  删除多余 STRM 文件
                </label>
              </div>
            </div>

            {/* STRM 路由策略配置（302 模式生效） */}
            {data.enable302 && (
              <div className="space-y-4 pt-4 border-t">
                <div className="flex items-center gap-2">
                  <Settings className="w-4 h-4 text-muted-foreground" />
                  <h3 className="text-sm font-medium">STRM 路由策略</h3>
                </div>
                <p className="text-xs text-muted-foreground">
                  302 模式下生效。默认 redirect（不走本机带宽），仅以下 UA 强制走 proxy。
                </p>
                <div className="grid grid-cols-1 md:grid-cols-3 gap-5">
                  <div className="space-y-3">
                    <Label>强制代理 UA 标识</Label>
                    <Input
                      value={forceProxyUaInput}
                      onChange={(e) => setForceProxyUaInput(e.target.value)}
                      placeholder="Infuse, VidHub"
                    />
                    <p className="text-xs text-muted-foreground">
                      逗号分隔
                    </p>
                  </div>
                  <div className="space-y-3">
                    <Label>单账号代理并发上限</Label>
                    <Input
                      type="number"
                      min="1"
                      max="20"
                      value={data.strm?.accountProxyConcurrencyLimit ?? 8}
                      onChange={(e) =>
                        setData({ ...data, strm: { ...data.strm, accountProxyConcurrencyLimit: parseInt(e.target.value) || 8 } })
                      }
                    />
                    <p className="text-xs text-muted-foreground">
                      超过自动切 redirect
                    </p>
                  </div>
                  <div className="space-y-3">
                    <Label>Redirect 检测超时（ms）</Label>
                    <Input
                      type="number"
                      min="500"
                      max="10000"
                      step="500"
                      value={data.strm?.redirectCheckTimeoutMs ?? 5000}
                      onChange={(e) =>
                        setData({ ...data, strm: { ...data.strm, redirectCheckTimeoutMs: parseInt(e.target.value) || 5000 } })
                      }
                    />
                    <p className="text-xs text-muted-foreground">
                      失败降级 proxy
                    </p>
                  </div>
                </div>
              </div>
            )}
          </section>

          {/* 下载限流配置 */}
          <section className="border rounded-md p-4 sm:p-5 space-y-5">
            <div>
              <h2 className="text-base font-medium">下载限流配置</h2>
              <p className="text-xs text-muted-foreground mt-1">控制 115 API 与下载的并发上限</p>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-5">
              <div className="space-y-3">
                <Label>链接获取每秒请求数</Label>
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
                <p className="text-xs text-muted-foreground">linkMaxPerSecond</p>
              </div>
              <div className="space-y-3">
                <Label>链接获取并发数</Label>
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
                <p className="text-xs text-muted-foreground">linkMaxConcurrent</p>
              </div>
              <div className="space-y-3">
                <Label>文件下载并发数</Label>
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
                <p className="text-xs text-muted-foreground">downloadMaxConcurrent</p>
              </div>
            </div>

            <div className="space-y-3">
              <div className="flex items-center justify-between">
                <Label>自动下载媒体元数据</Label>
                <button
                  type="button"
                  role="switch"
                  aria-checked={data.download?.autoDownloadMetadata ?? true}
                  onClick={() =>
                    setData({
                      ...data,
                      download: {
                        ...(data.download || {}),
                        autoDownloadMetadata: !(data.download?.autoDownloadMetadata ?? true)
                      },
                    })
                  }
                  className={`inline-flex h-5 w-9 items-center rounded-full transition-colors ${
                    (data.download?.autoDownloadMetadata ?? true) ? "bg-primary" : "bg-muted"
                  }`}
                >
                  <span
                    className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                      (data.download?.autoDownloadMetadata ?? true) ? "translate-x-4" : "translate-x-0.5"
                    }`}
                  />
                </button>
              </div>
              <p className="text-xs text-muted-foreground">
                全量同步时自动下载 nfo/jpg/png/srt 等媒体元数据文件。关闭后只生成 STRM 视频索引文件。
              </p>
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
                              <span className="px-2 py-0.5 rounded border bg-green-500/20 text-green-400 border-green-500/30">
                                +{mountDryRun.diff.added.length} 待新增
                              </span>
                            )}
                            {mountDryRun.diff.removed.length > 0 && (
                              <span className="px-2 py-0.5 rounded border bg-red-500/20 text-red-400 border-red-500/30">
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
                          <summary className="cursor-pointer text-red-400">
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
                                    ? "bg-indigo-500/20 text-indigo-400 border-indigo-500/30"
                                    : row.source === "task"
                                      ? "bg-sky-500/20 text-sky-400 border-sky-500/30"
                                      : "bg-amber-500/20 text-amber-400 border-amber-500/30")
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
                                <span className="text-[11px] px-1.5 py-0.5 rounded border bg-green-500/20 text-green-400 border-green-500/30">
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
                          ? "bg-amber-500/10 border-amber-500/30 text-amber-400"
                          : "bg-slate-500/10 border-slate-500/30 text-slate-400")
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
          <section className="border rounded-md p-4 sm:p-5 space-y-5">
            <div className="flex items-center justify-between gap-2 flex-wrap">
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
              <div className="space-y-3">
                <Label>监控账号</Label>
                <div className="flex flex-wrap gap-5 p-3 border rounded-md">
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
              <div className="grid grid-cols-1 md:grid-cols-2 gap-5">
                <div className="space-y-3">
                  <Label>轮询间隔（秒）</Label>
                  <Input
                    type="number"
                    min="5"
                    max="300"
                    value={pollInterval}
                    onChange={(e) => setPollInterval(parseInt(e.target.value) || 10)}
                  />
                  <p className="text-xs text-muted-foreground">
                    建议 10-30 秒，太频繁可能触发限流（默认 10 秒）
                  </p>
                </div>
              </div>

              {/* Event Types */}
              <div className="space-y-3">
                <Label>处理的事件类型</Label>
                <div className="flex flex-wrap gap-5 p-3 border rounded-md">
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
              <div className="space-y-3">
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
              <div className="space-y-3">
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
              <div className="space-y-3">
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
              <div className="space-y-3">
                <Label>路径映射（115 网盘路径 → 本地保存路径）</Label>
                <div className="space-y-3">
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
                      <div className="flex-1 flex gap-1 items-center">
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
                        <TooltipProvider delayDuration={100}>
                          <Tooltip>
                            <TooltipTrigger asChild>
                              <span className="inline-flex">
                                <Button
                                  type="button"
                                  variant="outline"
                                  size="icon"
                                  onClick={() => openCloudPicker(index, mapping.account)}
                                  title={mapping.account ? "选择网盘目录" : ""}
                                  disabled={!mapping.account || accounts.length === 0}
                                >
                                  <FolderOpen className="w-4 h-4" />
                                </Button>
                              </span>
                            </TooltipTrigger>
                            {!mapping.account && (
                              <TooltipContent side="top" className="max-w-[240px]">
                                <p>全部账号模式下，不同账号的目录结构可能不一致，请手动输入路径，或先选择具体账号再选择目录。</p>
                              </TooltipContent>
                            )}
                          </Tooltip>
                        </TooltipProvider>
                      </div>
                      <span className="text-muted-foreground">→</span>
                      <div className="flex-1 flex gap-1 items-center">
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
                          type="button"
                          variant="outline"
                          size="icon"
                          onClick={() => openLocalPicker(index)}
                          title="选择本地目录"
                        >
                          <FolderOpen className="w-4 h-4" />
                        </Button>
                      </div>
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
                  <div className="flex-1 flex gap-1 items-center">
                    <Input
                      value={newCloudPath}
                      onChange={(e) => setNewCloudPath(e.target.value)}
                      placeholder="115 网盘路径，如 /电影"
                      className="flex-1"
                    />
                    <TooltipProvider delayDuration={100}>
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span className="inline-flex">
                            <Button
                              type="button"
                              variant="outline"
                              size="icon"
                              onClick={openNewCloudPicker}
                              title={newMappingAccount !== "__all__" ? "选择网盘目录" : ""}
                              disabled={newMappingAccount === "__all__" || accounts.length === 0}
                            >
                              <FolderOpen className="w-4 h-4" />
                            </Button>
                          </span>
                        </TooltipTrigger>
                        {newMappingAccount === "__all__" && (
                          <TooltipContent side="top" className="max-w-[240px]">
                            <p>全部账号模式下，不同账号的目录结构可能不一致，请手动输入路径，或先选择具体账号再选择目录。</p>
                          </TooltipContent>
                        )}
                      </Tooltip>
                    </TooltipProvider>
                  </div>
                  <span className="text-muted-foreground">→</span>
                  <div className="flex-1 flex gap-1 items-center">
                    <Input
                      value={newLocalPath}
                      onChange={(e) => setNewLocalPath(e.target.value)}
                      placeholder="本地路径，如/app/data/media/电影"
                      className="flex-1"
                    />
                    <Button
                      type="button"
                      variant="outline"
                      size="icon"
                      onClick={openNewLocalPicker}
                      title="选择本地目录"
                    >
                      <FolderOpen className="w-4 h-4" />
                    </Button>
                  </div>
                  <Button size="sm" onClick={addPathMapping}>
                    添加
                  </Button>
                </div>
                <p className="text-xs text-muted-foreground">
                  只有匹配到网盘路径前缀的文件才会被处理。支持多个路径映射。
                </p>
              </div>

              {/* Verify Button */}
              <div className="space-y-3">
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
                  <div className="rounded-md border p-3 space-y-3 text-sm">
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
                <div className="space-y-3">
                  <Label>监控状态</Label>
                  <div className="p-3 border rounded-md space-y-3">
                    {displayMonitorStates.map((state) => (
                      <div key={state.account} className="flex items-center justify-between">
                        <div className="flex items-center gap-2 flex-wrap">
                          <span className="text-sm font-medium">{state.account}</span>
                          <span className={`text-xs px-2 py-0.5 rounded ${
                            state.lastError
                              ? "bg-red-500/20 text-red-400"
                              : state.running
                                ? "bg-green-500/20 text-green-400"
                                : state.pending
                                  ? "bg-yellow-500/20 text-yellow-400"
                                  : "bg-muted text-muted-foreground"
                          }`}>
                            {state.lastError ? "异常" : state.running ? "运行中" : state.pending ? "待保存配置" : "已停止"}
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
          <section className="border rounded-md p-4 sm:p-5 space-y-5">
            <div>
              <h2 className="text-base font-medium">STRM 清理</h2>
              <p className="text-xs text-muted-foreground mt-1">扫描本地与网盘的一致性，清理失效 STRM</p>
            </div>
            <StrmCleanupCard />
          </section>

          <section className="border rounded-md p-4 sm:p-5 space-y-5">
            <div className="flex items-center gap-2">
              <UserCog className="h-5 w-5" />
              <h2 className="text-base font-medium">修改用户名和密码</h2>
            </div>
            <p className="text-xs text-muted-foreground">
              当前用户：<span className="font-medium text-foreground">{currentUsername}</span>
            </p>
            <div className="grid gap-4 max-w-sm">
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
                disabled={savingCredentials}
                onClick={handleSaveCredentials}
                className="mt-2"
              >
                {savingCredentials ? "保存中..." : "保存"}
              </Button>
            </div>
          </section>
        </div>
      )}
    </div>

    {/* Directory Picker Dialogs */}
    <DirectoryTreeDialog
      open={cloudPickerOpen}
      onOpenChange={setCloudPickerOpen}
      account={pickerAccount}
      onSelect={handleCloudPathSelected}
    />
    <LocalDirectoryTreeDialog
      open={localPickerOpen}
      onOpenChange={setLocalPickerOpen}
      onSelect={handleLocalPathSelected}
    />
    </>
  );
}
