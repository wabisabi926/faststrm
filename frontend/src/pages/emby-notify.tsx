// Emby 通知主组件：组合入口，编排子模块。
// 拆分自原 emby-notify.tsx（912 行 → 此文件仅负责 layout 与状态装配）。
// 详见 v1.1.1 改进任务清单 T4。

import { useState } from "react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Check, Server, XCircle } from "lucide-react";
import { LocalDirectoryTreeDialog } from "@/pages/task/components/LocalDirectoryTreeDialog";
import { DirectoryTreeDialog } from "@/pages/task/components/DirectoryTreeDialog";
import { useEmbySettings } from "./emby-notify/useEmbySettings";
import { ConnectionSection } from "./emby-notify/ConnectionSection";
import { NotifySettingsSection } from "./emby-notify/NotifySettingsSection";
import { RefreshSettingsSection } from "./emby-notify/RefreshSettingsSection";
import { WebhookInfoCard } from "./emby-notify/WebhookInfoCard";
import { SyncDeleteSection } from "./emby-notify/SyncDeleteSection";

export default function EmbyNotifyPage() {
  const [webhookCopied, setWebhookCopied] = useState(false);

  const {
    settings,
    loading,
    error,
    success,
    testResult,
    accounts,
    updateSetting,
    handleSave,
    testConnection,
    setError,
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
  } = useEmbySettings();

  const copyWebhookUrl = async () => {
    const webhookUrl = `${window.location.origin}/api/emby/webhook`;
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(webhookUrl);
      } else {
        const ta = document.createElement("textarea");
        ta.value = webhookUrl;
        ta.style.position = "fixed";
        ta.style.left = "-9999px";
        ta.style.top = "0";
        document.body.appendChild(ta);
        ta.select();
        document.execCommand("copy");
        document.body.removeChild(ta);
      }
      setWebhookCopied(true);
      setTimeout(() => setWebhookCopied(false), 2000);
    } catch {
      setError("复制失败，请手动复制");
    }
  };

  // FastStrm 是纯浏览器 SPA，无 SSR：组件渲染时 window 永远存在，
  // 直接用 location.origin 跟随当前访问端口（开发 5173 / 生产 8090 都正确），
  // 不再回退到旧版本中写死的历史默认端口。
  const webhookUrl = `${window.location.origin}/api/emby/webhook`;

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      {/* 页面标题 */}
      <div className="min-w-0 break-words">
        <h1 className="text-2xl font-semibold break-words">Emby 通知</h1>
        <p className="text-sm text-muted-foreground mt-1 break-words">配置 Emby 媒体服务器的通知与删除同步</p>
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
      <ConnectionSection
        settings={settings}
        loading={loading}
        updateSetting={updateSetting}
        onSave={handleSave}
        onTest={testConnection}
      />

      {/* 通知设置 */}
      <NotifySettingsSection
        settings={settings}
        loading={loading}
        updateSetting={updateSetting}
        onSave={handleSave}
      />

      {/* 媒体库刷库配置 */}
      <RefreshSettingsSection
        settings={settings}
        loading={loading}
        updateSetting={updateSetting}
        onSave={handleSave}
      />

      {/* Webhook 配置指引 */}
      <WebhookInfoCard
        webhookUrl={webhookUrl}
        copied={webhookCopied}
        onCopy={copyWebhookUrl}
      />

      {/* 删除同步设置 */}
      <SyncDeleteSection
        settings={settings}
        loading={loading}
        accounts={accounts}
        updateSetting={updateSetting}
        onSave={handleSave}
        newMappingEmbyPath={newMappingEmbyPath}
        newMappingCloudPath={newMappingCloudPath}
        newMappingAccount={newMappingAccount}
        setNewMappingEmbyPath={setNewMappingEmbyPath}
        setNewMappingCloudPath={setNewMappingCloudPath}
        setNewMappingAccount={setNewMappingAccount}
        updatePathMapping={updatePathMapping}
        addPathMapping={addPathMapping}
        removePathMapping={removePathMapping}
        openFolderPickerForNew={openFolderPickerForNew}
        openFolderPickerForMapping={openFolderPickerForMapping}
        openCloudPickerForNew={openCloudPickerForNew}
        openCloudPickerForMapping={openCloudPickerForMapping}
      />

      {/* 本地文件夹选择器：用于 Emby 路径前缀的快速选择 */}
      <LocalDirectoryTreeDialog
        open={folderPickerOpen}
        onOpenChange={(open: boolean) => {
          setFolderPickerOpen(open);
          if (!open && folderPickerTarget === null) {
            // 已被 handleFolderSelected 处理
          }
        }}
        onSelect={handleFolderSelected}
      />

      {/* 云盘目录选择器：用于网盘路径前缀的快速选择 */}
      {cloudPickerAccount && (
        <DirectoryTreeDialog
          open={cloudPickerOpen}
          onOpenChange={(open: boolean) => {
            setCloudPickerOpen(open);
          }}
          account={cloudPickerAccount}
          onSelect={handleCloudPathSelected}
        />
      )}
    </div>
  );
}
