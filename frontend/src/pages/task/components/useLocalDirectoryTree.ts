// 本地目录树状态管理 hook：树数据加载、节点展开/折叠、
// 路径选择、手动路径校验+跳转、确认选择。
// 从 LocalDirectoryTreeDialog.tsx 抽出，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T5。

import { useCallback, useEffect, useState } from "react";
import { axios, axiosInstance } from "@/lib/axios";
import type { DirectoryNodeApi } from "@/types/api";

export interface TreeNode {
  name: string;
  id: string; // 使用完整路径作为 ID
  isDir: boolean;
  hasChildren?: boolean;
  children?: TreeNode[];
}

export type ManualCheckState =
  | { status: "idle" }
  | { status: "checking" }
  | { status: "ok"; name: string }
  | { status: "err"; message: string };

export interface UseLocalDirectoryTreeResult {
  // 树状态
  tree: TreeNode[];
  rootMessage: string;
  loading: boolean;
  expandedNodes: Set<string>;
  loadingNodes: Set<string>;
  selectedPath: string;
  manualPath: string;
  manualCheck: ManualCheckState;
  // 操作
  loadTree: (basePath?: string) => Promise<void>;
  toggleNode: (node: TreeNode) => Promise<void>;
  handleSelect: (node: TreeNode) => void;
  checkAndJumpManual: () => Promise<void>;
  handleConfirm: () => void;
  setManualPath: (path: string) => void;
  resetManualCheck: () => void;
  // 当对话框打开/关闭时调用
  onOpenChange: (open: boolean) => void;
}

export function useLocalDirectoryTree(
  open: boolean,
  onOpenChange: (open: boolean) => void,
  onSelect: (path: string) => void,
): UseLocalDirectoryTreeResult {
  const [tree, setTree] = useState<TreeNode[]>([]);
  const [rootMessage, setRootMessage] = useState<string>("");
  const [loading, setLoading] = useState(false);
  const [expandedNodes, setExpandedNodes] = useState<Set<string>>(new Set());
  const [loadingNodes, setLoadingNodes] = useState<Set<string>>(new Set());
  const [selectedPath, setSelectedPath] = useState<string>("");
  const [manualPath, setManualPath] = useState<string>("");
  const [manualCheck, setManualCheck] = useState<ManualCheckState>({ status: "idle" });

  // 更新树节点（递归）
  const updateTreeNode = (
    nodes: TreeNode[],
    targetId: string,
    updatedNode: TreeNode,
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

  // 加载目录（basePath 为空时，后端自动返回根或盘符列表）
  const loadTree = useCallback(async (basePath: string = "") => {
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
  }, []);

  // 当对话框打开时加载根目录
  useEffect(() => {
    if (open) {
      void loadTree("");
      setExpandedNodes(new Set());
      setSelectedPath("");
      setRootMessage("");
    }
  }, [open, loadTree]);

  // 展开/折叠节点
  const toggleNode = useCallback(
    async (node: TreeNode) => {
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
            const children = (response.data.data || []).map((n: DirectoryNodeApi) => ({
              ...n,
              id: String(n.id),
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
    },
    [expandedNodes, tree],
  );

  // 选择路径
  const handleSelect = useCallback((node: TreeNode) => {
    setSelectedPath(node.id);
    // 同步到手动输入框，方便用户在此基础上微调
    setManualPath(node.id);
    setManualCheck({ status: "idle" });
  }, []);

  // 手动输入路径：校验 + 跳转到该目录（浏览树列出它的父目录内容并高亮）
  const checkAndJumpManual = useCallback(async () => {
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
      const statRes = await axiosInstance.post(
        "/api/directory/local/listChildren",
        { basePath: "", targetPath: p },
      );
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
    } catch (e: unknown) {
      let msg = "校验路径时网络错误";
      if (axios.isAxiosError(e)) {
        msg =
          (e.response?.data as { message?: string } | undefined)?.message ||
          e.message ||
          msg;
      } else if (e instanceof Error) {
        msg = e.message || msg;
      }
      setManualCheck({ status: "err", message: msg });
    }
  }, [manualPath]);

  // 对话框关闭时清理手动输入状态
  useEffect(() => {
    if (open) {
      // 打开时如果已经有选中路径，回填到输入框
      if (selectedPath) setManualPath(selectedPath);
    } else {
      setManualCheck({ status: "idle" });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  // 确认选择
  const handleConfirm = useCallback(() => {
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
  }, [selectedPath, onSelect, onOpenChange]);

  return {
    tree,
    rootMessage,
    loading,
    expandedNodes,
    loadingNodes,
    selectedPath,
    manualPath,
    manualCheck,
    loadTree,
    toggleNode,
    handleSelect,
    checkAndJumpManual,
    handleConfirm,
    setManualPath,
    resetManualCheck: () => setManualCheck({ status: "idle" }),
    onOpenChange,
  };
}
