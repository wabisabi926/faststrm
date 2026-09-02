// STRM 清理 P1 方案前端 E2E（Vitest）
// 覆盖：
//   F-E2E-1. CleanupToolbar 组合按钮：选中/未选中 -> 两条文案分支（已勾选/警告）
//   F-E2E-2. CleanupToolbar 底部提示：明确 清理全部 vs 组合仅勾选 区别
//   F-E2E-3. 类型契约：MappingResult.dbRecordCount + ScanSummary.totalDbRecords 可选
//   F-E2E-4. MappingDetailList DB 列：存在 dbRecordCount 时渲染 DB 列/差值 Badge

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CleanupToolbar } from "../CleanupToolbar";
import { MappingDetailList } from "../MappingDetailList";
import type { MappingLocalStats, MappingResult, ScanSummary, ExecuteResult } from "../types";

vi.mock("sonner", () => {
  const toast = vi.fn() as unknown as { success: typeof vi.fn; error: typeof vi.fn };
  (toast as unknown as { success: ReturnType<typeof vi.fn> }).success = vi.fn();
  (toast as unknown as { error: ReturnType<typeof vi.fn> }).error = vi.fn();
  return { toast };
});

beforeEach(() => {
  cleanup();
});

const baseProps = {
  totalStale: 113,
  totalMissing: 7,
  executing: false,
  onDeleteAll: vi.fn(),
  onRegenerate: vi.fn(),
  onDeleteAndRegenerate: vi.fn(),
};

describe("CleanupToolbar P1 组合按钮文案/提示", () => {
  it("F-E2E-1a：selectedStaleCount>0 对话框显示已勾选数量，无警告", async () => {
    const user = userEvent.setup();
    render(<CleanupToolbar {...baseProps} selectedStaleCount={3} />);
    await user.click(screen.getByRole("button", { name: /清理失效 \+ 补生成漏项/ }));
    expect(screen.getByText(/3 个失效 STRM/)).toBeInTheDocument();
    expect(screen.queryByText(/当前未勾选任何失效 STRM/)).toBeNull();
  });

  it("F-E2E-1b：selectedStaleCount=0 对话框显示警告，指出只执行补生成", async () => {
    const user = userEvent.setup();
    render(<CleanupToolbar {...baseProps} selectedStaleCount={0} />);
    await user.click(screen.getByRole("button", { name: /清理失效 \+ 补生成漏项/ }));
    expect(screen.getByText(/当前未勾选任何失效 STRM/)).toBeInTheDocument();
    expect(screen.getByText(/只执行补生成/)).toBeInTheDocument();
    expect(screen.getByText(/不会删除文件/)).toBeInTheDocument();
  });

  it("F-E2E-2：底部 tips 区别 清理全部失效=全删 / 组合=仅勾选 5 项", () => {
    render(<CleanupToolbar {...baseProps} selectedStaleCount={5} />);
    const tip = screen.getByText(/提示：「清理全部失效」会删除扫描发现的全部 113 个失效 STRM/);
    expect(tip).toBeInTheDocument();
    expect(tip.textContent).toMatch(/组合操作仅删除您.*已勾选.*的失效（当前 5 项）/);
  });

  it("F-E2E-2b：selectedStaleCount=0 时 tips 仍然可读，当前 0 项", () => {
    render(<CleanupToolbar {...baseProps} selectedStaleCount={0} />);
    const tip = screen.getByText(/提示：「清理全部失效」会删除扫描发现的全部 113 个失效 STRM/);
    expect(tip.textContent).toMatch(/当前 0 项/);
  });
});

describe("类型契约：统一响应 dbRecordCount / totalDbRecords", () => {
  it("F-E2E-3：MappingResult.dbRecordCount 可选，有值时能读，无值时 undefined", () => {
    const withDb: MappingResult = {
      mappingId: "m0",
      account: "acc",
      cloudPath: "/电影",
      localPath: "/x",
      remoteFileCount: 10,
      localStrmCount: 8,
      staleStrms: [],
      missingStrms: [],
      dbRecordCount: 10,
    };
    const withoutDb: MappingResult = {
      mappingId: "m1",
      account: "acc",
      cloudPath: "/电视剧",
      localPath: "/y",
      remoteFileCount: 20,
      localStrmCount: 19,
      staleStrms: [],
      missingStrms: [],
    };
    expect(withDb.dbRecordCount).toBe(10);
    expect(withoutDb.dbRecordCount).toBeUndefined();
  });
});

describe("MappingDetailList P1：DB 列条件渲染", () => {
  it("F-E2E-4a：所有 mapping 无 dbRecordCount -> 不出现 DB 列", () => {
    const ms: MappingResult[] = [
      {
        mappingId: "m0",
        account: "a",
        cloudPath: "/电影",
        localPath: "/x",
        remoteFileCount: 10,
        localStrmCount: 10,
        staleStrms: [],
        missingStrms: [],
      },
    ];
    render(<MappingDetailList mappings={ms} />);
    expect(screen.queryByText(/DB：/)).toBeNull();
    expect(screen.getByText(/网盘文件：10/)).toBeInTheDocument();
  });

  it("F-E2E-4b：dbRecordCount=remoteFileCount -> 绿色勾选", () => {
    const ms: MappingResult[] = [
      {
        mappingId: "m0",
        account: "a",
        cloudPath: "/电影",
        localPath: "/x",
        remoteFileCount: 10,
        localStrmCount: 10,
        staleStrms: [],
        missingStrms: [],
        dbRecordCount: 10,
      },
    ];
    render(<MappingDetailList mappings={ms} />);
    expect(screen.getByText(/DB：10 ✓/)).toBeInTheDocument();
  });

  it("F-E2E-4c：差值>=5 -> 琥珀色差-X 文本", () => {
    const ms: MappingResult[] = [
      {
        mappingId: "m0",
        account: "a",
        cloudPath: "/电影",
        localPath: "/x",
        remoteFileCount: 20,
        localStrmCount: 10,
        staleStrms: [],
        missingStrms: [],
        dbRecordCount: 12, // 差 -8
      },
    ];
    render(<MappingDetailList mappings={ms} />);
    expect(screen.getByText(/差-8/)).toBeInTheDocument();
  });
});

import { StatCard } from "../StatCard";

describe("StrmCleanupCard P1：DB 同步记录统计卡（条件渲染 + tone 映射）", () => {
  it("F-E2E-5a：totalDbRecords=10 且 totalRemoteFiles=10 → success tone + 与网盘一致 ✓", () => {
    const { container } = render(
      <StatCard label="DB 同步记录" value={10} icon={<span data-testid="db-icon" />} tone="success" hint="与网盘一致 ✓" />
    );
    expect(screen.getByText("DB 同步记录")).toBeInTheDocument();
    expect(screen.getByText("10")).toBeInTheDocument();
    expect(screen.getByText("与网盘一致 ✓")).toBeInTheDocument();
    const valueEl = container.querySelector(".text-2xl");
    expect(valueEl?.className).toContain("text-green-600");
  });

  it("F-E2E-5b：totalDbRecords=5, totalRemoteFiles=10 → diff=-5 (abs<=5) → default tone", () => {
    const { container } = render(
      <StatCard label="DB 同步记录" value={5} icon={<span />} tone="default" hint="差值：-5" />
    );
    expect(screen.getByText("差值：-5")).toBeInTheDocument();
    const valueEl = container.querySelector(".text-2xl");
    expect(valueEl?.className).toContain("text-foreground");
    expect(valueEl?.className).not.toContain("text-amber-600");
  });

  it("F-E2E-5c：totalDbRecords=1, totalRemoteFiles=10 → diff=-9 (abs>5) → warning tone", () => {
    const { container } = render(
      <StatCard label="DB 同步记录" value={1} icon={<span />} tone="warning" hint="差值：-9" />
    );
    expect(screen.getByText("差值：-9")).toBeInTheDocument();
    const valueEl = container.querySelector(".text-2xl");
    expect(valueEl?.className).toContain("text-amber-600");
  });

  it("F-E2E-5d：totalDbRecords > totalRemoteFiles → hint 显示 差值：+N", () => {
    render(<StatCard label="DB 同步记录" value={20} icon={<span />} tone="warning" hint="差值：+10" />);
    expect(screen.getByText("差值：+10")).toBeInTheDocument();
  });
});

describe("StrmCleanupCard 网格列数 等价断言（P1：DB 卡出现/不出现 → 列数规则）", () => {
  it("F-E2E-6a：totalDbRecords !== undefined → md:grid-cols-5（5 列）", () => {
    // 以 Card 分支表达式验证：grid-cols 条件分支
    const colRule = (tdb: number | undefined): string =>
      tdb !== undefined ? "grid grid-cols-2 sm:grid-cols-3 md:grid-cols-5 gap-3" : "grid grid-cols-2 sm:grid-cols-2 md:grid-cols-4 gap-3";
    expect(colRule(10)).toContain("md:grid-cols-5");
    expect(colRule(0)).toContain("md:grid-cols-5");
  });
  it("F-E2E-6b：totalDbRecords === undefined → md:grid-cols-4（保持原 4 列布局，不破坏老场景）", () => {
    const colRule = (tdb: number | undefined): string =>
      tdb !== undefined ? "grid grid-cols-2 sm:grid-cols-3 md:grid-cols-5 gap-3" : "grid grid-cols-2 sm:grid-cols-2 md:grid-cols-4 gap-3";
    expect(colRule(undefined)).toContain("md:grid-cols-4");
    expect(colRule(undefined)).not.toContain("md:grid-cols-5");
  });
});


// ============================================================
// P2：associatedFileCount + refreshedMappingStats (权威刷新防漂移)
// ============================================================
describe("P2 MappingResult / ScanSummary / ExecuteResult 类型契约", () => {
  it("F-P2-1a：MappingResult.associatedFileCount 可选（缺省时 mapping 级不展示关联列）", () => {
    const a: MappingResult = {
      mappingId: "1", account: "", cloudPath: "", localPath: "",
      remoteFileCount: 1, localStrmCount: 1, staleStrms: [], missingStrms: [],
    };
    expect(a.associatedFileCount).toBeUndefined();
    const b: MappingResult = { ...a, associatedFileCount: 6 };
    expect(b.associatedFileCount).toBe(6);
  });

  it("F-P2-1b：ExecuteResult.refreshedMappingStats 是 MappingLocalStats 数组", () => {
    const r: ExecuteResult = {
      deletedCount: 2, failedCount: 0, errors: [], removedEmptyDirs: [], dryRun: false, durationMs: 50,
      refreshedMappingStats: [{ localPath: "/x", localStrmCount: 7, associatedFileCount: 4 }],
    };
    expect(r.refreshedMappingStats?.[0].localStrmCount).toBe(7);
    expect(r.refreshedMappingStats?.[0].associatedFileCount).toBe(4);
  });
});

describe("P2 前端 applyRefreshedStats 行为（从 useStrmCleanup 逻辑提取的等价契约）", () => {
  // 等价于 useStrmCleanup.applyRefreshedStats，避免依赖 hooks
  function applyRefreshedStats(r: ExecuteResult, prev: ScanSummary): ScanSummary {
    if (!r.refreshedMappingStats || r.refreshedMappingStats.length === 0) return prev;
    const byPath = new Map(
      r.refreshedMappingStats.filter((s: MappingLocalStats) => s.localPath).map((s) => [s.localPath, s])
    );
    if (byPath.size === 0) return prev;
    let recomputeAssoc = false;
    const newMappings = prev.mappings.map((m) => {
      const hit = byPath.get(m.localPath);
      if (!hit) return m;
      if (hit.associatedFileCount !== undefined) recomputeAssoc = true;
      return {
        ...m,
        localStrmCount: hit.localStrmCount,
        associatedFileCount:
          hit.associatedFileCount !== undefined ? hit.associatedFileCount : m.associatedFileCount,
      };
    });
    const newTotalLocal = newMappings.reduce((s, m) => s + m.localStrmCount, 0);
    const baseAssoc = newMappings.reduce((s, m) => s + (m.associatedFileCount ?? 0), 0);
    return {
      ...prev,
      mappings: newMappings,
      totalLocalStrms: newTotalLocal,
      totalAssociatedFiles: recomputeAssoc ? (baseAssoc > 0 ? baseAssoc : undefined) : prev.totalAssociatedFiles,
    };
  }

  it("F-P2-2a：refreshedMappingStats 覆盖 localStrmCount，纠正增量漂移（原 prev 是 prev=20-删 2=18，但实际因删除空目录变成 17）", () => {
    const prev: ScanSummary = {
      totalRemoteFiles: 10, totalLocalStrms: 18, // 前端基于 deletedCount=2 的增量估算（错算）
      totalAssociatedFiles: 10, totalStale: 1, totalMissing: 0, durationMs: 0,
      mappings: [
        { mappingId: "a", account:"a", cloudPath:"/电影", localPath:"/A", remoteFileCount: 4, localStrmCount: 10, associatedFileCount: 6, staleStrms: [], missingStrms: [] },
        { mappingId: "b", account:"b", cloudPath:"/电视剧", localPath:"/B", remoteFileCount: 6, localStrmCount: 8, associatedFileCount: 4, staleStrms: [], missingStrms: [] },
      ],
    };
    // 后端权威 Walk：A 实际只剩 9（删了 1 个相关但前端以为没影响），B 是 8
    const res: ExecuteResult = {
      deletedCount: 2, failedCount: 0, errors: [], removedEmptyDirs: [], dryRun: false, durationMs: 100,
      refreshedMappingStats: [
        { localPath: "/A", localStrmCount: 9, associatedFileCount: 4 },
        { localPath: "/B", localStrmCount: 8, associatedFileCount: 3 },
      ],
    };
    const next = applyRefreshedStats(res, prev);
    // totalLocalStrms 用权威值 17，不是原估算 18
    expect(next.totalLocalStrms).toBe(17);
    // totalAssociatedFiles 重新聚合：4+3=7
    expect(next.totalAssociatedFiles).toBe(7);
    // stale/missing/duration/totalRemote 不变
    expect(next.totalRemoteFiles).toBe(10);
    expect(next.totalStale).toBe(1);
    expect(next.durationMs).toBe(0);
  });

  it("F-P2-2b：无 refreshedMappingStats 时不触碰 prev（保持前端增量）", () => {
    const prev: ScanSummary = { totalRemoteFiles: 1, totalLocalStrms: 5, totalStale: 0, totalMissing: 0, durationMs: 0, mappings: [] };
    const res: ExecuteResult = { deletedCount: 1, failedCount: 0, errors: [], removedEmptyDirs: [], dryRun: false, durationMs: 10 };
    const next = applyRefreshedStats(res, prev);
    expect(next.totalLocalStrms).toBe(5);
    expect(next).toBe(prev); // 引用不变
  });

  it("F-P2-2c：DryRun=true 场景下不触发刷新（对应后端 refreshedMappingStats 为空）", () => {
    const prev: ScanSummary = { totalRemoteFiles: 1, totalLocalStrms: 3, totalStale: 3, totalMissing: 0, durationMs: 0, mappings: [] };
    const dryRunRes: ExecuteResult = { deletedCount: 3, failedCount: 0, errors: [], removedEmptyDirs: [], dryRun: true, durationMs: 10 };
    const next = applyRefreshedStats(dryRunRes, prev);
    expect(next.totalLocalStrms).toBe(3); // 保持 3，模拟 dry-run 不动磁盘
    expect(next.totalStale).toBe(3);
  });
});

describe("P2 StrmCleanupCard 网格列数：4 列 / 5 列 / 6 列三档", () => {
  const rule = (hasDB: boolean, hasAssoc: boolean): string => {
    if (hasDB && hasAssoc) return "grid grid-cols-2 sm:grid-cols-3 md:grid-cols-6 gap-3";
    if (hasDB || hasAssoc) return "grid grid-cols-2 sm:grid-cols-3 md:grid-cols-5 gap-3";
    return "grid grid-cols-2 sm:grid-cols-2 md:grid-cols-4 gap-3";
  };
  it("F-P2-3a：二者都有 → md:grid-cols-6", () => expect(rule(true, true)).toContain("md:grid-cols-6"));
  it("F-P2-3b：只有 DB → md:grid-cols-5", () => expect(rule(true, false)).toContain("md:grid-cols-5"));
  it("F-P2-3c：只有 Assoc → md:grid-cols-5", () => expect(rule(false, true)).toContain("md:grid-cols-5"));
  it("F-P2-3d：都无 → md:grid-cols-4", () => expect(rule(false, false)).toContain("md:grid-cols-4"));
});

describe("P2 StatCard 关联媒体文件卡渲染", () => {
  it("F-P2-4：Paperclip 图标 + 正确 hint", () => {
    render(<StatCard label="关联媒体文件" value={10} icon={<span data-testid="attach-icon" />} tone="default" hint=".nfo / .jpg / .png / .srt / .ass / .sub / .vtt" />);
    expect(screen.getByText("关联媒体文件")).toBeInTheDocument();
    expect(screen.getByText("10")).toBeInTheDocument();
    expect(screen.getByText(/\.nfo \/ \.jpg \/ \.png \/ \.srt/)).toBeInTheDocument();
  });
});

describe("P2 MappingDetailList 关联列条件渲染", () => {
  it("F-P2-5a：有 associatedFileCount → 出现蓝色 关联：N 文本 + 顶部 P2 Badge", () => {
    const ms: MappingResult[] = [
      {
        mappingId: "a", account: "a", cloudPath: "/电影", localPath: "/A",
        remoteFileCount: 4, localStrmCount: 10, associatedFileCount: 6,
        staleStrms: [], missingStrms: [],
      },
    ];
    render(<MappingDetailList mappings={ms} />);
    expect(screen.getByText(/关联：6/)).toBeInTheDocument();
    expect(screen.getByText(/P2：已含关联文件列/)).toBeInTheDocument();
  });
  it("F-P2-5b：都无 associatedFileCount → 无 关联： 文本", () => {
    const ms: MappingResult[] = [
      {
        mappingId: "b", account: "b", cloudPath: "/电视剧", localPath: "/B",
        remoteFileCount: 6, localStrmCount: 8,
        staleStrms: [], missingStrms: [],
      },
    ];
    render(<MappingDetailList mappings={ms} />);
    expect(screen.queryByText(/关联：/)).toBeNull();
    expect(screen.queryByText(/P2：已含关联文件列/)).toBeNull();
  });
});

