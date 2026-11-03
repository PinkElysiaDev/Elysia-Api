"""Fixed-iteration diagnostics of the pinned current implementation."""

import importlib.util
from pathlib import Path
import re
import sys

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('versions', ROOT / 'scripts/performance-versions.py')
versions = importlib.util.module_from_spec(spec)
spec.loader.exec_module(versions)


def main():
    output = versions.EVIDENCE / 'micro'
    output.mkdir()
    source = versions.EVIDENCE / 'C/source/backend'
    overlay = output / 'overlay.json'
    benchmark = ROOT / 'backend/protocol/allocation_attribution_bench_test.go'
    versions.save_json(overlay, {'Replace': {str(source / 'protocol' / benchmark.name): str(benchmark)}})
    binaries, commands = {}, []

    def run(command, log, env=None, cwd=None):
        environment = env or versions.environment()
        commands.append({'command': command, 'cwd': str(cwd or source), 'log': str(log),
            'environment': {key: value for key, value in environment.items() if key in versions.RUNTIME or key.startswith('ELYSIA_')}})
        versions.execute(command, cwd or source, log, environment)

    for name, package in [('protocol', './protocol'), ('builtin', './protocol/builtin')]:
        binary = output / (name + '.test.exe')
        flags = ['-overlay=' + str(overlay)] if name == 'protocol' else []
        if name == 'protocol':
            flags.append('-gcflags=github.com/elysia-api/backend/protocol=-m=2')
        run(['go', 'test', '-c', '-buildvcs=false', '-pgo=off'] + flags + ['-o', str(binary), package], output / (name + '-build.log'))
        binaries[name] = {'binary': str(binary), 'sha256': versions.digest(binary), 'cwd': str(source / package)}
    shapes = [('Allocation', 'protocol', 100000), ('ReferenceLayoutRewrite', 'builtin', 200), ('NativeThinkingContinuation', 'builtin', 2000)]
    for repeat in range(10):
        order = shapes[repeat % len(shapes):] + shapes[:repeat % len(shapes)]
        for name, package, count in order:
            print('micro', repeat + 1, name, flush=True)
            run([binaries[package]['binary'], '-test.run=^$', '-test.bench=^Benchmark' + name,
                 '-test.benchtime=' + str(count) + 'x', '-test.count=1'], output / f'{repeat+1:02}-{name}.log', cwd=binaries[package]['cwd'])
    samples = {}
    for log in output.glob('[0-9][0-9]-*.log'):
        for line in log.read_text(encoding='utf-8').splitlines():
            match = re.match(r'(Benchmark\S+)-\d+\s+(\d+)\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op', line)
            if match:
                samples.setdefault(match[1], []).append(dict(iterations=int(match[2]), ns=float(match[3]), bytes=int(match[4]), objects=int(match[5])))
    if not samples or any(len(values) != 10 for values in samples.values()):
        raise RuntimeError('missing fixed-iteration benchmark samples')
    versions.save_json(output / 'summary.json', samples)
    run([binaries['protocol']['binary'], '-test.run=^TestDeepCompilationDiagnostics$', '-test.v'],
        output / 'retained-graph.log', versions.environment() | {'ELYSIA_DEEP_DIAGNOSTICS': '1'}, binaries['protocol']['cwd'])
    for name, count in [('ReferenceLayoutRewrite', 1000), ('NativeThinkingContinuation', 10000)]:
        directory = output / name
        directory.mkdir()
        for mode, flag in [('cpu', 'cpuprofile'), ('memory', 'memprofile')]:
            print('profile', name, mode, flush=True)
            run([binaries['builtin']['binary'], '-test.run=^$', '-test.bench=^Benchmark' + name + '$',
                 '-test.benchtime=' + str(count) + 'x', '-test.count=1', '-test.' + flag + '=' + str(directory / (mode + '.pprof'))],
                directory / (mode + '.log'), cwd=binaries['builtin']['cwd'])
    versions.save_json(output / 'manifest.json', {
        'implementation': versions.read_json(versions.EVIDENCE / 'builds.json')['versions']['C'],
        'benchmarkSHA256': versions.digest(benchmark), 'binaries': binaries, 'commands': commands,
        'note': 'No production changes. Timed benchmarks exclude fixture construction; separate diagnostic profiles include it. No legacy equivalence is claimed for reference rewrite or Native reconstruction.'})


if __name__ == '__main__':
    main()
