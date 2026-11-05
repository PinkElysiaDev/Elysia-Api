"""Continue an interrupted HTTP batch without rewriting its existing evidence."""

import argparse
import hashlib
import importlib.util
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('allocation', ROOT / 'scripts/allocation-optimization.py')
allocation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(allocation)
versions = allocation.versions


def check_completed(record, stage, scene, phase, build, version='C-package'):
    binary_key = 'sha256' if phase == 'standalone' else 'instrumentedSHA256'
    variant = 'C' if version.startswith('C-') else version
    expected = (stage, scene, phase, 'measure', version, build[variant][binary_key], build['driver']['sha256'], hashlib.sha256(versions.RUNNER_SOURCE).hexdigest())
    actual = tuple(record.get(key) for key in ['stage', 'scenario', 'phase', 'mode', 'version', 'binarySHA256', 'driverSHA256', 'runnerSHA256'])
    if actual != expected or any(record['environment'].get(key) != value for key, value in versions.RUNTIME.items()):
        raise RuntimeError('Completed task identity differs: ' + stage + '/' + scene)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--completed', required=True)
    parser.add_argument('--label', required=True)
    parser.add_argument('--stages', required=True)
    parser.add_argument('--scenes', required=True)
    parser.add_argument('--rounds', type=int, default=5)
    parser.add_argument('--phase', choices=['standalone', 'counters'], default='standalone')
    parser.add_argument('--matrix', action='store_true', help='Resume the historical/default/package matrix')
    args = parser.parse_args()
    previous, output = allocation.BASE / args.completed, allocation.BASE / args.label
    output.mkdir(parents=True)
    prior = versions.read_json(previous / 'runs.json') if (previous / 'runs.json').exists() else {
        'runs': [str(path.resolve()) for path in previous.glob('*/run.json')], 'failures': []}
    completed = {}
    source = Path(__file__).read_bytes()
    (output / 'resume.py').write_bytes(source)
    manifest = {'previous': str(previous), 'runs': [], 'failures': prior['failures'], 'complete': False, 'resumeSHA256': hashlib.sha256(source).hexdigest()}
    for path in prior['runs']:
        record = versions.read_json(path)
        if record['status'] == 'passed':
            completed[Path(path).parent.name] = (path, record)
        else:
            if Path(path).with_name('measurements.json').exists():
                raise RuntimeError('Investigate failure after measurement before resuming: ' + path)
            manifest['failures'].append(path)
    stages = args.stages.split(',')
    conditions = [(stage, allocation.BASE / stage, 'C-package') for stage in stages]
    if args.matrix:
        conditions = [(name, allocation.HISTORY, name) for name in ['R-release', 'R-build', 'A'] if args.phase == 'standalone' or name != 'R-release']
        for stage in stages:
            conditions.extend((stage + '-' + chain, allocation.BASE / stage, 'C-' + chain) for chain in ['default', 'package'])
    builds = {stage: versions.read_json(evidence / 'builds.json')['versions'] for stage, evidence, _ in conditions}
    provider_hash = versions.read_json(ROOT / '.tmp-dev/fixed-provider-build/report.json')['binarySHA256']
    for scene in args.scenes.split(','):
        for repeat in range(args.rounds):
            order = conditions[repeat % len(conditions):] + conditions[:repeat % len(conditions)]
            for stage, evidence, version in order:
                name = f'{scene}-{repeat+1}-{stage}'
                if name in completed:
                    path, record = completed[name]
                    check_completed(record, stage, scene, args.phase, builds[stage], version)
                    if record['provider']['sha256'] != provider_hash:
                        raise RuntimeError('Provider fixture differs: ' + name)
                    manifest['runs'].append(path)
                    print('reuse', name, flush=True)
                else:
                    versions.EVIDENCE = evidence
                    target = output / name
                    print('measure', name, flush=True)
                    try:
                        versions.run_case(version, scene, args.phase, 'measure', target)
                    except Exception:
                        manifest['failures'].append(str(target / 'run.json'))
                        versions.save_json(output / 'runs.json', manifest)
                        raise
                    record = versions.read_json(target / 'run.json')
                    record.update(stage=stage, resumedFrom=str(previous))
                    versions.save_json(target / 'run.json', record)
                    manifest['runs'].append(str(target / 'run.json'))
                versions.save_json(output / 'runs.json', manifest)
    manifest['complete'] = True
    versions.save_json(output / 'runs.json', manifest)


if __name__ == '__main__':
    main()
