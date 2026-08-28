// AddAccountDialog 业务测试
// 覆盖 v1.1.1 P1 核心场景：表单验证、115/openlist 提交路径、编辑模式、错误展示。

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AddAccountDialog, accountFormSchema } from "../AddAccountDialog";

// ---- 全局 mock ----
// vi.mock 工厂内部用 vi.fn()，不引用模块级变量，避免 hoisting 循环依赖
vi.mock("sonner", () => {
  const toast = vi.fn() as any;
  toast.error = vi.fn();
  toast.success = vi.fn();
  return { toast };
});

vi.mock("../QrCodeLogin", () => ({
  QrCodeLogin: () => (
    <button type="button" data-testid="qrcode-login">QR</button>
  ),
}));

// 用 vi.hoisted 把 calls 提升到顶部，让 vi.mock 工厂能引用
const hoisted = vi.hoisted(() => ({
  calls: [] as { url: string; data?: unknown }[],
}));

// axiosInstance 模拟：post/put 返回 mock 结果，track 调用
vi.mock("@/lib/axios", () => {
  const makeMethod = () => vi.fn(async (...args: unknown[]) => {
    hoisted.calls.push({ url: String(args[0]), data: args[1] });
    return { data: { ok: true }, status: 200 };
  });
  return {
    axios: {
      isAxiosError: (err: unknown): err is { response?: { data: unknown } } & Error => {
        return err instanceof Error && "response" in err;
      },
    },
    axiosInstance: {
      get: makeMethod(),
      post: makeMethod(),
      put: makeMethod(),
      delete: makeMethod(),
      interceptors: { request: { use: vi.fn() }, response: { use: vi.fn() } },
      defaults: {},
    },
  };
});

let toastMock: any;
let toastErrorMock: any;
let axiosPostMock: any;
let axiosPutMock: any;

beforeEach(async () => {
  const sonner = await import("sonner");
  toastMock = sonner.toast;
  toastErrorMock = sonner.toast.error;

  const axiosMod = await import("@/lib/axios");
  axiosPostMock = axiosMod.axiosInstance.post;
  axiosPutMock = axiosMod.axiosInstance.put;

  toastMock.mockClear();
  toastErrorMock.mockClear();
  axiosPostMock.mockClear();
  axiosPutMock.mockClear();
  hoisted.calls.length = 0;
});

function setPostFail(errorMsg: string) {
  axiosPostMock.mockImplementationOnce(async () => {
    const err = new Error("400") as any;
    err.response = { status: 400, data: { error: errorMsg } };
    throw err;
  });
}

describe("AddAccountDialog", () => {
  describe("accountFormSchema 验证", () => {
    it("115 + cookie 空 → 拒绝", () => {
      const r = accountFormSchema.safeParse({ accountType: "115", name: "t", cookie: "" });
      expect(r.success).toBe(false);
    });
    it("115 + cookie 有效 → 通过", () => {
      const r = accountFormSchema.safeParse({ accountType: "115", name: "t", cookie: "UID=x" });
      expect(r.success).toBe(true);
    });
    it("openlist 缺 password → 拒绝", () => {
      const r = accountFormSchema.safeParse({
        accountType: "openlist", name: "o", account: "a", url: "http://x",
      });
      expect(r.success).toBe(false);
    });
    it("openlist 全部字段齐全 → 通过", () => {
      const r = accountFormSchema.safeParse({
        accountType: "openlist", name: "o", account: "a", password: "p", url: "http://x",
      });
      expect(r.success).toBe(true);
    });
    it("accountType / name 为空 → 拒绝", () => {
      const r = accountFormSchema.safeParse({ accountType: "", name: "" });
      expect(r.success).toBe(false);
    });
  });

  describe("新增 115 账号（手动 cookie）", () => {
    it("完整填写 → POST /api/account + toast 成功", async () => {
      const user = userEvent.setup();
      render(<AddAccountDialog />);
      await user.click(screen.getByRole("button", { name: "Add Account" }));
      await waitFor(() => screen.getByRole("dialog"));

      await user.click(screen.getAllByRole("combobox")[0]);
      await user.click(await screen.findByRole("option", { name: "115" }));

      await user.type(screen.getByPlaceholderText(/输入账户名称/), "我的115");
      await user.click(screen.getByRole("button", { name: /手动粘贴/ }));
      await user.type(screen.getByPlaceholderText(/输入 115 网盘的 Cookie/), "UID=abc");

      await user.click(screen.getByRole("button", { name: /保存(?!中)/ }));

      await waitFor(() => expect(axiosPostMock).toHaveBeenCalled());
      const call = hoisted.calls[0];
      expect(call.url).toBe("/api/account");
      expect(call.data).toMatchObject({ accountType: "115", name: "我的115", cookie: "UID=abc" });
      expect(toastMock).toHaveBeenCalledWith("账号添加成功");
    });
  });

  describe("编辑账号 → PUT", () => {
    it("payload 带 originalName", async () => {
      const onSuccess = vi.fn();
      render(
        <AddAccountDialog
          account={{ accountType: "115", name: "old", cookie: "UID=old" }}
          trigger={<button>编辑</button>}
          onSuccess={onSuccess}
        />
      );
      const user = userEvent.setup();
      await user.click(screen.getByRole("button", { name: "编辑" }));
      await waitFor(() => screen.getByRole("dialog"));

      const nameInput = screen.getByDisplayValue("old");
      await user.clear(nameInput);
      await user.type(nameInput, "new");

      await user.click(screen.getByRole("button", { name: /保存(?!中)/ }));
      await waitFor(() => expect(axiosPutMock).toHaveBeenCalled());
      
      const call = hoisted.calls[0];
      expect(call.data).toMatchObject({ name: "new", originalName: "old" });
      expect(onSuccess).toHaveBeenCalled();
    });
  });

  describe("表单验证阻断提交", () => {
    it("115 + cookie 空 → 不发请求", async () => {
      const user = userEvent.setup();
      render(<AddAccountDialog />);
      await user.click(screen.getByRole("button", { name: "Add Account" }));
      await waitFor(() => screen.getByRole("dialog"));

      await user.click(screen.getAllByRole("combobox")[0]);
      await user.click(await screen.findByRole("option", { name: "115" }));

      await user.type(screen.getByPlaceholderText(/输入账户名称/), "x");
      await user.click(screen.getByRole("button", { name: /手动粘贴/ }));
      await user.click(screen.getByRole("button", { name: /保存(?!中)/ }));

      expect(axiosPostMock).not.toHaveBeenCalled();
    });
  });

  describe("后端错误展示", () => {
    it("POST 失败 → toast.error 显示 error 字段", async () => {
      setPostFail("账号名称已存在");
      const user = userEvent.setup();
      render(<AddAccountDialog />);
      await user.click(screen.getByRole("button", { name: "Add Account" }));
      await waitFor(() => screen.getByRole("dialog"));

      await user.click(screen.getAllByRole("combobox")[0]);
      await user.click(await screen.findByRole("option", { name: "115" }));
      await user.type(screen.getByPlaceholderText(/输入账户名称/), "dup");
      await user.click(screen.getByRole("button", { name: /手动粘贴/ }));
      await user.type(screen.getByPlaceholderText(/输入 115 网盘的 Cookie/), "UID=dup");
      await user.click(screen.getByRole("button", { name: /保存(?!中)/ }));

      await waitFor(() => expect(toastErrorMock).toHaveBeenCalledWith("账号名称已存在"));
    });
  });

  describe("openlist 提交", () => {
    it("全部字段齐全 → POST", async () => {
      const user = userEvent.setup();
      render(<AddAccountDialog />);
      await user.click(screen.getByRole("button", { name: "Add Account" }));
      await waitFor(() => screen.getByRole("dialog"));

      await user.click(screen.getAllByRole("combobox")[0]);
      await user.click(await screen.findByRole("option", { name: "openlist" }));

      await user.type(screen.getByPlaceholderText(/输入账户名称/), "my-ol");
      await user.type(screen.getByPlaceholderText(/输入用户名/), "admin");
      await user.type(screen.getByPlaceholderText(/输入密码/), "secret");
      await user.type(screen.getByPlaceholderText(/输入服务器地址/), "http://ol:5244");

      await user.click(screen.getByRole("button", { name: /保存(?!中)/ }));
      await waitFor(() => expect(axiosPostMock).toHaveBeenCalled());

      const call = hoisted.calls[0];
      expect(call.data).toMatchObject({
        accountType: "openlist", account: "admin", password: "secret", url: "http://ol:5244",
      });
    });
  });
});










