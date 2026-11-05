"""Normalize profile evidence, attribute disjoint allocation stacks, render flames."""

from collections import defaultdict
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime
import hashlib
import html
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import time
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
BASE = ROOT / '.tmp-dev/three-version-2026-11-04'


def read_json(path):
    return json.loads(path.read_text(encoding='utf-8'))


def parse_raw(text):
    """Read Go pprof's archived raw text, preserving complete sample stacks."""
    samples_text, locations_text = text.split('Samples:\n', 1)[1].split('\nLocations\n', 1)
    locations_text = locations_text.split('\nMappings\n', 1)[0]
    lines = samples_text.splitlines()
    types = [value.split('/')[0] for value in lines[0].split()]
    samples = []
    for line in lines[1:]:
        match = re.fullmatch(r'\s*([\d\s-]+):\s*([\d\s]+)', line)
        if match:
            values, stack = [[int(value) for value in group.split()] for group in match.groups()]
            if len(values) != len(types):
                raise ValueError('profile sample columns changed')
            samples.append((dict(zip(types, values)), stack))
    locations, current = {}, None
    for line in locations_text.splitlines():
        match = re.match(r'\s*(\d+): 0x[0-9a-f]+ (?:M=\d+ )?(?:\[F\] )?(.*)', line)
        if match:
            current = int(match[1])
            locations[current] = []
            line = match[2]
        if current is not None:
            # Filenames may contain spaces; the terminal source/line marker is
            # retained in full for evidence, while classification needs symbols.
            symbol = re.match(r'\s*(.*?)\s+(?:[A-Za-z]:[/\\]|/|[^ ]+[.]go:)', line)
            if symbol:
                locations[current].append(symbol[1])
    return [(values, [symbol for location in stack for symbol in locations[location]]) for values, stack in samples]


def is_continuation_stack(stack):
    frames = ('ContinuationNodeDigest', 'gatewayContinuation).historyDigest',
              'continuationHistory).', 'ContinuationHistoryDigest).')
    return any(frame in symbol for symbol in stack for frame in frames)


def category(stack):
    joined = '\n'.join(stack)
    if 'runtime/pprof.' in joined:
        return 'profiler'
    if is_continuation_stack(stack):
        return 'continuation-digests'
    if any(name in joined for name in ['.cloneCore', '.cloneSemantic', '.normalizeDocument', '.normalizeCore']):
        return 'semantic-ownership'
    if 'stringslite.Clone' in joined or 'strings.Clone' in joined:
        return 'owned-fragment-copies'
    if 'readObjectFieldsWithin' in joined or 'planObjectReader).readFields' in joined:
        return 'raw-field-tables'
    if 'nativeCaptureState).forItem' in joined:
        return 'native-child-scopes'
    if 'jsonOutputParts).add' in joined:
        return 'output-fragment-workspace'
    if 'readCore' in joined and '/protocol.' in joined:
        return 'core-projections'
    if 'buildOperandValues' in joined:
        return 'operand-evaluation'
    if any(name in joined for name in ['.buildObject', '.buildArray', '.buildIterationOutput', '.buildFilter', '.mergeObjects']):
        return 'plan-containers'
    if any(name in joined for name in ['packageStreamTransaction', 'trackStreamPosition', 'semanticRange']):
        return 'stream-state'
    if 'JSONOutput.encodeWithin' in joined or 'jsonOutputParts).encode' in joined:
        return 'owned-output-encoding'
    return 'other'


def check_cpu_window(raw, row):
    match = re.search(r'Time: (\d{4}-\d\d-\d\d \d\d:\d\d:\d\d)(?:\.(\d+))? ([+-]\d{4})', raw)
    if not match:
        raise ValueError('CPU profile timestamp missing')
    start = int(datetime.strptime(match[1] + ' ' + match[3], '%Y-%m-%d %H:%M:%S %z').timestamp()) * 10**9
    start += int((match[2] or '').ljust(9, '0'))
    duration = re.search(r'Duration: ([\d.]+)(ms|s)?', raw)
    if not duration:
        raise ValueError('CPU duration missing')
    end = start + int(float(duration[1]) * (10**6 if duration[2] == 'ms' else 10**9))
    if not start <= row['StartedUnixNS'] < row['FinishedUnixNS'] <= end:
        raise ValueError(f'fixed workload is not fully bracketed by CPU profile: {start}, {row["StartedUnixNS"]}, {row["FinishedUnixNS"]}, {end}')


def flame(command, destination):
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    args = ['go', 'tool', 'pprof', '-no_browser', f'-http=127.0.0.1:{port}'] + command
    with destination.with_suffix('.log').open('wb') as log:
        child = subprocess.Popen(args, stdout=log, stderr=subprocess.STDOUT, creationflags=subprocess.CREATE_NO_WINDOW)
        try:
            for _ in range(100):
                try:
                    with urllib.request.urlopen(f'http://127.0.0.1:{port}/flamegraph', timeout=2) as response:
                        destination.write_bytes(response.read())
                    return
                except OSError:
                    if child.poll() is not None:
                        raise RuntimeError('pprof viewer exited')
                    time.sleep(.1)
            raise RuntimeError('pprof viewer did not start')
        finally:
            stop_viewer(child)


def stop_viewer(child):
    # On Windows go tool launches pprof as a child. Stop only this viewer's tree
    # so rendering does not leave listeners and retained profiles behind.
    if os.name == 'nt':
        subprocess.run(['taskkill', '/PID', str(child.pid), '/T', '/F'],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                       creationflags=subprocess.CREATE_NO_WINDOW, check=False)
    else:
        child.terminate()
    child.wait(timeout=5)


def render_micro(output, jobs):
    source = BASE / 'micro/manifest.json'
    if not source.exists():
        return
    micro = read_json(source)
    binary = micro['binaries']['builtin']['binary']
    for name in ['ReferenceLayoutRewrite', 'NativeThinkingContinuation']:
        for sample in ['cpu', 'alloc_space', 'alloc_objects', 'inuse_space']:
            profile = BASE / 'micro' / name / ('cpu.pprof' if sample == 'cpu' else 'memory.pprof')
            destination = output / f'micro-{name}-{sample}.html'
            arguments = ['-sample_index=' + sample, binary, str(profile)]
            top = subprocess.check_output(['go', 'tool', 'pprof', '-top', '-nodecount=60', '-sample_index=' + sample,
                binary, str(profile)], stderr=subprocess.STDOUT)
            destination.with_suffix('.top.txt').write_bytes(top)
            jobs.append((arguments, destination, {'file': destination.name, 'kind': sample, 'scene': name, 'version': 'C-micro',
                'unit': 'whole fixed-iteration batch including fixture construction; see micro/manifest.json'}))


def render_flames(jobs):
    def render(job):
        arguments, destination, record = job
        flame(arguments, destination)
        return record | {'sha256': hashlib.sha256(destination.read_bytes()).hexdigest()}
    # Each worker owns a separate pprof process, listener and output file. The
    # calling thread alone publishes the index, after every worker has exited.
    with ThreadPoolExecutor(max_workers=4) as workers:
        return list(workers.map(render, jobs))


def write_index(output, manifest):
    (output / 'manifest.json').write_text(json.dumps(manifest, indent=2), encoding='utf-8')
    (output / 'index.html').write_text('<!doctype html><meta charset="utf-8"><h1>Protocol performance profiles</h1>'
        '<p>HTTP CPU/allocations normalized per 1000 completed requests; inuse is retained process heap. '
        'Normalized CPU profiles retain the original duration: their displayed samples/duration ratio is not CPU utilization. '
        'Micro profiles include fixture construction and use whole fixed batches. Timing runs are separate.</p><ul>' +
        ''.join('<li><a href="' + item['file'] + '">' + html.escape(item['file']) + '</a></li>' for item in manifest) +
        '</ul>', encoding='utf-8')


def main(comparisons=None):
    if comparisons is None:
        comparisons = [('R-build', 'A'), ('A', 'C-default'), ('C-default', 'C-package'), ('R-release', 'C-package')]
    output = BASE / 'profiles'
    output.mkdir(exist_ok=True)
    profiles, attribution, cpu_groups, jobs = {}, {}, {}, []
    for mode in ['cpu', 'memory']:
        for directory in sorted((BASE / ('standalone-' + mode)).iterdir()):
            run = read_json(directory / 'run.json')
            if run['status'] != 'passed':
                raise ValueError('incomplete profile: ' + str(directory))
            row, = read_json(directory / 'measurements.json')
            version, scene = run.get('stage', run['version']), run['scenario']
            prefix = f'{scene}-{version}'
            profile = directory / ('cpu.pprof' if mode == 'cpu' else 'alloc-after.pprof')
            base_args = [] if mode == 'cpu' else ['-base', str(directory / 'alloc-before.pprof')]
            command = ['go', 'tool', 'pprof']
            raw = subprocess.check_output(command + ['-raw'] + base_args + [run['binary'], str(profile)], text=True, stderr=subprocess.STDOUT)
            (output / (prefix + '-' + mode + '.raw.txt')).write_text(raw, encoding='utf-8')
            if mode == 'cpu':
                check_cpu_window(raw, row)
                groups = defaultdict(int)
                for values, stack in parse_raw(raw):
                    group = ('gc' if any(name.startswith('runtime.gcDrain') or name == 'runtime.gcAssistAlloc' for name in stack)
                        else 'continuation' if is_continuation_stack(stack)
                        else 'other')
                    groups[group] += values['cpu']
                cpu_groups[prefix] = {name: value / row['Requests'] / 1e6 for name, value in groups.items()}
            else:
                totals = defaultdict(lambda: {'bytes': 0, 'objects': 0})
                stacks = defaultdict(lambda: {'bytes': 0, 'objects': 0})
                for values, stack in parse_raw(raw):
                    group = category(stack)
                    totals[group]['bytes'] += values['alloc_space']
                    totals[group]['objects'] += values['alloc_objects']
                    key = tuple(stack)
                    stacks[key]['bytes'] += values['alloc_space']
                    stacks[key]['objects'] += values['alloc_objects']
                attribution[prefix] = {'requests': row['Requests'], 'frames': sum(x['Frames'] for x in row['Observations']),
                    'categories': {name: {metric: value / row['Requests'] for metric, value in values.items()} for name, values in totals.items()},
                    'stacks': [dict(stack=list(stack), bytesPerRequest=value['bytes'] / row['Requests'], objectsPerRequest=value['objects'] / row['Requests'])
                        for stack, value in sorted(stacks.items(), key=lambda item: item[1]['bytes'], reverse=True)[:80]]}
            samples = ['cpu'] if mode == 'cpu' else ['alloc_space', 'alloc_objects', 'inuse_space']
            for sample in samples:
                sampled = directory / 'heap-drained.pprof' if sample == 'inuse_space' else profile
                base = [] if sample == 'inuse_space' else base_args
                normalize = [] if sample == 'inuse_space' else ['-divide_by=' + str(row['Requests'] / 1000)]
                normalized = output / (prefix + '-' + sample + '.pprof')
                subprocess.run(command + ['-proto', '-sample_index=' + sample, '-output=' + str(normalized)] + normalize + base + [run['binary'], str(sampled)], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                profiles[(scene, version, sample)] = normalized
                destination = output / (prefix + '-' + sample + '.html')
                jobs.append((['-sample_index=' + sample, str(normalized)], destination,
                             {'file': destination.name, 'kind': sample, 'scene': scene, 'version': version,
                              'unit': 'retained bytes' if sample == 'inuse_space' else 'per 1000 completed requests'}))
                top = subprocess.check_output(command + ['-top', '-nodecount=60', '-sample_index=' + sample, str(normalized)], stderr=subprocess.STDOUT)
                destination.with_suffix('.top.txt').write_bytes(top)
            print('rendered', prefix, mode, flush=True)
    for scene in sorted({key[0] for key in profiles}):
        for before, after in comparisons:
            for sample in ['cpu', 'alloc_space', 'alloc_objects', 'inuse_space']:
                destination = output / f'{scene}-{before}-to-{after}-{sample}.html'
                jobs.append((['-sample_index=' + sample, '-diff_base', str(profiles[(scene, before, sample)]), str(profiles[(scene, after, sample)])], destination,
                             {'file': destination.name, 'kind': 'difference-' + sample, 'scene': scene, 'before': before, 'after': after}))
    render_micro(output, jobs)
    manifest = render_flames(jobs)
    (output / 'attribution.json').write_text(json.dumps(attribution, indent=2), encoding='utf-8')
    (output / 'cpu-groups.json').write_text(json.dumps(cpu_groups, indent=2), encoding='utf-8')
    write_index(output, manifest)


if __name__ == '__main__':
    main()
