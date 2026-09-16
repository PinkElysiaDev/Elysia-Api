import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './tests',
  fullyParallel: true,
  use: {
    baseURL: 'http://127.0.0.1:5274',
    channel: process.env.PLAYWRIGHT_CHANNEL,
    trace: 'retain-on-failure',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] }, testIgnore: /login-motion\.spec\.ts/ },
    // cinematic 用例软解 1080p 视频 + WebGL2 合成,CPU 饥饿会让
    // requestVideoFrameCallback 间隔超过 900ms 停顿阈值导致过场被误判
    // 中断(长期并行 flaky 的根因):独立 project 强制单 worker 串行。
    {
      name: 'chromium-cinematic',
      use: { ...devices['Desktop Chrome'] },
      testMatch: /login-motion\.spec\.ts/,
      fullyParallel: false,
      workers: 1,
    },
  ],
  webServer: {
    command: 'npm run dev -- --port 5274 --strictPort',
    url: 'http://127.0.0.1:5274',
    reuseExistingServer: !process.env.CI,
  },
})
