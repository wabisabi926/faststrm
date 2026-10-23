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

  test('侧栏导航到 /account → 网盘账号', async ({ page }) => {
    await clickSidebarLink(page, '网盘账号', true);
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
    await expect(page.getByRole('button', { name: /开始扫描/ })).toBeVisible();
    await expect(page.getByRole('button', { name: /全量对账/ })).toHaveCount(0);
  });

  test('侧栏导航到 /settings → 常规', async ({ page }) => {
    await clickSidebarLink(page, '常规', true);
    await expect(page).toHaveURL(/\/settings/, { timeout: 5000 });
    await expectSidebarGroupVisible(page, '设置');
    await expect(page.getByRole('heading', { name: '常规' })).toBeVisible();
    await expect(page.getByText('配置索引')).toHaveCount(0);
    await expect(page.getByRole('link', { name: /播放诊断/ }).first()).toBeVisible();
  });

  test('侧栏导航到 /history → 任务记录', async ({ page }) => {
    await clickSidebarLink(page, '任务记录', true);
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
    await expectSidebarGroupVisible(page, '设置');
  });

  test('任务列表正文使用紧凑字号', async ({ page }) => {
    await page.route(/\/api\/tasks$/, (route) =>
      route.fulfill({
        json: {
          tasks: [
            {
              id: 'e2e-task',
              account: 'e2e-account',
              accountType: '115',
              originPath: '/remote/demo',
              targetPath: '/local/demo',
              strmType: 'local',
              runtime: { status: 'completed' },
            },
          ],
        },
      })
    );
    await page.route(/\/api\/account$/, (route) =>
      route.fulfill({ json: [{ name: 'e2e-account', accountType: '115' }] })
    );

    await page.goto('/task');
    const path = page.getByText('/remote/demo');
    await expect(path).toBeVisible();
    const fontSize = await path.evaluate((el) => getComputedStyle(el).fontSize);
    expect(fontSize).toBe('12px');
  });

  test('日志详情历史模式渲染文件列表', async ({ page }) => {
    await page.route(/\/api\/taskHistory\/stats$/, (route) =>
      route.fulfill({
        json: {
          total: 1,
          completed: 1,
          failed: 0,
          cancelled: 0,
          running: 0,
          filesDownloaded: 2,
          filesTotal: 2,
          avgDurationMs: 5000,
        },
      })
    );
    await page.route(/\/api\/taskHistory(\?.*)?$/, (route) =>
      route.fulfill({
        json: {
          items: [
            {
              id: 1,
              taskId: 'e2e-task',
              status: 'completed',
              startedAt: Date.now() - 5000,
            },
          ],
        },
      })
    );
    await page.route(/\/api\/taskHistory\/1\/logs$/, (route) =>
      route.fulfill({
        json: {
          logs: [
            JSON.stringify({ filePath: 'movie.mkv', percent: 100, done: true }),
            JSON.stringify({ filePath: 'show.srt', percent: 50, error: 'download failed' }),
          ],
        },
      })
    );

    await page.goto('/log/e2e-task?executionId=1');
    const filePath = page.getByText('movie.mkv');
    await expect(filePath).toBeVisible();
    await expect(page.getByText('show.srt')).toBeVisible();
    await expect(page.getByText('download failed')).toBeVisible();
    const fontSize = await filePath.evaluate((el) => getComputedStyle(el).fontSize);
    expect(fontSize).toBe('12px');
  });

  test('任务记录使用统计卡和表格骨架', async ({ page }) => {
    await page.route(/\/api\/taskHistory\/stats$/, (route) =>
      route.fulfill({
        json: {
          total: 1,
          completed: 1,
          failed: 0,
          cancelled: 0,
          running: 0,
          filesDownloaded: 2,
          filesTotal: 2,
          avgDurationMs: 5000,
        },
      })
    );
    await page.route(/\/api\/taskHistory(\?.*)?$/, (route) =>
      route.fulfill({
        json: {
          items: [
            {
              id: 1,
              taskId: 'demo-task',
              account: 'demo-account',
              originPath: '/remote/demo',
              targetPath: '/local/demo',
              status: 'completed',
              startedAt: Date.now() - 5000,
              endedAt: Date.now(),
              durationMs: 5000,
              summary: { totalFiles: 2, downloadedFiles: 2, deletedFiles: 0 },
            },
          ],
        },
      })
    );
    await page.route(/\/api\/history\/strm\?limit=200$/, (route) =>
      route.fulfill({
        json: {
          items: [
            {
              id: 1,
              taskId: 'demo-task',
              kind: 'full',
              account: 'demo-account',
              success: true,
              totalFiles: 2,
              successFiles: 2,
              failedFiles: 0,
              elapsedMs: 1200,
              apiRequests: 3,
              createdAt: Date.now(),
            },
          ],
        },
      })
    );
    await page.route(/\/api\/history\/strm\/stats$/, (route) =>
      route.fulfill({
        json: {
          totalCount: 1,
          successCount: 1,
          failedCount: 0,
          totalFiles: 2,
          successFiles: 2,
          failedFiles: 0,
          totalApiRequests: 3,
          totalElapsedMs: 1200,
        },
      })
    );

    await page.goto('/history');
    await expect(page.getByText('记录总数')).toBeVisible();
    await expect(page.getByText('demo-task')).toBeVisible();
    await expect(page.getByRole('columnheader', { name: '任务 / 路径' })).toBeVisible();

    await expect(page.getByRole('columnheader', { name: '任务 / 路径' })).toBeVisible();
    await expect(page.getByRole('columnheader', { name: '耗时' })).toBeVisible();
  });

  test('日志详情页可达且深色模式正常', async ({ page }) => {
    await page.goto('/log/e2e-no-task');
    await expect(page.getByRole('heading', { name: '任务日志' })).toBeVisible();
    await expect(page.getByText('暂无任务日志')).toBeVisible();

    await page.evaluate(() => localStorage.setItem('theme', 'dark'));
    await page.reload();
    await expect(page.locator('html')).toHaveClass(/dark/);

    const card = page.locator('.bg-card').first();
    await expect(card).toBeVisible();
    const background = await card.evaluate((el) => getComputedStyle(el).backgroundColor);
    expect(background).not.toBe('rgb(255, 255, 255)');
  });
});
