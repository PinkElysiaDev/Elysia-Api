import { defineConfig, devices } from '@playwright/test'

const port = Number(process.env.PLAYWRIGHT_PORT ?? 5274)
const baseURL = `http://127.0.0.1:${port}`

export default defineConfig({
  testDir: './tests',
  fullyParallel: true,
  use: {
    baseURL,
    channel: process.env.PLAYWRIGHT_CHANNEL,
    trace: 'retain-on-failure',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] }, testIgnore: /login-motion\.spec\.ts/ },
    { name: 'webkit', use: { ...devices['Desktop Safari'] }, testIgnore: /login-motion\.spec\.ts/ },
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
    command: `npm run dev -- --port ${port} --strictPort`,
    url: baseURL,
    reuseExistingServer: !process.env.CI && !process.env.PROTOCOL_E2E_URL,
  },
})
