import { expect, test, type Page } from '@playwright/test'
import { cliCommandOf, type AgentMessage, type AgentStreamEvent } from '../src/lib/agent/types'

type StreamWindow = Window & {
  pushAgentEvent: (event: AgentStreamEvent) => void
  finishAgentStream: () => void
}

const chart = '```chart\n' + JSON.stringify({
  type: 'bar', title: '模型用量', x: ['模型 A', '模型 B'],
  series: [{ name: '请求数', data: [100, 50] }],
}) + '\n```'
const message = (seq: number, role: AgentMessage['role'], content: unknown): AgentMessage => ({
  seq, role, content, createdAt: '2026-09-27T12:00:00Z',
})

async function openAgent(page: Page, messages: AgentMessage[] = []) {
  const session = {
    id: 'regression', title: '流式与图表回归检查', mode: 'create', status: 'idle',
    createdAt: '2026-09-27T12:00:00Z', updatedAt: '2026-09-27T12:00:00Z',
    plan: [{ title: '查看用量', status: 'done' }],
    settings: { modelSourceId: 'test', modelName: 'test-model', thinkingEnabled: false },
  }
  await page.addInitScript(() => {
    localStorage.setItem('elysia-webui.panel-token', 'test-only-token')
    localStorage.setItem('elysia-webui.theme', 'light')
    const originalFetch = window.fetch
    window.fetch = (input, init) => {
      if (String(input).endsWith('/agent/sessions/regression/messages') && init?.method === 'POST') {
        return Promise.resolve(new Response(new ReadableStream({
          start(controller) {
            const streamWindow = window as StreamWindow
            streamWindow.pushAgentEvent = (event) => controller.enqueue(new TextEncoder().encode(
              `event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`,
            ))
            streamWindow.finishAgentStream = () => controller.close()
          },
        }), { headers: { 'Content-Type': 'text/event-stream' } }))
      }
      return originalFetch(input, init)
    }
  })
  await page.route('**/api/admin/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    const data = path.endsWith('/seq') ? { seq: 1 }
      : path.endsWith('/agent/sessions') ? { items: [session] }
        : path.endsWith('/sessions/regression') ? { session, messages }
          : path.endsWith('/models') ? { items: [{ id: 'test', sourceId: 'test', name: 'test-model', enabled: true, type: 'llm', maxTokens: 100000 }] }
            : path.endsWith('/model-sources') ? { items: [{ id: 'test', name: 'Test', enabled: true }] }
              : { items: [], total: 0 }
    await route.fulfill({ json: { ok: true, data } })
  })
  await page.goto('/#/agent')
  await page.locator('[role="button"]').filter({ has: page.getByText(session.title, { exact: true }) }).press('Enter')
  await expect(page.getByPlaceholder('请描述你的任务')).toBeVisible()
}

async function push(page: Page, ...events: AgentStreamEvent[]) {
  await page.evaluate((events) => events.forEach((event) => (window as StreamWindow).pushAgentEvent(event)), events)
}

const bars = '.recharts-bar-rectangle path.recharts-rectangle'

test('only elysia_cli exposes a CLI command preview', () => {
  const input = { command: 'elysia source ls' }
  expect(cliCommandOf('elysia_cli', input)).toBe(input.command)
  expect(cliCommandOf('bash', input)).toBeUndefined()
  expect(cliCommandOf('ask_user', input)).toBeUndefined()
  expect(cliCommandOf('elysia_cli', undefined)).toBeUndefined()
})

async function readyChart(page: Page) {
  await openAgent(page, [message(1, 'user', { text: '显示用量' }), message(2, 'assistant', { text: chart })])
  await expect(page.locator(bars)).toHaveCount(2)
  // Recharts' first entrance takes 1500 ms; measure only after it has settled.
  await expect.poll(() => page.locator(bars).first().evaluate((el) => (el as SVGGraphicsElement).getBBox().height)).toBeGreaterThan(180)
  await page.waitForTimeout(200)
}

test('tool_result embedded messages stay between assistant replies during the stream', async ({ page }) => {
  const persisted: AgentMessage[] = []
  await openAgent(page, persisted)
  await page.getByPlaceholder('请描述你的任务').fill('分析用量')
  await page.getByPlaceholder('请描述你的任务').press('Enter')
  await page.waitForFunction(() => Boolean((window as StreamWindow).pushAgentEvent))
  persisted.push(message(1, 'user', { text: '分析用量' }), message(2, 'assistant', {
    text: '先读取日志。', reasoning: '第一阶段思考',
    toolCalls: [{ id: 'a', name: 'elysia_cli', arguments: { command: 'elysia usage logs --days 1 --status failed --limit 10' } }],
  }))
  await push(page, ...persisted.map((message): AgentStreamEvent => ({ type: 'message', message })),
    { type: 'tool_call', callId: 'a', name: 'elysia_cli', input: { command: 'elysia usage logs --days 1 --status failed --limit 10' } })
  const groups = page.getByRole('button', { name: /^调用工具/ })
  await groups.first().click()
  const firstTool = page.getByRole('button', { name: '查看 elysia_cli 调用详情' }).first()
  await expect(firstTool).toContainText('elysia usage logs --days 1 --status failed --limit 10')
  await expect(firstTool).not.toContainText('command=')
  await firstTool.click()
  const result = { callId: 'a', name: 'elysia_cli', ok: true, input: { command: 'elysia usage logs --days 1 --status failed --limit 10' }, data: { output: '日志结果', exitCode: 0 } }
  persisted.push(message(3, 'tool_result', result))
  // This matches Engine.persistToolResult: there is no separate `message` event.
  await push(page, { type: 'tool_result', callId: 'a', result, message: persisted[2] },
    { type: 'text_delta', delta: '接着按模型汇总。' })
  await expect(firstTool).toHaveAttribute('aria-expanded', 'true')
  await expect(page.locator('[data-seq="3"] pre')).toContainText('日志结果')
  const liveText = page.getByText('接着按模型汇总。', { exact: true })
  expect(await groups.first().evaluate((tool, text) => !!(tool.compareDocumentPosition(text!) & Node.DOCUMENT_POSITION_FOLLOWING), await liveText.elementHandle())).toBe(true)
  persisted.push(message(4, 'assistant', { text: '接着按模型汇总。', reasoning: '第二阶段思考' }))
  await push(page, { type: 'message', message: persisted[3] },
    { type: 'tool_call', callId: 'b', name: 'elysia_cli', input: { command: 'elysia usage stats --days 1' } })
  await expect(groups).toHaveCount(2)
  await expect(groups.first()).toHaveAttribute('aria-expanded', 'true')
  await expect(groups.nth(1)).toContainText('执行中')
  const secondResult = { callId: 'b', name: 'elysia_cli', ok: true, input: { command: 'elysia usage stats --days 1' }, data: { requests: 100 } }
  persisted.push(message(5, 'tool_result', secondResult))
  await push(page, { type: 'tool_result', callId: 'b', result: secondResult, message: persisted[4] },
    { type: 'tool_call', callId: 'c', name: 'elysia_cli', input: { command: 'elysia model ls' } })
  await expect(groups.nth(1)).toContainText('2 次')
  const thirdResult = { callId: 'c', name: 'elysia_cli', ok: true, input: { command: 'elysia model ls' }, data: ['test-model'] }
  persisted.push(message(6, 'tool_result', thirdResult), message(7, 'assistant', { text: '分析完毕。' }))
  await push(page, { type: 'tool_result', callId: 'c', result: thirdResult, message: persisted[5] },
    { type: 'message', message: persisted[6] })
  await expect(groups).toHaveCount(2)
  await expect(page.getByRole('button', { name: '复制本轮正文' })).toHaveCount(0)
  await push(page, { type: 'turn_done' })
  await page.evaluate(() => (window as StreamWindow).finishAgentStream())
  await expect(page.getByRole('button', { name: '复制本轮正文' })).toHaveCount(1)
  await expect(groups).toHaveCount(2)
  await expect(groups.first()).toHaveAttribute('aria-expanded', 'true')
  await expect(firstTool).toHaveAttribute('aria-expanded', 'true')
  // Reload from stored messages to exercise history replay independently of SSE.
  await page.reload()
  await page.locator('[role="button"]').filter({ has: page.getByText('流式与图表回归检查', { exact: true }) }).press('Enter')
  await expect(groups).toHaveCount(2)
  await groups.first().click()
  await expect(firstTool).toContainText('elysia usage logs --days 1 --status failed --limit 10')
  await expect(firstTool).not.toContainText('command=')
  await firstTool.click()
  await expect(page.locator('[data-seq="3"] pre')).toContainText('日志结果')
})

test('typing in the composer preserves the existing chart and its bars', async ({ page }) => {
  await readyChart(page)
  const original = await page.locator('.recharts-wrapper').elementHandle()
  await page.getByPlaceholder('请描述你的任务').fill('准备下一条消息')
  expect(await original!.evaluate((el) => el.isConnected)).toBe(true)
  expect(await page.locator(bars).first().evaluate((el) => (el as SVGGraphicsElement).getBBox().height)).toBeGreaterThan(180)
})

test('streaming text after a chart preserves that chart instance', async ({ page }) => {
  await openAgent(page)
  const composer = page.getByPlaceholder('请描述你的任务')
  await composer.fill('显示用量')
  await composer.press('Enter')
  await page.waitForFunction(() => Boolean((window as StreamWindow).pushAgentEvent))
  await push(page, { type: 'message', message: message(1, 'user', { text: '显示用量' }) },
    { type: 'text_delta', delta: chart })
  await expect(page.locator(bars)).toHaveCount(2)
  const original = await page.locator('.recharts-wrapper').elementHandle()
  await push(page, { type: 'text_delta', delta: '\n\n这是按模型分组的用量统计。' })
  await expect(page.getByText('这是按模型分组的用量统计。', { exact: true })).toBeVisible()
  expect(await original!.evaluate((el) => el.isConnected)).toBe(true)
})

test('resizing changes chart width without replaying its entrance animation', async ({ page }) => {
  await readyChart(page)
  const original = await page.locator('.recharts-wrapper').elementHandle()
  await page.getByRole('button', { name: '任务方案', exact: true }).click()
  expect(await original!.evaluate((el) => el.isConnected)).toBe(true)
  const originalWidth = (await page.locator('.recharts-wrapper').boundingBox())!.width
  const samples = page.evaluate(async (selector) => {
    const heights: number[] = []
    const start = performance.now()
    while (performance.now() - start < 1800) {
      await new Promise(requestAnimationFrame)
      heights.push((document.querySelector(selector) as SVGGraphicsElement | null)?.getBBox().height ?? 0)
    }
    return heights
  }, bars)
  const handle = await page.getByRole('separator', { name: '拖拽调整侧栏宽度' }).boundingBox()
  await page.mouse.move(handle!.x + 3, handle!.y + 100)
  await page.mouse.down()
  await page.mouse.move(handle!.x - 100, handle!.y + 100, { steps: 12 })
  await page.mouse.up()
  expect(await original!.evaluate((el) => el.isConnected)).toBe(true)
  await page.getByRole('button', { name: '收起任务资料', exact: true }).click()
  await page.setViewportSize({ width: 850, height: 900 })
  const heights = await samples
  expect((await page.locator('.recharts-wrapper').boundingBox())!.width).toBeLessThan(originalWidth)
  expect(Math.min(...heights)).toBeGreaterThan(180)
})

test('agent list and chat enter directly without a page fade or slide', async ({ page }) => {
  await openAgent(page)
  const animationsOnParents = (element: Element) => {
    const animations: string[] = []
    for (let parent: Element | null = element; parent; parent = parent.parentElement) {
      const name = getComputedStyle(parent).animationName
      if (name !== 'none') animations.push(name)
    }
    return animations
  }
  expect(await page.getByPlaceholder('请描述你的任务').evaluate(animationsOnParents)).toEqual([])
  await page.goto('/#/logs')
  await page.getByRole('link', { name: 'AI 助手', exact: true }).click()
  expect(await page.getByRole('heading', { name: 'AI 助手', exact: true }).evaluate(animationsOnParents)).toEqual([])
})
