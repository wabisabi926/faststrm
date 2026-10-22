import { useEffect, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import { Stethoscope } from "lucide-react";
import axiosInstance from "@/lib/axios";
import type { AxiosError } from "axios";
import { DirectoryTreeDialog } from "@/pages/task/components/DirectoryTreeDialog";
import { LocalDirectoryTreeDialog } from "@/pages/task/components/LocalDirectoryTreeDialog";
import type {
  PathMapping,
  Settings as SettingsType,
  MonitorState,
  VerifyResult,
  DisplayMonitorState,
} from "./settings/types";
import { DEFAULT_MONITOR_CONFIG } from "./settings/types";
import { BasicSettingsTab } from "./settings/BasicSettingsTab";
import { MonitorSettingsTab } from "./settings/MonitorSettingsTab";

type SettingsTab = "general" | "monitor";

export default function SettingsPage({ section }: { section?: "general" | "monitor" } = {}) {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const activeTab: SettingsTab =
    section === "monitor" || searchParams.get("tab") === "monitor" ? "monitor" : "general";

  // 兼容旧链接：清理独立成页；生活事件设置改为独立菜单；
  // 早期四页签方案留下的 notify/security 链接回退到原页面。
  useEffect(() => {
    const tab = searchParams.get("tab");
    if (tab === "cleanup") {
      navigate("/cleanup", { replace: true });
    } else if (tab === "monitor") {
      navigate("/settings/monitor", { replace: true });
    } else if (tab === "notify") {
      navigate("/notify", { replace: true });
    } else if (tab === "security") {
      navigate("/account-security", { replace: true });
    }
  }, [searchParams, navigate]);

  const [data, setData] = useState<SettingsType>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [strmExtensionsInput, setStrmExtensionsInput] = useState("");
  const [downloadExtensionsInput, setDownloadExtensionsInput] = useState("");
  // STRM 路由策略配置
  const [forceProxyUaInput, setForceProxyUaInput] = useState("");
  const [forceProxyFormatsInput, setForceProxyFormatsInput] = useState("");
  // 全局文件过滤
  const [globalMinFileSizeMb, setGlobalMinFileSizeMb] = useState("");
  const [globalBlacklistInput, setGlobalBlacklistInput] = useState("");

  // Life monitor states
  const [accounts, setAccounts] = useState<string[]>([]);
  const [monitorStates, setMonitorStates] = useState<MonitorState[]>([]);
  const [verifying, setVerifying] = useState(false);
  const [verifyResult, setVerifyResult] = useState<VerifyResult>(null);

  // Life monitor form state
  const [monitorEnabled, setMonitorEnabled] = useState(false);
  const [selectedAccounts, setSelectedAccounts] = useState<string[]>([]);
  const [pollInterval, setPollInterval] = useState(10);
  const [pathMappings, setPathMappings] = useState<PathMapping[]>([]);
  const [newMappingAccount, setNewMappingAccount] = useState<string>("__all__");
  const [removeEmptyDirs, setRemoveEmptyDirs] = useState(true);
  const [notifyOnlyOnError, setNotifyOnlyOnError] = useState(false);
  const [eventTypes, setEventTypes] = useState({
    create: true,
    remove: true,
    rename: true,
    move: true,
  });
  const [minFileSizeMb, setMinFileSizeMb] = useState(""); // MB 输入框显示用
  const [firstPullMode, setFirstPullMode] = useState<"latest" | "all" | "last">("latest");
  const [moveMediaMode, setMoveMediaMode] = useState<"recreate" | "local_move">("local_move");
  // 移出映射时删除本地旧 STRM（默认开启）
  const [moveOutRemoveLocalStrm, setMoveOutRemoveLocalStrm] = useState(true);
  // 旧 STRM 扩展搜索目录（多行文本，一行一个路径）
  const [mediaMountPaths, setMediaMountPaths] = useState("");

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
  const displayMonitorStates: DisplayMonitorState = (() => {
    const byAccount = new Map<string, MonitorState>();
    for (const s of monitorStates) byAccount.set(s.account, s);
    const result: DisplayMonitorState = [];
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
        setForceProxyFormatsInput((settings.strm?.forceProxyFormats || []).join(", "));

        // 全局文件过滤
        const loadedGlobalMin = typeof settings.download?.minFileSize === "number" ? settings.download.minFileSize : 0;
        setGlobalMinFileSizeMb(loadedGlobalMin > 0 ? (loadedGlobalMin / (1024 * 1024)).toString() : "");
        setGlobalBlacklistInput((settings.download?.strmGenerateBlacklist || []).join(", "));

        // Load life monitor config
        const monitor = settings.lifeMonitor || DEFAULT_MONITOR_CONFIG;
        setMonitorEnabled(monitor.enabled);
        setSelectedAccounts(monitor.accounts || []);
        setPollInterval(monitor.pollInterval || 10);
        setPathMappings(monitor.pathMappings || []);
        setRemoveEmptyDirs(monitor.removeEmptyDirs ?? true);
        setNotifyOnlyOnError(monitor.notifyOnlyOnError ?? false);
        setEventTypes(monitor.eventTypes || DEFAULT_MONITOR_CONFIG.eventTypes);
        const loadedMinSize = typeof monitor.minFileSize === "number" ? monitor.minFileSize : 0;
        setMinFileSizeMb(loadedMinSize > 0 ? (loadedMinSize / (1024 * 1024)).toString() : "");
        setFirstPullMode(monitor.firstPullMode || "latest");
        setMoveMediaMode(monitor.moveMediaMode || "local_move");
        setMoveOutRemoveLocalStrm(monitor.moveOutRemoveLocalStrm ?? true);
        setMediaMountPaths((monitor.mediaMountPath || []).join("\n"));

        // Load accounts（生活监控仅支持 115）
        const accountsResp = await axiosInstance.get("/api/account");
        setAccounts(
          (accountsResp.data || [])
            .filter((a: { accountType?: string }) => a.accountType === "115")
            .map((a: { name: string }) => a.name)
        );

        // Load monitor states
        await refreshMonitorStates();
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

  const parseExts = (raw: string) =>
    raw
      .split(/[,，;；]/)
      .map(ext => ext.trim())
      .filter(ext => ext.length > 0)
      .map(ext => (ext.startsWith(".") ? ext : `.${ext}`))
      .map(ext => ext.toLowerCase());

  // 解析 MB 输入为字节；空值或非法值视为 0（不过滤）
  const parseMb = (raw: string) => {
    const mb = parseFloat(raw);
    return Number.isFinite(mb) && mb > 0 ? Math.floor(mb * 1024 * 1024) : 0;
  };

  // 解析「一行一个」的多行输入：去空行、去首尾空格、去重
  const parseLines = (raw: string) =>
    Array.from(
      new Set(
        raw
          .split(/\r?\n/)
          .map((line) => line.trim())
          .filter((line) => line.length > 0)
      )
    );

  // 常规设置只提交自己负责的字段，避免把 Emby/Telegram/清理配置按旧快照覆盖。
  const buildSaveData = () => {
    // 解析强制代理 UA tokens
    const forceProxyUaTokens = forceProxyUaInput
      .split(",")
      .map(token => token.trim())
      .filter(token => token.length > 0);

    // 解析强制代理格式（容器名/扩展名，点号可选；后端按小写匹配）
    const forceProxyFormats = forceProxyFormatsInput
      .split(",")
      .map(token => token.trim().toLowerCase())
      .filter(token => token.length > 0);

    // 解析全局文件名黑名单关键词
    const globalBlacklist = globalBlacklistInput
      .split(",")
      .map(keyword => keyword.trim())
      .filter(keyword => keyword.length > 0);

    const downloadExtensions = parseExts(downloadExtensionsInput);
    return {
      "user-agent": data["user-agent"] ?? "",
      strmExtensions: parseExts(strmExtensionsInput),
      downloadExtensions,
      strmPrefix: data.strmPrefix ?? "",
      enablePathEncoding: data.enablePathEncoding !== false,
      download: {
        downloadMaxConcurrent: data.download?.downloadMaxConcurrent,
        // 扩展名列表是唯一开关；留空 = 不下载附带文件。
        autoDownloadMetadata: downloadExtensions.length > 0,
        minFileSize: parseMb(globalMinFileSizeMb),
        strmGenerateBlacklist: globalBlacklist,
      },
      strm: {
        forceProxyUaTokens,
        forceProxyFormats,
        accountProxyConcurrencyLimit: data.strm?.accountProxyConcurrencyLimit,
        redirectCheckTimeoutMs: data.strm?.redirectCheckTimeoutMs,
        disableRangeProxy: data.strm?.disableRangeProxy,
        proxyChunkSizeMB: data.strm?.proxyChunkSizeMB,
        proxyConcurrency: data.strm?.proxyConcurrency,
        strmUrlTemplate: data.strm?.strmUrlTemplate ?? "",
        strmFilenameTemplate: data.strm?.strmFilenameTemplate ?? "",
      },
      rateLimit: data.rateLimit,
    };
  };

  // 生活监控统一走 /api/lifeMonitor：后端会校验账号并 Refresh，避免保存后仍跑旧配置。
  const buildMonitorUpdates = (overrides: Partial<{
    enabled: boolean;
    accounts: string[];
    pathMappings: PathMapping[];
  }> = {}) => ({
    enabled: monitorEnabled,
    accounts: selectedAccounts,
    pollInterval,
    pathMappings: pathMappings.map((mapping) => ({ ...mapping, mappingType: "media" })),
    removeEmptyDirs,
    notifyOnlyOnError,
    eventTypes,
    minFileSize: parseMb(minFileSizeMb),
    firstPullMode,
    moveMediaMode,
    moveOutRemoveLocalStrm,
    mediaMountPath: parseLines(mediaMountPaths),
    ...overrides,
  });

  const saveSettings = async (): Promise<boolean> => {
    setSaving(true);
    try {
      const saveData = buildSaveData();
      await axiosInstance.post("/api/settings", saveData);
      // 留空保持空：不回填默认值，只把本地快照合并为刚提交的内容。
      setData(prev => ({ ...prev, ...saveData }));
      toast.success("保存成功");
      return true;
    } catch (error: unknown) {
      const apiError = error as { response?: { data?: { message?: string; error?: string } } };
      toast.error(
        apiError.response?.data?.message ||
          apiError.response?.data?.error ||
          "保存失败"
      );
      return false;
    } finally {
      setSaving(false);
    }
  };

  const onSave = async (): Promise<void> => {
    await saveSettings();
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
      const resp = await axiosInstance.post("/api/lifeMonitor", {
        action: "updateConfig",
        updates: buildMonitorUpdates(),
      });
      toast.success(resp.data?.message || "监控已更新");
      await refreshMonitorStates();
    } catch (error: unknown) {
      const apiError = error as { response?: { data?: { error?: string; message?: string } } };
      toast.error(apiError.response?.data?.error || apiError.response?.data?.message || "启动监控失败");
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

  /**
   * 从监控配置里移除一个账号名（用于 settings.LifeMonitor.Accounts 中存在
   * 但 AccountStore 里根本没这个账号的场景：此时「停止」没有意义，用户只
   * 想把这个无效条目从列表里删掉）。
   *
   * 流程：本地 selectedAccounts 过滤掉该名 → 调用 /api/lifeMonitor
   * 持久化并热刷新 → 返回成功/失败 toast。
   */
  const handleRemoveFromMonitor = async (account: string) => {
    try {
      const nextSelected = selectedAccounts.filter(a => a !== account);
      const nextMappings = pathMappings.filter(mapping => mapping.account !== account);

      await axiosInstance.post("/api/lifeMonitor", {
        action: "updateConfig",
        updates: buildMonitorUpdates({ accounts: nextSelected, pathMappings: nextMappings }),
      });

      setSelectedAccounts(nextSelected);
      setPathMappings(nextMappings);

      toast.success(`已从监控列表移除「${account}」`);

      // 移除后刷新 monitor states，让卡片消失
      await refreshMonitorStates();
    } catch (error: unknown) {
      let msg = `从监控列表移除「${account}」失败`;
      if (error && typeof error === 'object' && 'response' in error) {
        const apiError = error as { response?: { data?: { message?: string; error?: string } } };
        msg = apiError.response?.data?.message || apiError.response?.data?.error || msg;
      }
      toast.error(msg);
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
    <div className="mx-auto max-w-5xl space-y-4 sm:space-y-6 pb-24">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between px-1">
        <div>
          <h1 className="text-xl sm:text-2xl font-semibold">
            {activeTab === "monitor" ? "生活事件" : "常规"}
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            {activeTab === "monitor"
              ? "监控开关、路径映射与生活事件运行状态。"
              : "全局 User-Agent、扩展名、STRM 生成、清理与限流配置。"}
          </p>
        </div>
        <Link
          to="/diagnose"
          className="inline-flex shrink-0 items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-foreground"
        >
          <Stethoscope className="h-3.5 w-3.5" />
          播放诊断
        </Link>
      </div>

      {activeTab === "general" && (
        <BasicSettingsTab
          data={data}
          setData={setData}
          strmExtensionsInput={strmExtensionsInput}
          setStrmExtensionsInput={setStrmExtensionsInput}
          downloadExtensionsInput={downloadExtensionsInput}
          setDownloadExtensionsInput={setDownloadExtensionsInput}
          forceProxyUaInput={forceProxyUaInput}
          setForceProxyUaInput={setForceProxyUaInput}
          forceProxyFormatsInput={forceProxyFormatsInput}
          setForceProxyFormatsInput={setForceProxyFormatsInput}
          globalMinFileSizeMb={globalMinFileSizeMb}
          setGlobalMinFileSizeMb={setGlobalMinFileSizeMb}
          globalBlacklistInput={globalBlacklistInput}
          setGlobalBlacklistInput={setGlobalBlacklistInput}
          saving={saving}
          onSave={onSave}
        />
      )}

      {/* 生活事件监控配置 */}
      {activeTab === "monitor" && (
        <MonitorSettingsTab
          monitorEnabled={monitorEnabled}
          setMonitorEnabled={setMonitorEnabled}
          accounts={accounts}
          selectedAccounts={selectedAccounts}
          toggleAccount={toggleAccount}
          pollInterval={pollInterval}
          setPollInterval={setPollInterval}
          eventTypes={eventTypes}
          setEventTypes={setEventTypes}
          removeEmptyDirs={removeEmptyDirs}
          setRemoveEmptyDirs={setRemoveEmptyDirs}
          notifyOnlyOnError={notifyOnlyOnError}
          setNotifyOnlyOnError={setNotifyOnlyOnError}
          minFileSizeMb={minFileSizeMb}
          setMinFileSizeMb={setMinFileSizeMb}
          firstPullMode={firstPullMode}
          setFirstPullMode={setFirstPullMode}
          moveMediaMode={moveMediaMode}
          setMoveMediaMode={setMoveMediaMode}
          moveOutRemoveLocalStrm={moveOutRemoveLocalStrm}
          setMoveOutRemoveLocalStrm={setMoveOutRemoveLocalStrm}
          mediaMountPaths={mediaMountPaths}
          setMediaMountPaths={setMediaMountPaths}
          pathMappings={pathMappings}
          setPathMappings={setPathMappings}
          newMappingAccount={newMappingAccount}
          setNewMappingAccount={setNewMappingAccount}
          newCloudPath={newCloudPath}
          setNewCloudPath={setNewCloudPath}
          newLocalPath={newLocalPath}
          setNewLocalPath={setNewLocalPath}
          openCloudPicker={openCloudPicker}
          openLocalPicker={openLocalPicker}
          openNewCloudPicker={openNewCloudPicker}
          openNewLocalPicker={openNewLocalPicker}
          addPathMapping={addPathMapping}
          removePathMapping={removePathMapping}
          verifying={verifying}
          verifyResult={verifyResult}
          handleVerify={handleVerify}
          displayMonitorStates={displayMonitorStates}
          handleStopMonitor={handleStopMonitor}
          handleStartAccount={handleStartAccount}
          handleRemoveFromMonitor={handleRemoveFromMonitor}
          handleStartMonitor={handleStartMonitor}
        />
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
