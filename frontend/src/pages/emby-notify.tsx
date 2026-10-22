// Emby 通知主组件：组合入口，编排子模块。
// 拆分自原 emby-notify.tsx（912 行 → 此文件仅负责 layout 与状态装配）。
// 详见 v1.1.1 改进任务清单 T4。

import { useState } from "react";
import { Link } from "react-router-dom";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Check, ChevronDown, Server, XCircle } from "lucide-react";
import { LocalDirectoryTreeDialog } from "@/pages/task/components/LocalDirectoryTreeDialog";
import { DirectoryTreeDialog } from "@/pages/task/components/DirectoryTreeDialog";
import { useEmbySettings } from "./emby-notify/useEmbySettings";
import { ConnectionSection } from "./emby-notify/ConnectionSection";
import { NotifySettingsSection } from "./emby-notify/NotifySettingsSection";
import { RefreshSettingsSection } from "./emby-notify/RefreshSettingsSection";
import { WebhookInfoCard } from "./emby-notify/WebhookInfoCard";
import { SyncDeleteSection } from "./emby-notify/SyncDeleteSection";

export default function EmbyNotifyPage({ embedded = false }: { embedded?: boolean }) {
  const [webhookCopied, setWebhookCopied] = useState(false);
  const [advancedOpen, setAdvancedOpen] = useState(false);

  const {
    settings,
    loaded,
    telegram,
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

  // FastStrm 是纯浏览器 SPA，无 SSR：组件渲染时 window 永远存在，
  // 直接用 location.origin 跟随当前访问端口（开发 5173 / 生产 8090 都正确）。
  const webhookAuth = (settings.webhookAuth || "").trim();
  const webhookUrl = `${window.location.origin}/api/emby/webhook${
    webhookAuth ? `?auth=${encodeURIComponent(webhookAuth)}` : ""
  }`;

  const copyWebhookUrl = async () => {
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

  return (
    <div className={`${embedded ? "" : "mx-auto max-w-5xl"} space-y-6`}>
      {/* 页面标题 */}
      {!embedded && (
        <div className="min-w-0 break-words">
          <h1 className="text-xl sm:text-2xl font-semibold break-words">Emby 通知</h1>
          <p className="text-sm text-muted-foreground mt-1 break-words">配置 Emby 媒体服务器的通知与删除同步</p>
        </div>
      )}

      {/* 错误/成功提示 */}
      {error && (
        <Alert variant="destructive">
          <Server className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {loaded && !telegram.configured && (
        <Alert>
          <Server className="h-4 w-4" />
          <AlertDescription>
            Emby 通知通过 Telegram 发送，但当前未配置 Telegram 机器人，通知会被静默丢弃。
            <Link to="/notify?tab=telegram" className="ml-1 underline underline-offset-2">
              去配置 Telegram
            </Link>
          </AlertDescription>
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
        onTest={testConnection}
      />

      {/* 通知设置 */}
      <NotifySettingsSection
        settings={settings}
        updateSetting={updateSetting}
      />

      {/* 高级设置：刷库、Webhook 指引、删除同步 */}
      <div className="space-y-4">
        <button
          type="button"
          onClick={() => setAdvancedOpen((v) => !v)}
          aria-expanded={advancedOpen}
          className="flex w-full items-center justify-between gap-3 rounded-md border p-4 text-left transition-colors hover:bg-muted/40"
        >
          <div>
            <h2 className="text-base font-medium">高级设置</h2>
            <p className="mt-1 text-xs text-muted-foreground">
              媒体库刷新、Webhook 配置指引与删除同步
            </p>
          </div>
          <ChevronDown
            className={`h-4 w-4 shrink-0 text-muted-foreground transition-transform ${
              advancedOpen ? "rotate-180" : ""
            }`}
          />
        </button>

        {advancedOpen && (
          <div className="space-y-4">
            <RefreshSettingsSection
              settings={settings}
              updateSetting={updateSetting}
            />

            <WebhookInfoCard
              webhookUrl={webhookUrl}
              authConfigured={Boolean(webhookAuth)}
              telegramConfigured={telegram.configured}
              copied={webhookCopied}
              onCopy={copyWebhookUrl}
            />

            <SyncDeleteSection
              settings={settings}
              accounts={accounts}
              updateSetting={updateSetting}
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
          </div>
        )}
      </div>

      <div className="sticky bottom-0 z-10 flex flex-wrap items-center gap-3 border-t bg-background/95 px-1 py-3 backdrop-blur-sm">
        <Button onClick={() => void handleSave()} disabled={!loaded || loading} className="w-full sm:w-auto">
          {loading ? "保存中..." : "保存 Emby 设置"}
        </Button>
        {!loaded && (
          <span className="text-xs text-muted-foreground">
            设置尚未加载成功，保存已禁用，避免覆盖现有配置。
          </span>
        )}
      </div>

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
