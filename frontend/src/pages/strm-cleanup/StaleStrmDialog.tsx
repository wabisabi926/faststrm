// 失效 STRM 列表对话框：展示并支持批量删除失效条目。
// 从 StrmCleanupCard.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T3。

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
import { Button } from "@/components/ui/button";
import { FileWarning, Loader2, Trash2 } from "lucide-react";
import type { StaleStrm } from "./types";
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
}: StaleStrmDialogProps) {
  if (allStale.length === 0) return null;

  return (
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
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            关闭
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
