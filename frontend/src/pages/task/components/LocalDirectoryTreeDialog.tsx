import * as React from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import axiosInstance from "@/lib/axios";
import { ChevronRight, ChevronDown, Folder, Loader2, HardDrive, AlertCircle, CheckCircle2 } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

interface TreeNode {
  name: string;
  id: string; // 使用完整路径作为 ID
  isDir: boolean;
  hasChildren?: boolean;
  children?: TreeNode[];
}

interface LocalDirectoryTreeDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSelect: (path: string) => void;
}

export function LocalDirectoryTreeDialog({
  open,
  onOpenChange,
  onSelect,
}: LocalDirectoryTreeDialogProps) {
  const [tree, setTree] = React.useState<TreeNode[]>([]);
  const [rootMessage, setRootMessage] = React.useState<string>("");
  const [loading, setLoading] = React.useState(false);
  const [expandedNodes, setExpandedNodes] = React.useState<Set<string>>(
    new Set()
  );
  const [loadingNodes, setLoadingNodes] = React.useState<Set<string>>(
    new Set()
  );
  const [selectedPath, setSelectedPath] = React.useState<string>("");
  const [manualPath, setManualPath] = React.useState<string>("");
  const [manualCheck, setManualCheck] = React.useState<
    | { status: "idle" }
    | { status: "checking" }
    | { status: "ok"; name: string }
    | { status: "err"; message: string }
  >({ status: "idle" });

  // 加载目录（basePath 为空时，后端自动返回根或盘符列表）
  const loadTree = React.useCallback(
    async (basePath: string = "") => {
      setLoading(true);
      try {
        const response = await axiosInstance.post("/api/directory/local/list", {
          basePath,
        });

        if (response.data.code === 200) {
          setTree(response.data.data || []);
          setRootMessage(response.data.message || "");
        } else {
          console.error("Failed to load directory tree:", response.data.message);
          setTree([]);
          setRootMessage(response.data.message || "");
        }
      } catch (error) {
        console.error("Error loading directory tree:", error);
        setTree([]);
        setRootMessage("");
      } finally {
        setLoading(false);
      }
    },
    []
  );

  // 当对话框打开时加载根目录
  React.useEffect(() => {
    if (open) {
      loadTree("");
      setExpandedNodes(new Set());
      setSelectedPath("");
      setRootMessage("");
    }
  }, [open, loadTree]);

  // 展开/折叠节点
  const toggleNode = async (node: TreeNode) => {
    if (expandedNodes.has(node.id)) {
      setExpandedNodes((prev) => {
        const next = new Set(prev);
        next.delete(node.id);
        return next;
      });
      return;
    }

    if (node.children === undefined) {
      setLoadingNodes((prev) => new Set(prev).add(node.id));
      try {
        const response = await axiosInstance.post("/api/directory/local/list", {
          basePath: node.id,
        });

        if (response.data.code === 200) {
          const children = (response.data.data || []).map((n: any) => ({ ...n, id: String(n.id) }));
          const updatedTree = updateTreeNode(tree, node.id, {
            ...node,
            children: children,
          });
          setTree(updatedTree);
          if (children.length > 0) {
            setExpandedNodes((prev) => new Set(prev).add(node.id));
          }
        }
      } catch (error) {
        console.error("Error loading children:", error);
        const updatedTree = updateTreeNode(tree, node.id, {
          ...node,
          children: [],
        });
        setTree(updatedTree);
      } finally {
        setLoadingNodes((prev) => {
          const next = new Set(prev);
          next.delete(node.id);
          return next;
        });
      }
    } else {
      setExpandedNodes((prev) => new Set(prev).add(node.id));
    }
  };

  // 更新树节点
  const updateTreeNode = (
    nodes: TreeNode[],
    targetId: string,
    updatedNode: TreeNode
  ): TreeNode[] => {
    return nodes.map((node) => {
      if (node.id === targetId) {
        return updatedNode;
      }
      if (node.children) {
        return {
          ...node,
          children: updateTreeNode(node.children, targetId, updatedNode),
        };
      }
      return node;
    });
  };

  // 选择路径
  const handleSelect = (node: TreeNode) => {
    setSelectedPath(node.id);
    // 同步到手动输入框，方便用户在此基础上微调
    setManualPath(node.id);
    setManualCheck({ status: "idle" });
  };

  // 手动输入路径：校验 + 跳转到该目录（浏览树列出它的父目录内容并高亮）
  const checkAndJumpManual = async () => {
    const p = manualPath.trim();
    if (!p) {
      setManualCheck({ status: "err", message: "请输入路径" });
      return;
    }
    setManualCheck({ status: "checking" });
    try {
      // 1) 走 listChildren 接口校验路径存在、可访问、是目录
      const listRes = await axiosInstance.post("/api/directory/local/list", {
        basePath: p,
      });
      if (listRes.data.code === 200) {
        // 存在：把该目录的子项加载到视图里
        setTree(listRes.data.data || []);
        setSelectedPath(p);
        setManualCheck({
          status: "ok",
          name: `目录有效（含 ${(listRes.data.data || []).length} 个条目）`,
        });
        return;
      }
      // 2) 如果 basePath=p 失败，可能传的是文件路径，走 listChildren(basePath="", targetPath=p) 看是否存在
      const statRes = await axiosInstance.post("/api/directory/local/listChildren", {
        basePath: "",
        targetPath: p,
      });
      if (statRes.data.code === 200) {
        setSelectedPath(p);
        setManualCheck({ status: "ok", name: "路径已选择" });
        return;
      }
      setManualCheck({
        status: "err",
        message:
          (statRes.data && statRes.data.message) ||
          (listRes.data && listRes.data.message) ||
          "无法访问该路径（请检查沙箱授权 / 权限 / 路径是否存在）",
      });
    } catch (e: any) {
      const msg =
        e?.response?.data?.message || e?.message || "校验路径时网络错误";
      setManualCheck({ status: "err", message: msg });
    }
  };

  // 对话框关闭时清理手动输入状态
  React.useEffect(() => {
    if (open) {
      // 打开时如果已经有选中路径，回填到输入框
      if (selectedPath) setManualPath(selectedPath);
    } else {
      setManualCheck({ status: "idle" });
    }
  }, [open]); // eslint-disable-line react-hooks/exhaustive-deps

  // 确认选择
  const handleConfirm = () => {
    if (selectedPath) {
      // 返回给调用方的路径统一做一次规范化（去掉盘符末尾多余反斜杠，保持 API 友好）
      let finalPath = selectedPath;
      // Windows 盘符: "C:\" -> "C:" (去掉尾部\，便于后续 join 不会出 C:\\foo)
      if (/^[A-Za-z]:\\$/.test(finalPath)) {
        finalPath = finalPath.slice(0, -1);
      }
      onSelect(finalPath);
      onOpenChange(false);
    }
  };

  // 判断是否为 Windows 盘符节点（id 形如 "C:\"）
  const isDriveNode = (node: TreeNode) => /^[A-Za-z]:\\$/.test(node.id);

  // 渲染树节点
  const renderTreeNode = (
    node: TreeNode,
    level: number = 0
  ): React.ReactNode => {
    const isExpanded = expandedNodes.has(node.id);
    const isLoading = loadingNodes.has(node.id);
    const isSelected = selectedPath === node.id;
    const hasLoadedChildren = node.children !== undefined;
    const hasChildrenToShow =
      hasLoadedChildren && node.children && node.children.length > 0;
    const isDrive = isDriveNode(node);

    return (
      <div key={node.id} className="select-none">
        <div
          className={`flex items-center gap-1 px-2 py-1.5 rounded hover:bg-gray-100 dark:hover:bg-gray-800 cursor-pointer ${
            isSelected ? "bg-blue-50 text-blue-600 dark:bg-blue-900/30 dark:text-blue-300" : ""
          }`}
          style={{ paddingLeft: `${level * 20 + 8}px` }}
          onClick={(e) => {
            const target = e.target as HTMLElement;
            if (
              target.closest(".chevron-icon") ||
              target.closest(".folder-icon")
            ) {
              if (node.isDir) {
                toggleNode(node);
              }
            } else {
              handleSelect(node);
              if (node.isDir) {
                toggleNode(node);
              }
            }
          }}
        >
          {node.isDir ? (
            <>
              {isLoading ? (
                <Loader2 className="w-4 h-4 animate-spin text-gray-400 chevron-icon" />
              ) : hasLoadedChildren && hasChildrenToShow ? (
                isExpanded ? (
                  <ChevronDown className="w-4 h-4 text-gray-400 chevron-icon" />
                ) : (
                  <ChevronRight className="w-4 h-4 text-gray-400 chevron-icon" />
                )
              ) : hasLoadedChildren && !hasChildrenToShow ? (
                <div className="w-4 h-4" />
              ) : (
                <ChevronRight className="w-4 h-4 text-gray-400 chevron-icon" />
              )}
              {isDrive ? (
                <HardDrive className="w-4 h-4 text-amber-500 folder-icon" />
              ) : (
                <Folder className="w-4 h-4 text-blue-500 folder-icon" />
              )}
            </>
          ) : (
            <div className="w-4 h-4" />
          )}
          <span className="text-sm flex-1 truncate">{node.name}</span>
        </div>
        {node.isDir && isExpanded && hasChildrenToShow && (
          <div>
            {node.children!.map((child) =>
              renderTreeNode(child, level + 1)
            )}
          </div>
        )}
      </div>
    );
  };

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
                setManualCheck({ status: "idle" });
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  checkAndJumpManual();
                }
              }}
              placeholder="例：/vol1/电影 或 D:\Media\电影 或 /volume1/video"
              className="font-mono text-sm"
            />
            <Button
              variant="outline"
              onClick={checkAndJumpManual}
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
            <div>{tree.map((node) => renderTreeNode(node))}</div>
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
