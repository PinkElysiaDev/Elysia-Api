import { expect, test, type Page } from '@playwright/test'
import type { AgentMessage, AgentStreamEvent } from '../src/lib/agent/types'

type StreamWindow = Window & {
  pushAgentEvent: (event: AgentStreamEvent) => void
  finishAgentStream: () => void
}

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
  await page.getByText(session.title, { exact: true }).click()
  await expect(page.getByPlaceholder('请描述你的任务')).toBeVisible()
}

async function push(page: Page, ...events: AgentStreamEvent[]) {
  await page.evaluate((events) => events.forEach((event) => (window as StreamWindow).pushAgentEvent(event)), events)
}

test('tool_result embedded messages stay between assistant replies during the stream', async ({ page }) => {
  const persisted: AgentMessage[] = []
  await openAgent(page, persisted)
  await page.getByPlaceholder('请描述你的任务').fill('分析用量')
  await page.getByPlaceholder('请描述你的任务').press('Enter')
  await page.waitForFunction(() => Boolean((window as StreamWindow).pushAgentEvent))
  persisted.push(message(1, 'user', { text: '分析用量' }), message(2, 'assistant', {
    text: '先读取日志。', reasoning: '第一阶段思考',
    toolCalls: [{ id: 'a', name: 'bash', arguments: { command: 'elysia usage logs --days 1 --status failed --limit 10' } }],
  }))
  await push(page, ...persisted.map((message): AgentStreamEvent => ({ type: 'message', message })),
    { type: 'tool_call', callId: 'a', name: 'bash', input: { command: 'elysia usage logs --days 1 --status failed --limit 10' } })
  const groups = page.getByRole('button', { name: /^调用工具/ })
  await groups.first().click()
  const firstTool = page.getByRole('button', { name: '查看 bash 调用详情' }).first()
  await expect(firstTool).toContainText('elysia usage logs --days 1 --status failed --limit 10')
  await expect(firstTool).not.toContainText('command=')
  await firstTool.click()
  const result = { callId: 'a', name: 'bash', ok: true, input: { command: 'elysia usage logs --days 1 --status failed --limit 10' }, data: { output: '日志结果', exitCode: 0 } }
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
    { type: 'tool_call', callId: 'b', name: 'bash', input: { command: 'elysia usage stats --days 1' } })
  await expect(groups).toHaveCount(2)
  await expect(groups.first()).toHaveAttribute('aria-expanded', 'true')
  await expect(groups.nth(1)).toContainText('执行中')
  const secondResult = { callId: 'b', name: 'bash', ok: true, input: { command: 'elysia usage stats --days 1' }, data: { requests: 100 } }
  persisted.push(message(5, 'tool_result', secondResult))
  await push(page, { type: 'tool_result', callId: 'b', result: secondResult, message: persisted[4] },
    { type: 'tool_call', callId: 'c', name: 'bash', input: { command: 'elysia model ls' } })
  await expect(groups.nth(1)).toContainText('2 次')
  const thirdResult = { callId: 'c', name: 'bash', ok: true, input: { command: 'elysia model ls' }, data: ['test-model'] }
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
  await page.getByText('流式与图表回归检查', { exact: true }).click()
  await expect(groups).toHaveCount(2)
  await groups.first().click()
  await expect(firstTool).toContainText('elysia usage logs --days 1 --status failed --limit 10')
  await expect(firstTool).not.toContainText('command=')
  await firstTool.click()
  await expect(page.locator('[data-seq="3"] pre')).toContainText('日志结果')
})
