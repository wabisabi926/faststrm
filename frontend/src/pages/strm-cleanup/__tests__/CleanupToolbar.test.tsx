// CleanupToolbar 回归测试
// 验证修复点：三个 AlertDialog 必须各自独立状态。
// 修复前的 bug：confirmOpen 被三个 AlertDialog 共享，点击「清理全部失效」
// 会让"清理全部"与"清理+补生成组合"两个 AlertDialogContent 同时 open，
// 弹窗叠加导致「确认清理」按钮的 onClick 失效。

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CleanupToolbar } from "../CleanupToolbar";

vi.mock("sonner", () => {
  const toast = vi.fn() as unknown as { success: typeof vi.fn; error: typeof vi.fn };
  (toast as unknown as { success: ReturnType<typeof vi.fn> }).success = vi.fn();
  (toast as unknown as { error: ReturnType<typeof vi.fn> }).error = vi.fn();
  return { toast };
});

beforeEach(() => {
  cleanup();
});

describe("CleanupToolbar AlertDialog 状态独立性（修复点回归）", () => {
  it("totalMissing=0 时「补生成全部漏项」按钮被禁用，「清理全部失效」可点", () => {
    render(
      <CleanupToolbar
        totalStale={113}
        totalMissing={0}
        executing={false}
        onDeleteAll={vi.fn()}
        onRegenerate={vi.fn()}
        onDeleteAndRegenerate={vi.fn()}
        selectedStaleCount={0}
      />
    );

    const deleteAllBtn = screen.getByRole("button", { name: /清理全部失效/ });
    expect(deleteAllBtn).not.toBeDisabled();

    const regenBtn = screen.getByRole("button", { name: /补生成全部漏项/ });
    expect(regenBtn).toBeDisabled();
  });

  it("点击「清理全部失效」只弹出 1 个确认框，不叠加组合操作弹窗", async () => {
    const user = userEvent.setup();
    const onDeleteAll = vi.fn();
    const onDeleteAndRegenerate = vi.fn();

    render(
      <CleanupToolbar
        totalStale={113}
        totalMissing={0}
        executing={false}
        onDeleteAll={onDeleteAll}
        onRegenerate={vi.fn()}
        onDeleteAndRegenerate={onDeleteAndRegenerate}
        selectedStaleCount={10}
      />
    );

    expect(screen.queryByText(/一键清理全部/)).toBeNull();
    expect(screen.queryByText(/确认执行清理\+补生成组合操作/)).toBeNull();

    await user.click(screen.getByRole("button", { name: /清理全部失效/ }));

    expect(screen.getByText(/一键清理全部 113 个失效 STRM/)).toBeInTheDocument();
    expect(screen.queryByText(/确认执行清理\+补生成组合操作/)).toBeNull();

    await user.click(screen.getByRole("button", { name: "确认清理" }));
    expect(onDeleteAll).toHaveBeenCalledTimes(1);
    expect(onDeleteAndRegenerate).not.toHaveBeenCalled();
  });

  it("点击「清理失效 + 补生成漏项」触发组合操作回调而非 delete_all", async () => {
    const user = userEvent.setup();
    const onDeleteAll = vi.fn();
    const onDeleteAndRegenerate = vi.fn();

    render(
      <CleanupToolbar
        totalStale={113}
        totalMissing={5}
        executing={false}
        onDeleteAll={onDeleteAll}
        onRegenerate={vi.fn()}
        onDeleteAndRegenerate={onDeleteAndRegenerate}
        selectedStaleCount={10}
      />
    );

    await user.click(screen.getByRole("button", { name: /清理失效 \+ 补生成漏项/ }));

    expect(screen.getByText(/确认执行清理\+补生成组合操作/)).toBeInTheDocument();
    expect(screen.queryByText(/一键清理全部 113 个失效 STRM/)).toBeNull();

    await user.click(screen.getByRole("button", { name: "确认执行" }));
    expect(onDeleteAndRegenerate).toHaveBeenCalledTimes(1);
    expect(onDeleteAll).not.toHaveBeenCalled();
  });

  it("点击「补生成全部漏项」触发 onRegenerate 回调", async () => {
    const user = userEvent.setup();
    const onRegenerate = vi.fn();

    render(
      <CleanupToolbar
        totalStale={0}
        totalMissing={7}
        executing={false}
        onDeleteAll={vi.fn()}
        onRegenerate={onRegenerate}
        onDeleteAndRegenerate={vi.fn()}
        selectedStaleCount={0}
      />
    );

    await user.click(screen.getByRole("button", { name: /补生成全部漏项/ }));

    expect(screen.getByText(/确认补生成 7 个缺失 STRM/)).toBeInTheDocument();
    expect(screen.queryByText(/一键清理全部/)).toBeNull();
    expect(screen.queryByText(/确认执行清理\+补生成组合操作/)).toBeNull();

    await user.click(screen.getByRole("button", { name: "确认生成" }));
    expect(onRegenerate).toHaveBeenCalledTimes(1);
  });

  it("executing=true 时所有操作按钮被禁用", () => {
    render(
      <CleanupToolbar
        totalStale={113}
        totalMissing={5}
        executing={true}
        onDeleteAll={vi.fn()}
        onRegenerate={vi.fn()}
        onDeleteAndRegenerate={vi.fn()}
        selectedStaleCount={0}
      />
    );

    expect(screen.getByRole("button", { name: /删除中/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: /生成中/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: /执行中/ })).toBeDisabled();
  });
});
