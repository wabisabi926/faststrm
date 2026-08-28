/**
 * E2E 测试 — 核心路径最小集
 *
 * 运行前提：Go 后端已在 http://localhost:8090 启动
 *   npx playwright test
 */
import { test, expect, request } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://localhost:8090';

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
  // 使用 getByRole 精确匹配，避免 h1 和 button 都叫 "登录" 的歧义
  await expect(page.getByRole('heading', { name: '登录' })).toBeVisible();
  await expect(page.getByPlaceholder(/请输入用户名/)).toBeVisible();
  await expect(page.getByPlaceholder(/请输入密码/)).toBeVisible();
  await expect(page.getByRole('button', { name: '登录' })).toBeVisible();
});

test('正确凭证 → 登录成功跳转', async ({ page }) => {
  await page.goto('/login');
  await page.getByPlaceholder(/请输入用户名/).fill('admin');
  await page.getByPlaceholder(/请输入密码/).fill('admin');
  await page.getByRole('button', { name: '登录' }).click();

  // 等待 URL 变化（登录成功后 navigate("/") → home.tsx → Navigate /account）
  await expect(page).toHaveURL(/\/account/, { timeout: 10000 });

  // 验证侧栏可见，说明已登录 —— 侧栏分组标签是 div，用 getByText exact 匹配
  await expect(page.getByText('主菜单', { exact: true })).toBeVisible({ timeout: 5000 });
  await expect(page.getByRole('link', { name: '账户', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: '任务', exact: true })).toBeVisible();
});

test('错误密码 → 显示 alert', async ({ page }) => {
  // axios interceptor 在收到 401 时会做 window.location.href = '/login'（整页刷新）
  // 所以 dialog 触发后页面立刻就跳了，不能等 waitForEvent 后再 accept
  // 改用 page.on 注册即时处理
  let dialogMessage = '';
  page.on('dialog', async dialog => {
    dialogMessage = dialog.message();
    // 立即 accept，不管页面会不会马上跳转
    try { await dialog.accept(); } catch { /* 忽略页面跳转导致的 accept 失败 */ }
  });

  await page.goto('/login');
  await page.getByPlaceholder(/请输入用户名/).fill('admin');
  await page.getByPlaceholder(/请输入密码/).fill('wrong_password_xyz');
  await page.getByRole('button', { name: '登录' }).click();

  // 等待请求完成 + 页面跳转
  await page.waitForTimeout(2000);
  expect(dialogMessage).toContain('登录失败');
});

test('登录后登出 → 返回登录页', async ({ page }) => {
  // 先登录
  await page.goto('/login');
  await page.getByPlaceholder(/请输入用户名/).fill('admin');
  await page.getByPlaceholder(/请输入密码/).fill('admin');
  await page.getByRole('button', { name: '登录' }).click();
  await expect(page).toHaveURL(/\/account/, { timeout: 10000 });

  // 右上角用户菜单 —— Radix Menubar 的 trigger 用数据属性定位
  const userMenuTrigger = page.locator('[data-slot="menubar-trigger"]').first();
  await expect(userMenuTrigger).toBeVisible({ timeout: 5000 });
  await userMenuTrigger.click();

  // 等待 menubar content 出现，然后点击"退出登录"
  const logoutItem = page.getByRole('menuitem', { name: /退出登录/ });
  await expect(logoutItem).toBeVisible({ timeout: 3000 });
  await logoutItem.click();

  // 登出后应跳回登录页
  await expect(page).toHaveURL(/\/login/, { timeout: 10000 });
  await expect(page.getByRole('heading', { name: '登录' })).toBeVisible();
});
