// Self-tests and their Node subprocesses may only fetch loopback fixtures.
const fetch = globalThis.fetch
globalThis.fetch = async (input, options) => {
  const url = new URL(typeof input === 'string' || input instanceof URL ? input : input.url)
  if (!['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname)) throw new Error('Self-test blocked an external request')
  return fetch(input, options)
}
const preload = `--import=${import.meta.url}`
if (!(process.env.NODE_OPTIONS || '').includes(preload)) process.env.NODE_OPTIONS = `${process.env.NODE_OPTIONS || ''} ${preload}`.trim()
