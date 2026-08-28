/**
 * E2E 测试 — 核心路径最小集
 *
 * 运行前提：Go 后端已在 http://localhost:8090 启动
 *   cd .. && go run ./cmd/server --config ./e2e-tmp
 *   npx playwright test
 *
 * 为什么只做 3 个用例？
 *   完整 E2E（登录→加账号→建任务）需要 Go 后端 + 临时配置目录 + 测试数据，
 *   且涉及真实 115 API 调用，不适合在 CI 里跑（会变 flaky）。
 *   这 3 个用例覆盖了：API 健康检查、登录页渲染、登录失败反馈。
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
  // 未登录访问首页应重定向到登录页
  await page.goto(`${BASE}/login`);
  await expect(page.getByText('登录')).toBeVisible();
  await expect(page.getByPlaceholder(/请输入用户名/)).toBeVisible();
  await expect(page.getByPlaceholder(/请输入密码/)).toBeVisible();
  await expect(page.getByRole('button', { name: /登录/ })).toBeVisible();
});

test('错误密码 → 显示登录失败提示', async ({ page }) => {
  page.on('dialog', async dialog => {
    expect(dialog.message()).toContain('登录失败');
    await dialog.accept();
  });

  await page.goto(`${BASE}/login`);
  await page.getByPlaceholder(/请输入用户名/).fill('admin');
  await page.getByPlaceholder(/请输入密码/).fill('wrong_password_xyz');
  await page.getByRole('button', { name: /登录/ }).click();

  // 等待请求完成（dialog 会在 request 失败时触发）
  await page.waitForTimeout(500);
});
