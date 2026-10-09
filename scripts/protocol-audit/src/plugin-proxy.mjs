import { createServer, request } from 'node:http'

// Proxy only to the temporary gateway, never to an absolute URL supplied by a
// client. Forward original bytes; evidence redaction happens in the callback.
export async function captureProxy(baseUrl, capture, onError) {
  const origin = new URL(baseUrl)
  if (origin.protocol !== 'http:' || origin.hostname !== '127.0.0.1') throw new Error('Plugin audit requires a loopback gateway')
  const pending = new Set()
  const server = createServer((incoming, outgoing) => {
    if (!incoming.url.startsWith('/') || incoming.url.startsWith('//')) {
      outgoing.writeHead(400); outgoing.end('Audit proxy requires an origin-relative path'); return
    }
    const upstream = request({ hostname: origin.hostname, port: origin.port, path: incoming.url, method: incoming.method, headers: { ...incoming.headers, host: origin.host } }, response => {
      outgoing.writeHead(response.statusCode, response.headers)
      response.pipe(outgoing, { end: false })
      response.once('end', () => { outgoing.addTrailers(response.trailers); outgoing.end() })
      response.once('error', () => outgoing.destroy())
      response.once('close', () => {
        const work = Promise.resolve().then(() => capture({
          method: incoming.method, path: incoming.url, userAgent: incoming.headers['user-agent'],
          status: response.statusCode, requestId: response.headers['x-elysia-request-id'], complete: response.complete,
        })).catch(onError).finally(() => pending.delete(work))
        pending.add(work)
      })
    })
    upstream.on('error', () => { if (!outgoing.headersSent) outgoing.writeHead(502); outgoing.end('Audit proxy transport failed') })
    incoming.on('error', () => upstream.destroy())
    outgoing.on('close', () => { if (!outgoing.writableFinished) upstream.destroy() })
    incoming.pipe(upstream)
  })
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  return {
    baseUrl: `http://127.0.0.1:${server.address().port}`,
    close: async () => {
      server.closeAllConnections()
      await new Promise(resolve => server.close(resolve))
      // Response close handlers enqueue evidence before this next event turn.
      await new Promise(resolve => setImmediate(resolve))
      await Promise.allSettled([...pending])
    },
  }
}
