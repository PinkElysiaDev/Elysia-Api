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


def check_completed(record, stage, scene, phase, build):
    binary_key = 'sha256' if phase == 'standalone' else 'instrumentedSHA256'
    expected = (stage, scene, phase, 'measure', 'C-package', build['C'][binary_key], build['driver']['sha256'], hashlib.sha256(versions.RUNNER_SOURCE).hexdigest())
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
    builds = {stage: versions.read_json(allocation.BASE / stage / 'builds.json')['versions'] for stage in stages}
    for scene in args.scenes.split(','):
        for repeat in range(args.rounds):
            order = stages[repeat % len(stages):] + stages[:repeat % len(stages)]
            for stage in order:
                name = f'{scene}-{repeat+1}-{stage}'
                if name in completed:
                    path, record = completed[name]
                    check_completed(record, stage, scene, args.phase, builds[stage])
                    manifest['runs'].append(path)
                    print('reuse', name, flush=True)
                else:
                    versions.EVIDENCE = allocation.BASE / stage
                    target = output / name
                    print('measure', name, flush=True)
                    try:
                        versions.run_case('C-package', scene, args.phase, 'measure', target)
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
