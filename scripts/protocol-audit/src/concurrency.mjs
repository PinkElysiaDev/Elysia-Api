export async function mapConcurrent(items, concurrency, fn) {
  let cursor = 0
  const values = new Array(items.length)
  const workers = await Promise.allSettled(Array.from({ length: Math.min(concurrency, items.length) }, async () => {
    while (cursor < items.length) {
      const index = cursor++
      values[index] = await fn(items[index], index)
    }
  }))
  // Wait for all active work before a caller closes the backend or finalizes reports.
  for (const worker of workers) if (worker.status === 'rejected') throw worker.reason
  return values
}

export function serialize(fn) {
  let pending = Promise.resolve()
  return (...args) => {
    const result = pending.then(() => fn(...args))
    pending = result.catch(() => {})
    return result
  }
}
