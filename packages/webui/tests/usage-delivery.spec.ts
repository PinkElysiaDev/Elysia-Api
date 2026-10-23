import { expect, test } from '@playwright/test'
import { realpathSync } from 'node:fs'
import { basename, dirname } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import type { UsageLogDetail } from '../src/lib/types'

// Only the runner's disposable log database is seeded. Reads and downloads use
// the real backend so optional historical fields follow the persistence path.
function seedUsageRecords(records: UsageLogDetail[]) {
  const directory = realpathSync(process.env.PROTOCOL_E2E_RUN_DIRECTORY!)
  const databasePath = realpathSync(process.env.PROTOCOL_E2E_LOG_DATABASE!)
  expect(basename(directory)).toMatch(/^elysia-protocol-e2e-/)
  expect(dirname(databasePath)).toBe(directory)
  expect(new URL(process.env.PROTOCOL_E2E_URL!).hostname).toBe('127.0.0.1')
  const database = new DatabaseSync(databasePath)
  try {
    database.exec('PRAGMA busy_timeout=5000')
    const insert = database.prepare(`INSERT INTO usage_records
      (request_id,started_at,started_ms,ended_at,key_name,requested_model_group,model_name,platform,stream,status_code,error,record_json,content_bytes)
      VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`)
    for (const record of records) {
      const payload = JSON.stringify(record)
      insert.run(record.requestId, record.startedAt, Date.parse(record.startedAt), record.endedAt,
        record.keyName, record.requestedModelGroup, record.modelName, record.platform, Number(record.stream),
        record.statusCode, record.error ?? '', payload, Buffer.byteLength(payload))
    }
  } finally {
    database.close()
  }
}

test('real usage details and exports preserve independent delivery evidence and old records', async ({ page, request }) => {
  test.skip(!process.env.PROTOCOL_E2E_URL || !process.env.PROTOCOL_E2E_LOG_DATABASE, 'requires the isolated backend runner and its disposable log database')
  const suffix = Date.now()
  const timestamp = new Date().toISOString()
  const emptyBody = { content: '', truncated: false }
  const base: UsageLogDetail = {
    requestId: `delivery-legacy-${suffix}`, startedAt: timestamp, endedAt: timestamp,
    keyName: 'synthetic-delivery', keyHash: '', requestedModelGroup: 'synthetic-model', groupName: 'synthetic-model', modelName: 'synthetic-model',
    platform: 'openai-responses', sourceFormat: 'anthropic-messages', targetFormat: 'openai-responses',
    stream: true, statusCode: 200, firstByteMs: 10, durationMs: 20, usage: { inputTokens: 1, outputTokens: 1 }, retryCount: 0,
    incomingBody: emptyBody, outgoingBody: emptyBody, providerResponse: emptyBody, downstreamResponse: emptyBody,
  }
  const records: UsageLogDetail[] = [
    base,
    { ...base, requestId: `delivery-failed-${suffix}`, statusCode: 502, upstreamHttpStatus: 200,
      error: 'synthetic downstream write failure', errorKind: 'downstream_transport',
      delivery: { httpStatus: 200, outcome: 'failed', phase: 'response_finalize', cause: 'downstream_transport', error: 'synthetic final write failure' },
    },
    { ...base, requestId: `delivery-metadata-${suffix}`, statusCode: 502, upstreamHttpStatus: 200,
      error: 'response.metadata contains an unsupported synthetic field', errorKind: 'unsupported_capability',
      delivery: { httpStatus: 200, outcome: 'completed' },
      conversionIssues: [{ code: 'unsupported_capability', severity: 'error', direction: 'decode_event', stage: 'decode_event',
        path: '/metadata/unsupported', reason: 'response.metadata contains an unsupported synthetic field', suggestion: '',
        protocol: { definitionId: 'openai-responses', family: 'openai_responses', revision: 'synthetic', wireVersion: 'v1' } }],
      providerResponse: { ...emptyBody, content: '{"type":"response.metadata","metadata":{"unsupported":true}}' },
    },
    { ...base, requestId: `delivery-canceled-${suffix}`, statusCode: 503, upstreamHttpStatus: 200,
      error: 'context canceled', errorKind: 'request_canceled',
      delivery: { httpStatus: 200, outcome: 'failed', phase: 'response', cause: 'request_canceled', error: 'context canceled' },
    },
  ]
  seedUsageRecords(records)
  const token = process.env.PROTOCOL_E2E_TOKEN!
  const headers = { Authorization: `Bearer ${token}` }
  await page.addInitScript((value) => localStorage.setItem('elysia-webui.panel-token', value), token)
  await page.goto('/#/usage-logs')

  for (const record of records) {
    const persisted = await request.get(`${process.env.PROTOCOL_E2E_URL}/api/admin/usage/logs/${record.requestId}`, { headers })
    expect(persisted.status()).toBe(200)
    expect((await persisted.json()).data).toMatchObject(record)
    await page.getByRole('button', { name: `查看请求 ${record.requestId} 详情`, exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '调用详情' })
    await expect(dialog.getByTestId('logical-status')).toHaveText(String(record.statusCode))
    await expect(dialog.getByTestId('upstream-http-status')).toHaveText(record.upstreamHttpStatus ? '200' : '未记录')
    await expect(dialog.getByTestId('downstream-http-status')).toHaveText(record.delivery ? '200' : '未记录')
    await expect(dialog.getByTestId('delivery-outcome')).toHaveText(!record.delivery ? '未记录交付结果' : record.delivery.outcome === 'failed' ? '交付失败' : '服务端交付完成')
    if (record.conversionIssues) {
      await expect(dialog.getByText('上游事件解码失败（502）', { exact: false })).toBeVisible()
      await expect(dialog.getByText('协议能力不支持', { exact: false }).first()).toBeVisible()
      await dialog.getByLabel('转换诊断').locator('summary').click()
      await expect(dialog.getByText('阶段：decode_event', { exact: false })).toBeVisible()
    }
    if (record.delivery?.cause === 'request_canceled') {
      await expect(dialog.getByText('请求取消（来源未明）（503）', { exact: false })).toBeVisible()
      await expect(dialog.getByText('客户端取消', { exact: false })).toHaveCount(0)
    }
    const download = page.waitForEvent('download')
    await dialog.getByRole('button', { name: '导出完整日志' }).click()
    const stream = await (await download).createReadStream()
    let text = ''
    for await (const chunk of stream!) text += chunk.toString()
    const exported = JSON.parse(text)
    expect(exported.raw).toMatchObject(record)
    expect(exported.overview.statusCode).toBe(record.statusCode)
    expect(exported.overview.upstreamHttpStatus).toBe(record.upstreamHttpStatus)
    expect(exported.overview.delivery).toEqual(record.delivery)
    expect(exported.overview.conversionIssues).toEqual(record.conversionIssues)
    await dialog.getByRole('button', { name: '关闭', exact: true }).last().click()
  }
})
