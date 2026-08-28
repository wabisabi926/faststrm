// 本地目录树节点：递归渲染单个节点。
// 从 LocalDirectoryTreeDialog.tsx 抽出 renderTreeNode 函数组件化，逻辑零变更。
// 详见 v1.1.1 改进任务清单 T5。

import { ChevronRight, ChevronDown, Folder, Loader2, HardDrive } from "lucide-react";
import type { TreeNode } from "./useLocalDirectoryTree";

export interface LocalTreeNodeProps {
  node: TreeNode;
  level: number;
  expandedNodes: Set<string>;
  loadingNodes: Set<string>;
  selectedPath: string;
  onToggle: (node: TreeNode) => void;
  onSelect: (node: TreeNode) => void;
}

// 判断是否为 Windows 盘符节点（id 形如 "C:\"）
function isDriveNode(node: TreeNode) {
  return /^[A-Za-z]:\\$/.test(node.id);
}

export function LocalTreeNode(props: LocalTreeNodeProps) {
  const {
    node,
    level,
    expandedNodes,
    loadingNodes,
    selectedPath,
    onToggle,
    onSelect,
  } = props;

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
              onToggle(node);
            }
          } else {
            onSelect(node);
            if (node.isDir) {
              onToggle(node);
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
          {node.children!.map((child) => (
            <LocalTreeNode
              key={child.id}
              node={child}
              level={level + 1}
              expandedNodes={expandedNodes}
              loadingNodes={loadingNodes}
              selectedPath={selectedPath}
              onToggle={onToggle}
              onSelect={onSelect}
            />
          ))}
        </div>
      )}
    </div>
  );
}
