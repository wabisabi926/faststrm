// 验证 Testing Library + jest-dom 匹配器链路完整可用。
// 此文件用于 T1 框架接入验证，后续可保留或删除。

import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Button } from "@/components/ui/button";

describe("Button 组件渲染与交互", () => {
  it("渲染默认 children 文本", () => {
    render(<Button>点击我</Button>);
    // jest-dom 的 getByRole 与 toBeInTheDocument 匹配器
    expect(screen.getByRole("button", { name: "点击我" })).toBeInTheDocument();
  });

  it("点击触发 onClick 回调", async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    render(<Button onClick={onClick}>提交</Button>);
    await user.click(screen.getByRole("button", { name: "提交" }));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("disabled 状态下点击无效", async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    render(
      <Button onClick={onClick} disabled>
        禁用
      </Button>
    );
    const btn = screen.getByRole("button", { name: "禁用" });
    expect(btn).toBeDisabled();
    await user.click(btn);
    expect(onClick).not.toHaveBeenCalled();
  });
});
