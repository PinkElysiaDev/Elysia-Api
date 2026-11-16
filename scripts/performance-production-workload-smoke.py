"""Exercise frozen production with the archived driver; results are setup-only."""

import argparse
import importlib.util
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('allocation', ROOT / 'scripts/allocation-optimization.py')
allocation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(allocation)
versions = allocation.versions
SMOKE_REQUESTS = 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stage', required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    if not re.fullmatch(r'[a-z0-9-]+', args.stage):
        parser.error('stage must be a lowercase identifier')
    args.output = args.output.resolve()
    allocation.cutover_conditions([args.stage], allocation.D0_STAGE)
    args.output.mkdir(parents=True)
    candidate = versions.read_json(allocation.BASE / args.stage / 'builds.json')['versions']['C']
    baseline = versions.read_json(allocation.BASE / allocation.D0_STAGE / 'builds.json')['versions']['C']
    driver_cwd = Path(baseline['source']) / 'backend/server'
    report = {'setupOnly': True, 'performanceAcceptance': False, 'requestsPerScenario': SMOKE_REQUESTS,
              'candidate': candidate, 'driverCWD': str(driver_cwd), 'results': []}
    versions.save_json(args.output / 'setup-only.json', report)
    versions.EVIDENCE = allocation.BASE / args.stage
    for scene in versions.SCENARIOS:
        output = args.output / scene
        print(scene, 'setup-only workload', flush=True)
        try:
            versions.run_case('C-production', scene, 'standalone', 'measure', output,
                              requests=SMOKE_REQUESTS, driver_cwd=driver_cwd)
        finally:
            record = versions.read_json(output / 'run.json')
            record['setupOnly'] = True
            record['performanceAcceptance'] = False
            versions.save_json(output / 'run.json', record)
            report['results'].append({'scenario': scene, 'status': record['status'], 'directory': str(output),
                                      'error': record.get('error')})
            versions.save_json(args.output / 'setup-only.json', report)
        print(scene, 'passed', flush=True)


if __name__ == '__main__':
    main()
