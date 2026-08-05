"use client";
import { useEffect, useState } from "react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { Checkbox } from "@/components/ui/checkbox";
import { toast } from "sonner";
import axiosInstance from "@/lib/axios";

type PathMapping = {
  cloudPath: string;
  localPath: string;
};

type LifeMonitorConfig = {
  enabled: boolean;
  accounts: string[];
  pollInterval: number;
  pathMappings: PathMapping[];
  mediaExtensions: string[];
  removeEmptyDirs: boolean;
  eventTypes: {
    create: boolean;
    remove: boolean;
    rename: boolean;
    move: boolean;
  };
};

type Settings = {
  "user-agent"?: string;
  strmExtensions?: string[];
  downloadExtensions?: string[];
  mediaMountPath?: string[];
  emby?: { url?: string; apiKey?: string };
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
  mediaExtensions: [".mkv", ".mp4", ".avi", ".mov", ".rmvb", ".flv", ".webm"],
  removeEmptyDirs: true,
  eventTypes: {
    create: true,
    remove: true,
    rename: true,
    move: true,
  },
};

export default function SettingsPage() {
  const [data, setData] = useState<Settings>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [strmExtensionsInput, setStrmExtensionsInput] = useState("");
  const [downloadExtensionsInput, setDownloadExtensionsInput] = useState("");
  const [mediaMountPathInput, setMediaMountPathInput] = useState("");
  
  // Life monitor states
  const [accounts, setAccounts] = useState<string[]>([]);
  const [monitorStates, setMonitorStates] = useState<MonitorState[]>([]);
  const [verifying, setVerifying] = useState(false);
  const [verifyResult, setVerifyResult] = useState<{ success: boolean; message: string } | null>(null);

  // Life monitor form state
  const [monitorEnabled, setMonitorEnabled] = useState(false);
  const [selectedAccounts, setSelectedAccounts] = useState<string[]>([]);
  const [pollInterval, setPollInterval] = useState(30);
  const [pathMappings, setPathMappings] = useState<PathMapping[]>([]);
  const [mediaExtensionsInput, setMediaExtensionsInput] = useState("");
  const [removeEmptyDirs, setRemoveEmptyDirs] = useState(true);
  const [eventTypes, setEventTypes] = useState({
    create: true,
    remove: true,
    rename: true,
    move: true,
  });

  // New path mapping input
  const [newCloudPath, setNewCloudPath] = useState("");
  const [newLocalPath, setNewLocalPath] = useState("");

  useEffect(() => {
    const loadData = async () => {
      try {
        const settingsResp = await axiosInstance.get("/api/settings");
        const settings = settingsResp.data || {};
        setData(settings);
        setStrmExtensionsInput((settings.strmExtensions || []).join(", "));
        setDownloadExtensionsInput((settings.downloadExtensions || []).join(", "));
        setMediaMountPathInput((settings.mediaMountPath || []).join(", "));

        // Load life monitor config
        const monitor = settings.lifeMonitor || DEFAULT_MONITOR_CONFIG;
        setMonitorEnabled(monitor.enabled);
        setSelectedAccounts(monitor.accounts || []);
        setPollInterval(monitor.pollInterval || 30);
        setPathMappings(monitor.pathMappings || []);
        setMediaExtensionsInput((monitor.mediaExtensions || []).join(", "));
        setRemoveEmptyDirs(monitor.removeEmptyDirs ?? true);
        setEventTypes(monitor.eventTypes || DEFAULT_MONITOR_CONFIG.eventTypes);

        // Load accounts
        const accountsResp = await axiosInstance.get("/api/account");
        setAccounts(accountsResp.data?.map?.((a: { name: string }) => a.name) || []);

        // Load monitor states
        const monitorResp = await axiosInstance.get("/api/lifeMonitor");
        setMonitorStates(monitorResp.data?.states || []);
      } catch (err) {
        console.error("Failed to load settings:", err);
        toast.error("加载设置失败");
      } finally {
        setLoading(false);
      }
    };

    loadData();
  }, []);

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

      const mediaMountPath = mediaMountPathInput
        .split(",")
        .map(p => p.trim())
        .filter(p => p.length > 0);

      const mediaExtensions = mediaExtensionsInput
        .split(",")
        .map(ext => ext.trim())
        .filter(ext => ext.length > 0)
        .map(ext => ext.startsWith(".") ? ext : `.${ext}`)
        .map(ext => ext.toLowerCase());

      const saveData = {
        ...data,
        strmExtensions,
        downloadExtensions,
        mediaMountPath,
        lifeMonitor: {
          enabled: monitorEnabled,
          accounts: selectedAccounts,
          pollInterval,
          pathMappings,
          mediaExtensions,
          removeEmptyDirs,
          eventTypes,
        },
      };

      await axiosInstance.put("/api/settings", saveData);
      setData(saveData);
      toast.success("保存成功");
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
    setPathMappings(prev => [...prev, { cloudPath: newCloudPath.trim(), localPath: newLocalPath.trim() }]);
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
      const results = [];
      for (const account of selectedAccounts) {
        const resp = await axiosInstance.post("/api/lifeMonitor", {
          action: "verify",
          account,
        });
        results.push(resp.data);
      }
      const allSuccess = results.every((r: { success: boolean }) => r.success);
      setVerifyResult({
        success: allSuccess,
        message: allSuccess ? "所有账号验证通过" : "部分账号验证失败",
      });
      if (allSuccess) {
        toast.success("账号验证通过，生活事件已开启");
      } else {
        toast.error("部分账号验证失败");
      }
    } catch (err) {
      toast.error("验证失败");
    } finally {
      setVerifying(false);
    }
  };

  const handleStartMonitor = async () => {
    try {
      const resp = await axiosInstance.post("/api/lifeMonitor", {
        action: "updateConfig",
        updates: {
          enabled: monitorEnabled,
          accounts: selectedAccounts,
          pollInterval,
          pathMappings,
          mediaExtensions: mediaExtensionsInput.split(",").map(e => e.trim()).filter(Boolean),
          removeEmptyDirs,
          eventTypes,
        },
      });
      toast.success(resp.data?.message || "监控已更新");
      // Refresh states
      const monitorResp = await axiosInstance.get("/api/lifeMonitor");
      setMonitorStates(monitorResp.data?.states || []);
    } catch (err) {
      toast.error("启动监控失败");
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
    } catch (err) {
      toast.error("停止监控失败");
    }
  };

  const handleStartAccount = async (account: string) => {
    try {
      await axiosInstance.post("/api/lifeMonitor", {
        action: "start",
        account,
      });
      toast.success(`监控已启动: ${account}`);
      const monitorResp = await axiosInstance.get("/api/lifeMonitor");
      setMonitorStates(monitorResp.data?.states || []);
    } catch (err) {
      toast.error("启动监控失败");
    }
  };

  if (loading) return <div>Loading...</div>;

  return (
    <div className="mx-auto max-w-3xl space-y-8">
      <div>
        <h1 className="text-2xl font-semibold">Settings</h1>
        <p className="text-sm text-muted-foreground mt-1">
          配置全局选项与 Emby 通知
        </p>
      </div>

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
          <div className="space-y-2">
            <Label>媒体挂载路径 (mediaMountPath)</Label>
            <Input
              value={mediaMountPathInput}
              onChange={(e) => setMediaMountPathInput(e.target.value)}
              placeholder="/root/webdav/115, /mnt/media"
            />
            <p className="text-xs text-muted-foreground">
              多个路径用逗号分隔，保存后自动重载 nginx
            </p>
          </div>
        </div>
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
        <h2 className="text-base font-medium">Emby</h2>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label>Emby URL</Label>
            <Input
              value={data.emby?.url || ""}
              onChange={(e) =>
                setData({
                  ...data,
                  emby: { ...(data.emby || {}), url: e.target.value },
                })
              }
              placeholder="http://host.docker.internal:8096"
            />
          </div>
          <div className="space-y-2">
            <Label>Emby API Key</Label>
            <Input
              value={data.emby?.apiKey || ""}
              onChange={(e) =>
                setData({
                  ...data,
                  emby: { ...(data.emby || {}), apiKey: e.target.value },
                })
              }
              placeholder="xxxxxxxxxxxxxxxx"
            />
          </div>
        </div>
      </section>

      <Separator />

      {/* Life Monitor Section */}
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
            <div className="space-y-2">
              <Label>媒体文件扩展名</Label>
              <Input
                value={mediaExtensionsInput}
                onChange={(e) => setMediaExtensionsInput(e.target.value)}
                placeholder=".mkv, .mp4, .avi"
              />
              <p className="text-xs text-muted-foreground">
                只处理匹配扩展名的文件
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

          {/* Path Mappings */}
          <div className="space-y-2">
            <Label>路径映射（115 网盘路径 → 本地保存路径）</Label>
            <div className="space-y-2">
              {pathMappings.map((mapping, index) => (
                <div key={index} className="flex gap-2 items-center">
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
                    placeholder="本地路径，如 /data/media/电影"
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
                placeholder="本地路径，如 /data/media/电影"
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
          <div className="flex gap-2">
            <Button
              variant="outline"
              onClick={handleVerify}
              disabled={verifying || selectedAccounts.length === 0}
            >
              {verifying ? "验证中..." : "验证账号的生活事件功能"}
            </Button>
            {verifyResult && (
              <span className={`text-sm ${verifyResult.success ? "text-green-500" : "text-red-500"}`}>
                {verifyResult.message}
              </span>
            )}
          </div>

          {/* Monitor Status */}
          {monitorStates.length > 0 && (
            <div className="space-y-2">
              <Label>监控状态</Label>
              <div className="p-3 border rounded-md space-y-2">
                {monitorStates.map((state) => (
                  <div key={state.account} className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium">{state.account}</span>
                      <span className={`text-xs px-2 py-0.5 rounded ${
                        state.running
                          ? "bg-green-100 text-green-800"
                          : "bg-gray-100 text-gray-800"
                      }`}>
                        {state.running ? "运行中" : "已停止"}
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
                    </div>
                    <Button
                      variant={state.running ? "destructive" : "outline"}
                      size="sm"
                      onClick={() => state.running ? handleStopMonitor(state.account) : handleStartAccount(state.account)}
                    >
                      {state.running ? "停止" : "启动"}
                    </Button>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      </section>

      <div className="pt-2 flex gap-2">
        <Button disabled={saving} onClick={onSave}>
          {saving ? "Saving..." : "保存设置"}
        </Button>
        {monitorEnabled && selectedAccounts.length > 0 && pathMappings.length > 0 && (
          <Button onClick={handleStartMonitor}>
            保存并启动监控
          </Button>
        )}
      </div>
    </div>
  );
}