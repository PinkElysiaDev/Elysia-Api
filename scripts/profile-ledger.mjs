import { execFileSync } from 'node:child_process'
import { readFile, writeFile, mkdir } from 'node:fs/promises'
import { resolve, join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

// Instrument a disposable source snapshot, never the working production code.
// Byte counters describe presented/scanned spans; they are not heap allocations.
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const directory = resolve(process.argv[2] || join(root, '.tmp-dev/conversion-ledger'))
await mkdir(directory, { recursive: true })
execFileSync('git', ['archive', '--format=tar', `--output=${join(directory, 'source.tar')}`, '31ea3a9', 'backend'], { cwd: root, windowsHide: true })
execFileSync('tar', ['-xf', join(directory, 'source.tar'), '-C', directory], { windowsHide: true })
const changes = []
async function replace(file, from, to) {
  const path = join(directory, 'backend/protocol', file)
  const text = await readFile(path, 'utf8')
  if (text.split(from).length !== 2) throw new Error(`Expected exactly one instrumentation site: ${file}: ${from}`)
  await writeFile(path, text.replace(from, to))
  changes.push({ file, from, to })
}
await writeFile(join(directory, 'backend/protocol/diagnostic_ledger.go'), `package protocol
import "runtime"

type DiagnosticMetric struct { Calls, Bytes int64 }
type DiagnosticContainer struct { Bytes int; Stack []string }
var diagnosticStage, diagnosticRule string
var diagnosticCounts map[string]DiagnosticMetric
var diagnosticContainers []DiagnosticContainer
func DiagnosticStart(stage string) { diagnosticStage=stage; diagnosticRule=""; diagnosticCounts=map[string]DiagnosticMetric{};diagnosticContainers=nil }
func DiagnosticRead() map[string]DiagnosticMetric { result:=diagnosticCounts; diagnosticCounts=nil; return result }
func DiagnosticReadContainers() []DiagnosticContainer { return diagnosticContainers }
func diagnosticCount(operation string, bytes int) {
 if diagnosticCounts==nil { return }
 if operation=="container.output" && bytes>1<<20 {
  pcs:=make([]uintptr,20);n:=runtime.Callers(2,pcs);frames:=runtime.CallersFrames(pcs[:n]);record:=DiagnosticContainer{Bytes:bytes}
  for {frame,more:=frames.Next();record.Stack=append(record.Stack,frame.Function);if !more {break}}
  diagnosticContainers=append(diagnosticContainers,record)
 }
 key:=diagnosticStage+"/"+diagnosticRule+"/"+operation
 entry:=diagnosticCounts[key]; entry.Calls++; entry.Bytes+=int64(bytes); diagnosticCounts[key]=entry
}
`)
await replace('typed_limits.go', 'func (meter *typedMeter) scanRaw(raw string) bool {', 'func (meter *typedMeter) scanRaw(raw string) bool {\n diagnosticCount("scanRaw.input",len(raw))')
await replace('typed_limits.go', '\t\t\tlength := end - offset', '\t\t\tlength := end - offset\n diagnosticCount("scanRaw.stringSecondPass",end-offset-2)')
await replace('typed_limits.go', '\tentry := meter.cache.entries[value]', '\tdiagnosticCount("cache.metricLookupKey",len(raw))\n entry := meter.cache.entries[value]\n if entry.hasMetrics { diagnosticCount("cache.metricHit",len(raw)) }')
await replace('typed_limits.go', 'func (meter *typedMeter) visit(plan *typedPlan, value reflect.Value) bool {', 'func (meter *typedMeter) visit(plan *typedPlan, value reflect.Value) bool {\n diagnosticCount("meter.visit",0)')
await replace('semantic_cache.go', '\tentry := cache.entries[value]', '\tdiagnosticCount("cache.normalizeLookupKey",len(value.raw))\n entry := cache.entries[value]\n if entry.hasNormalized { diagnosticCount("cache.normalizeHit",len(value.raw)) }')
await replace('semantic_cache.go', '\tprevious, exists := cache.entries[value]', '\tdiagnosticCount("cache.storeLookupKey",len(value.raw))\n previous, exists := cache.entries[value]')
await replace('semantic_cache.go', '\tcache.entries[value] = entry', '\tdiagnosticCount("cache.insertKey",len(value.raw))\n cache.entries[value] = entry')
await replace('conversion_state.go', 'func needsSemanticCompaction(raw string) bool {', 'func needsSemanticCompaction(raw string) bool {\n diagnosticCount("compaction.input",len(raw))')
await replace('conversion_state.go', 'func (state *conversionState) normalize(value reflect.Value, plan *typedPlan) {', 'func (state *conversionState) normalize(value reflect.Value, plan *typedPlan) {\n diagnosticCount("normalize.visit",0)')
await replace('conversion_state.go', 'func cloneSemantic(value reflect.Value, plan *typedPlan) reflect.Value {', 'func cloneSemantic(value reflect.Value, plan *typedPlan) reflect.Value {\n diagnosticCount("clone.visit",0)')
await replace('conversion_state.go', '\tisNodeRule := rule.Action', '\tpreviousRule:=diagnosticRule; diagnosticRule=rule.ID; defer func(){diagnosticRule=previousRule}()\n diagnosticCount("rule.apply",0)\n isNodeRule := rule.Action')
await replace('conversion_state.go', '\tvalue, err := encodeSemanticWithin(state.document, state.limits)', '\tdiagnosticCount("state.flushEncode",0)\n value, err := encodeSemanticWithin(state.document, state.limits)')
await replace('value_container.go', '\t\t\treturn offset + 1\n\t\t}\n\t}\n\treturn len(raw)\n}', '\t\t\tdiagnosticCount("jsonStringEnd.span",offset+1-start)\n return offset + 1\n\t\t}\n\t}\n diagnosticCount("jsonStringEnd.span",len(raw)-start)\n\treturn len(raw)\n}')
await replace('value_encode.go', '\treturn Value{raw: output.String()}, true', '\tdiagnosticCount("container.output",length)\n return Value{raw: output.String()}, true')
await replace('value.go', 'func (value Value) Bytes() []byte { return []byte(value.raw) }', 'func (value Value) Bytes() []byte { diagnosticCount("Value.Bytes.requested",len(value.raw)); return []byte(value.raw) }')
await replace('value.go', '\traw, err := json.Marshal(value)', '\tdiagnosticCount("genericMarshal.call",0)\n raw, err := json.Marshal(value)')
await replace('value_reader.go', '\treturn strings.Clone(raw)', '\tdiagnosticCount("view.copy",len(raw))\n return strings.Clone(raw)')
await replace('resource_budget.go', 'func checkSemanticValue(value any, limits Limits) error {', 'func checkSemanticValue(value any, limits Limits) error {\n diagnosticCount("checkSemanticValue.call",0)')
await replace('typed_module.go', 'func checkMappingSemantic(value any, limits Limits, domain string) error {', 'func checkMappingSemantic(value any, limits Limits, domain string) error {\n diagnosticCount("checkMappingSemantic.call",0)')
await replace('runtime.go', '\toptions.ResolveRequestScope = nil', '\tdiagnosticCount("native.preserve",0)\n options.ResolveRequestScope = nil')
await replace('runtime.go', '\tvar before Value\n', '\tdiagnosticCount("native.baselineEncode",0)\n var before Value\n')
await replace('native_reconcile.go', '\tstate := nativeReconciler{ctx:', '\tdiagnosticCount("native.reconcile",len(original.raw)+len(before.raw)+len(after.raw))\n state := nativeReconciler{ctx:')
await writeFile(join(directory, 'backend/protocol/builtin/diagnostic_ledger_test.go'), `package builtin
import ("encoding/json"; "os"; "strconv"; "testing"; p "github.com/elysia-api/backend/protocol")
func TestDiagnosticLedger(t *testing.T) {
 size,_:=strconv.Atoi(os.Getenv("ELYSIA_LEDGER_MIB")); if size==0 {size=2}
 compiled:=shippedProjectionProtocol(t,Anthropic)
 conversion,err:=p.ResolveConversion(p.DefaultConversionPolicy(compiled,compiled)); if err!=nil {t.Fatal(err)}
 result:=map[string]any{};containers:=map[string]any{}
 for _,changed:=range []bool{false,true} {
 body:=largeAnthropicRequest(size<<20,0)
 p.DiagnosticStart("decode")
 request,err:=compiled.DecodeRequest(t.Context(),body,p.EvaluationContext{}); if err!=nil {t.Fatal(err)}
 decode:=p.DiagnosticRead();decodeContainers:=p.DiagnosticReadContainers()
 p.DiagnosticStart("convert")
 request,err=conversion.Request(t.Context(),request,p.ConversionContext{Source:compiled.Identity(),Target:compiled.Identity()},nil); if err!=nil {t.Fatal(err)}
 convert:=p.DiagnosticRead();convertContainers:=p.DiagnosticReadContainers()
 if changed { request.Model=p.StringValue("actual-model") }
 p.DiagnosticStart("encode")
 wire,err:=compiled.EncodeRequest(t.Context(),request,p.EvaluationContext{}); if err!=nil || len(wire)<size<<20 {t.Fatal(err)}
 encode:=p.DiagnosticRead();encodeContainers:=p.DiagnosticReadContainers()
 value,err:=p.ParseValue(wire);if err!=nil{t.Fatal(err)}
 p.DiagnosticStart("validateWire")
 if err:=compiled.ValidateWireOutput(p.EncodeRequest,value);err!=nil{t.Fatal(err)}
 validation:=p.DiagnosticRead();validationContainers:=p.DiagnosticReadContainers()
 result[strconv.FormatBool(changed)]=map[string]any{"decode":decode,"convert":convert,"encode":encode,"validateWire":validation}
 containers[strconv.FormatBool(changed)]=map[string]any{"decode":decodeContainers,"convert":convertContainers,"encode":encodeContainers,"validateWire":validationContainers}
 }
 raw,_:=json.MarshalIndent(result,"","  "); if err:=os.WriteFile(os.Getenv("ELYSIA_LEDGER_OUTPUT"),raw,0600);err!=nil {t.Fatal(err)}
 raw,_=json.MarshalIndent(containers,"","  ");if err:=os.WriteFile(os.Getenv("ELYSIA_LEDGER_OUTPUT")+".containers.json",raw,0600);err!=nil {t.Fatal(err)}
}
`)
await writeFile(join(directory, 'instrumentation.json'), JSON.stringify({ baseline: '31ea3a9', changes, limitations: 'Single-thread diagnostic only. Input/lookup spans may overlap and are not unique bytes, physical reads or actual heap allocations. Never use instrumented timings for acceptance.' }, null, 2))
console.log(directory)
