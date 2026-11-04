"""Diagnose held/cancelled gateway retention with a real isolated master key."""

import argparse
import importlib.util
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('allocation', ROOT / 'scripts/allocation-optimization.py')
allocation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(allocation)
versions, base = allocation.versions, allocation.BASE


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stages', required=True)
    parser.add_argument('--label', required=True)
    args = parser.parse_args()
    stages = args.stages.split(',')
    if any(not re.fullmatch(r'[a-z0-9-]+', name) for name in stages + [args.label]):
        parser.error('stage and label must be lowercase identifiers')
    provider_root = ROOT / '.tmp-dev/fixed-provider-build'
    provider = versions.read_json(provider_root / 'report.json')
    provider_binary = provider_root / 'server.test.exe'
    assert versions.digest(provider_binary) == provider['binarySHA256']
    files = ['backend/server/conversion_http_performance_test.go', 'backend/server/performance_review_test.go']
    for stage in stages:
        alias = args.label + '-' + stage + '-fixture'
        allocation.fixtures(alias, stage, files, 'instrumented')
    for stage in stages:
        build = versions.read_json(base / (args.label + '-' + stage + '-fixture') / 'builds.json')['versions']['C']
        output = base / args.label / stage
        output.mkdir(parents=True)
        env = versions.environment() | {
            'ELYSIA_CONVERSION_HTTP_EXECUTION': 'packages', 'ELYSIA_FLAME_MASTER_KEY': '1',
            'ELYSIA_REVIEW_HELD': '1', 'ELYSIA_CONVERSION_HTTP_CANCEL': '1',
            'ELYSIA_FLAME_WORKLOAD': 'image', 'ELYSIA_FLAME_STREAM': 'sse',
            'ELYSIA_FLAME_OUTPUT': str(output), 'ELYSIA_CONVERSION_HTTP_MIB': '2',
            'ELYSIA_CONVERSION_HTTP_WORKERS': '4', 'ELYSIA_CONVERSION_HTTP_LOG': 'off',
            'ELYSIA_CONVERSION_HTTP_PROVIDER_BINARY': str(provider_binary),
            'ELYSIA_CONVERSION_HTTP_PROVIDER_CWD': str(provider_root / 'source/backend/server'),
            'ELYSIA_CONVERSION_HTTP_PROVIDER_COMMIT': provider['commit'],
            'ELYSIA_CONVERSION_HTTP_PROVIDER_EXECUTION': provider['settings']['execution'],
        }
        command = [build['instrumented'], '-test.run=^TestReviewHeldProfiles$', '-test.v', '-test.timeout=8m']
        record = {'stage': stage, 'command': command, 'binarySHA256': build['instrumentedSHA256'],
                  'productionCommit': build['commit'], 'fixtureHashes': build['benchmarkOverlay'],
                  'environment': {key: value for key, value in env.items() if key in versions.RUNTIME or key.startswith('ELYSIA_')},
                  'purpose': 'Held and cancelled retention only; forced GC is excluded from timings.', 'status': 'running'}
        versions.save_json(output / 'run.json', record)
        try:
            versions.execute(command, Path(build['source']) / 'backend/server', output / 'run.log', env)
            record['status'] = 'passed'
        except Exception as error:
            record.update(status='failed', error=str(error))
            raise
        finally:
            versions.save_json(output / 'run.json', record)
        print(stage, 'held/cancelled retention passed', flush=True)


if __name__ == '__main__':
    main()
