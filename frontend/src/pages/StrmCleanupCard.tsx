import * as React from "react";
import axiosInstance from "@/lib/axios";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Checkbox } from "@/components/ui/checkbox";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { toast } from "sonner";
import {
  AlertCircle,
  CheckCircle2,
  DatabaseZap,
  Loader2,
  SearchX,
  Trash2,
  FileWarning,
  ListTree,
  Zap,
  RefreshCw,
} from "lucide-react";

type StaleStrm = {
  relPath: string;
  fullPath?: string;
  strmContent?: string;
  localPath: string;
  mappingId: string;
};

type MissingStrm = {
  relPath: string;
  mediaExtension: string;
  mappingId: string;
};

type MappingResult = {
  mappingId: string;
  account: string;
  cloudPath: string;
  localPath: string;
  remoteFileCount: number;
  localStrmCount: number;
  staleStrms: StaleStrm[];
  missingStrms: MissingStrm[];
  error?: string;
};

type ScanSummary = {
  totalRemoteFiles: number;
  totalLocalStrms: number;
  totalStale: number;
  totalMissing: number;
  durationMs: number;
  mappings: MappingResult[];
};

type ExecuteResult = {
  deletedCount: number;
  failedCount: number;
  errors: Array<{ path: string; error: string }>;
  removedEmptyDirs: string[];
  dryRun: boolean;
  durationMs: number;
  regeneratedCount?: number;
  deletedAllCount?: number;
  cleanupSummary?: {
    deleted: number;
    regenerated: number;
    failed: number;
  };
};

type LogEntry = {
  time: string;
  action: string;
  detail: string;
  success: boolean;
};

type ReconcileItem = {
  cloudFileCount: number;
  localStrmCount: number;
  dbRecordCount: number;
  staleStrms: unknown[];
  missingStrms: unknown[];
};

type ReconcileResponse = {
  results?: ReconcileItem[];
};

type AxiosError = {
  response?: { data?: { error?: string } };
  message?: string;
};

export function StrmCleanupCard() {
  const [scanning, setScanning] = React.useState(false);
  const [scanResult, setScanResult] = React.useState<ScanSummary | null>(null);
  const [staleDialogOpen, setStaleDialogOpen] = React.useState(false);
  const [missingDialogOpen, setMissingDialogOpen] = React.useState(false);
  const [selectedStale, setSelectedStale] = React.useState<Set<string>>(new Set());
  const [executing, setExecuting] = React.useState(false);
  const [confirmOpen, setConfirmOpen] = React.useState(false);
  const [logs, setLogs] = React.useState<LogEntry[]>([]);
  const [regenConfirmOpen, setRegenConfirmOpen] = React.useState(false);
  const [regenAllConfirmOpen, setRegenAllConfirmOpen] = React.useState(false);
  const [reconcileLoading, setReconcileLoading] = React.useState(false);

  const allStale = React.useMemo(() => {
    if (!scanResult) return [] as StaleStrm[];
    return scanResult.mappings.flatMap((m) => m.staleStrms);
  }, [scanResult]);

  const allMissing = React.useMemo(() => {
    if (!scanResult) return [] as MissingStrm[];
    return scanResult.mappings.flatMap((m) => m.missingStrms);
  }, [scanResult]);

  const appendLog = (action: string, detail: string, success: boolean) => {
    setLogs((prev) => [
      { time: new Date().toLocaleTimeString(), action, detail, success },
      ...prev.slice(0, 49),
    ]);
  };

  const handleScan = async () => {
    setScanning(true);
    setScanResult(null);
    setSelectedStale(new Set());
    try {
      const res = await axiosInstance.post("/api/strmCleanup/scan", {
        useSettingsDefaults: true,
      });
      const data = res.data as ScanSummary;
      const normalizedMappings: MappingResult[] = data.mappings.map(
        (raw: Omit<MappingResult, "mappingId">, idx: number) => ({
          mappingId: `mapping-${idx}`,
          account: raw.account,
          cloudPath: raw.cloudPath,
          localPath: raw.localPath,
          remoteFileCount: raw.remoteFileCount,
          localStrmCount: raw.localStrmCount,
          staleStrms: (raw.staleStrms || []).map((s: Omit<StaleStrm, "localPath" | "mappingId">) => ({
            ...s,
            localPath: raw.localPath,
            mappingId: `mapping-${idx}`,
          })),
          missingStrms: (raw.missingStrms || []).map((m: Omit<MissingStrm, "mappingId">) => ({
            ...m,
            mappingId: `mapping-${idx}`,
          })),
          error: raw.error,
        })
      );
      setScanResult({
        totalRemoteFiles: data.totalRemoteFiles ?? 0,
        totalLocalStrms: data.totalLocalStrms ?? 0,
        totalStale: data.totalStale ?? 0,
        totalMissing: data.totalMissing ?? 0,
        durationMs: data.durationMs ?? 0,
        mappings: normalizedMappings,
      });
      appendLog("扫描", `完成：${data.totalStale ?? 0} 失效 / ${data.totalMissing ?? 0} 漏生成`, true);
      toast.success(
        `扫描完成：发现 ${data.totalStale ?? 0} 个失效 STRM，${data.totalMissing ?? 0} 个漏生成`
      );
    } catch (err) {
      const axiosErr = err as AxiosError;
      const msg = axiosErr?.response?.data?.error || axiosErr?.message || "扫描失败";
      appendLog("扫描", msg, false);
      toast.error(msg);
      console.error(err);
    } finally {
      setScanning(false);
    }
  };

  const handleReconcile = async () => {
    setReconcileLoading(true);
    try {
      const res = await axiosInstance.post("/api/strmCleanup/scan", {
        action: "reconcile",
        useSettingsDefaults: true,
      });
      const data = res.data as ReconcileResponse;
      // 汇总对账结果
      const totalCloud = data.results?.reduce((s: number, r: ReconcileItem) => s + r.cloudFileCount, 0) || 0;
      const totalLocal = data.results?.reduce((s: number, r: ReconcileItem) => s + r.localStrmCount, 0) || 0;
      const totalDb = data.results?.reduce((s: number, r: ReconcileItem) => s + r.dbRecordCount, 0) || 0;
      const totalStale = data.results?.reduce((s: number, r: ReconcileItem) => s + r.staleStrms.length, 0) || 0;
      const totalMissing = data.results?.reduce((s: number, r: ReconcileItem) => s + r.missingStrms.length, 0) || 0;
      appendLog("全量对账", `云端:${totalCloud} 本地:${totalLocal} DB:${totalDb} 失效:${totalStale} 缺失:${totalMissing}`, true);
      toast.success(`全量对账完成`, {
        description: `云端: ${totalCloud} | 本地STRM: ${totalLocal} | DB记录: ${totalDb} | 失效: ${totalStale} | 缺失: ${totalMissing}`,
        duration: 10000,
      });
    } catch (err) {
      const axiosErr = err as AxiosError;
      const msg = axiosErr?.response?.data?.error || axiosErr?.message || "对账失败";
      appendLog("全量对账", msg, false);
      toast.error("对账失败", { description: msg });
      console.error(err);
    } finally {
      setReconcileLoading(false);
    }
  };

  const toggleAllStale = (checked: boolean) => {
    if (checked) {
      setSelectedStale(new Set(allStale.map((s) => s.mappingId + "::" + s.relPath)));
    } else {
      setSelectedStale(new Set());
    }
  };

  const toggleStale = (key: string, checked: boolean) => {
    setSelectedStale((prev) => {
      const next = new Set(prev);
      if (checked) next.add(key);
      else next.delete(key);
      return next;
    });
  };

  const buildEntriesFromSelection = () => {
    const entriesByLocal = new Map<
      string,
      { localPath: string; staleRelPaths: string[] }
    >();
    for (const key of selectedStale) {
      const [mappingId, relPath] = key.split(/::([\s\S]*)/);
      const stale = allStale.find(
        (s) => s.mappingId === mappingId && s.relPath === relPath
      );
      if (!stale) continue;
      if (!entriesByLocal.has(stale.localPath)) {
        entriesByLocal.set(stale.localPath, {
          localPath: stale.localPath,
          staleRelPaths: [],
        });
      }
      entriesByLocal.get(stale.localPath)!.staleRelPaths.push(relPath);
    }
    return [...entriesByLocal.values()];
  };

  const handleExecuteDelete = async () => {
    setExecuting(true);
    setConfirmOpen(false);
    try {
      const entries = buildEntriesFromSelection();
      const res = await axiosInstance.post("/api/strmCleanup/execute", {
        entries,
        action: "delete",
        dryRun: false,
      });
      handleDeleteResult(res.data);
    } catch (err) {
      const axiosErr = err as AxiosError;
      const msg = axiosErr?.response?.data?.error || axiosErr?.message || "删除失败";
      appendLog("删除", msg, false);
      toast.error(msg);
      console.error(err);
    } finally {
      setExecuting(false);
    }
  };

  const handleDeleteAll = async () => {
    setExecuting(true);
    setConfirmOpen(false);
    try {
      const res = await axiosInstance.post("/api/strmCleanup/execute", {
        entries: [],
        action: "delete_all",
        scanSummary: { mappings: scanResult!.mappings },
        dryRun: false,
      });
      handleDeleteResult(res.data);
    } catch (err) {
      const axiosErr = err as AxiosError;
      const msg = axiosErr?.response?.data?.error || axiosErr?.message || "批量删除失败";
      appendLog("批量删除", msg, false);
      toast.error(msg);
    } finally {
      setExecuting(false);
    }
  };

  const handleDeleteAndRegenerate = async () => {
    setExecuting(true);
    setConfirmOpen(false);
    try {
      const entries = buildEntriesFromSelection();
      const missingItems = allMissing.map((m) => ({
        localPath: scanResult!.mappings.find((x) => x.mappingId === m.mappingId)?.localPath || "",
        relPath: m.relPath,
        mappingId: m.mappingId,
      }));
      const res = await axiosInstance.post("/api/strmCleanup/execute", {
        entries,
        action: "delete_and_regenerate",
        scanSummary: { mappings: scanResult!.mappings },
        missingItems,
        dryRun: false,
      });
      handleCombinedResult(res.data);
    } catch (err) {
      const axiosErr = err as AxiosError;
      const msg = axiosErr?.response?.data?.error || axiosErr?.message || "组合操作失败";
      appendLog("清理+补生成", msg, false);
      toast.error(msg);
    } finally {
      setExecuting(false);
    }
  };

  const handleRegenerate = async () => {
    setExecuting(true);
    setRegenConfirmOpen(false);
    try {
      const missingItems = allMissing.map((m) => ({
        localPath: scanResult!.mappings.find((x) => x.mappingId === m.mappingId)?.localPath || "",
        relPath: m.relPath,
        mappingId: m.mappingId,
      }));
      const res = await axiosInstance.post("/api/strmCleanup/execute", {
        entries: [],
        action: "regenerate",
        scanSummary: { mappings: scanResult!.mappings },
        missingItems,
        dryRun: false,
      });
      handleRegenerateResult(res.data);
    } catch (err) {
      const axiosErr = err as AxiosError;
      const msg = axiosErr?.response?.data?.error || axiosErr?.message || "补生成失败";
      appendLog("补生成", msg, false);
      toast.error(msg);
    } finally {
      setExecuting(false);
    }
  };

  const handleDeleteResult = (r: ExecuteResult) => {
    appendLog("删除", `删除 ${r.deletedCount} 个失效 STRM，失败 ${r.failedCount} 个`, r.failedCount === 0);
    toast.success(
      `已删除 ${r.deletedCount} 个失效 STRM，清理了 ${r.removedEmptyDirs.length} 个空目录` +
        (r.failedCount > 0 ? `，失败 ${r.failedCount} 个` : "")
    );
    setScanResult((prev) => {
      if (!prev) return prev;
      const deletedKeys = new Set(selectedStale);
      const newMappings = prev.mappings.map((m) => ({
        ...m,
        staleStrms: m.staleStrms.filter(
          (s) => !deletedKeys.has(s.mappingId + "::" + s.relPath)
        ),
      }));
      const remainingStale = newMappings.reduce((s, m) => s + m.staleStrms.length, 0);
      return {
        ...prev,
        mappings: newMappings,
        totalStale: remainingStale,
        totalLocalStrms: prev.totalLocalStrms - r.deletedCount,
      };
    });
    setSelectedStale(new Set());
    setStaleDialogOpen(false);
  };

  const handleRegenerateResult = (r: ExecuteResult) => {
    appendLog("补生成", `生成 ${r.regeneratedCount || 0} 个 STRM，失败 ${r.failedCount} 个`, r.failedCount === 0);
    toast.success(
      `已补生成 ${r.regeneratedCount || 0} 个 STRM` +
        (r.failedCount > 0 ? `，失败 ${r.failedCount} 个` : "")
    );
  };

  const handleCombinedResult = (r: ExecuteResult) => {
    const summary = r.cleanupSummary;
    const parts: string[] = [];
    if (summary) {
      parts.push(`清理 ${summary.deleted}`);
      parts.push(`补生成 ${summary.regenerated}`);
      if (summary.failed > 0) parts.push(`失败 ${summary.failed}`);
    }
    appendLog("清理+补生成", parts.join(" / "), (summary?.failed || 0) === 0);
    toast.success(`组合操作完成：${parts.join("，")}`);
    setScanResult((prev) => {
      if (!prev) return prev;
      const deletedKeys = new Set(selectedStale);
      const newMappings = prev.mappings.map((m) => ({
        ...m,
        staleStrms: m.staleStrms.filter(
          (s) => !deletedKeys.has(s.mappingId + "::" + s.relPath)
        ),
      }));
      const remainingStale = newMappings.reduce((s, m) => s + m.staleStrms.length, 0);
      return { ...prev, mappings: newMappings, totalStale: remainingStale };
    });
    setSelectedStale(new Set());
    setStaleDialogOpen(false);
  };

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
                  <Dialog
                    open={staleDialogOpen}
                    onOpenChange={setStaleDialogOpen}
                  >
                    <DialogTrigger asChild>
                      <Button variant="link" size="sm" className="h-auto p-0">
                        查看并清理
                      </Button>
                    </DialogTrigger>
                    <DialogContent className="max-w-4xl max-h-[85vh] flex flex-col">
                      <DialogHeader>
                        <DialogTitle className="flex items-center gap-2">
                          <FileWarning className="size-5 text-destructive" />
                          失效 STRM 列表
                          <Badge variant="destructive" className="ml-2">
                            {allStale.length}
                          </Badge>
                        </DialogTitle>
                        <DialogDescription>
                          这些 STRM 指向的媒体文件在 115 网盘树中不存在，可能已被删除或移动。
                        </DialogDescription>
                      </DialogHeader>
                      <div className="flex items-center justify-between py-2 text-sm">
                        <div className="flex items-center gap-2">
                          <Checkbox
                            id="sel-all-stale"
                            checked={
                              allStale.length > 0 &&
                              selectedStale.size === allStale.length
                            }
                            onCheckedChange={(c) => toggleAllStale(c === true)}
                          />
                          <label
                            htmlFor="sel-all-stale"
                            className="cursor-pointer text-muted-foreground"
                          >
                            全选
                          </label>
                          <span className="text-muted-foreground">
                            已选 {selectedStale.size} 个
                          </span>
                        </div>
                        <div className="flex gap-2">
                          <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
                            <AlertDialogTrigger asChild>
                              <Button
                                variant="destructive"
                                size="sm"
                                disabled={selectedStale.size === 0 || executing}
                              >
                                {executing ? (
                                  <Loader2 className="mr-2 size-4 animate-spin" />
                                ) : (
                                  <Trash2 className="mr-2 size-4" />
                                )}
                                删除选中 ({selectedStale.size})
                              </Button>
                            </AlertDialogTrigger>
                            <AlertDialogContent>
                              <AlertDialogHeader>
                                <AlertDialogTitle>
                                  确认删除 {selectedStale.size} 个失效 STRM？
                                </AlertDialogTitle>
                                <AlertDialogDescription>
                                  删除后将自动清理空父目录。此操作不可撤销。
                                </AlertDialogDescription>
                              </AlertDialogHeader>
                              <AlertDialogFooter>
                                <AlertDialogCancel>取消</AlertDialogCancel>
                                <AlertDialogAction
                                  onClick={handleExecuteDelete}
                                  className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
                                >
                                  确认删除
                                </AlertDialogAction>
                              </AlertDialogFooter>
                            </AlertDialogContent>
                          </AlertDialog>
                        </div>
                      </div>
                      <div className="flex-1 overflow-auto rounded border">
                        <Table>
                          <TableHeader className="sticky top-0 bg-background">
                            <TableRow>
                              <TableHead className="w-10"></TableHead>
                              <TableHead>路径映射</TableHead>
                              <TableHead>本地文件（相对路径）</TableHead>
                              <TableHead>STRM 内容（预览）</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {allStale.length === 0 && (
                              <TableRow>
                                <TableCell colSpan={4} className="text-center text-muted-foreground py-8">
                                  无失效 STRM
                                </TableCell>
                              </TableRow>
                            )}
                            {allStale.map((s) => {
                              const key = s.mappingId + "::" + s.relPath;
                              const checked = selectedStale.has(key);
                              return (
                                <TableRow key={key} className={checked ? "bg-muted/40" : ""}>
                                  <TableCell>
                                    <Checkbox
                                      checked={checked}
                                      onCheckedChange={(c) => toggleStale(key, c === true)}
                                    />
                                  </TableCell>
                                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                                    {s.localPath}
                                  </TableCell>
                                  <TableCell className="font-mono text-xs">{s.relPath}</TableCell>
                                  <TableCell className="font-mono text-xs max-w-sm truncate text-muted-foreground">
                                    {s.strmContent || "-"}
                                  </TableCell>
                                </TableRow>
                              );
                            })}
                          </TableBody>
                        </Table>
                      </div>
                      <DialogFooter>
                        <Button variant="outline" onClick={() => setStaleDialogOpen(false)}>
                          关闭
                        </Button>
                      </DialogFooter>
                    </DialogContent>
                  </Dialog>
                )}
              </StatCard>
              <StatCard
                label="漏生成 STRM"
                value={scanResult.totalMissing}
                icon={<SearchX className="size-4" />}
                tone={scanResult.totalMissing > 0 ? "warning" : "success"}
              >
                {scanResult.totalMissing > 0 && (
                  <Dialog
                    open={missingDialogOpen}
                    onOpenChange={setMissingDialogOpen}
                  >
                    <DialogTrigger asChild>
                      <Button variant="link" size="sm" className="h-auto p-0">
                        查看详情
                      </Button>
                    </DialogTrigger>
                    <DialogContent className="max-w-4xl max-h-[85vh] flex flex-col">
                      <DialogHeader>
                        <DialogTitle className="flex items-center gap-2">
                          <SearchX className="size-5 text-amber-500" />
                          漏生成 STRM 列表
                          <Badge className="ml-2">{allMissing.length}</Badge>
                        </DialogTitle>
                        <DialogDescription>
                          这些文件在 115 网盘中存在，但本地未生成对应的 STRM。可直接点击补生成。
                        </DialogDescription>
                      </DialogHeader>
                      <div className="flex-1 overflow-auto rounded border">
                        <Table>
                          <TableHeader className="sticky top-0 bg-background">
                            <TableRow>
                              <TableHead>路径映射</TableHead>
                              <TableHead>网盘文件路径</TableHead>
                              <TableHead>扩展名</TableHead>
                            </TableRow>
                          </TableHeader>
                          <TableBody>
                            {allMissing.map((m, idx) => {
                              const mp = scanResult.mappings.find(
                                (x) => x.mappingId === m.mappingId
                              );
                              return (
                                <TableRow key={idx}>
                                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                                    {mp?.localPath ?? "-"}
                                  </TableCell>
                                  <TableCell className="font-mono text-xs">{m.relPath}</TableCell>
                                  <TableCell>
                                    <Badge variant="secondary">{m.mediaExtension}</Badge>
                                  </TableCell>
                                </TableRow>
                              );
                            })}
                          </TableBody>
                        </Table>
                      </div>
                      <DialogFooter>
                        <AlertDialog open={regenConfirmOpen} onOpenChange={setRegenConfirmOpen}>
                          <AlertDialogTrigger asChild>
                            <Button disabled={executing}>
                              <Zap className="mr-2 size-4" />
                              {executing ? "生成中..." : "批量补生成"}
                            </Button>
                          </AlertDialogTrigger>
                          <AlertDialogContent>
                            <AlertDialogHeader>
                              <AlertDialogTitle>
                                确认补生成 {allMissing.length} 个 STRM？
                              </AlertDialogTitle>
                              <AlertDialogDescription>
                                将为所有漏生成的文件创建 STRM。此操作使用云端原始路径，不包含 pickcode。
                              </AlertDialogDescription>
                            </AlertDialogHeader>
                            <AlertDialogFooter>
                              <AlertDialogCancel>取消</AlertDialogCancel>
                              <AlertDialogAction onClick={handleRegenerate}>
                                确认生成
                              </AlertDialogAction>
                            </AlertDialogFooter>
                          </AlertDialogContent>
                        </AlertDialog>
                        <Button variant="outline" onClick={() => setMissingDialogOpen(false)}>
                          关闭
                        </Button>
                      </DialogFooter>
                    </DialogContent>
                  </Dialog>
                )}
              </StatCard>
            </div>

            {/* 操作工具栏 */}
            {(scanResult.totalStale > 0 || scanResult.totalMissing > 0) && (
              <div className="rounded-md border p-3 space-y-2">
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="text-sm font-medium">一键操作：</span>
                  <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
                    <AlertDialogTrigger asChild>
                      <Button
                        variant="destructive"
                        size="sm"
                        disabled={scanResult.totalStale === 0 || executing}
                      >
                        <Trash2 className="mr-2 size-4" />
                        {executing ? "删除中..." : `清理全部失效 (${scanResult.totalStale})`}
                      </Button>
                    </AlertDialogTrigger>
                    <AlertDialogContent>
                      <AlertDialogHeader>
                        <AlertDialogTitle>
                          一键清理全部 {scanResult.totalStale} 个失效 STRM？
                        </AlertDialogTitle>
                        <AlertDialogDescription>
                          将删除扫描发现的全部失效 STRM 文件，并自动清理空父目录。此操作不可撤销。
                        </AlertDialogDescription>
                      </AlertDialogHeader>
                      <AlertDialogFooter>
                        <AlertDialogCancel>取消</AlertDialogCancel>
                        <AlertDialogAction onClick={handleDeleteAll}>
                          确认清理
                        </AlertDialogAction>
                      </AlertDialogFooter>
                    </AlertDialogContent>
                  </AlertDialog>

                  <AlertDialog open={regenAllConfirmOpen} onOpenChange={setRegenAllConfirmOpen}>
                    <AlertDialogTrigger asChild>
                      <Button
                        size="sm"
                        disabled={scanResult.totalMissing === 0 || executing}
                      >
                        <Zap className="mr-2 size-4" />
                        {executing ? "生成中..." : `补生成全部漏项 (${scanResult.totalMissing})`}
                      </Button>
                    </AlertDialogTrigger>
                    <AlertDialogContent>
                      <AlertDialogHeader>
                        <AlertDialogTitle>
                          确认补生成 {scanResult.totalMissing} 个缺失 STRM？
                        </AlertDialogTitle>
                        <AlertDialogDescription>
                          将为所有漏生成的文件创建 STRM 文件。
                        </AlertDialogDescription>
                      </AlertDialogHeader>
                      <AlertDialogFooter>
                        <AlertDialogCancel>取消</AlertDialogCancel>
                        <AlertDialogAction onClick={handleRegenerate}>
                          确认生成
                        </AlertDialogAction>
                      </AlertDialogFooter>
                    </AlertDialogContent>
                  </AlertDialog>

                  {(scanResult.totalStale > 0 || scanResult.totalMissing > 0) && (
                    <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
                      <AlertDialogTrigger asChild>
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={executing}
                        >
                          <RefreshCw className="mr-2 size-4" />
                          {executing ? "执行中..." : "清理失效 + 补生成漏项"}
                        </Button>
                      </AlertDialogTrigger>
                      <AlertDialogContent>
                        <AlertDialogHeader>
                          <AlertDialogTitle>
                            确认执行清理+补生成组合操作？
                          </AlertDialogTitle>
                          <AlertDialogDescription>
                            将删除 {scanResult.totalStale} 个失效 STRM 并补生成 {scanResult.totalMissing} 个漏项。
                          </AlertDialogDescription>
                        </AlertDialogHeader>
                        <AlertDialogFooter>
                          <AlertDialogCancel>取消</AlertDialogCancel>
                          <AlertDialogAction onClick={handleDeleteAndRegenerate}>
                            确认执行
                          </AlertDialogAction>
                        </AlertDialogFooter>
                      </AlertDialogContent>
                    </AlertDialog>
                  )}
                </div>
                <p className="text-xs text-muted-foreground">
                  提示：先清理失效 STRM 避免冲突，再补生成缺失项。建议执行完后重新扫描验证。
                </p>
              </div>
            )}

            {/* 操作日志 */}
            {logs.length > 0 && (
              <div className="rounded-md border p-3 space-y-2">
                <div className="text-sm font-medium flex items-center gap-2">
                  <span>操作日志</span>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-6 px-2 text-xs"
                    onClick={() => setLogs([])}
                  >
                    清空
                  </Button>
                </div>
                <div className="space-y-1 max-h-32 overflow-auto">
                  {logs.map((log, idx) => (
                    <div key={idx} className="flex items-center gap-2 text-xs">
                      <span className="text-muted-foreground font-mono w-16 shrink-0">{log.time}</span>
                      <span className={`w-14 shrink-0 ${log.success ? "text-green-600" : "text-destructive"}`}>
                        {log.action}
                      </span>
                      <span className="truncate">{log.detail}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* 路径映射扫描明细 */}
            <div className="space-y-2">
              <div className="text-sm font-medium">路径映射扫描明细</div>
              <div className="space-y-2">
                {scanResult.mappings.map((m) => (
                  <div
                    key={m.mappingId}
                    className="p-3 rounded-md border text-sm space-y-1"
                  >
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge variant="secondary">{m.account}</Badge>
                      <span className="font-mono text-xs">
                        {m.cloudPath} → {m.localPath}
                      </span>
                      {m.error ? (
                        <Badge variant="destructive">失败</Badge>
                      ) : (
                        <Badge>完成</Badge>
                      )}
                    </div>
                    <div className="flex flex-wrap gap-4 text-xs text-muted-foreground">
                      <span>网盘文件：{m.remoteFileCount}</span>
                      <span>本地 STRM：{m.localStrmCount}</span>
                      <span className="text-destructive">失效：{m.staleStrms.length}</span>
                      <span className="text-amber-600">漏生成：{m.missingStrms.length}</span>
                    </div>
                    {m.error && (
                      <div className="text-xs text-destructive">错误：{m.error}</div>
                    )}
                  </div>
                ))}
              </div>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
}

function StatCard({
  label,
  value,
  icon,
  tone = "default",
  hint,
  children,
}: {
  label: string;
  value: number;
  icon: React.ReactNode;
  tone?: "default" | "success" | "destructive" | "warning";
  hint?: string;
  children?: React.ReactNode;
}) {
  const toneClass =
    tone === "destructive"
      ? "text-destructive"
      : tone === "success"
      ? "text-green-600"
      : tone === "warning"
      ? "text-amber-600"
      : "text-foreground";
  return (
    <div className="rounded-md border p-3 space-y-1">
      <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <span className={toneClass}>{icon}</span>
        {label}
      </div>
      <div className={`text-2xl font-semibold ${toneClass}`}>{value}</div>
      {hint && <div className="text-[11px] text-muted-foreground">{hint}</div>}
      {children || null}
    </div>
  );
}
