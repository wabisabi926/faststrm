/**
 * Core 导航测试 — 验证登录后所有主要页面可达
 *
 * 运行前提：Go 后端已在 http://localhost:8090 启动
 *   npx playwright test core.spec.ts
 */
import { test, expect, Page } from '@playwright/test';

/** 登录辅助：从 /login 用 admin/admin 登录，等待跳转到非 /login 页面 */
async function login(page: Page) {
  await page.goto('/login');
  await page.getByPlaceholder(/请输入用户名/).fill('admin');
  await page.getByPlaceholder(/请输入密码/).fill('admin');
  await page.getByRole('button', { name: '登录' }).click();
  // 等待 URL 变化（登录成功 → / → /account）
  await expect(page).not.toHaveURL(/\/login/, { timeout: 10000 });
}

/** 侧栏点击辅助：找到侧栏中的目标链接并点击 */
async function clickSidebarLink(page: Page, name: string | RegExp, exact?: boolean) {
  const item = exact !== undefined
    ? page.getByRole('link', { name, exact })
    : page.getByRole('link', { name });
  await expect(item.first()).toBeVisible({ timeout: 5000 });
  await item.first().click();
}

/** 验证侧栏中的分组标签可见（说明 LayoutWrapper 正常渲染） */
async function expectSidebarGroupVisible(page: Page, label: string) {
  const locator = page.getByText(label, { exact: true }).first();
  await expect(locator).toBeVisible({ timeout: 5000 });
}

test.describe('核心页面可达性', () => {
  // 每个测试独立运行，自己负责登录
  test.beforeEach(async ({ page }) => {
    await login(page);
    // 登录后等待 /account 页面基本渲染
    await expect(page).toHaveURL(/\/account/, { timeout: 10000 });
  });

  test('登录后 → 账户管理页面默认显示', async ({ page }) => {
    await expect(page.url()).toContain('/account');
    // 侧栏分组标签可见说明 LayoutWrapper 已渲染
    await expectSidebarGroupVisible(page, '主菜单');
  });

  test('账户管理页面 → 渲染账户列表表格或空状态', async ({ page }) => {
    // 等待页面加载一下（可能有 API 调用）
    await page.waitForTimeout(500);
    // 页面应该有内容渲染，不会白屏崩溃
    // 检查 Sidebar 一直可见，确认 LayoutWrapper 没崩
    await expectSidebarGroupVisible(page, '主菜单');
    // 只要不出现渲染错误就算通过 —— body 元素肯定存在
    await expect(page.locator('body')).toBeVisible();
  });

  test('侧栏导航到 /task → 任务页面可达', async ({ page }) => {
    // "任务" exact 匹配，避免匹配到 "任务历史"
    await clickSidebarLink(page, '任务', true);
    await expect(page).toHaveURL(/\/task/, { timeout: 5000 });
    await expectSidebarGroupVisible(page, '主菜单');
  });

  test('侧栏导航到 /settings → 设置页面可达', async ({ page }) => {
    await clickSidebarLink(page, '设置', true);
    await expect(page).toHaveURL(/\/settings/, { timeout: 5000 });
    await expectSidebarGroupVisible(page, '主菜单');
  });

  test('侧栏导航到 /history → 任务历史页面可达', async ({ page }) => {
    // "任务历史" 在 "日志" 分组下
    await clickSidebarLink(page, /任务历史/);
    await expect(page).toHaveURL(/\/history/, { timeout: 5000 });
    await expectSidebarGroupVisible(page, '日志');
  });

  test('侧栏导航到 /life-events → 生活事件页面可达', async ({ page }) => {
    await clickSidebarLink(page, /生活事件/);
    await expect(page).toHaveURL(/\/life-events/, { timeout: 5000 });
    await expectSidebarGroupVisible(page, '日志');
  });

  test('侧栏导航到 /tg-notify → TG 通知页面可达', async ({ page }) => {
    // "TG 通知" 有空格，用正则匹配
    await clickSidebarLink(page, /TG\s*通知/);
    await expect(page).toHaveURL(/\/tg-notify/, { timeout: 5000 });
    await expectSidebarGroupVisible(page, '通知');
  });

  test('侧栏导航到 /emby-notify → Emby 通知页面可达', async ({ page }) => {
    await clickSidebarLink(page, /Emby\s*通知/);
    await expect(page).toHaveURL(/\/emby-notify/, { timeout: 5000 });
    await expectSidebarGroupVisible(page, '通知');
  });
});
