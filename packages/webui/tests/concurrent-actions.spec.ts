import { expect, test } from '@playwright/test'

for (const isFailure of [false, true]) {
  test(`concurrent actions retain independent busy state after ${isFailure ? 'failure' : 'success'}`, async ({ page }) => {
    const groups = ['first', 'second'].map((id) => ({ id, name: id, models: [], enabled: true, strategy: 'round-robin' }))
    const releases: Record<string, () => void> = {}
    const pending = Object.fromEntries(groups.map(({ id }) => [id, new Promise<void>((resolve) => { releases[id] = resolve })]))
    const attempts: Record<string, number> = {}
    await page.addInitScript(() => localStorage.setItem('elysia-webui.panel-token', 'test-token'))
    await page.route('**/api/admin/**', async (route) => {
      const path = new URL(route.request().url()).pathname
      if (path.includes('/model-groups/') && route.request().method() === 'PUT') {
        const id = path.split('/').at(-1)!
        attempts[id] = (attempts[id] ?? 0) + 1
        await pending[id]
        if (id === 'second' && isFailure) {
          await route.fulfill({ status: 400, json: { ok: false, error: { message: 'simulated failure' } } })
          return
        }
        groups.find((group) => group.id === id)!.enabled = false
      }
      await route.fulfill({ json: { ok: true, data: { items: path.endsWith('/model-groups') ? groups : [] } } })
    })
    await page.goto('/#/groups')
    const first = page.getByRole('row').filter({ hasText: 'first' }).getByRole('switch')
    const second = page.getByRole('row').filter({ hasText: 'second' }).getByRole('switch')
    await first.click()
    await second.click()
    await expect(first).toBeDisabled()
    await expect(second).toBeDisabled()
    await expect.poll(() => attempts).toEqual({ first: 1, second: 1 })
    releases.second()
    await expect(second).toBeEnabled()
    await expect(first).toBeDisabled()
    releases.first()
    await expect(first).toBeEnabled()
    await expect(first).toHaveAttribute('aria-checked', 'false')
    await expect(second).toHaveAttribute('aria-checked', String(isFailure))
  })
}
