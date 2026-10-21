/**
 * Core 导航测试 — 验证登录后所有主要页面可达
 *
 * 运行前提：Go 后端已在 http://localhost:8090 启动
 *   npx playwright test core.spec.ts
 */
import { test, expect, Page } from '@playwright/test';

const USERNAME = process.env.E2E_USERNAME || 'admin';
const PASSWORD = process.env.E2E_PASSWORD || 'admin';

/** 登录辅助：从 /login 登录，等待进入概览 */
async function login(page: Page) {
  await page.goto('/login');
  await page.getByPlaceholder(/请输入用户名/).fill(USERNAME);
  await page.getByPlaceholder(/请输入密码/).fill(PASSWORD);
  await page.getByRole('button', { name: '登录' }).click();
  await expect(page).not.toHaveURL(/\/login/, { timeout: 10000 });
  await expect(page.getByRole('heading', { name: '概览' })).toBeVisible({ timeout: 10000 });
}

/** 侧栏点击辅助 */
async function clickSidebarLink(page: Page, name: string | RegExp, exact?: boolean) {
  const item = exact !== undefined
    ? page.getByRole('link', { name, exact })
    : page.getByRole('link', { name });
  await expect(item.first()).toBeVisible({ timeout: 5000 });
  await item.first().click();
}

async function expectSidebarGroupVisible(page: Page, label: string) {
  const locator = page.getByText(label, { exact: true }).first();
  await expect(locator).toBeVisible({ timeout: 5000 });
}

test.describe('核心页面可达性', () => {
  test.beforeEach(async ({ page }) => {
    await login(page);
  });

  test('登录后 → 默认进入概览', async ({ page }) => {
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByRole('heading', { name: '概览' })).toBeVisible();
    await expectSidebarGroupVisible(page, '媒体库');
  });

  test('侧栏导航到 /account → 账号管理', async ({ page }) => {
    await clickSidebarLink(page, '账号管理', true);
    await expect(page).toHaveURL(/\/account/, { timeout: 5000 });
    await expectSidebarGroupVisible(page, '媒体库');
  });

  test('侧栏导航到 /task → 任务管理', async ({ page }) => {
    await clickSidebarLink(page, '任务管理', true);
    await expect(page).toHaveURL(/\/task/, { timeout: 5000 });
    await expectSidebarGroupVisible(page, '媒体库');
  });

  test('侧栏导航到 /cleanup → STRM 清理', async ({ page }) => {
    await clickSidebarLink(page, 'STRM 清理', true);
    await expect(page).toHaveURL(/\/cleanup/, { timeout: 5000 });
    await expect(page.getByRole('heading', { name: 'STRM 清理' })).toBeVisible();
  });

  test('侧栏导航到 /settings → 设置中心', async ({ page }) => {
    await clickSidebarLink(page, '设置中心', true);
    await expect(page).toHaveURL(/\/settings/, { timeout: 5000 });
    await expectSidebarGroupVisible(page, '设置');
  });

  test('侧栏导航到 /history → 执行历史', async ({ page }) => {
    await clickSidebarLink(page, '执行历史', true);
    await expect(page).toHaveURL(/\/history/, { timeout: 5000 });
    await expectSidebarGroupVisible(page, '日志');
  });

  test('侧栏导航到 /life-events → 生活事件', async ({ page }) => {
    await clickSidebarLink(page, '生活事件', true);
    await expect(page).toHaveURL(/\/life-events/, { timeout: 5000 });
    await expectSidebarGroupVisible(page, '日志');
  });

  test('侧栏导航到 /notify → 通知中心', async ({ page }) => {
    await clickSidebarLink(page, '通知中心', true);
    await expect(page).toHaveURL(/\/notify/, { timeout: 5000 });
    await expectSidebarGroupVisible(page, '通知');
  });
});
