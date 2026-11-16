import assert from 'node:assert/strict'
import test from 'node:test'
import { buildBackendTestPlan } from './backend.mjs'

test('focused checks run each layer once and exclude unrelated gateway matrices', () => {
  for (const [suite, builtinTest, gatewayTest] of [
    ['output', 'TestOutputConstraintDirections', 'TestGatewayOutputConstraintDirections'],
    ['gemini-stream', 'TestGeminiPackageStreamToolArgumentUpdates', 'TestGatewayParallelGeminiToolSignatures'],
  ]) {
    const plan = buildBackendTestPlan([`--suite=${suite}`, '--jobs=2', '--race'], {})
    assert.equal(plan.tasks.length, 2)
    assert.deepEqual(plan.tasks.map(task => task.args.at(-1)), ['./protocol/builtin', './server'])
    for (const [index, task] of plan.tasks.entries()) {
      assert.equal(task.args.filter(arg => arg === '-race').length, 1)
      assert.equal(task.args[task.args.indexOf('-p') + 1], '1')
      assert.equal(task.options.env.GOMAXPROCS, '2')
      assert.equal(task.options.env.CGO_ENABLED, '1')
      const selection = new RegExp(task.args[task.args.indexOf('-run') + 1])
      assert(selection.test([builtinTest, gatewayTest][index]))
      assert(!selection.test('TestGatewayUsageBodyPolicy'))
    }
  }
})

test('full checks include fuzz seeds without a duplicate focused pass', () => {
  const plan = buildBackendTestPlan(['--suite=all', '--jobs=3'], { GOFLAGS: '-run=^TestOne$ -skip=Fuzz' })
  assert.equal(plan.tasks.length, 1)
  const [task] = plan.tasks
  assert.equal(task.args.at(-1), './...')
  assert.equal(task.args[task.args.indexOf('-p') + 1], '3')
  assert(!task.args.includes('-run'))
  assert(!task.args.includes('-race'))
  assert.equal(task.options.env.CGO_ENABLED, '0')
  assert.equal(task.options.env.GOFLAGS, '')
})

test('invalid selection cannot silently run a smaller or unbounded suite', () => {
  for (const args of [['--suite=unknown'], ['--jobs=0'], ['--jobs=5'], ['--jobs=1.5'], ['--jobs=no'], ['--typo']]) {
    assert.throws(() => buildBackendTestPlan(args, {}))
  }
})
