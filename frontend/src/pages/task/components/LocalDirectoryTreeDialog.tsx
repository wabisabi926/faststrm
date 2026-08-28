// 本地目录树选择对话框：组合入口，编排 useLocalDirectoryTree hook + LocalTreeNode 渲染。
// 拆分自原 LocalDirectoryTreeDialog.tsx（418 行 → 此文件仅负责 Dialog 框架 + 装配）。
// 详见 v1.1.1 改进任务清单 T5。

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Loader2, AlertCircle, CheckCircle2 } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useLocalDirectoryTree } from "./useLocalDirectoryTree";
import { LocalTreeNode } from "./LocalTreeNode";

export interface LocalDirectoryTreeDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSelect: (path: string) => void;
}

export function LocalDirectoryTreeDialog({
  open,
  onOpenChange,
  onSelect,
}: LocalDirectoryTreeDialogProps) {
  const {
    tree,
    rootMessage,
    loading,
    expandedNodes,
    loadingNodes,
    selectedPath,
    manualPath,
    manualCheck,
    toggleNode,
    handleSelect,
    checkAndJumpManual,
    handleConfirm,
    setManualPath,
    resetManualCheck,
  } = useLocalDirectoryTree(open, onOpenChange, onSelect);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-[95vw] sm:max-w-[600px] max-h-[80vh] flex flex-col">
        <DialogHeader>
          <DialogTitle>选择本地目录</DialogTitle>
          <DialogDescription>
            从下方根节点开始浏览，或直接粘贴已知路径后点「跳转」
          </DialogDescription>
        </DialogHeader>

        {/* 手动路径输入 + 校验 + 跳转（fNOS/Docker/NAS 环境下浏览根不全时的兜底） */}
        <div className="space-y-1.5 shrink-0">
          <div className="flex gap-2">
            <Input
              value={manualPath}
              onChange={(e) => {
                setManualPath(e.target.value);
                resetManualCheck();
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  void checkAndJumpManual();
                }
              }}
              placeholder="例：/vol1/电影 或 D:\Media\电影 或 /volume1/video"
              className="font-mono text-sm"
            />
            <Button
              variant="outline"
              onClick={() => void checkAndJumpManual()}
              disabled={manualCheck.status === "checking" || !manualPath.trim()}
              className="shrink-0"
            >
              {manualCheck.status === "checking" ? (
                <>
                  <Loader2 className="w-4 h-4 mr-1.5 animate-spin" />
                  校验中
                </>
              ) : (
                "跳转"
              )}
            </Button>
          </div>
          {manualCheck.status === "ok" && (
            <div className="flex items-center gap-1.5 text-xs text-emerald-600 dark:text-emerald-400">
              <CheckCircle2 className="w-3.5 h-3.5" />
              {manualCheck.name}
            </div>
          )}
          {manualCheck.status === "err" && (
            <div className="flex items-start gap-1.5 text-xs text-destructive">
              <AlertCircle className="w-3.5 h-3.5 mt-0.5 shrink-0" />
              <span className="break-all">{manualCheck.message}</span>
            </div>
          )}
          {manualCheck.status === "idle" && manualPath.trim().length > 0 && (
            <div className="text-xs text-muted-foreground">
              输入完成后按 Enter 或点「跳转」定位到该目录
            </div>
          )}
        </div>

        <div className="flex-1 min-h-[300px] max-h-[500px] border rounded-md p-2 overflow-auto">
          {loading ? (
            <div className="flex items-center justify-center py-8">
              <Loader2 className="w-6 h-6 animate-spin text-gray-400" />
              <span className="ml-2 text-sm text-gray-500">加载中...</span>
            </div>
          ) : tree.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-8 text-sm text-gray-500 gap-2">
              <span>{rootMessage || "暂无目录"}</span>
              {rootMessage && (
                <span className="text-xs text-muted-foreground">
                  可在上方输入框直接粘贴路径后点「跳转」访问
                </span>
              )}
            </div>
          ) : (
            <div>
              {tree.map((node) => (
                <LocalTreeNode
                  key={node.id}
                  node={node}
                  level={0}
                  expandedNodes={expandedNodes}
                  loadingNodes={loadingNodes}
                  selectedPath={selectedPath}
                  onToggle={(n) => void toggleNode(n)}
                  onSelect={handleSelect}
                />
              ))}
            </div>
          )}
        </div>

        {selectedPath && (
          <div className="text-sm text-gray-600 dark:text-gray-300 px-2 py-1 bg-gray-50 dark:bg-gray-800 rounded">
            已选择: <span className="font-medium break-all">{selectedPath}</span>
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button onClick={handleConfirm} disabled={!selectedPath}>
            确认
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
