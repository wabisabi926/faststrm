// 失效 STRM 列表对话框：展示并支持批量删除失效条目。
// P3 强化：StaleStrm.content（扫描预读 512B）+ truncated 查看完整（/preview）+ 预览 Dialog
import * as React from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
  DialogClose,
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
import { Button } from "@/components/ui/button";
import { FileWarning, Loader2, Maximize2, Trash2 } from "lucide-react";
import type { StaleStrm, StrmPreviewResponse } from "./types";
import { staleKey } from "./types";

export interface StaleStrmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  allStale: StaleStrm[];
  selectedStale: Set<string>;
  executing: boolean;
  confirmOpen: boolean;
  setConfirmOpen: (open: boolean) => void;
  toggleAllStale: (checked: boolean) => void;
  toggleStale: (key: string, checked: boolean) => void;
  onExecuteDelete: () => void;
  // P3：预览指定 .strm 文件（来自 useStrmCleanup.previewStrm）。undefined 时回退到本地 content
  previewStrm?: (p: { localPath: string; relPath: string; maxBytes?: number }) => Promise<StrmPreviewResponse>;
}

export function StaleStrmDialog({
  open,
  onOpenChange,
  allStale,
  selectedStale,
  executing,
  confirmOpen,
  setConfirmOpen,
  toggleAllStale,
  toggleStale,
  onExecuteDelete,
  previewStrm,
}: StaleStrmDialogProps) {
  // P3：STRM 内容预览 Dialog（内嵌，不依赖 Drawer 组件，最大兼容性）
  const [previewOpen, setPreviewOpen] = React.useState(false);
  const [previewState, setPreviewState] = React.useState<
    | { kind: "idle" }
    | { kind: "loading" }
    | { kind: "data"; s: StaleStrm; source: "scan" | "network"; full: boolean }
  >({ kind: "idle" });

  const openPreview = async (s: StaleStrm, full: boolean) => {
    if (!full && s.content) {
      setPreviewState({ kind: "data", s, source: "scan", full: false });
      setPreviewOpen(true);
      return;
    }
    if (!previewStrm) {
      setPreviewState({ kind: "data", s, source: "scan", full });
      setPreviewOpen(true);
      return;
    }
    setPreviewState({ kind: "loading" });
    try {
      const r = await previewStrm({
        localPath: s.localPath,
        relPath: s.relPath,
        maxBytes: full ? -1 : 8192,
      });
      const merged: StaleStrm = {
        ...s,
        content: r.content ?? s.content,
        strmContent: r.content ?? s.strmContent,
        truncated: Boolean(r.truncated),
        size: r.size ?? s.size,
      };
      setPreviewState({ kind: "data", s: merged, source: "network", full });
      setPreviewOpen(true);
    } catch (e) {
      setPreviewState({
        kind: "data",
        s: { ...s, content: String(e), strmContent: String(e) },
        source: "network",
        full,
      });
      setPreviewOpen(true);
    }
  };

  if (allStale.length === 0) return null;

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
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
              <Badge variant="outline" className="text-[10px] text-sky-700 border-sky-300 ml-2">
                P3：预览 STRM 内容
              </Badge>
            </DialogTitle>
            <DialogDescription>
              这些 STRM 指向的媒体文件在 115 网盘树中不存在，可能已被删除或移动。点击任意内容单元格可预览 STRM 文件。
            </DialogDescription>
          </DialogHeader>
          <div className="flex items-center justify-between py-2 text-sm">
            <div className="flex items-center gap-2">
              <Checkbox
                id="sel-all-stale"
                checked={allStale.length > 0 && selectedStale.size === allStale.length}
                onCheckedChange={(c) => toggleAllStale(c === true)}
              />
              <label htmlFor="sel-all-stale" className="cursor-pointer text-muted-foreground">
                全选
              </label>
              <span className="text-muted-foreground">已选 {selectedStale.size} 个</span>
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
                    <AlertDialogTitle>确认删除 {selectedStale.size} 个失效 STRM？</AlertDialogTitle>
                    <AlertDialogDescription>
                      删除后将自动清理空父目录。此操作不可撤销。
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>取消</AlertDialogCancel>
                    <AlertDialogAction
                      onClick={onExecuteDelete}
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
                  <TableHead className="w-10" />
                  <TableHead>路径映射</TableHead>
                  <TableHead>本地文件（相对路径）</TableHead>
                  <TableHead>STRM 内容（预览 · 点击单元格查看完整）</TableHead>
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
                  const key = staleKey(s.mappingId, s.relPath);
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
                      <TableCell className="font-mono text-xs">
                        <div className="flex items-center gap-2 min-w-0">
                          <button
                            type="button"
                            className="min-w-0 flex-1 text-left truncate text-muted-foreground hover:text-foreground focus:outline-none"
                            onClick={() => void openPreview(s, false)}
                            title={s.strmContent || "(空内容)"}
                          >
                            {s.strmContent || "-"}
                          </button>
                          <Maximize2 className="size-3 text-muted-foreground/60 shrink-0" aria-hidden />
                          {s.truncated && (
                            <Badge variant="outline" className="shrink-0 text-[10px] px-1.5 py-0">
                              <button
                                type="button"
                                onClick={(e) => {
                                  e.stopPropagation();
                                  void openPreview(s, true);
                                }}
                                className="text-[10px] hover:underline"
                              >
                                查看完整
                              </button>
                            </Badge>
                          )}
                        </div>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              关闭
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* P3：STRM 内容预览内嵌 Dialog */}
      <Dialog open={previewOpen} onOpenChange={setPreviewOpen}>
        <DialogContent className="max-w-3xl max-h-[85vh] flex flex-col">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2 flex-wrap">
              <FileWarning className="size-4 text-destructive" />
              STRM 内容预览
              {previewState.kind === "data" && previewState.s.relPath && (
                <Badge variant="outline" className="font-mono font-normal max-w-[50%] truncate">
                  {previewState.s.relPath}
                </Badge>
              )}
              {previewState.kind === "data" && previewState.source === "network" && (
                <Badge variant="secondary" className="text-[10px]">
                  从本地文件读取
                </Badge>
              )}
              {previewState.kind === "data" && previewState.source === "scan" && (
                <Badge variant="outline" className="text-[10px]">
                  扫描时预取（前 512 字节）
                </Badge>
              )}
              {previewState.kind === "data" && previewState.s.size !== undefined && (
                <span className="text-xs text-muted-foreground">
                  ({previewState.s.size} 字节
                  {previewState.s.truncated ? ", 已截断" : ""})
                </span>
              )}
            </DialogTitle>
            <DialogDescription className="font-mono text-[11px] break-all">
              {previewState.kind === "data" && `${previewState.s.localPath}/${previewState.s.relPath}`}
            </DialogDescription>
          </DialogHeader>
          <div className="flex-1 overflow-auto rounded-md border bg-muted/30">
            {previewState.kind === "loading" ? (
              <div className="flex items-center gap-2 text-muted-foreground text-sm p-8 justify-center">
                <Loader2 className="size-4 animate-spin" />
                读取中…
              </div>
            ) : previewState.kind === "data" && previewState.s.content ? (
              <pre className="whitespace-pre-wrap break-words p-4 text-xs font-mono leading-relaxed text-foreground/90">
                {previewState.s.content}
              </pre>
            ) : (
              <div className="text-muted-foreground text-sm py-12 text-center">
                (该文件为空、或当前环境未提供 previewStrm 接口)
              </div>
            )}
          </div>
          <DialogFooter className="flex items-center justify-between">
            {previewState.kind === "data" && !previewState.full && previewState.s.truncated && previewStrm && (
              <Button
                size="sm"
                onClick={() => void openPreview(previewState.s, true)}
                className="mr-auto"
              >
                查看完整（读取本地文件）
              </Button>
            )}
            <DialogClose asChild>
              <Button variant="outline">关闭</Button>
            </DialogClose>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
