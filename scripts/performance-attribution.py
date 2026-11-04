"""Build a diagnostic-only counter overlay over the pinned current source."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parent.parent
BASE = ROOT / '.tmp-dev/three-version-2026-11-04'
SOURCE = BASE / 'C/source/backend'
OUTPUT = BASE / 'attribution-build'


def main():
    global SOURCE, OUTPUT
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stage', help='Frozen allocation candidate; omitted uses the original comparison')
    args = parser.parse_args()
    identity = 'd7e29817'
    if args.stage:
        if not re.fullmatch(r'[a-z0-9-]+', args.stage):
            parser.error('stage must be a lowercase identifier')
        stage = ROOT / '.tmp-dev/allocation-optimization-2026-11-04' / args.stage
        build = json.loads((stage / 'builds.json').read_text(encoding='utf-8'))['versions']['C']
        SOURCE = Path(build['source']) / 'backend'
        OUTPUT = stage / 'attribution-build'
        identity = build['commit']
    OUTPUT.mkdir(exist_ok=True)
    labels = []

    def count(label, amount='1'):
        if label not in labels:
            labels.append(label)
        return f'allocationProbe({labels.index(label)}, uint64({amount}))'

    changes = {}

    def edit(name, transform):
        source = SOURCE / name
        original = source.read_text(encoding='utf-8')
        text = transform(original)
        if text == original:
            raise RuntimeError('counter insertion did not match: ' + name)
        target = OUTPUT / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text, encoding='utf-8')
        changes[str(source)] = str(target)

    def insert_entry(text, name, statement):
        pattern = r'(func (?:\([^\n]*\) )?' + re.escape(name) + r'\([^\n]*\)[^\n]*\{\n)'
        text, matches = re.subn(pattern, lambda match: match[1] + '\t' + statement + '\n', text)
        if matches != 1:
            raise RuntimeError(f'expected one function {name}, got {matches}')
        return text

    def generated(text):
        def entry(match):
            name = match[2]
            statement = count('core.' + name + '.calls')
            if 'pointer []string' in match[1]:
                statement += '; if len(pointer) == 0 { ' + count('core.' + name + '.whole') + ' }'
            return match[1] + '\t' + statement + '\n'
        text = re.sub(r'(func (readCore\w+)\([^\n]*\)[^\n]*\{\n)', entry, text)
        return text
    edit('protocol/plan_core_generated.go', generated)
    edit('protocol/plan_core.go', lambda text: insert_entry(text, 'finishCoreOutput',
        count('core.containers') + '; ' + count('core.field-capacity', 'cap(node.fields)') + '; ' +
        count('core.fields-used', 'len(node.fields)') + '; ' + count('core.item-capacity', 'cap(node.items)') + '; ' + count('core.items-used', 'len(node.items)')))
    edit('protocol/value_container.go', lambda text: insert_entry(text, 'readObjectFieldsWithin', count('raw.field-tables')).replace(
        'fields[key] = Value{raw: reader.fragment(raw[offset:end])}',
        count('raw.fields-visited') + '\n\t\tfields[key] = Value{raw: reader.fragment(raw[offset:end])}'))
    edit('protocol/value_reader.go', lambda text: text.replace('return strings.Clone(raw)',
        count('raw.detached-fragments') + '\n\t' + count('raw.detached-bytes', 'len(raw)') + '\n\treturn strings.Clone(raw)'))
    def field_views(text):
        text = insert_entry(text, 'readFields', count('scoped.field-reads'))
        text = text.replace('type planObjectReader struct {', 'type planObjectReader struct {\n probeSeen map[allocationProbeSpan]bool')
        text = insert_entry(text, 'readFields', 'if allocationProbeEnabled.Load() && reader != nil { reader.probeRead(value) }')
        text = text.replace('return reader.fields, nil', count('scoped.root-hits') + '\n\t\treturn reader.fields, nil')
        return text.replace('return child.fields, nil', count('scoped.child-hits') + '\n\t\t\treturn child.fields, nil')
    edit('protocol/plan_object_reader.go', field_views)
    edit('protocol/plan_native_capture.go', lambda text: insert_entry(text, 'forItem', count('native.child-scopes')))

    def capture_scopes(text, consumer, kind):
        assignment = 'child.capture = state.capture.forItem(plan.source.expression, index)'
        needed = count('native.' + kind + '-needing-capture')
        skipped = count('native.' + kind + '-without-capture')
        if 'if shouldCapture {' in text:
            block = 'if shouldCapture {\n\t\t\t' + assignment + '\n\t\t}'
            if text.count(block) != 1:
                raise RuntimeError('capture qualification boundary changed')
            return text.replace(block, 'if shouldCapture { ' + needed + '; ' + assignment +
                ' } else if state.capture != nil { ' + skipped + ' }')
        return text.replace(assignment, 'if ' + consumer + '.hasCapture || ' + consumer + '.hasSourceCapture { ' +
            needed + ' } else { ' + skipped + ' }\n\t\t\t' + assignment)

    def output_plans(text):
        text = insert_entry(text, 'buildObject', count('plan.objects') + '; ' + count('plan.object-field-capacity', 'len(plan.fields)'))
        text = insert_entry(text, 'buildArray', count('plan.arrays') + '; ' + count('plan.array-capacity', 'len(plan.items)'))
        return capture_scopes(text, 'body', 'iterations')
    edit('protocol/plan_output.go', output_plans)

    def containers(text):
        text = insert_entry(text, 'buildOperandValues', count('plan.operand-slices') + '; ' + count('plan.operand-capacity', 'len(plan.items)'))
        for operation in ['merge', 'concat', 'all', 'any', 'equal', 'integer', 'join', 'merge_usage']:
            text = insert_entry(text, 'buildOperandValuesInto', 'if plan.expression.op == "' + operation + '" { ' +
                count('operands.' + operation + '.calls') + '; ' + count('operands.' + operation + '.arity', 'len(plan.items)') + ' }')
        text = insert_entry(text, 'plannedObjectFields', 'if output.node == nil { ' + count('fields.raw') +
            ' } else if !output.node.hasCanonicalKeys { ' + count('fields.fold') +
            ' } else if !output.node.hasSortedFields { ' + count('fields.sort') + ' } else { ' + count('fields.ready') + ' }')
        return capture_scopes(text, 'plan.when', 'filters')
    edit('protocol/plan_container.go', containers)

    def output_parts(text):
        text = insert_entry(text, 'encodeWithin', count('output.encode-calls'))
        text = insert_entry(text, 'add', count('output.fragments') + '; ' + count('output.fragment-bytes', 'len(raw)') +
            '; if len(parts.parts) == cap(parts.parts) { ' + count('output.parts-growth') + ' }')
        return text
    edit('protocol/json_output.go', output_parts)
    edit('protocol/typed_limits.go', lambda text: insert_entry(text, 'scanRaw',
        count('semantic.raw-scans') + '; ' + count('semantic.raw-scan-bytes', 'len(raw)')))
    edit('protocol/semantic_cache.go', lambda text: insert_entry(text, 'normalize',
        count('semantic.normalize-calls') + '; ' + count('semantic.normalize-input-bytes', 'len(value.raw)') +
        '; if cache == nil { ' + count('semantic.normalize-without-cache') + ' }'))
    edit('protocol/runtime.go', lambda text: insert_entry(text, 'EncodeRequestValue', count('runtime.request-encodes')))
    edit('protocol/continuation.go', lambda text: insert_entry(text, 'ContinuationNodeDigest', count('continuation.node-digests')))
    history_probe = ('func ProbeHistoryDigest(nodes int) { ' + count('continuation.history-digests') + '; ' +
        count('continuation.history-prefix-nodes', 'nodes') + ' }\n')
    history_probe += 'func ProbeHistoryNodeDigest() { ' + count('continuation.history-node-digests') + ' }\n'
    edit('server/continuation.go', lambda text: insert_entry(text, 'historyDigest', 'protocol.ProbeHistoryDigest(len(nodes))').replace(
        'parts = append(parts, role+":"+protocol.ContinuationNodeDigest(n))',
        'protocol.ProbeHistoryNodeDigest()\n\t\t\t\tparts = append(parts, role+":"+protocol.ContinuationNodeDigest(n))'))
    if (SOURCE / 'server/continuation_history.go').exists():
        edit('server/continuation_history.go', lambda text: text.replace('digest := protocol.ContinuationNodeDigest(node)',
            'protocol.ProbeHistoryNodeDigest()\n\t\tdigest := protocol.ContinuationNodeDigest(node)'))
    edit('protocol/package_stream_state.go', lambda text: text.replace('transaction.owner.packageStates[transaction.key] = &packageStreamState{',
        count('stream.committed-state-objects') + '\n\ttransaction.owner.packageStates[transaction.key] = &packageStreamState{'))

    edit('protocol/value.go', lambda text: text.replace('func (value Value) Bytes() []byte {',
        'func (value Value) Bytes() []byte { ' + count('wire.bytes-copies') + '; ' + count('wire.bytes-copied', 'len(value.raw)') + ';'))
    edit('server/gateway_protocol.go', lambda text: text.replace(
        'wireValue, err = candidate.finalizeRequestWire',
        'encodedProbe := protocol.ProbeEncodedWire(wireValue)\n\twireValue, err = candidate.finalizeRequestWire').replace(
        'record.OutgoingBody = record.sanitizeBodyText(wireValue.JSONString())',
        'protocol.ProbeFinalWire(encodedProbe, wireValue)\n\trecord.OutgoingBody = record.sanitizeBodyText(wireValue.JSONString())\n\tprotocol.ProbeLogWire(wireValue, record.OutgoingBody.Content)'))
    wire_probe = '''
func ProbeEncodedWire(value Value) [2]uintptr {
    if !allocationProbeEnabled.Load() { return [2]uintptr{} }
    ENCODE
    return [2]uintptr{uintptr(unsafe.Pointer(unsafe.StringData(value.raw))), uintptr(len(value.raw))}
}
func ProbeFinalWire(before [2]uintptr, value Value) {
    if !allocationProbeEnabled.Load() { return }
    FINAL
    if before == [2]uintptr{uintptr(unsafe.Pointer(unsafe.StringData(value.raw))), uintptr(len(value.raw))} { SAME } else { CHANGED }
}
func ProbeLogWire(value Value, log string) {
    if !allocationProbeEnabled.Load() { return }
    LOG
    if len(log) > 0 && unsafe.StringData(log) == unsafe.StringData(value.raw) { SHARED }
}
'''.replace('ENCODE', count('gateway.encoded-wires') + '; ' + count('gateway.encoded-bytes', 'value.Len()')).replace(
        'FINAL', count('gateway.final-bytes', 'value.Len()')).replace('SAME', count('gateway.final-same-storage')).replace(
        'CHANGED', count('gateway.final-new-storage')).replace('LOG', count('gateway.log-bytes', 'len(log)')).replace('SHARED', count('gateway.log-shares-wire'))
    scope_probe = '''
type allocationProbeSpan struct { address uintptr; bytes int }
func (reader *planObjectReader) probeRead(value Value) {
    if reader.probeSeen == nil { reader.probeSeen = map[allocationProbeSpan]bool{}; SCOPE }
    identity := allocationProbeSpan{uintptr(unsafe.Pointer(unsafe.StringData(value.raw))), len(value.raw)}
    if reader.probeSeen[identity] { REPEAT } else { DISTINCT; reader.probeSeen[identity] = true }
}
'''.replace('SCOPE', count('scoped.active-scopes')).replace('REPEAT', count('scoped.repeated-storage-reads')).replace('DISTINCT',
        count('scoped.distinct-storage-reads') + '; ' + count('scoped.distinct-storage-bytes', 'value.Len()'))

    helper = OUTPUT / 'allocation_probe.go'
    helper.write_text('package protocol\nimport ("sync/atomic"; "unsafe")\n' + scope_probe + wire_probe +
        'var allocationProbeEnabled atomic.Bool\nvar allocationProbeCounts [' + str(len(labels)) + ']atomic.Uint64\n' + history_probe +
        'func allocationProbe(index int, amount uint64) { if allocationProbeEnabled.Load() { allocationProbeCounts[index].Add(amount) } }\n' +
        'func ResetAllocationProbe() { allocationProbeEnabled.Store(false); for index := range allocationProbeCounts { allocationProbeCounts[index].Store(0) }; allocationProbeEnabled.Store(true) }\n' +
        'func ReadAllocationProbe() map[string]uint64 { result := map[string]uint64{}; labels := []string{' +
        ','.join(json.dumps(label) for label in labels) + '}; for index, label := range labels { result[label] = allocationProbeCounts[index].Load() }; return result }\n', encoding='utf-8')
    changes[str(SOURCE / 'protocol/allocation_probe.go')] = str(helper)

    def collector(text):
        text = text.replace('"github.com/elysia-api/backend/config"', '"github.com/elysia-api/backend/config"\n"github.com/elysia-api/backend/protocol"')
        text = text.replace('before = memory', 'before = memory\nprotocol.ResetAllocationProbe()')
        text = text.replace('"Allocated":', '"Attribution": protocol.ReadAllocationProbe(),\n"Allocated":')
        return text
    edit('server/performance_versioned_process_test.go', collector)
    for target in changes.values():
        subprocess.run(['gofmt', '-w', target], check=True)
    (OUTPUT / 'overlay.json').write_text(json.dumps({'Replace': changes}, indent=2), encoding='utf-8')
    manifest = {name: {'overlay': target, 'sha256': hashlib.sha256(Path(target).read_bytes()).hexdigest()} for name, target in changes.items()}
    (OUTPUT / 'manifest.json').write_text(json.dumps(manifest, indent=2), encoding='utf-8')
    command = ['go', 'test', '-c', '-buildvcs=false', '-pgo=off', '-overlay=' + str(OUTPUT / 'overlay.json'),
               '-ldflags=-X github.com/elysia-api/backend/server.AppVersion=1.7.0 -X github.com/elysia-api/backend/server.AppCommit=' + identity,
               '-o', str(OUTPUT / 'gateway.test.exe'), './server']
    with (OUTPUT / 'build.log').open('wb') as log:
        subprocess.run(command, cwd=SOURCE, stdout=log, stderr=subprocess.STDOUT, check=True)
    (OUTPUT / 'build.json').write_text(json.dumps({'command': command, 'labels': labels, 'commit': identity,
        'source': str(SOURCE), 'runnerSHA256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
        'binarySHA256': hashlib.sha256((OUTPUT / 'gateway.test.exe').read_bytes()).hexdigest(),
        'purpose': 'Diagnostic call counts only; never use its timing/allocation totals for acceptance.'}, indent=2), encoding='utf-8')
    print(OUTPUT)


if __name__ == '__main__':
    main()
