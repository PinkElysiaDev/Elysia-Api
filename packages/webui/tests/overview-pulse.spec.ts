import { expect, test, type Page, type Route } from '@playwright/test'
import { realpathSync } from 'node:fs'
import { basename, dirname } from 'node:path'
import { DatabaseSync } from 'node:sqlite'

const MINUTE_MS = 60_000
const HOVER_INSET_PX = 1
const NOW = '2026-10-26T12:37:30+08:00'
const RANGES = { 60: '近 60 分钟', 360: '近 6 小时', 1440: '近 24 小时' } as const
type Query = { path: string; from: number; to: number; bucketMinutes: number }

test.use({ timezoneId: 'Asia/Shanghai', viewport: { width: 1440, height: 1000 } })

function pulse(page: Page) { return page.getByRole('region', { name: '实时脉搏' }) }
function throughput(page: Page) { return pulse(page).getByRole('group', { name: 'Token 吞吐' }) }
async function selectRange(page: Page, minutes: keyof typeof RANGES) {
  await pulse(page).getByRole('button', { name: RANGES[minutes], exact: true }).click()
}

function historicalUsage(query: Query, rate: number) {
  const width = query.bucketMinutes * MINUTE_MS
  const offset = 480 * MINUTE_MS
  const first = Math.floor((query.from + offset) / width) * width - offset
  const points = []
  for (let t = first; t < query.to; t += width) {
    const minutes = (Math.min(t + width, query.to) - Math.max(t, query.from)) / MINUTE_MS
    points.push({ t, requests: minutes * 2, totalTokens: minutes * rate, avgDurationMs: 100, p95DurationMs: 200 })
  }
  const minutes = (query.to - query.from) / MINUTE_MS
  return { points, window: { requests: minutes * 2, totalTokens: minutes * rate, avgDurationMs: 100, p95DurationMs: 200 } }
}

async function mockOverview(page: Page, options: { now?: string } = {}) {
  const now = new Date(options.now ?? NOW)
  // Keep pointer throttling and the minute clock on the same timeline.
  await page.clock.install({ time: now })
  await page.addInitScript(() => {
    localStorage.setItem('elysia-webui.panel-token', 'synthetic-only-token')
    localStorage.setItem('elysia-webui.theme', 'light')
  })
  const fixture = {
    minuteTokens: 1200,
    hasMinuteError: false,
    isHistoryEmpty: false,
    historyRate: (_minutes: number) => 6000,
    failedRanges: new Set<number>(),
    requests: [] as Query[],
    beforeReply: async (_query: Query) => {},
  }
  const fail = (route: Route) => route.fulfill({ status: 500, json: { ok: false, error: { code: 'synthetic_failure', message: 'synthetic statistics failure' } } })
  await page.route('**/api/admin/**', async (route) => {
    const url = new URL(route.request().url())
    const query = { path: url.pathname, from: Date.parse(url.searchParams.get('from') ?? ''), to: Date.parse(url.searchParams.get('to') ?? ''), bucketMinutes: Number(url.searchParams.get('bucketMinutes')) }
    fixture.requests.push(query)
    await fixture.beforeReply(query)
    const minutes = (query.to - query.from) / MINUTE_MS
    let data: unknown
    if (query.path.endsWith('/pulse')) {
      if (fixture.failedRanges.has(minutes)) { await fail(route); return }
      data = fixture.isHistoryEmpty
        ? { points: [], window: { requests: 0, totalTokens: 0, avgDurationMs: 0, p95DurationMs: 0 } }
        : historicalUsage(query, fixture.historyRate(minutes))
    } else if (query.path.endsWith('/stats')) {
      if (minutes === 1 && fixture.hasMinuteError) { await fail(route); return }
      data = { requests: 6, success: 6, failed: 0, inputTokens: 1000, outputTokens: 200, totalTokens: minutes === 1 ? fixture.minuteTokens : 24000, cacheHitTokens: 900, cacheHitRate: 0.9, avgDurationMs: 100, avgFirstByteMs: 10 }
    } else if (query.path.endsWith('/seq')) data = { seq: 1 }
    else if (/\/usage\/(trend|by-model|by-model-daily)$/.test(query.path)) data = []
    else if (query.path.endsWith('/health')) data = { status: 'ok', database: true, memory: { alloc: 0, sys: 0, numGC: 0 } }
    else data = { items: [], total: 0 }
    await route.fulfill({ json: { ok: true, data } })
  })
  return fixture
}

// Use the chart's rendered endpoint ticks, so the test also works on mobile.
async function hoverBucket(page: Page, bucketMinutes: number, position: 'first' | 'middle' | 'last', now = NOW) {
  const region = pulse(page)
  const chart = region.locator('.recharts-wrapper')
  await chart.scrollIntoViewIfNeeded()
  const to = Math.floor(Date.parse(now) / MINUTE_MS) * MINUTE_MS
  const spanMinutes = bucketMinutes === 1 ? 60 : bucketMinutes === 5 ? 360 : 1440
  const from = to - spanMinutes * MINUTE_MS
  const width = bucketMinutes * MINUTE_MS
  const first = Math.floor((from + 480 * MINUTE_MS) / width) * width - 480 * MINUTE_MS
  const count = Math.ceil((to - first) / width)
  const index = position === 'first' ? 0 : position === 'last' ? count - 1 : Math.floor(count / 2)
  const point = await chart.locator('svg.recharts-surface').evaluate((svg, { fraction, inset }) => {
    const ticks = svg.querySelectorAll('.recharts-xAxis text')
    const firstX = Number(ticks[0].getAttribute('x'))
    const lastX = Number(ticks[ticks.length - 1].getAttribute('x'))
    const box = svg.getBoundingClientRect()
    // Stay within the plot when Recharts rounds fractional edge coordinates.
    const x = Math.max(firstX + inset, Math.min(lastX - inset, firstX + (lastX - firstX) * fraction))
    return { x: box.x + x, y: box.y + box.height / 2 }
  }, { fraction: index / (count - 1), inset: HOVER_INSET_PX })
  await page.mouse.move(point.x, point.y)
  await expect(throughput(page)).toContainText('时段平均吞吐')
}

test('idle throughput reuses one minute query and hover uses its historical bucket', async ({ page }) => {
  const fixture = await mockOverview(page)
  await page.goto('/#/overview')
  await expect(throughput(page)).toContainText('1.2k tpm')
  await expect(throughput(page)).toContainText('12:36–12:37 · 1分钟平均')
  for (const [minutes, bucket] of [[60, 1], [360, 5], [1440, 15]] as const) {
    await selectRange(page, minutes)
    await expect(pulse(page)).toContainText(`${RANGES[minutes]}内`)
    await expect(throughput(page)).toContainText('上一分钟吞吐')
    await expect(throughput(page)).toContainText('1.2k tpm')
    await hoverBucket(page, bucket, 'middle')
    await expect(throughput(page)).toContainText('6k tpm')
    await expect(throughput(page)).toContainText(`${bucket}分钟平均`)
    await expect(pulse(page).getByText('100 ms', { exact: false })).toBeVisible()
    await page.mouse.move(1, 1)
    await expect(throughput(page)).toContainText('上一分钟吞吐')
    await expect(throughput(page)).toContainText('1.2k tpm')
  }
  await selectRange(page, 60)
  await expect(pulse(page)).toContainText('近 60 分钟内')
  expect(fixture.requests.filter(query => query.path.endsWith('/stats') && query.to - query.from === MINUTE_MS)).toHaveLength(1)
  expect(new Set(fixture.requests.filter(query => query.path.endsWith('/pulse')).map(query => query.bucketMinutes))).toEqual(new Set([1, 5, 15]))
})

test('partial first and last buckets use their covered minutes for TPM and RPM', async ({ page }) => {
  await mockOverview(page)
  await page.goto('/#/overview')
  await selectRange(page, 1440)
  await expect(pulse(page)).toContainText('近 24 小时内')
  await hoverBucket(page, 15, 'first')
  await expect(throughput(page)).toContainText('昨日 12:37–昨日 12:45 · 8分钟平均')
  await expect(throughput(page)).toContainText('6k tpm')
  await expect(pulse(page).getByText('2.0', { exact: true })).toBeVisible()
  await hoverBucket(page, 15, 'last')
  await expect(throughput(page)).toContainText('12:30–12:37 · 7分钟平均')
  await expect(throughput(page)).toContainText('6k tpm')
  await expect(pulse(page).getByText('2.0', { exact: true })).toBeVisible()
})

for (const tokens of [0, 100000]) {
  test(`a successful one-minute count of ${tokens} is shown without a cap`, async ({ page }) => {
    const fixture = await mockOverview(page)
    fixture.minuteTokens = tokens
    fixture.isHistoryEmpty = true
    await page.goto('/#/overview')
    await expect(throughput(page)).toContainText(tokens === 0 ? '0 tpm' : '100k tpm')
    await expect(pulse(page).getByText('0.0', { exact: true })).toBeVisible()
    await expect(throughput(page)).not.toContainText('加载失败')
  })
}

for (const minuteFails of [false, true]) {
  test(`failed range changes never divide retained daily tokens by an hour; minute failure=${minuteFails}`, async ({ page }) => {
    const fixture = await mockOverview(page)
    fixture.failedRanges.add(60)
    fixture.hasMinuteError = minuteFails
    fixture.historyRate = () => 1000
    await page.goto('/#/overview')
    await expect(pulse(page).getByText('synthetic statistics failure')).toBeVisible()
    await selectRange(page, 1440)
    await expect(pulse(page)).toContainText('近 24 小时内')
    await hoverBucket(page, 15, 'middle')
    await expect(throughput(page)).toContainText('1k tpm')
    await selectRange(page, 60)
    await expect(pulse(page).getByText('synthetic statistics failure')).toBeVisible()
    await expect(throughput(page)).toContainText('上一分钟吞吐')
    await expect(throughput(page)).toContainText(minuteFails ? '— tpm' : '1.2k tpm')
    await expect(throughput(page)).not.toContainText('24k')
    if (minuteFails) {
      fixture.hasMinuteError = false
      await throughput(page).getByRole('button', { name: '重试上一分钟统计' }).click()
      await expect(throughput(page)).toContainText('1.2k tpm')
    }
    fixture.failedRanges.clear()
    await pulse(page).getByRole('button', { name: '重试', exact: true }).click()
    await expect(pulse(page)).toContainText('近 60 分钟内')
    await expect(pulse(page).getByText('synthetic statistics failure')).toHaveCount(0)
  })
}

test('retained charts keep their window while loading and late responses cannot replace the selected range', async ({ page }) => {
  const fixture = await mockOverview(page)
  fixture.historyRate = minutes => minutes === 60 ? 1000 : minutes === 360 ? 3000 : 2000
  let releaseDaily!: () => void
  const dailyReady = new Promise<void>(resolve => { releaseDaily = resolve })
  fixture.beforeReply = async query => {
    if (query.path.endsWith('/pulse') && query.bucketMinutes === 15) await dailyReady
  }
  await page.goto('/#/overview')
  await expect(pulse(page)).toContainText('近 60 分钟内')
  await selectRange(page, 1440)
  await expect(pulse(page).getByRole('status', { name: '' })).toContainText('更新中')
  await expect(pulse(page)).toContainText('近 60 分钟内')
  await hoverBucket(page, 1, 'last')
  await expect(throughput(page)).toContainText('1k tpm')
  await expect(throughput(page)).toContainText('1分钟平均')
  await selectRange(page, 360)
  await expect(pulse(page)).toContainText('近 6 小时内')
  await expect(throughput(page)).toContainText('上一分钟吞吐')
  await hoverBucket(page, 5, 'middle')
  await expect(throughput(page)).toContainText('3k tpm')
  const lateResponse = page.waitForResponse(response => response.url().includes('/usage/pulse?') && new URL(response.url()).searchParams.get('bucketMinutes') === '15')
  releaseDaily()
  await lateResponse
  await expect(throughput(page)).toContainText('3k tpm')
  await expect(throughput(page)).toContainText('5分钟平均')
})

test('minute rollover clears hover and cannot relabel the previous minute count as fresh data', async ({ page }) => {
  const fixture = await mockOverview(page)
  const initialTo = Math.floor(Date.parse(NOW) / MINUTE_MS) * MINUTE_MS
  let releaseMinute!: () => void
  const minuteReady = new Promise<void>(resolve => { releaseMinute = resolve })
  fixture.beforeReply = async query => {
    if (query.path.endsWith('/stats') && query.to - query.from === MINUTE_MS && query.to > initialTo) await minuteReady
  }
  await page.goto('/#/overview')
  await expect(throughput(page)).toContainText('1.2k tpm')
  await hoverBucket(page, 1, 'last')
  await page.clock.fastForward(60_000)
  await expect(throughput(page)).toContainText('上一分钟吞吐')
  await expect(throughput(page)).toContainText('— tpm')
  await expect(throughput(page)).toContainText('12:37–12:38')
  fixture.minuteTokens = 2400
  releaseMinute()
  await expect(throughput(page)).toContainText('2.4k tpm')
})

for (const width of [375, 1440]) {
  test(`cross-day minute labels and chart interaction fit a ${width}px viewport`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 })
    const now = '2026-10-26T00:00:30+08:00'
    await mockOverview(page, { now })
    await page.goto('/#/overview')
    await expect(throughput(page)).toContainText('昨日 23:59–00:00 · 1分钟平均')
    await selectRange(page, 1440)
    await expect(pulse(page)).toContainText('近 24 小时内')
    await hoverBucket(page, 15, 'last', now)
    await expect(throughput(page)).toContainText('昨日 23:45–00:00 · 15分钟平均')
    await expect(throughput(page)).toContainText('6k tpm')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await pulse(page).screenshot({ path: testInfo.outputPath(`pulse-${width}.png`) })
  })
}

test('real backend minute totals and pulse buckets agree with isolated stored records', async ({ page, request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL || !process.env.PROTOCOL_E2E_LOG_DATABASE, 'requires the isolated backend runner')
  const directory = realpathSync(process.env.PROTOCOL_E2E_RUN_DIRECTORY!)
  const databasePath = realpathSync(process.env.PROTOCOL_E2E_LOG_DATABASE!)
  expect(basename(directory)).toMatch(/^elysia-protocol-e2e-/)
  expect(dirname(databasePath)).toBe(directory)
  const backend = process.env.PROTOCOL_E2E_URL!
  expect(new URL(backend).hostname).toBe('127.0.0.1')
  const to = Math.floor(Date.parse(NOW) / MINUTE_MS) * MINUTE_MS
  const records = [
    { suffix: 'minute', at: to - 30_000, status: 200, tokens: 1200 },
    { suffix: 'history', at: to - 20 * MINUTE_MS, status: 200, tokens: 90000 },
    { suffix: 'failed', at: to - 20 * MINUTE_MS, status: 502, tokens: 1000000000 },
    { suffix: 'exclusive-end', at: to, status: 200, tokens: 9000000 },
  ]
  const database = new DatabaseSync(databasePath)
  try {
    database.exec('PRAGMA busy_timeout=5000')
    const insert = database.prepare('INSERT INTO usage_records (request_id,started_at,started_ms,ended_at,status_code,input_tokens,output_tokens,total_tokens,cache_hit_tokens,duration_ms,record_json) VALUES(?,?,?,?,?,?,?,?,?,?,?)')
    for (const record of records) {
      const timestamp = new Date(record.at).toISOString()
      insert.run(`synthetic-pulse-${record.suffix}`, timestamp, record.at, timestamp, record.status, record.tokens - 200, 200, record.tokens, 900, 100, '{}')
    }
  } finally { database.close() }
  const token = process.env.PROTOCOL_E2E_TOKEN!
  const headers = { Authorization: `Bearer ${token}` }
  const bounds = { from: new Date(to - MINUTE_MS).toISOString(), to: new Date(to).toISOString() }
  const minute = await request.get(`${backend}/api/admin/usage/stats`, { headers, params: bounds })
  expect(minute.ok()).toBe(true)
  expect((await minute.json()).data).toMatchObject({ totalTokens: 1200, requests: 1 })
  const history = await request.get(`${backend}/api/admin/usage/pulse`, { headers, params: { ...bounds, from: new Date(to - 1440 * MINUTE_MS).toISOString(), bucketMinutes: 15, utcOffsetMinutes: 480 } })
  expect(history.ok()).toBe(true)
  expect((await history.json()).data.window).toMatchObject({ totalTokens: 91200, requests: 3 })
  await page.clock.install({ time: new Date(NOW) })
  await page.addInitScript(value => localStorage.setItem('elysia-webui.panel-token', value), token)
  await page.goto('/#/overview')
  await expect(throughput(page)).toContainText('1.2k tpm')
  await selectRange(page, 1440)
  await expect(pulse(page)).toContainText('近 24 小时内')
  // The previous full bucket is 12:15–12:30, with 90,000 successful tokens.
  const chart = pulse(page).locator('.recharts-wrapper')
  await chart.scrollIntoViewIfNeeded()
  const point = await chart.locator('svg.recharts-surface').evaluate(svg => {
    const ticks = svg.querySelectorAll('.recharts-xAxis text')
    const first = Number(ticks[0].getAttribute('x')), last = Number(ticks[ticks.length - 1].getAttribute('x'))
    const box = svg.getBoundingClientRect()
    return { x: box.x + last - (last - first) / 96, y: box.y + box.height / 2 }
  })
  await page.mouse.move(point.x, point.y)
  await expect(throughput(page)).toContainText('6k tpm')
  await expect(throughput(page)).toContainText('12:15–12:30 · 15分钟平均')
})
