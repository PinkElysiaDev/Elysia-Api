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
D0_STAGE = 'backend-p0'
D0_SOURCE_SHA256 = 'c6c398515bf0ca229861207085a09d18f1a72a3eeaf3ee14e678fa1c31c04fa5'
D0_BINARY_SHA256 = '9dd2b44b844ccf758d561eb9825d33167c7f3036eaed886c44bce2194a6ca055'
D0_INSTRUMENTED_SHA256 = 'f675a69821dafdc7f42bb4fac691ab6a8d2c4e06fa401ef4af567484cefeb19f'
FIXED_DRIVER_SHA256 = 'a449b44c32a85b505392044b9d00e8f6e8785b33448d4f1829507aac2beb5d25'
FIXED_PROVIDER_SHA256 = 'e6f143b21a6a1ed778660ba9cd311994415d80ecc4791199496bad3da590b3ad'
FIXED_PROVIDER_SOURCE_SHA256 = '0b33f2f85e0f3225737e701609c3e160323aa0fd8a439fe3fd0fd272d9ae3589'
FIXED_GO_VERSION = 'go1.27.2'
SOURCE = Path(__file__).read_bytes()
spec = importlib.util.spec_from_file_location('versions', ROOT / 'scripts/performance-versions.py')
versions = importlib.util.module_from_spec(spec)
spec.loader.exec_module(versions)


def freeze(name, reference=None):
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
    builds = versions.read_json((BASE / reference if reference else HISTORY) / 'builds.json')
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
                package_directory = {'protocol': 'protocol', 'instrumented': 'server', 'builtin': 'protocol/builtin'}[package]
                versions.execute(command, Path(build['source']) / 'backend' / package_directory, log)
                for line in log.read_text(encoding='utf-8').splitlines():
                    match = re.match(r'(Benchmark\S+)-\d+\s+(\d+)\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op', line)
                    if match:
                        records.append(dict(stage=stage, round=repeat+1, benchmark=match[1], iterations=int(match[2]), ns=float(match[3]), bytes=int(match[4]), objects=int(match[5]), binarySHA256=versions.digest(binary)))
            print('micro', label, repeat+1, stage, flush=True)
    versions.save_json(output / 'samples.json', records)


def fixtures(name, reference, files, package):
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
    binary = directory / (package + '.exe')
    flags = '-s -w -X github.com/elysia-api/backend/server.AppVersion=1.7.0 -X github.com/elysia-api/backend/server.AppCommit=' + build['commit']
    target = './server' if package == 'instrumented' else './protocol'
    command = ['go', 'test', '-c', '-buildvcs=false', '-pgo=off', '-ldflags', flags, '-overlay=' + str(overlay), '-o', str(binary), target]
    versions.execute(command, source / 'backend', directory / 'build.log')
    build.update({package: str(binary), package + 'SHA256': versions.digest(binary)})
    build.update(benchmarkOverlay=manifest, benchmarkReference=reference, benchmarkBuild=command)
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


def matrix(stages, label, scenes, rounds, phase, include_history=True):
    conditions = [(name, HISTORY, name) for name in ['R-release', 'R-build', 'A']
                  if include_history and (phase == 'standalone' or name != 'R-release')]
    for stage in stages:
        conditions.extend((stage + '-' + chain, BASE / stage, 'C-' + chain) for chain in ['default', 'package'])
    run_matrix(conditions, label, scenes, rounds, phase)


def run_matrix(conditions, label, scenes, rounds, phase, driver_cwd=None):
    for scene in scenes:
        for repeat in range(rounds):
            order = conditions[repeat % len(conditions):] + conditions[:repeat % len(conditions)]
            for name, evidence, variant in order:
                versions.EVIDENCE = evidence
                target = BASE / label / f'{scene}-{repeat+1}-{name}'
                print(label, scene, repeat+1, name, flush=True)
                options = {'driver_cwd': driver_cwd} if driver_cwd is not None else {}
                versions.run_case(variant, scene, phase, 'measure', target, **options)
                record = versions.read_json(target / 'run.json')
                record.update(stage=name, allocationRunnerSHA256=hashlib.sha256(SOURCE).hexdigest())
                versions.save_json(target / 'run.json', record)


def verify_source_files(source, manifest):
    source = Path(source).resolve()
    for relative, expected in manifest.items():
        path = (source / relative).resolve()
        if not path.is_relative_to(source) or versions.digest(path) != expected:
            raise RuntimeError(f'frozen source differs: {source}/{relative}')


def verify_frozen_stage(stage):
    directory = BASE / stage
    archive = versions.read_json(directory / 'builds.json')
    build = archive['versions']['C']
    manifest = versions.read_json(directory / 'source-manifest.json')
    source_hash = hashlib.sha256(json.dumps(manifest, sort_keys=True).encode()).hexdigest()
    if archive['runtime'] != versions.RUNTIME or source_hash != build['sourceSHA256']:
        raise RuntimeError(f'frozen runtime/source manifest differs: {stage}')
    verify_source_files(build['source'], manifest)
    for name, hash_key in [('binary', 'sha256'), ('instrumented', 'instrumentedSHA256')]:
        if (versions.digest(build[name]) != build[hash_key]
                or build['buildInfo'][name].splitlines()[0].rsplit(' ', 1)[-1] != FIXED_GO_VERSION):
            raise RuntimeError(f'frozen binary/runtime differs: {stage}/{name}')
    driver = archive['versions']['driver']
    if driver['sha256'] != FIXED_DRIVER_SHA256 or versions.digest(driver['binary']) != FIXED_DRIVER_SHA256:
        raise RuntimeError(f'fixed workload driver differs: {stage}')
    return build


def cutover_conditions(stages, reference):
    if reference != D0_STAGE or reference in stages:
        raise RuntimeError('cutover requires the immutable backend-p0 D0 and distinct production candidates')
    baseline = verify_frozen_stage(reference)
    if (baseline['sourceSHA256'] != D0_SOURCE_SHA256 or baseline['sha256'] != D0_BINARY_SHA256
            or baseline['instrumentedSHA256'] != D0_INSTRUMENTED_SHA256):
        raise RuntimeError('D0 archive differs from the agreed frozen baseline')
    provider = ROOT / '.tmp-dev/fixed-provider-build'
    report = versions.read_json(provider / 'report.json')
    manifest = versions.read_json(provider / 'source-manifest.json')
    source_hash = hashlib.sha256(json.dumps(manifest, ensure_ascii=False, separators=(',', ':')).encode()).hexdigest()
    if (report['binarySHA256'] != FIXED_PROVIDER_SHA256 or versions.digest(provider / 'server.test.exe') != FIXED_PROVIDER_SHA256
            or report['sourceSHA256'] != FIXED_PROVIDER_SOURCE_SHA256 or source_hash != FIXED_PROVIDER_SOURCE_SHA256):
        raise RuntimeError('fixed workload upstream differs from D0')
    verify_source_files(provider / 'source', {entry['path']: entry['sha256'] for entry in manifest})
    conditions = [(reference + '-default', BASE / reference, 'C-default')]
    for stage in stages:
        candidate = verify_frozen_stage(stage)
        versions.read_production_packages(candidate['source'])
        versions.read_production_compiler(candidate['source'])
        conditions.append((stage + '-production', BASE / stage, 'C-production'))
    return conditions


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['freeze', 'fixtures', 'micro', 'http', 'render', 'matrix', 'cutover'])
    parser.add_argument('--stages', required=True)
    parser.add_argument('--label')
    parser.add_argument('--reference')
    parser.add_argument('--fixtures', default='backend/protocol/continuation_allocation_test.go')
    parser.add_argument('--package', choices=['protocol', 'instrumented'], default='protocol')
    parser.add_argument('--pattern', default='^Benchmark(Continuation|SemanticOwnership|Allocation|NativeThinkingContinuation|ReferenceLayoutRewrite)')
    parser.add_argument('--iterations', type=int, default=100)
    parser.add_argument('--rounds', type=int)
    parser.add_argument('--scenes', default=','.join(versions.SCENARIOS))
    parser.add_argument('--phase', choices=['standalone', 'counters', 'attribution'], default='standalone')
    parser.add_argument('--mode', choices=['measure', 'cpu', 'memory'], default='measure')
    parser.add_argument('--chain', choices=['package', 'default', 'production'], default='package')
    parser.add_argument('--current-only', action='store_true', help='Compare both current chains without historical release conditions')
    args = parser.parse_args()
    if args.current_only and args.action != 'matrix':
        parser.error('--current-only requires matrix')
    stages = args.stages.split(',')
    if any(not re.fullmatch(r'[a-z0-9-]+', stage) for stage in stages):
        parser.error('stage names must be lowercase identifiers')
    if args.reference and not re.fullmatch(r'[a-z0-9-]+', args.reference):
        parser.error('reference must be a lowercase identifier')
    if args.action == 'freeze':
        for stage in stages:
            freeze(stage, args.reference)
    elif args.action == 'fixtures':
        fixtures(stages[0], args.reference, args.fixtures.split(','), args.package)
    elif args.action == 'render':
        renderer_spec = importlib.util.spec_from_file_location('profile_summary', ROOT / 'scripts/performance-profile-summary.py')
        renderer = importlib.util.module_from_spec(renderer_spec)
        renderer_spec.loader.exec_module(renderer)
        renderer.BASE = BASE / args.label
        renderer.main(list(zip(stages, stages[1:])))
    elif args.action == 'micro':
        micro(stages, args.label, args.pattern, args.iterations, args.rounds or 10)
    elif args.action == 'matrix':
        if args.phase == 'attribution' or args.mode != 'measure':
            parser.error('matrix accepts standalone/counter measurements; profile separately')
        matrix(stages, args.label, args.scenes.split(','), args.rounds or 5, args.phase, not args.current_only)
    elif args.action == 'cutover':
        if args.phase == 'attribution' or args.mode != 'measure' or args.rounds not in (None, 5):
            parser.error('cutover requires five standalone/counter measurement rounds; profile separately')
        if not args.label or set(args.scenes.split(',')) != set(versions.SCENARIOS):
            parser.error('cutover requires a fresh label and the complete eight-scene workload')
        conditions = cutover_conditions(stages, args.reference or D0_STAGE)
        baseline = versions.read_json(BASE / D0_STAGE / 'builds.json')['versions']['C']
        driver_cwd = Path(baseline['source']) / 'backend/server'
        directory = BASE / args.label
        directory.mkdir(parents=True)
        identities = []
        for name, archive, version in conditions:
            build = versions.read_json(archive / 'builds.json')['versions']['C']
            identities.append({'name': name, 'archive': str(archive), 'version': version,
                               'commit': build['commit'], 'sourceSHA256': build['sourceSHA256'],
                               'binarySHA256': build['sha256'], 'instrumentedSHA256': build['instrumentedSHA256']})
        versions.save_json(directory / 'cutover-preflight.json', {
            'conditions': identities,
            'runtime': versions.RUNTIME, 'driverSHA256': FIXED_DRIVER_SHA256, 'driverCWD': str(driver_cwd),
            'providerSHA256': FIXED_PROVIDER_SHA256, 'providerSourceSHA256': FIXED_PROVIDER_SOURCE_SHA256,
            'baselineSourceSHA256': D0_SOURCE_SHA256, 'baselineBinarySHA256': D0_BINARY_SHA256,
            'allocationRunnerSHA256': hashlib.sha256(SOURCE).hexdigest(),
            'processRunnerSHA256': hashlib.sha256(versions.RUNNER_SOURCE).hexdigest(),
            'rounds': 5, 'scenarios': versions.SCENARIOS, 'phase': args.phase,
        })
        run_matrix(conditions, args.label, args.scenes.split(','), 5, args.phase, driver_cwd)
    else:
        http(stages, args.label, args.scenes.split(','), args.rounds or 5, args.phase, args.mode, args.chain)


if __name__ == '__main__':
    main()
