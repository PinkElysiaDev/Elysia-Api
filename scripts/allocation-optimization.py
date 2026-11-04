"""Freeze allocation candidates and reuse the matched-process performance runner."""

import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parent.parent
BASE = ROOT / '.tmp-dev/allocation-optimization-2026-11-04'
HISTORY = ROOT / '.tmp-dev/three-version-2026-11-04'
SOURCE = Path(__file__).read_bytes()
spec = importlib.util.spec_from_file_location('versions', ROOT / 'scripts/performance-versions.py')
versions = importlib.util.module_from_spec(spec)
spec.loader.exec_module(versions)


def freeze(name):
    directory = BASE / name
    directory.mkdir(parents=True)
    source = directory / 'C/source'
    names = subprocess.check_output(['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard', 'backend'], cwd=ROOT).decode().split('\0')
    manifest = {}
    for relative in sorted(set(filter(None, names))):
        original = ROOT / relative
        if not original.is_file():
            continue
        raw = original.read_bytes()
        destination = source / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_bytes(raw)
        manifest[relative] = hashlib.sha256(raw).hexdigest()
    source_hash = hashlib.sha256(json.dumps(manifest, sort_keys=True).encode()).hexdigest()
    parent = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
    identity = parent[:8] + '-source-' + source_hash[:12]
    backend = source / 'backend'
    flags = '-s -w -X github.com/elysia-api/backend/server.AppVersion=1.7.0 -X github.com/elysia-api/backend/server.AppCommit=' + identity
    commands = []
    binaries = {}
    build_info = {}
    for key, command in [
        ('binary', ['go', 'build']),
        ('instrumented', ['go', 'test', '-c']),
        ('protocol', ['go', 'test', '-c']),
        ('builtin', ['go', 'test', '-c']),
    ]:
        binary = directory / (key + '.exe')
        package = {'binary': '.', 'instrumented': './server', 'protocol': './protocol', 'builtin': './protocol/builtin'}[key]
        command += ['-buildvcs=false', '-pgo=off', '-ldflags', flags, '-o', str(binary), package]
        commands.append(command)
        versions.execute(command, backend, directory / (key + '-build.log'))
        binaries[key] = str(binary)
        binaries['sha256' if key == 'binary' else key + 'SHA256'] = versions.digest(binary)
        build_info[key] = subprocess.check_output(['go', 'version', '-m', str(binary)], text=True)
    builds = versions.read_json(HISTORY / 'builds.json')
    builds['versions']['C'] = dict(binaries, commit=identity, parentCommit=parent, source=str(source), sourceSHA256=source_hash, commands=commands, buildInfo=build_info)
    # Keep the fixed driver and upstream; new candidates cannot change the load.
    versions.save_json(directory / 'builds.json', builds)
    versions.save_json(directory / 'source-manifest.json', manifest)
    (directory / 'runner.py').write_bytes(SOURCE)
    print(name, identity, flush=True)


def micro(stages, label, pattern, iterations, rounds):
    output = BASE / label
    output.mkdir(parents=True)
    records = []
    for repeat in range(rounds):
        order = stages[repeat % len(stages):] + stages[:repeat % len(stages)]
        for stage in order:
            build = versions.read_json(BASE / stage / 'builds.json')['versions']['C']
            for package in ['protocol', 'instrumented', 'builtin']:
                binary = build[package]
                log = output / f'{repeat+1:02}-{stage}-{package}.log'
                command = [binary, '-test.run=^$', '-test.bench=' + pattern, '-test.benchtime=' + str(iterations) + 'x', '-test.count=1', '-test.timeout=8m']
                versions.execute(command, Path(build['source']) / 'backend', log)
                for line in log.read_text(encoding='utf-8').splitlines():
                    match = re.match(r'(Benchmark\S+)-\d+\s+(\d+)\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op', line)
                    if match:
                        records.append(dict(stage=stage, round=repeat+1, benchmark=match[1], iterations=int(match[2]), ns=float(match[3]), bytes=int(match[4]), objects=int(match[5]), binarySHA256=versions.digest(binary)))
            print('micro', label, repeat+1, stage, flush=True)
    versions.save_json(output / 'samples.json', records)


def fixtures(name, reference, files):
    directory = BASE / name
    directory.mkdir(parents=True)
    builds = versions.read_json(BASE / reference / 'builds.json')
    build = builds['versions']['C']
    source = Path(build['source'])
    replacements = {}
    manifest = {}
    for relative in files:
        raw = (ROOT / relative).read_bytes()
        target = directory / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(raw)
        replacements[str(source / relative)] = str(target)
        manifest[relative] = hashlib.sha256(raw).hexdigest()
    overlay = directory / 'overlay.json'
    versions.save_json(overlay, {'Replace': replacements})
    binary = directory / 'protocol.exe'
    flags = '-s -w -X github.com/elysia-api/backend/server.AppVersion=1.7.0 -X github.com/elysia-api/backend/server.AppCommit=' + build['commit']
    command = ['go', 'test', '-c', '-buildvcs=false', '-pgo=off', '-ldflags', flags, '-overlay=' + str(overlay), '-o', str(binary), './protocol']
    versions.execute(command, source / 'backend', directory / 'build.log')
    build.update(protocol=str(binary), protocolSHA256=versions.digest(binary), benchmarkOverlay=manifest, benchmarkReference=reference, benchmarkBuild=command)
    versions.save_json(directory / 'builds.json', builds)


def http(stages, label, scenes, rounds, phase, mode, chain):
    output = BASE / label
    for scene in scenes:
        for repeat in range(rounds):
            order = stages[repeat % len(stages):] + stages[:repeat % len(stages)]
            for stage in order:
                versions.EVIDENCE = BASE / stage
                target = output / f'{scene}-{repeat+1}-{stage}'
                print(label, scene, repeat+1, stage, flush=True)
                versions.run_case('C-' + chain, scene, phase, mode, target)
                record = versions.read_json(target / 'run.json')
                record['stage'] = stage
                record['allocationRunnerSHA256'] = hashlib.sha256(SOURCE).hexdigest()
                versions.save_json(target / 'run.json', record)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['freeze', 'fixtures', 'micro', 'http', 'render'])
    parser.add_argument('--stages', required=True)
    parser.add_argument('--label')
    parser.add_argument('--reference')
    parser.add_argument('--fixtures', default='backend/protocol/continuation_allocation_test.go')
    parser.add_argument('--pattern', default='^Benchmark(Continuation|SemanticOwnership|Allocation|NativeThinkingContinuation|ReferenceLayoutRewrite)')
    parser.add_argument('--iterations', type=int, default=100)
    parser.add_argument('--rounds', type=int)
    parser.add_argument('--scenes', default=','.join(versions.SCENARIOS))
    parser.add_argument('--phase', choices=['standalone', 'counters'], default='standalone')
    parser.add_argument('--mode', choices=['measure', 'cpu', 'memory'], default='measure')
    parser.add_argument('--chain', choices=['package', 'default'], default='package')
    args = parser.parse_args()
    stages = args.stages.split(',')
    if any(not re.fullmatch(r'[a-z0-9-]+', stage) for stage in stages):
        parser.error('stage names must be lowercase identifiers')
    if args.action == 'freeze':
        for stage in stages:
            freeze(stage)
    elif args.action == 'fixtures':
        fixtures(stages[0], args.reference, args.fixtures.split(','))
    elif args.action == 'render':
        renderer_spec = importlib.util.spec_from_file_location('profile_summary', ROOT / 'scripts/performance-profile-summary.py')
        renderer = importlib.util.module_from_spec(renderer_spec)
        renderer_spec.loader.exec_module(renderer)
        renderer.BASE = BASE / args.label
        renderer.main(list(zip(stages, stages[1:])))
    elif args.action == 'micro':
        micro(stages, args.label, args.pattern, args.iterations, args.rounds or 10)
    else:
        http(stages, args.label, args.scenes.split(','), args.rounds or 5, args.phase, args.mode, args.chain)


if __name__ == '__main__':
    main()
