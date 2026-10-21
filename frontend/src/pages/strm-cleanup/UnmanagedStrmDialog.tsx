// 未托管 STRM 明细对话框：只读展示，不提供任何删除入口。
//
// 这些文件云端已不存在，但没有出现在托管登记表里（手写、第三方工具生成、
// 或纳管前的历史遗留）。为了避免误删，它们不进入删除候选，也不参与「清理全部失效」。
// 之前只显示一个数字，用户不知道具体是哪些文件，无法判断该不该手动处理。

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
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ShieldAlert } from "lucide-react";
import type { StaleStrm } from "./types";

export interface UnmanagedStrmDialogProps {
  allUnmanaged: StaleStrm[];
}

export function UnmanagedStrmDialog({ allUnmanaged }: UnmanagedStrmDialogProps) {
  if (allUnmanaged.length === 0) return null;

  return (
    <Dialog>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm" className="mt-2">
          <ShieldAlert className="mr-1.5 size-3.5" />
          查看明细（{allUnmanaged.length}）
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>未托管的失效 STRM</DialogTitle>
          <DialogDescription>
            这些文件在云端已不存在，但没有托管登记记录，因此不会被自动清理，也不会进入删除候选。
            确认不再需要时，请直接在文件系统里删除；若刚升级，历史纳管可能仍在后台进行，稍后重新扫描即可。
          </DialogDescription>
        </DialogHeader>
        <div className="max-h-[60vh] overflow-auto rounded border">
          <Table>
            <TableHeader className="sticky top-0 bg-background">
              <TableRow>
                <TableHead>路径映射</TableHead>
                <TableHead>本地文件（相对路径）</TableHead>
                <TableHead className="w-24">原始原因</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {allUnmanaged.map((s, i) => (
                <TableRow key={`${s.mappingId}-${s.relPath}-${i}`}>
                  <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                    {s.localPath}
                  </TableCell>
                  <TableCell className="font-mono text-xs break-all">{s.relPath}</TableCell>
                  <TableCell>
                    {s.reason === "account_missing" ? (
                      <Badge variant="destructive" className="text-[10px] px-1.5 py-0">
                        账号失效
                      </Badge>
                    ) : (
                      <Badge variant="outline" className="text-[10px] px-1.5 py-0">
                        云端已删
                      </Badge>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        <DialogFooter>
          <p className="text-xs text-muted-foreground">
            共 {allUnmanaged.length} 个；本列表只读，不会执行任何删除。
          </p>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
