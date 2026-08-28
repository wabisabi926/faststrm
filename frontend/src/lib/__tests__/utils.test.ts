// Vitest 框架自检：验证 cn 工具函数与 jest-dom 匹配器正常工作。
// 此文件用于 T1 框架接入验证，后续可保留或删除。

import { describe, it, expect } from "vitest";
import { cn } from "@/lib/utils";

describe("cn (className merge util)", () => {
  it("合并多个 class 字符串", () => {
    expect(cn("a", "b")).toBe("a b");
  });

  it("过滤假值", () => {
    expect(cn("a", false, null, undefined, "b")).toBe("a b");
  });

  it("tailwind-merge 解析冲突的 tailwind 类", () => {
    // 后写的 p-2 应覆盖 p-4
    expect(cn("p-4", "p-2")).toBe("p-2");
  });

  it("支持数组与对象输入（clsx 特性）", () => {
    expect(cn(["a", { b: true, c: false }])).toBe("a b");
  });
});
