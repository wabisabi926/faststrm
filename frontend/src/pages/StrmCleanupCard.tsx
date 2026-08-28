// 失效 STRM 清理主组件：组合入口，编排子模块。
// 拆分自原 StrmCleanupCard.tsx（993 行 → 此文件仅负责 layout 与状态装配）。
// 详见 v1.1.1 改进任务清单 T3。

import * as React from "react";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertCircle,
  CheckCircle2,
  DatabaseZap,
  FileWarning,
  ListTree,
  Loader2,
  SearchX,
} from "lucide-react";
import { useStrmCleanup } from "./strm-cleanup/useStrmCleanup";
import { StatCard } from "./strm-cleanup/StatCard";
import { StaleStrmDialog } from "./strm-cleanup/StaleStrmDialog";
import { MissingStrmDialog } from "./strm-cleanup/MissingStrmDialog";
import { CleanupToolbar } from "./strm-cleanup/CleanupToolbar";
import { CleanupLogPanel } from "./strm-cleanup/CleanupLogPanel";
import { MappingDetailList } from "./strm-cleanup/MappingDetailList";

export function StrmCleanupCard() {
  // 对话框开关由组件层管理
  const [staleDialogOpen, setStaleDialogOpen] = React.useState(false);
  const [missingDialogOpen, setMissingDialogOpen] = React.useState(false);
  const [confirmOpen, setConfirmOpen] = React.useState(false);
  const [regenConfirmOpen, setRegenConfirmOpen] = React.useState(false);
  const [regenAllConfirmOpen, setRegenAllConfirmOpen] = React.useState(false);

  const {
    scanning,
    scanResult,
    selectedStale,
    executing,
    reconcileLoading,
    logs,
    allStale,
    allMissing,
    toggleAllStale,
    toggleStale,
    handleScan,
    handleReconcile,
    handleExecuteDelete,
    handleDeleteAll,
    handleDeleteAndRegenerate,
    handleRegenerate,
    clearLogs,
  } = useStrmCleanup({ setStaleDialogOpen });

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 pt-2">
          <div className="flex-1 min-w-0 w-full sm:w-auto">
            <CardTitle className="flex items-center gap-2">
              <DatabaseZap className="size-5 text-indigo-500 shrink-0" />
              <span className="break-words">失效 STRM 清理</span>
            </CardTitle>
            <CardDescription className="mt-2 break-words">
              扫描本地与 115 网盘文件树的一致性，找出网盘已删除但本地仍残留的
              STRM 文件（失效）以及网盘有但本地缺失的 STRM 文件（漏生成），使用生活事件监控配置中的路径映射作为扫描范围
            </CardDescription>
          </div>
          <div className="flex items-center gap-2 shrink-0 flex-wrap">
            <Button onClick={handleScan} disabled={scanning}>
              {scanning ? (
                <>
                  <Loader2 className="mr-2 size-4 animate-spin" />
                  扫描中...
                </>
              ) : (
                <>
                  <ListTree className="mr-2 size-4" />
                  扫描路径映射
                </>
              )}
            </Button>
            <Button
              variant="outline"
              onClick={handleReconcile}
              disabled={reconcileLoading}
            >
              {reconcileLoading ? (
                <Loader2 className="mr-2 size-4 animate-spin" />
              ) : (
                <DatabaseZap className="mr-2 size-4" />
              )}
              {reconcileLoading ? "对账中..." : "全量对账"}
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {!scanResult && !scanning && (
          <Alert>
            <AlertCircle className="size-4" />
            <AlertTitle>未执行扫描</AlertTitle>
            <AlertDescription>
              扫描范围使用「115 生活事件监控」中已配置的路径映射和账号。请先确保路径映射已保存。
            </AlertDescription>
          </Alert>
        )}

        {scanResult?.mappings.some((m) => m.error) && (
          <Alert variant="destructive">
            <AlertCircle className="size-4" />
            <AlertTitle>部分扫描失败</AlertTitle>
            <AlertDescription>
              {scanResult.mappings
                .filter((m) => m.error)
                .map((m) => `${m.cloudPath}: ${m.error}`)
                .join("；")}
            </AlertDescription>
          </Alert>
        )}

        {scanResult && (
          <>
            {/* 统计面板 */}
            <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-3">
              <StatCard
                label="网盘媒体文件"
                value={scanResult.totalRemoteFiles}
                icon={<ListTree className="size-4" />}
                tone="default"
                hint={`对比 ${(scanResult.durationMs / 1000).toFixed(1)} 秒`}
              />
              <StatCard
                label="本地 STRM"
                value={scanResult.totalLocalStrms}
                icon={<CheckCircle2 className="size-4" />}
                tone="default"
              />
              <StatCard
                label="失效 STRM"
                value={scanResult.totalStale}
                icon={<FileWarning className="size-4" />}
                tone={scanResult.totalStale > 0 ? "destructive" : "success"}
              >
                {scanResult.totalStale > 0 && (
                  <StaleStrmDialog
                    open={staleDialogOpen}
                    onOpenChange={setStaleDialogOpen}
                    allStale={allStale}
                    selectedStale={selectedStale}
                    executing={executing}
                    confirmOpen={confirmOpen}
                    setConfirmOpen={setConfirmOpen}
                    toggleAllStale={toggleAllStale}
                    toggleStale={toggleStale}
                    onExecuteDelete={handleExecuteDelete}
                  />
                )}
              </StatCard>
              <StatCard
                label="漏生成 STRM"
                value={scanResult.totalMissing}
                icon={<SearchX className="size-4" />}
                tone={scanResult.totalMissing > 0 ? "warning" : "success"}
              >
                {scanResult.totalMissing > 0 && (
                  <MissingStrmDialog
                    open={missingDialogOpen}
                    onOpenChange={setMissingDialogOpen}
                    allMissing={allMissing}
                    mappings={scanResult.mappings}
                    executing={executing}
                    regenConfirmOpen={regenConfirmOpen}
                    setRegenConfirmOpen={setRegenConfirmOpen}
                    onRegenerate={handleRegenerate}
                  />
                )}
              </StatCard>
            </div>

            {/* 操作工具栏 */}
            <CleanupToolbar
              totalStale={scanResult.totalStale}
              totalMissing={scanResult.totalMissing}
              executing={executing}
              confirmOpen={confirmOpen}
              setConfirmOpen={setConfirmOpen}
              regenAllConfirmOpen={regenAllConfirmOpen}
              setRegenAllConfirmOpen={setRegenAllConfirmOpen}
              onDeleteAll={handleDeleteAll}
              onRegenerate={handleRegenerate}
              onDeleteAndRegenerate={handleDeleteAndRegenerate}
            />

            {/* 操作日志 */}
            <CleanupLogPanel logs={logs} onClear={clearLogs} />

            {/* 路径映射扫描明细 */}
            <MappingDetailList mappings={scanResult.mappings} />
          </>
        )}
      </CardContent>
    </Card>
  );
}
