// 漏生成 STRM 列表对话框：展示并支持批量补生成。
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
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { SearchX, Zap } from "lucide-react";
import type { MappingResult, MissingStrm } from "./types";

export interface MissingStrmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  allMissing: MissingStrm[];
  mappings: MappingResult[];
  executing: boolean;
  regenConfirmOpen: boolean;
  setRegenConfirmOpen: (open: boolean) => void;
  onRegenerate: () => void;
}

export function MissingStrmDialog({
  open,
  onOpenChange,
  allMissing,
  mappings,
  executing,
  regenConfirmOpen,
  setRegenConfirmOpen,
  onRegenerate,
}: MissingStrmDialogProps) {
  if (allMissing.length === 0) return null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
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
                const mp = mappings.find((x) => x.mappingId === m.mappingId);
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
                <AlertDialogAction onClick={onRegenerate}>
                  确认生成
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            关闭
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
