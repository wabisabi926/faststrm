import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright 配置
 * 本地运行前需要手动启动 Go 后端：
 *   cd .. && go run ./cmd/server --config ./tmp-config
 * 然后：npx playwright test
 *
 * CI 集成见 .github/workflows/ci-e2e.yml（可选）
 */
export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: process.env.BASE_URL || 'http://localhost:8090',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  // 超时：登录页测试不需要太长
  timeout: 15000,
});
