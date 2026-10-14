import assert from 'node:assert/strict'

const blocked = message => Object.assign(new Error(message), { code: 'blocked' })

/** Runs configuration checks independently of provider generation availability. */
export async function checkPersistence({ run, needRoute, admin, call, recovery, restart, runtimeEvidence, id, source, token, checkTokenReply }) {
  const readState = async () => {
    const source = (await admin('model-sources')).items.find(item => item.id === id)
    const group = (await admin('model-groups')).items.find(item => item.id === id)
    const token = (await admin('api-tokens')).items.find(item => item.name === 'audit-relay')
    assert.ok(source && group && token, '持久化准备对象缺失')
    const select = (value, keys) => Object.fromEntries(keys.filter(key => Object.hasOwn(value, key)).map(key => [key, value[key]]))
    return {
      source: select(source, ['id', 'name', 'enabled', 'baseUrl', 'platform', 'autoFetchModels', 'manualModels']),
      group: select(group, ['id', 'name', 'enabled', 'models', 'strategy', 'maxRetries']),
      token: select(token, ['name', 'enabled', 'allowedGroups', 'scopes']),
    }
  }
  await run('call', '原有配置的生成调用单独验收', async () => { needRoute(); await recovery() })
  await run('save-config', '配置修改保存且读取一致', async () => {
    needRoute()
    await admin(`model-sources/${id}`, { ...source, name: 'Updated audit source' }, 'PUT')
    assert.equal((await admin('model-sources')).items.find(item => item.id === id)?.name, 'Updated audit source')
  })
  await run('disable', '禁用源后状态持久化且调用被拒绝', async () => {
    needRoute()
    await admin(`model-sources/${id}/enabled`, { enabled: false }, 'PATCH')
    assert.equal((await admin('model-sources')).items.find(item => item.id === id)?.enabled, false)
    await call({ expected: [400, 404, 503] })
  })
  const enabled = await run('enable', '重新启用源并核对配置', async () => {
    needRoute()
    await admin(`model-sources/${id}/enabled`, { enabled: true }, 'PATCH')
    assert.equal((await admin('model-sources')).items.find(item => item.id === id)?.enabled, true)
  })
  await run('enable-recovery', '启用后的生成调用单独验收', async () => { needRoute(); if (!enabled) throw blocked('依赖 enable 成功'); await recovery() })
  const restarted = await run('restart-state', '重启前后源、模型组和令牌配置一致，不依赖生成成功', async () => {
    needRoute()
    const before = await readState()
    await restart()
    await runtimeEvidence('restart')
    assert.deepEqual(await readState(), before)
  })
  await run('restart-recovery', '重启后的生成调用单独验收', async () => { needRoute(); if (!restarted) throw blocked('依赖 restart-state 成功'); await recovery() })
  const created = await run('create-token', '创建可撤销令牌并确认持久记录', async () => {
    needRoute()
    await admin('api-tokens', { name: `revoke-${id}`, token, enabled: true, allowedGroups: [id], scopes: [] })
    assert.ok((await admin('api-tokens')).items.some(item => item.name === `revoke-${id}` && item.enabled))
  })
  await run('token-call', '撤销前生成调用单独验收', async () => { needRoute(); if (!created) throw blocked('依赖 create-token 成功'); await checkTokenReply(await call({ token })) })
  const revoked = await run('revoke-token', '删除令牌且持久记录消失，不依赖生成成功', async () => {
    needRoute(); if (!created) throw blocked('依赖 create-token 成功')
    await admin(`api-tokens/revoke-${id}`, undefined, 'DELETE')
    assert.ok(!(await admin('api-tokens')).items.some(item => item.name === `revoke-${id}`))
  })
  await run('revoked-token-rejection', '已删除令牌被拒绝', async () => { needRoute(); if (!revoked) throw blocked('依赖 revoke-token 成功'); await call({ token, expected: 401 }) })
  await run('revoke-recovery', '仍有效令牌的生成调用单独验收', async () => { needRoute(); await recovery() })
}
