import * as React from "react";
import { Button } from "@/components/ui/button";
import axiosInstance from "@/lib/axios";
import { ChevronRight, ChevronDown, Folder, Loader2 } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
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
} from "@/components/ui/alert-dialog";

interface TreeNode {
  name: string;
  id: number;
  isDir: boolean;
  cid?: number;
  fid?: number;
  path?: string;
  hasChildren?: boolean;
  children?: TreeNode[];
}

interface DirectoryTreeDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  account: string;
  onSelect: (path: string) => void;
  onSelectWithTargetPath?: (originPath: string, targetPath: string) => void;
}

export function DirectoryTreeDialog({
  open,
  onOpenChange,
  account,
  onSelect,
  onSelectWithTargetPath,
}: DirectoryTreeDialogProps) {
  const [tree, setTree] = React.useState<TreeNode[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [expandedNodes, setExpandedNodes] = React.useState<Set<number>>(
    new Set()
  );
  const [loadingNodes, setLoadingNodes] = React.useState<Set<number>>(
    new Set()
  );
  const [selectedPath, setSelectedPath] = React.useState<string>("");
  const [showAutoFillDialog, setShowAutoFillDialog] = React.useState(false);

  const loadTree = React.useCallback(
    async (cid: string = "0") => {
      if (!account) return;

      setLoading(true);
      try {
        const response = await axiosInstance.get("/api/directory/remote/list", {
          params: { account, cid },
        });

        if (response.data.code === 200) {
          setTree(response.data.data || []);
        } else {
          console.error("Failed to load directory tree:", response.data.message);
          setTree([]);
        }
      } catch (error) {
        console.error("Error loading directory tree:", error);
        setTree([]);
      } finally {
        setLoading(false);
      }
    },
    [account]
  );

  React.useEffect(() => {
    if (open && account) {
      loadTree("0");
      setExpandedNodes(new Set());
      setSelectedPath("");
    }
  }, [open, account, loadTree]);

  const toggleNode = async (node: TreeNode, parentPath: string = "") => {
    const currentPath = parentPath
      ? `${parentPath}/${node.name}`
      : node.name;

    if (expandedNodes.has(node.id)) {
      setExpandedNodes((prev) => {
        const next = new Set(prev);
        next.delete(node.id);
        return next;
      });
    } else {
      if (node.children === undefined) {
        setLoadingNodes((prev) => new Set(prev).add(node.id));
        try {
          const nodeCid = node.cid !== undefined ? String(node.cid) : "0";
          const response = await axiosInstance.get("/api/directory/remote/list", {
            params: { account, cid: nodeCid },
          });

          if (response.data.code === 200) {
            const children: TreeNode[] = (response.data.data || []).map((child: any) => ({
              ...child,
              path: currentPath ? `${currentPath}/${child.name}` : child.name,
            }));
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
    }
  };

  const updateTreeNode = (
    nodes: TreeNode[],
    targetId: number,
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

  const handleSelect = (path: string) => {
    setSelectedPath(path);
  };

  const handleConfirm = () => {
    if (!selectedPath) return;

    if (onSelectWithTargetPath) {
      setShowAutoFillDialog(true);
      return;
    }

    onSelect(selectedPath);
    onOpenChange(false);
  };

  const handleAutoFillConfirm = () => {
    if (!selectedPath || !onSelectWithTargetPath) return;

    onSelectWithTargetPath(selectedPath, selectedPath);
    setShowAutoFillDialog(false);
    onOpenChange(false);
  };

  const handleAutoFillCancel = () => {
    if (!selectedPath) return;

    onSelect(selectedPath);
    setShowAutoFillDialog(false);
    onOpenChange(false);
  };

  const renderTreeNode = (
    node: TreeNode,
    parentPath: string = "",
    level: number = 0
  ): React.ReactNode => {
    const currentPath = parentPath
      ? `${parentPath}/${node.name}`
      : node.name;
    const isExpanded = expandedNodes.has(node.id);
    const isLoading = loadingNodes.has(node.id);
    const isSelected = selectedPath === currentPath;
    const hasLoadedChildren = node.children !== undefined;
    const hasChildrenToShow = hasLoadedChildren && node.children && node.children.length > 0;

    return (
      <div key={node.id} className="select-none">
        <div
          className={`flex items-center gap-1 px-2 py-1.5 rounded hover:bg-gray-100 cursor-pointer ${
            isSelected ? "bg-blue-50 text-blue-600" : ""
          }`}
          style={{ paddingLeft: `${level * 20 + 8}px` }}
          onClick={(e) => {
            const target = e.target as HTMLElement;
            if (target.closest('.chevron-icon') || target.closest('.folder-icon')) {
              if (node.isDir) {
                toggleNode(node, parentPath);
              }
            } else {
              handleSelect(currentPath);
              if (node.isDir) {
                toggleNode(node, parentPath);
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
              <Folder className="w-4 h-4 text-blue-500 folder-icon" />
            </>
          ) : (
            <div className="w-4 h-4" />
          )}
          <span className="text-sm flex-1 truncate">{node.name}</span>
        </div>
        {node.isDir && isExpanded && hasChildrenToShow && (
          <div>
            {node.children!.map((child) =>
              renderTreeNode(child, currentPath, level + 1)
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
          <DialogTitle>选择目录</DialogTitle>
          <DialogDescription>
            选择远程路径，当前账户: {account}
          </DialogDescription>
        </DialogHeader>

        <div className="flex-1 min-h-[300px] max-h-[500px] border rounded-md p-2 overflow-auto">
          {loading ? (
            <div className="flex items-center justify-center py-8">
              <Loader2 className="w-6 h-6 animate-spin text-gray-400" />
              <span className="ml-2 text-sm text-gray-500">加载中...</span>
            </div>
          ) : tree.length === 0 ? (
            <div className="flex items-center justify-center py-8 text-sm text-gray-500">
              暂无目录
            </div>
          ) : (
            <div>{tree.map((node) => renderTreeNode(node))}</div>
          )}
        </div>

        {selectedPath && (
          <div className="text-sm text-gray-600 px-2 py-1 bg-gray-50 rounded">
            已选择: <span className="font-medium">{selectedPath}</span>
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

      <AlertDialog open={showAutoFillDialog} onOpenChange={setShowAutoFillDialog}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>自动填写本地路径</AlertDialogTitle>
            <AlertDialogDescription>
              将为您自动填写本地路径，是否需要？
              <br />
              <span className="font-medium text-gray-700 mt-2 block">
                本地路径: {selectedPath}
              </span>
              <br />
              <span className="text-sm text-gray-500">填写后可在表单中修改</span>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={handleAutoFillCancel}>
              我自己填写
            </AlertDialogCancel>
            <AlertDialogAction onClick={handleAutoFillConfirm}>
              好的
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Dialog>
  );
}
