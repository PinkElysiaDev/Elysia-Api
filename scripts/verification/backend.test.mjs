import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import test from 'node:test'
import { buildBackendTestPlan, serverRaceShards, serverTestShards, serverShardMatches } from './backend.mjs'

const packages = ['.', './agent', './config', './internal/architecture', './protocol', './protocol/builtin', './relay', './server', './storage', './webui']

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
      assert.equal(task.args[task.args.indexOf('-p') + 1], '2')
      assert.equal(task.options.env.GOMAXPROCS, '2')
      assert.equal(task.options.env.GOMEMLIMIT, '2GiB')
      assert.equal(task.options.env.GOFLAGS, '-p=2')
      assert.equal(task.options.env.CGO_ENABLED, '1')
      assert(task.args.includes('-parallel=2'))
      assert(task.args.includes('-timeout=15m'))
      assert.equal(task.options.timeoutMillis, 16 * 60 * 1000)
      const selection = new RegExp(task.args[task.args.indexOf('-run') + 1])
      assert(selection.test([builtinTest, gatewayTest][index]))
      assert(!selection.test('TestGatewayUsageBodyPolicy'))
    }
  }
})

test('fast tier excludes server and full tier covers each package including fuzz seeds once', () => {
  const fast = buildBackendTestPlan(['--suite=fast'], {}, packages)
  assert.equal(fast.tasks.length, 1)
  assert(!fast.tasks[0].args.includes('./server'))
  const plan = buildBackendTestPlan(['--suite=all', '--jobs=3'], { GOFLAGS: '-run=^TestOne$ -skip=Fuzz' }, packages)
  assert.equal(plan.tasks.length, packages.length - 1 + serverTestShards.length)
  const nonServer = plan.tasks.filter(task => !task.args.includes('./server'))
  assert.deepEqual(nonServer.map(task => task.args.at(-1)).sort(), packages.filter(pkg => pkg !== './server').sort())
  assert(nonServer.every(task => !task.args.includes('-run') && !task.args.includes('-skip') && !task.exclusive && !task.slots && task.allowNoTests))
  assert.equal(buildBackendTestPlan(['--suite=all'], {}, packages).jobs, 4)
  const server = plan.tasks.filter(task => task.args.includes('./server'))
  assert(server.every(task => task.args.at(-1) === './server' && !task.group && !task.exclusive))
  for (const task of plan.tasks) {
    assert.equal(task.args[task.args.indexOf('-p') + 1], '1')
    assert(!task.args.includes('-race'))
    assert.equal(task.options.env.CGO_ENABLED, '0')
    assert.equal(task.options.env.GOFLAGS, '-p=1')
    assert.equal(task.options.env.GOMEMLIMIT, '1GiB')
  }
  const directory = new URL('../../backend/server/', import.meta.url)
  const names = readdirSync(directory).filter(name => name.endsWith('_test.go')).flatMap(file =>
    [...readFileSync(new URL(file, directory), 'utf8').matchAll(/^func ((?:Test|Fuzz)\w+|Example\w*)\((?:\w+ \*testing\.[TF])?\)/gm)].map(match => match[1]))
  names.push('TestFutureFeature', 'FuzzFutureFeature', 'ExampleFutureFeature')
  assert(names.length > 600)
  for (const name of names) {
    assert.equal(serverTestShards.filter(shard => serverShardMatches(shard, name)).length, 1, name)
    assert.equal(server.filter(task => serverShardMatches({
      run: task.args.includes('-run') ? task.args[task.args.indexOf('-run') + 1] : undefined,
      skip: task.args.includes('-skip') ? task.args[task.args.indexOf('-skip') + 1] : undefined,
    }, name)).length, 1, name)
  }
})

test('race full tier shards every non-server package and partitions all server tests', () => {
  const plan = buildBackendTestPlan(['--suite=all', '--race'], {}, packages)
  const nonServer = plan.tasks.filter(task => !task.args.includes('./server'))
  assert.deepEqual(nonServer.map(task => task.args.at(-1)), packages.filter(pkg => pkg !== './server'))
  const server = plan.tasks.filter(task => task.args.includes('./server'))
  assert.equal(server.length, serverRaceShards.length)
  assert(server.every(task => task.group === 'server' && task.exclusive))
  assert(plan.tasks.filter(task => ['./protocol', './protocol/builtin'].includes(task.args.at(-1))).every(task => task.exclusive))
  assert(plan.tasks.every(task => task.args.includes('-race') && !task.args.includes('./...')))
  // Go test -list lists these top-level declarations (not subtests). Read the
  // current source rather than a hand-maintained list; future names also enter
  // the complement automatically. Fuzz seeds must be selected exactly once.
  const directory = new URL('../../backend/server/', import.meta.url)
  const names = readdirSync(directory).filter(name => name.endsWith('_test.go')).flatMap(file =>
    [...readFileSync(new URL(file, directory), 'utf8').matchAll(/^func ((?:Test|Fuzz)\w+|Example\w*)\((?:\w+ \*testing\.[TF])?\)/gm)].map(match => match[1]))
  names.push('TestFutureFeature', 'FuzzFutureFeature', 'ExampleFutureFeature')
  assert(names.length > 600)
  for (const name of names) {
    assert.equal(serverRaceShards.filter(shard => serverShardMatches(shard, name)).length, 1, name)
    assert.equal(server.filter(task => serverShardMatches({
      run: task.args.includes('-run') ? task.args[task.args.indexOf('-run') + 1] : undefined,
      skip: task.args.includes('-skip') ? task.args[task.args.indexOf('-skip') + 1] : undefined,
    }, name)).length, 1, name)
  }
})

test('invalid selection cannot silently run a smaller or unbounded suite', () => {
  for (const args of [['--suite=unknown'], ['--jobs=0'], ['--jobs=5'], ['--jobs=1.5'], ['--jobs=no'], ['--typo']]) {
    assert.throws(() => buildBackendTestPlan(args, {}, packages))
  }
  assert.throws(() => buildBackendTestPlan(['--suite=fast'], {}, []))
})
