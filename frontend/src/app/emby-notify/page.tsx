"use client";

import { useState, useEffect } from "react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Server, Bell, Play, Eye, Copy, Check, RefreshCw, XCircle, FolderOpen, HardDrive } from "lucide-react";
import axiosInstance from "@/lib/axios";
import { LocalDirectoryTreeDialog } from "@/app/task/components/LocalDirectoryTreeDialog";
import { DirectoryTreeDialog } from "@/app/task/components/DirectoryTreeDialog";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";

interface EmbySettings {
  url?: string;
  apiKey?: string;
  notifyMediaAdded?: boolean;
  notifyMediaRemoved?: boolean;
  notifyPlayback?: boolean;
  playbackShowProgress?: boolean;
  playbackShowOverview?: boolean;
  webhookAuth?: string;
  libraryId?: string;
  syncDeleteEnabled?: boolean;
  syncDeletePathMappings?: Array<{ embyPath: string; cloudPath: string; account?: string }>;
  syncDeleteNotify?: boolean;
  syncDeleteDryRun?: boolean;
}

interface TestResult {
  success: boolean;
  message: string;
}

export default function EmbyNotifyPage() {
  const [settings, setSettings] = useState<EmbySettings>({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<TestResult | null>(null);
  const [webhookCopied, setWebhookCopied] = useState(false);

  // 115 账号列表（用于路径映射的账号下拉选择）
  const [accounts, setAccounts] = useState<string[]>([]);

  // 加载当前设置
  useEffect(() => {
    loadSettings();
    void loadAccounts();
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
        setSettings(response.data.emby);
      }
    } catch (err) {
      console.error("加载设置失败:", err);
    }
  };

  const handleSave = async () => {
    try {
      setLoading(true);
      setError(null);
      setSuccess(null);

      const response = await axiosInstance.post("/api/emby/settings", settings);

      if (response.data?.success) {
        setSuccess("Emby 通知设置已保存！");
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

      const response = await axiosInstance.get("/api/emby/test-connection", {
        params: {
          url: settings.url,
          apiKey: settings.apiKey,
        },
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

  const copyWebhookUrl = async () => {
    const webhookUrl = `${window.location.origin}/api/emby/webhook`;
    try {
      await navigator.clipboard.writeText(webhookUrl);
      setWebhookCopied(true);
      setTimeout(() => setWebhookCopied(false), 2000);
    } catch {
      setError("复制失败，请手动复制");
    }
  };

  const updateSetting = <K extends keyof EmbySettings>(key: K, value: EmbySettings[K]) => {
    setSettings((prev) => ({ ...prev, [key]: value }));
  };

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

  // 云盘目录选择器
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

  const updatePathMapping = (index: number, field: "embyPath" | "cloudPath" | "account", value: string) => {
    const mappings = [...(settings.syncDeletePathMappings || [])];
    mappings[index] = { ...mappings[index], [field]: value };
    updateSetting("syncDeletePathMappings", mappings);
  };

  const addPathMapping = () => {
    if (!newMappingEmbyPath || !newMappingCloudPath) return;
    const mappings = [...(settings.syncDeletePathMappings || [])];
    mappings.push({
      embyPath: newMappingEmbyPath,
      cloudPath: newMappingCloudPath,
      account: newMappingAccount || undefined,
    });
    updateSetting("syncDeletePathMappings", mappings);
    setNewMappingEmbyPath("");
    setNewMappingCloudPath("");
    setNewMappingAccount("");
  };

  const removePathMapping = (index: number) => {
    const mappings = [...(settings.syncDeletePathMappings || [])];
    mappings.splice(index, 1);
    updateSetting("syncDeletePathMappings", mappings);
  };

  const webhookUrl = typeof window !== "undefined" ? `${window.location.origin}/api/emby/webhook` : "http://localhost:3000/api/emby/webhook";

  return (
    <div className="container mx-auto p-6 space-y-6">
      {/* 页面标题 */}
      <div className="flex items-center space-x-2">
        <Bell className="h-6 w-6" />
        <h1 className="text-3xl font-bold">Emby 通知</h1>
      </div>

      {/* 错误/成功提示 */}
      {error && (
        <Alert variant="destructive">
          <Server className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {success && (
        <Alert>
          <Check className="h-4 w-4" />
          <AlertDescription>{success}</AlertDescription>
        </Alert>
      )}

      {testResult && (
        <Alert variant={testResult.success ? "default" : "destructive"}>
          {testResult.success ? <Check className="h-4 w-4" /> : <XCircle className="h-4 w-4" />}
          <AlertDescription>{testResult.message}</AlertDescription>
        </Alert>
      )}

      {/* Emby 连接配置 */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center space-x-2">
            <Server className="h-5 w-5" />
            <span>Emby 连接配置</span>
          </CardTitle>
          <CardDescription>配置 Emby 服务器连接信息</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label htmlFor="embyUrl">Emby URL</Label>
              <Input
                id="embyUrl"
                placeholder="http://192.168.1.100:8096"
                value={settings.url || ""}
                onChange={(e) => updateSetting("url", e.target.value)}
              />
              <p className="text-xs text-muted-foreground">
                Emby 服务器地址，包括端口号
              </p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="embyApiKey">Emby API Key</Label>
              <Input
                id="embyApiKey"
                type="password"
                placeholder="xxxxxxxxxxxxxxxxxxxxxxxx"
                value={settings.apiKey || ""}
                onChange={(e) => updateSetting("apiKey", e.target.value)}
              />
              <p className="text-xs text-muted-foreground">
                在 Emby 后台「设置 → 高级设置 → API Key」获取
              </p>
            </div>
          </div>
          <div className="flex space-x-2">
            <Button onClick={handleSave} disabled={loading}>
              {loading ? "保存中..." : "保存配置"}
            </Button>
            <Button variant="outline" onClick={testConnection} disabled={loading}>
              <RefreshCw className="h-4 w-4 mr-2" />
              测试连接
            </Button>
          </div>
        </CardContent>
      </Card>

      {/* 通知设置 */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center space-x-2">
            <Bell className="h-5 w-5" />
            <span>通知设置</span>
          </CardTitle>
          <CardDescription>选择需要启用的通知类型</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div className="flex items-center space-x-2 p-3 rounded-lg border">
              <Checkbox
                id="notifyMediaAdded"
                checked={!!settings.notifyMediaAdded}
                onCheckedChange={(checked) =>
                  updateSetting("notifyMediaAdded", checked === true)
                }
              />
              <div className="flex-1">
                <Label htmlFor="notifyMediaAdded" className="cursor-pointer">
                  入库通知
                </Label>
                <p className="text-xs text-muted-foreground">
                  电影/剧集入库时发送通知
                </p>
              </div>
            </div>

            <div className="flex items-center space-x-2 p-3 rounded-lg border">
              <Checkbox
                id="notifyMediaRemoved"
                checked={!!settings.notifyMediaRemoved}
                onCheckedChange={(checked) =>
                  updateSetting("notifyMediaRemoved", checked === true)
                }
              />
              <div className="flex-1">
                <Label htmlFor="notifyMediaRemoved" className="cursor-pointer">
                  删除通知
                </Label>
                <p className="text-xs text-muted-foreground">
                  媒体删除时发送通知
                </p>
              </div>
            </div>

            <div className="flex items-center space-x-2 p-3 rounded-lg border">
              <Checkbox
                id="notifyPlayback"
                checked={!!settings.notifyPlayback}
                onCheckedChange={(checked) =>
                  updateSetting("notifyPlayback", checked === true)
                }
              />
              <div className="flex-1">
                <Label htmlFor="notifyPlayback" className="cursor-pointer">
                  播放通知
                </Label>
                <p className="text-xs text-muted-foreground">
                  播放开始/暂停/结束时发送通知
                </p>
              </div>
            </div>

            <div className="flex items-center space-x-2 p-3 rounded-lg border">
              <Checkbox
                id="playbackShowProgress"
                checked={!!settings.playbackShowProgress}
                onCheckedChange={(checked) =>
                  updateSetting("playbackShowProgress", checked === true)
                }
                disabled={!settings.notifyPlayback}
              />
              <div className="flex-1">
                <Label
                  htmlFor="playbackShowProgress"
                  className={`cursor-pointer ${!settings.notifyPlayback ? "text-muted-foreground" : ""}`}
                >
                  显示播放进度
                </Label>
                <p className="text-xs text-muted-foreground">
                  在播放通知中显示进度百分比
                </p>
              </div>
            </div>
          </div>

          <div className="flex space-x-2">
            <Button onClick={handleSave} disabled={loading}>
              {loading ? "保存中..." : "保存设置"}
            </Button>
          </div>
        </CardContent>
      </Card>

      {/* Webhook 配置指引 */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center space-x-2">
            <Play className="h-5 w-5" />
            <span>Webhook 配置指引</span>
          </CardTitle>
          <CardDescription>
            在 Emby 后台配置 Webhook 以接收通知
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-4">
            <div className="space-y-2">
              <h4 className="font-medium">步骤 1：打开 Emby Webhook 设置</h4>
              <p className="text-sm text-muted-foreground">
                在 Emby 后台：<code className="bg-muted px-1 rounded">设置 → 通知 → Webhook</code>
              </p>
            </div>

            <div className="space-y-2">
              <h4 className="font-medium">步骤 2：添加 Webhook URL</h4>
              <div className="flex items-center space-x-2">
                <code className="flex-1 bg-muted px-3 py-2 rounded text-sm font-mono truncate">
                  {webhookUrl}
                </code>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={copyWebhookUrl}
                  className="shrink-0"
                >
                  {webhookCopied ? (
                    <>
                      <Check className="h-4 w-4 mr-1" />
                      已复制
                    </>
                  ) : (
                    <>
                      <Copy className="h-4 w-4 mr-1" />
                      复制
                    </>
                  )}
                </Button>
              </div>
            </div>

            <div className="space-y-2">
              <h4 className="font-medium">步骤 3：勾选事件类型</h4>
              <p className="text-sm text-muted-foreground">
                在 Emby Webhook 设置中勾选以下事件：
              </p>
              <div className="flex flex-wrap gap-2">
                <code className="bg-green-100 dark:bg-green-900/30 text-green-800 dark:text-green-200 px-2 py-1 rounded text-xs">
                  媒体入库 (library.new)
                </code>
                <code className="bg-green-100 dark:bg-green-900/30 text-green-800 dark:text-green-200 px-2 py-1 rounded text-xs">
                  媒体删除 (library.deleted)
                </code>
                <code className="bg-green-100 dark:bg-green-900/30 text-green-800 dark:text-green-200 px-2 py-1 rounded text-xs">
                  播放开始 (playback.start)
                </code>
                <code className="bg-green-100 dark:bg-green-900/30 text-green-800 dark:text-green-200 px-2 py-1 rounded text-xs">
                  播放暂停 (playback.pause)
                </code>
                <code className="bg-green-100 dark:bg-green-900/30 text-green-800 dark:text-green-200 px-2 py-1 rounded text-xs">
                  播放结束 (playback.stop)
                </code>
              </div>
            </div>

            <div className="rounded-lg bg-muted/50 p-3">
              <p className="text-sm text-muted-foreground">
                <strong>提示：</strong>配置完成后，Emby 的媒体变动（入库/删除）和播放状态将实时推送到你的 Telegram。
              </p>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* 删除同步设置 */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center space-x-2">
            <XCircle className="h-5 w-5" />
            <span>删除同步</span>
          </CardTitle>
          <CardDescription>
            监听 Emby 删除事件，自动删除本地 STRM 文件 + 关联字幕/图片 + 清理 DB 记录
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <div className="flex items-center space-x-2 p-3 rounded-lg border">
              <Checkbox
                id="syncDeleteEnabled"
                checked={!!settings.syncDeleteEnabled}
                onCheckedChange={(checked) =>
                  updateSetting("syncDeleteEnabled", checked === true)
                }
              />
              <div className="flex-1">
                <Label htmlFor="syncDeleteEnabled" className="cursor-pointer">
                  启用删除同步
                </Label>
                <p className="text-xs text-muted-foreground">
                  Emby 删除媒体时自动清理 STRM
                </p>
              </div>
            </div>

            <div className="flex items-center space-x-2 p-3 rounded-lg border">
              <Checkbox
                id="syncDeleteDryRun"
                checked={!!settings.syncDeleteDryRun}
                onCheckedChange={(checked) =>
                  updateSetting("syncDeleteDryRun", checked === true)
                }
                disabled={!settings.syncDeleteEnabled}
              />
              <div className="flex-1">
                <Label
                  htmlFor="syncDeleteDryRun"
                  className={`cursor-pointer ${!settings.syncDeleteEnabled ? "text-muted-foreground" : ""}`}
                >
                  试运行模式
                </Label>
                <p className="text-xs text-muted-foreground">
                  只记日志不实际删除（首次验证用）
                </p>
              </div>
            </div>

            <div className="flex items-center space-x-2 p-3 rounded-lg border">
              <Checkbox
                id="syncDeleteNotify"
                checked={!!settings.syncDeleteNotify}
                onCheckedChange={(checked) =>
                  updateSetting("syncDeleteNotify", checked === true)
                }
                disabled={!settings.syncDeleteEnabled}
              />
              <div className="flex-1">
                <Label
                  htmlFor="syncDeleteNotify"
                  className={`cursor-pointer ${!settings.syncDeleteEnabled ? "text-muted-foreground" : ""}`}
                >
                  删除通知
                </Label>
                <p className="text-xs text-muted-foreground">
                  删除时发送 TG 通知（替代上方删除通知）
                </p>
              </div>
            </div>
          </div>

          {/* 路径映射表 */}
          <div className="space-y-2">
            <Label>路径映射（Emby 路径 → 115 网盘路径）</Label>
            {(settings.syncDeletePathMappings || []).map((mapping, index) => (
              <div key={index} className="flex gap-2 items-center">
                <div className="flex-1 flex gap-1 items-center">
                  <Input
                    className="flex-1"
                    placeholder="Emby 路径前缀，如 /app/data/strm/电影"
                    value={mapping.embyPath}
                    onChange={(e) => updatePathMapping(index, "embyPath", e.target.value)}
                  />
                  <Button
                    type="button"
                    variant="outline"
                    size="icon"
                    className="shrink-0"
                    onClick={() => openFolderPickerForMapping(index)}
                    title="选择本地文件夹"
                  >
                    <FolderOpen className="h-4 w-4" />
                  </Button>
                </div>
                <span className="text-muted-foreground">→</span>
                <div className="flex-1 flex gap-1 items-center">
                  <Input
                    className="flex-1"
                    placeholder="网盘路径前缀，如 /电影"
                    value={mapping.cloudPath}
                    onChange={(e) => updatePathMapping(index, "cloudPath", e.target.value)}
                  />
                  <TooltipProvider>
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <span className="shrink-0 inline-flex">
                          <Button
                            type="button"
                            variant="outline"
                            size="icon"
                            className="shrink-0"
                            disabled={!mapping.account}
                            onClick={() => openCloudPickerForMapping(index)}
                          >
                            <HardDrive className="h-4 w-4" />
                          </Button>
                        </span>
                      </TooltipTrigger>
                      <TooltipContent>
                        {mapping.account ? "选择网盘目录" : "请先选择具体账号"}
                      </TooltipContent>
                    </Tooltip>
                  </TooltipProvider>
                </div>
                <Select
                  value={mapping.account || "__all__"}
                  onValueChange={(v) => updatePathMapping(index, "account", v === "__all__" ? "" : v)}
                >
                  <SelectTrigger className="w-[130px] h-10">
                    <SelectValue placeholder="账号（可选）" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="__all__">遍历全部账号</SelectItem>
                    {accounts.map((acc) => (
                      <SelectItem key={acc} value={acc}>
                        {acc}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() => removePathMapping(index)}
                >
                  删除
                </Button>
              </div>
            ))}
            <div className="flex gap-2 items-center">
              <div className="flex-1 flex gap-1 items-center">
                <Input
                  className="flex-1"
                  placeholder="Emby 路径前缀"
                  value={newMappingEmbyPath}
                  onChange={(e) => setNewMappingEmbyPath(e.target.value)}
                />
                <Button
                  type="button"
                  variant="outline"
                  size="icon"
                  className="shrink-0"
                  onClick={openFolderPickerForNew}
                  title="选择本地文件夹"
                >
                  <FolderOpen className="h-4 w-4" />
                </Button>
              </div>
              <span className="text-muted-foreground">→</span>
              <div className="flex-1 flex gap-1 items-center">
                <Input
                  className="flex-1"
                  placeholder="网盘路径前缀"
                  value={newMappingCloudPath}
                  onChange={(e) => setNewMappingCloudPath(e.target.value)}
                />
                <TooltipProvider>
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <span className="shrink-0 inline-flex">
                        <Button
                          type="button"
                          variant="outline"
                          size="icon"
                          className="shrink-0"
                          disabled={!newMappingAccount}
                          onClick={openCloudPickerForNew}
                        >
                          <HardDrive className="h-4 w-4" />
                        </Button>
                      </span>
                    </TooltipTrigger>
                    <TooltipContent>
                      {newMappingAccount ? "选择网盘目录" : "请先选择具体账号"}
                    </TooltipContent>
                  </Tooltip>
                </TooltipProvider>
              </div>
              <Select
                value={newMappingAccount || "__all__"}
                onValueChange={(v) => setNewMappingAccount(v === "__all__" ? "" : v)}
              >
                <SelectTrigger className="w-[130px] h-10">
                  <SelectValue placeholder="账号（可选）" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__all__">遍历全部账号</SelectItem>
                  {accounts.map((acc) => (
                    <SelectItem key={acc} value={acc}>
                      {acc}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Button size="sm" onClick={addPathMapping}>
                添加
              </Button>
            </div>
            <p className="text-xs text-muted-foreground">
              只有匹配到 Emby 路径前缀的删除事件才会被处理。账号留空时遍历所有 115 账号删除 DB 记录。
            </p>
          </div>

          <div className="flex space-x-2">
            <Button onClick={handleSave} disabled={loading}>
              {loading ? "保存中..." : "保存设置"}
            </Button>
          </div>

          <div className="p-3 bg-muted/50 rounded-lg text-sm">
            <p className="font-semibold mb-1">工作流程</p>
            <p className="text-xs text-muted-foreground">
              Emby 删除媒体 → 匹配路径映射 → 去重检查（60s） → 防误删（STRM 存在 + 标题匹配 + 目录文件数 ≤100） → 删 STRM + 字幕 + nfo → 清空目录 → 更新 DB → TG 通知
            </p>
          </div>
        </CardContent>
      </Card>

      {/* 注意事项 */}
      <Alert>
        <Eye className="h-4 w-4" />
        <AlertDescription>
          <strong>注意：</strong>本页面需要配置正确的 Emby URL 和 API Key。如果使用 Docker 部署，请确保 Emby 容器可以访问到 faststrm 服务。
        </AlertDescription>
      </Alert>

      {/* 本地文件夹选择器：用于 Emby 路径前缀的快速选择 */}
      <LocalDirectoryTreeDialog
        open={folderPickerOpen}
        onOpenChange={(open) => {
          setFolderPickerOpen(open);
          if (!open) setFolderPickerTarget(null);
        }}
        onSelect={handleFolderSelected}
      />

      {/* 云盘目录选择器：用于网盘路径前缀的快速选择 */}
      {cloudPickerAccount && (
        <DirectoryTreeDialog
          open={cloudPickerOpen}
          onOpenChange={(open) => {
            setCloudPickerOpen(open);
            if (!open) {
              setCloudPickerTarget(null);
              setCloudPickerAccount("");
            }
          }}
          account={cloudPickerAccount}
          onSelect={handleCloudPathSelected}
        />
      )}
    </div>
  );
}
