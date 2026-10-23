/**
 * E2E 测试 — 核心路径最小集
 *
 * 运行前提：Go 后端已在 http://localhost:8090 启动
 *   npx playwright test
 */
import { test, expect, request } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://localhost:8090';
const USERNAME = process.env.E2E_USERNAME || 'admin';
const PASSWORD = process.env.E2E_PASSWORD || 'admin';

// ==================== API 层 ====================

test('API /api/health 健康检查', async () => {
  const api = await request.newContext();
  const res = await api.get(`${BASE}/api/health`);
  expect(res.status()).toBe(200);
  const body = await res.json();
  expect(body).toHaveProperty('status');
  expect(body.status).toBe('ok');
  await api.dispose();
});

test('API /api/docs Swagger UI 存在', async () => {
  const api = await request.newContext();
  const res = await api.get(`${BASE}/api/docs`);
  expect(res.status()).toBe(200);
  const body = await res.json();
  expect(body.openapi).toMatch(/^3\./);
  expect(body.paths).toBeDefined();
  await api.dispose();
});

// ==================== UI 层 ====================

test('登录页能正常渲染', async ({ page }) => {
  await page.goto('/login');
  await expect(page.getByRole('heading', { name: '登录' })).toBeVisible();
  await expect(page.getByPlaceholder(/请输入用户名/)).toBeVisible();
  await expect(page.getByPlaceholder(/请输入密码/)).toBeVisible();
  await expect(page.getByRole('button', { name: '登录' })).toBeVisible();
});

test('正确凭证 → 登录成功进入概览', async ({ page }) => {
  await page.goto('/login');
  await page.getByPlaceholder(/请输入用户名/).fill(USERNAME);
  await page.getByPlaceholder(/请输入密码/).fill(PASSWORD);
  await page.getByRole('button', { name: '登录' }).click();

  await expect(page).not.toHaveURL(/\/login/, { timeout: 10000 });
  await expect(page.getByRole('heading', { name: '概览' })).toBeVisible({ timeout: 10000 });
  await expect(page.getByText('媒体库', { exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: '网盘账号', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: '任务管理', exact: true })).toBeVisible();
});

test('错误密码 → 显示 alert', async ({ page }) => {
  let dialogMessage = '';
  page.on('dialog', async dialog => {
    dialogMessage = dialog.message();
    try { await dialog.accept(); } catch { /* 忽略页面跳转导致的 accept 失败 */ }
  });

  await page.goto('/login');
  await page.getByPlaceholder(/请输入用户名/).fill(USERNAME);
  await page.getByPlaceholder(/请输入密码/).fill(`${PASSWORD}_wrong_xyz`);
  await page.getByRole('button', { name: '登录' }).click();

  await page.waitForTimeout(2000);
  expect(dialogMessage).toMatch(/账号或密码错误|登录失败/);
});

test('登录后登出 → 返回登录页', async ({ page }) => {
  await page.goto('/login');
  await page.getByPlaceholder(/请输入用户名/).fill(USERNAME);
  await page.getByPlaceholder(/请输入密码/).fill(PASSWORD);
  await page.getByRole('button', { name: '登录' }).click();
  await expect(page.getByRole('heading', { name: '概览' })).toBeVisible({ timeout: 10000 });

  const userMenuTrigger = page.locator('[data-slot="menubar-trigger"]').first();
  await expect(userMenuTrigger).toBeVisible({ timeout: 5000 });
  await userMenuTrigger.click();

  const logoutItem = page.getByRole('menuitem', { name: /退出登录/ });
  await expect(logoutItem).toBeVisible({ timeout: 3000 });
  await logoutItem.click();

  await expect(page).toHaveURL(/\/login/, { timeout: 10000 });
  await expect(page.getByRole('heading', { name: '登录' })).toBeVisible();
});
