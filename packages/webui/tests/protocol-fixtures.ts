import { expect, type APIRequestContext } from '@playwright/test'
import { ProtocolDocument } from '../src/lib/protocol-document'
import { parseProtocolPackage } from '../src/lib/protocol-v2'

/** Read the active server package without rounding authored JSON numbers. */
export async function readActiveProtocolPackage(request: APIRequestContext, base: string, headers: Record<string, string>, id: string) {
  const listing = await request.get(`${base}/api/admin/protocols`, { headers })
  expect(listing.ok()).toBeTruthy()
  const runtime = (await listing.json()).data
  const active = runtime.active.find((entry: { protocolId: string }) => entry.protocolId === id)
  expect(active).toBeDefined()
  expect(runtime.loaded[id]).toBe(active.revisionHash)
  const revisions = await request.get(`${base}/api/admin/protocols/${id}/revisions`, { headers })
  expect(revisions.ok()).toBeTruthy()
  const document = new ProtocolDocument(await revisions.text())
  const entries = JSON.parse(document.read('/data/items')!) as { hash: string }[]
  const index = entries.findIndex((entry) => entry.hash === active.revisionHash)
  expect(index).toBeGreaterThanOrEqual(0)
  const source = document.read(`/data/items/${index}/definition`)!
  const compiled = parseProtocolPackage(source)
  expect(compiled.read('/mapping/coreVersion')).toBe('2')
  return source
}

/** Decode through the source package before testing policies on the central core. */
export async function decodeProtocolRequest(request: APIRequestContext, base: string, headers: Record<string, string>, definition: string, input: unknown) {
  return decodeProtocolDocument(request, base, headers, definition, input, 'decode_request')
}

/** Decode provider output through its active Mapping before testing target projections. */
export async function decodeProtocolResponse(request: APIRequestContext, base: string, headers: Record<string, string>, definition: string, input: unknown) {
  return decodeProtocolDocument(request, base, headers, definition, input, 'decode_response')
}

async function decodeProtocolDocument(request: APIRequestContext, base: string, headers: Record<string, string>, definition: string, input: unknown, direction: string) {
  const response = await request.post(`${base}/api/admin/protocols/preview`, {
    headers: { ...headers, 'Content-Type': 'application/json' },
    data: `{"definition":${definition},"direction":"${direction}","input":${JSON.stringify(input)},"sequence":false}`,
  })
  expect(response.ok(), await response.text()).toBeTruthy()
  const result = (await response.json()).data
  expect(result.issues.filter((issue: { severity: string }) => issue.severity === 'error')).toEqual([])
  expect(result.output.schemaVersion).toBe(2)
  return result.output
}
