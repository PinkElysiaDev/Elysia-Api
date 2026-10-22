import { execFileSync, spawn } from 'node:child_process'
import { mkdir, readFile, writeFile, readdir } from 'node:fs/promises'
import { resolve, join } from 'node:path'
import { createHash } from 'node:crypto'

// Single-thread explanatory experiment, never used for formal timing. These
// counters measure logical spans presented to operations, not RAM bus traffic.
const repo = resolve('.'), directory = resolve(process.argv[2] || '.tmp-dev/performance-review-2026-10-22/ledger')
await mkdir(directory, { recursive: true })
if ((await readdir(directory)).length) throw new Error('Use a new empty ledger directory; prior instrumentation evidence must be preserved')
execFileSync('git', ['archive', '--format=tar', `--output=${join(directory, 'source.tar')}`, '760b54af', 'backend'], { windowsHide: true })
execFileSync('tar', ['-xf', join(directory, 'source.tar'), '-C', directory], { windowsHide: true })
const changes = []
async function replace(file, from, to) {
  const path = join(directory, 'backend/protocol', file), raw = await readFile(path, 'utf8')
  if (raw.split(from).length !== 2) throw new Error(`Instrumentation site not unique: ${file}: ${from}`)
  await writeFile(path, raw.replace(from, to)); changes.push({ file, from, to })
}
await writeFile(join(directory, 'backend/protocol/review_ledger.go'), `package protocol
type ReviewMetric struct { Calls, Bytes int64 }
var reviewCounts map[string]ReviewMetric
func ReviewStart() { reviewCounts=map[string]ReviewMetric{} }
func ReviewRead() map[string]ReviewMetric { out:=reviewCounts;reviewCounts=nil;return out }
func reviewCount(name string, bytes int) { if reviewCounts==nil{return}; n:=reviewCounts[name];n.Calls++;n.Bytes+=int64(bytes);reviewCounts[name]=n }
`)
await replace('typed_limits.go', 'func (meter *typedMeter) raw(raw string) bool {', 'func (meter *typedMeter) raw(raw string) bool {\nreviewCount("meter.presented",len(raw))')
await replace('typed_limits.go', 'func (meter *typedMeter) scanRaw(raw string) bool {', 'func (meter *typedMeter) scanRaw(raw string) bool {\nreviewCount("meter.scanned",len(raw))')
await replace('typed_limits.go', '\t\t\ttext := raw[offset+1 : end-1]', '\t\t\ttext := raw[offset+1 : end-1]\nreviewCount("meter.string.extraPassSpan",len(text))')
await replace('typed_limits.go', '\tentry, _ := meter.cache.lookup(value)', '\tentry, _ := meter.cache.lookup(value)\nif entry.hasMetrics { reviewCount("meter.cachedSpan",len(raw)) }')
await replace('semantic_cache.go', 'func (cache *semanticCache) lookup(value Value) (semanticValueEntry, bool) {', 'func (cache *semanticCache) lookup(value Value) (semanticValueEntry, bool) {\nreviewCount("cache.identityLookup",len(value.raw))')
await replace('semantic_document.go', 'func needsSemanticCompaction(raw string) bool {', 'func needsSemanticCompaction(raw string) bool {\nreviewCount("compaction.presented",len(raw))')
await replace('semantic_document.go', 'func (cache *semanticCache) normalizeDocument(value reflect.Value, plan *typedPlan) {', 'func (cache *semanticCache) normalizeDocument(value reflect.Value, plan *typedPlan) {\nreviewCount("normalize.visit",0)')
await replace('semantic_document.go', 'func cloneSemantic(value reflect.Value, plan *typedPlan) reflect.Value {', 'func cloneSemantic(value reflect.Value, plan *typedPlan) reflect.Value {\nreviewCount("clone.visit",0)')
await replace('value_container.go', '\t\t\treturn quote + 1', '\t\t\treviewCount("stringEnd.traversed",quote+1-start)\nreturn quote + 1')
await replace('value_encode.go', '\treturn Value{raw: output.String()}, true', '\treviewCount("container.materialized",length)\nreturn Value{raw: output.String()}, true')
await replace('json_output.go', '\treturn Value{raw: builder.String()}, nil', '\treviewCount("plannedContainer.materialized",parts.bytes)\nreturn Value{raw: builder.String()}, nil')
await replace('value.go', 'func (value Value) Bytes() []byte { return []byte(value.raw) }', 'func (value Value) Bytes() []byte { reviewCount("Value.Bytes.requested",len(value.raw));return []byte(value.raw) }')
await replace('value.go', '\traw, err := sonic.ConfigStd.MarshalToString(value)', '\traw, err := sonic.ConfigStd.MarshalToString(value)\nreviewCount("genericMarshal.output",len(raw))')
await replace('value_reader.go', '\treturn strings.Clone(raw)', '\treviewCount("reader.clone",len(raw))\nreturn strings.Clone(raw)')
await replace('runtime.go', '\tvar before Value', '\treviewCount("native.baselineEncode",0)\nvar before Value')
await replace('native_reconcile.go', 'func reconcileNative(ctx context.Context, original, before, after Value, policy NativePolicy, limits Limits) (Value, error) {', 'func reconcileNative(ctx context.Context, original, before, after Value, policy NativePolicy, limits Limits) (Value, error) {\nreviewCount("native.reconcile.presented",len(original.raw)+len(before.raw)+len(after.raw))')
await writeFile(join(directory, 'backend/protocol/builtin/review_ledger_test.go'), `package builtin
import ("encoding/json";"os";"strconv";"testing";p "github.com/elysia-api/backend/protocol")
func TestReviewLedger(t *testing.T) {
 size,_:=strconv.Atoi(os.Getenv("ELYSIA_LEDGER_MIB"));if size==0{size=2}
 compiled:=shippedProjectionProtocol(t,Anthropic);conversion,err:=p.ResolveConversion(p.DefaultConversionPolicy(compiled,compiled));if err!=nil{t.Fatal(err)}
 all:=map[string]any{}
 for _,changed:=range []bool{false,true} {
 body:=largeAnthropicRequest(size<<20,0);result:=map[string]any{}
 p.ReviewStart(); request,err:=compiled.DecodeRequest(t.Context(),body,p.EvaluationContext{});if err!=nil{t.Fatal(err)};result["decode"]=p.ReviewRead()
 p.ReviewStart(); request,err=conversion.Request(t.Context(),request,p.ConversionContext{Source:compiled.Identity(),Target:compiled.Identity()},nil);if err!=nil{t.Fatal(err)};result["convert"]=p.ReviewRead()
 if changed{request.Model=p.StringValue("actual-model")}
 p.ReviewStart(); wire,err:=compiled.EncodeRequest(t.Context(),request,p.EvaluationContext{});if err!=nil||len(wire)<size<<20{t.Fatal(err)};result["encode"]=p.ReviewRead()
 value,err:=p.ParseValue(wire);if err!=nil{t.Fatal(err)}
 p.ReviewStart();if err:=compiled.ValidateWireOutput(p.EncodeRequest,value);err!=nil{t.Fatal(err)};result["validateWire"]=p.ReviewRead()
 all[strconv.FormatBool(changed)]=result
 }
 raw,_:=json.MarshalIndent(all,"","  ");if err:=os.WriteFile(os.Getenv("ELYSIA_LEDGER_OUTPUT"),raw,0600);err!=nil{t.Fatal(err)}
}
`)
await writeFile(join(directory, 'instrumentation.json'), JSON.stringify({ revision: '760b54af', changes, limits: 'Single-thread synthetic request; stages exclude routing, transport and logging. Overlapping spans are counted each occurrence. Identity lookup bytes describe referenced span, not bytes hashed. Six extra string passes are code-inferred (five Count plus UTF8 validation); not hardware reads.' }, null, 2))
console.log(directory)
if (process.argv.includes('--run')) {
  const steps = [], env = { ...process.env, GOMAXPROCS: '4', GOGC: '100', GOMEMLIMIT: '8GiB', CGO_ENABLED: '0' }
  for (const key of Object.keys(env)) if (/^ELYSIA_/.test(key)) delete env[key]
  async function run(name, command, args, extra = {}) {
    const started = Date.now(), chunks = []
    console.log(new Date().toISOString(), name)
    const child = spawn(command, args, { cwd: join(directory, 'backend'), env: { ...env, ...extra }, windowsHide: true, stdio: ['ignore','pipe','pipe'] })
    child.stdout.on('data', b => chunks.push(b)); child.stderr.on('data', b => chunks.push(b))
    const code = await new Promise((r,j) => { child.once('close',r); child.once('error',j) })
    await writeFile(join(directory,name+'.log'), Buffer.concat(chunks))
    steps.push({ name, command, args, environment: extra, code, elapsedMS: Date.now()-started })
    await writeFile(join(directory,'steps.json'), JSON.stringify(steps,null,2))
    if (code) throw new Error(`Ledger experiment failed: ${name}; see preserved log`)
  }
  const binary = join(directory,'ledger.test.exe')
  await run('build','go',['test','-pgo=off','-c','-o',binary,'./protocol/builtin'])
  await writeFile(join(directory,'binary.json'),JSON.stringify({ binary, sha256:createHash('sha256').update(await readFile(binary)).digest('hex'), revision:'760b54af', instrumented:true },null,2))
  for (const size of [2,31]) await run(`ledger-${size}`,binary,['-test.run=^TestReviewLedger$','-test.v','-test.timeout=5m'],{ ELYSIA_LEDGER_MIB:String(size), ELYSIA_LEDGER_OUTPUT:join(directory,`ledger-${size}.json`) })
}
