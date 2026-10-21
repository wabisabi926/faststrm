// Emby 通知设置业务逻辑 hook：抽离加载/保存/测试连接/账号列表/路径映射管理。
// 从 emby-notify.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T4。

import { useEffect, useState } from "react";
import axiosInstance from "@/lib/axios";
import {
  type EmbySettings,
  type PathMapping,
  type TestResult,
  DEFAULT_NOTIFY_SETTINGS,
} from "./types";

export interface UseEmbySettingsResult {
  // 基础状态
  settings: EmbySettings;
  loading: boolean;
  error: string | null;
  success: string | null;
  testResult: TestResult | null;
  accounts: string[];
  // 基础操作
  updateSetting: <K extends keyof EmbySettings>(key: K, value: EmbySettings[K]) => void;
  handleSave: () => Promise<void>;
  testConnection: () => Promise<void>;
  loadSettings: () => Promise<void>;
  loadAccounts: () => Promise<void>;
  // 提示状态清理
  setError: (msg: string | null) => void;
  setSuccess: (msg: string | null) => void;
  setTestResult: (r: TestResult | null) => void;
  // 路径映射管理
  newMappingEmbyPath: string;
  newMappingCloudPath: string;
  newMappingAccount: string;
  setNewMappingEmbyPath: (v: string) => void;
  setNewMappingCloudPath: (v: string) => void;
  setNewMappingAccount: (v: string) => void;
  updatePathMapping: (index: number, field: "embyPath" | "cloudPath" | "account", value: string) => void;
  addPathMapping: () => void;
  removePathMapping: (index: number) => void;
  // 目录选择器
  folderPickerOpen: boolean;
  folderPickerTarget: number | "new" | null;
  setFolderPickerOpen: (open: boolean) => void;
  handleFolderSelected: (path: string) => void;
  openFolderPickerForNew: () => void;
  openFolderPickerForMapping: (index: number) => void;
  cloudPickerOpen: boolean;
  cloudPickerTarget: number | "new" | null;
  cloudPickerAccount: string;
  setCloudPickerOpen: (open: boolean) => void;
  handleCloudPathSelected: (path: string) => void;
  openCloudPickerForNew: () => void;
  openCloudPickerForMapping: (index: number) => void;
}

export function useEmbySettings(): UseEmbySettingsResult {
  const [settings, setSettings] = useState<EmbySettings>({ ...DEFAULT_NOTIFY_SETTINGS });
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<TestResult | null>(null);
  const [accounts, setAccounts] = useState<string[]>([]);

  // 删除同步路径映射
  const [newMappingEmbyPath, setNewMappingEmbyPath] = useState("");
  const [newMappingCloudPath, setNewMappingCloudPath] = useState("");
  const [newMappingAccount, setNewMappingAccount] = useState("");

  // 本地文件夹选择器（Emby 路径前缀）
  const [folderPickerOpen, setFolderPickerOpen] = useState(false);
  const [folderPickerTarget, setFolderPickerTarget] = useState<number | "new" | null>(null);

  // 云盘目录选择器（网盘路径前缀）
  const [cloudPickerOpen, setCloudPickerOpen] = useState(false);
  const [cloudPickerTarget, setCloudPickerTarget] = useState<number | "new" | null>(null);
  const [cloudPickerAccount, setCloudPickerAccount] = useState<string>("");

  useEffect(() => {
    void loadSettings();
    void loadAccounts();
    // 依赖为 setState + axios 单例，引用稳定；故意不写依赖避免重复加载
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const loadAccounts = async () => {
    try {
      const response = await axiosInstance.get("/api/account");
      const list: Array<{ accountType?: string; name: string }> = response.data || [];
      setAccounts(list.filter((a) => a.accountType === "115").map((a) => a.name));
    } catch (err) {
      console.error("加载账号列表失败:", err);
    }
  };

  const loadSettings = async () => {
    try {
      const response = await axiosInstance.get("/api/settings");
      if (response.data?.emby) {
        setSettings({ ...DEFAULT_NOTIFY_SETTINGS, ...response.data.emby });
      }
    } catch (err) {
      console.error("加载设置失败:", err);
    }
  };

  // persistSettings 统一的保存实现：/api/emby/settings 收全量 EmbySettings，
  // 所以「保存设置」与「添加路径映射」可以共用同一条落库路径。
  const persistSettings = async (next: EmbySettings, successMsg: string) => {
    try {
      setLoading(true);
      setError(null);
      setSuccess(null);

      const response = await axiosInstance.post("/api/emby/settings", next);

      if (response.data?.success) {
        setSuccess(successMsg);
      }
    } catch (raw) {
      const err = raw as {
        response?: { data?: { error?: string; details?: string; message?: string } };
        message?: string;
      };
      const apiMsg = err.response?.data?.error || err.response?.data?.message;
      const detail = err.response?.data?.details;
      setError(apiMsg ? (detail ? `${apiMsg}：${detail}` : apiMsg) : "保存设置失败");
    } finally {
      setLoading(false);
    }
  };

  // takePendingMapping 把「添加」行里已填好的内容并入 mappings。
  // 两个字段都填齐才算完整；只填一半时忽略（保持原有校验），由调用方决定是否提示。
  const takePendingMapping = (): PathMapping | null => {
    const embyPath = newMappingEmbyPath.trim();
    const cloudPath = newMappingCloudPath.trim();
    if (!embyPath || !cloudPath) return null;
    return { embyPath, cloudPath, account: newMappingAccount || undefined };
  };

  const clearPendingMapping = () => {
    setNewMappingEmbyPath("");
    setNewMappingCloudPath("");
    setNewMappingAccount("");
  };

  const handleSave = async () => {
    // 「保存设置」也要吸收还没点「添加」的那一行：用户填完路径直接点保存时，
    // 不该因为少点一次「添加」而丢掉刚填的映射（原来必须先添加再保存，两次点击才生效）。
    const pending = takePendingMapping();
    if (pending) {
      const next: EmbySettings = {
        ...settings,
        syncDeletePathMappings: [...(settings.syncDeletePathMappings || []), pending],
      };
      setSettings(next);
      clearPendingMapping();
      await persistSettings(next, "Emby 通知设置已保存（含新增路径映射）！");
      return;
    }
    await persistSettings(settings, "Emby 通知设置已保存！");
  };

  const testConnection = async () => {
    if (!settings.url || !settings.apiKey) {
      setTestResult({
        success: false,
        message: "请先填写 Emby URL 和 API Key",
      });
      return;
    }

    try {
      setLoading(true);
      setTestResult(null);

      const response = await axiosInstance.post("/api/emby/test-connection", {
        url: settings.url,
        apiKey: settings.apiKey,
      });

      const ok = !!response.data?.success;
      const msg =
        (typeof response.data?.message === "string" && response.data.message) ||
        (ok ? "连接成功" : "连接失败");

      setTestResult({
        success: ok,
        message: ok ? `连接成功：${msg.replace(/^连接成功[：:] */, "")}` : msg,
      });
    } catch (raw) {
      const err = raw as {
        response?: { data?: { message?: string; debug?: unknown } };
        message?: string;
      };
      const apiMsg =
        (typeof err.response?.data?.message === "string" && err.response.data.message) ||
        null;
      setTestResult({
        success: false,
        message: apiMsg || "测试连接失败，请检查网络和配置",
      });
    } finally {
      setLoading(false);
    }
  };

  const updateSetting = <K extends keyof EmbySettings>(key: K, value: EmbySettings[K]) => {
    setSettings((prev) => ({ ...prev, [key]: value }));
  };

  const updatePathMapping = (index: number, field: "embyPath" | "cloudPath" | "account", value: string) => {
    const mappings = [...(settings.syncDeletePathMappings || [])];
    mappings[index] = { ...mappings[index], [field]: value };
    updateSetting("syncDeletePathMappings", mappings);
  };

  // 添加即生效：点「添加」直接落库，不必再点一次「保存设置」。
  // 删除仍保留「点保存才生效」——减法操作留一步缓冲，避免误点立即丢配置。
  const addPathMapping = async () => {
    const pending = takePendingMapping();
    if (!pending) return;
    const next: EmbySettings = {
      ...settings,
      syncDeletePathMappings: [...(settings.syncDeletePathMappings || []), pending],
    };
    setSettings(next);
    clearPendingMapping();
    await persistSettings(next, "路径映射已添加并保存");
  };

  const removePathMapping = (index: number) => {
    const mappings = [...(settings.syncDeletePathMappings || [])];
    mappings.splice(index, 1);
    updateSetting("syncDeletePathMappings", mappings);
  };

  const handleFolderSelected = (path: string) => {
    if (folderPickerTarget === null) return;
    if (folderPickerTarget === "new") {
      setNewMappingEmbyPath(path);
    } else {
      updatePathMapping(folderPickerTarget, "embyPath", path);
    }
    setFolderPickerOpen(false);
    setFolderPickerTarget(null);
  };

  const openFolderPickerForNew = () => {
    setFolderPickerTarget("new");
    setFolderPickerOpen(true);
  };

  const openFolderPickerForMapping = (index: number) => {
    setFolderPickerTarget(index);
    setFolderPickerOpen(true);
  };

  const handleCloudPathSelected = (path: string) => {
    if (cloudPickerTarget === null) return;
    if (cloudPickerTarget === "new") {
      setNewMappingCloudPath(path);
    } else {
      updatePathMapping(cloudPickerTarget, "cloudPath", path);
    }
    setCloudPickerOpen(false);
    setCloudPickerTarget(null);
    setCloudPickerAccount("");
  };

  const openCloudPickerForNew = () => {
    if (!newMappingAccount) return;
    setCloudPickerTarget("new");
    setCloudPickerAccount(newMappingAccount);
    setCloudPickerOpen(true);
  };

  const openCloudPickerForMapping = (index: number) => {
    const mapping = (settings.syncDeletePathMappings || [])[index];
    if (!mapping?.account) return;
    setCloudPickerTarget(index);
    setCloudPickerAccount(mapping.account);
    setCloudPickerOpen(true);
  };

  return {
    settings,
    loading,
    error,
    success,
    testResult,
    accounts,
    updateSetting,
    handleSave,
    testConnection,
    loadSettings,
    loadAccounts,
    setError,
    setSuccess,
    setTestResult,
    newMappingEmbyPath,
    newMappingCloudPath,
    newMappingAccount,
    setNewMappingEmbyPath,
    setNewMappingCloudPath,
    setNewMappingAccount,
    updatePathMapping,
    addPathMapping,
    removePathMapping,
    folderPickerOpen,
    folderPickerTarget,
    setFolderPickerOpen,
    handleFolderSelected,
    openFolderPickerForNew,
    openFolderPickerForMapping,
    cloudPickerOpen,
    cloudPickerTarget,
    cloudPickerAccount,
    setCloudPickerOpen,
    handleCloudPathSelected,
    openCloudPickerForNew,
    openCloudPickerForMapping,
  };
}
