"""Measure isolated Agent calls with identical test fixtures on frozen backends."""

import argparse
import hashlib
import importlib.util
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('allocation', ROOT / 'scripts/allocation-optimization.py')
allocation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(allocation)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stages', required=True)
    parser.add_argument('--label', required=True)
    parser.add_argument('--rounds', type=int, default=5)
    parser.add_argument('--logging', choices=['off', 'body'], default='off')
    parser.add_argument('--chain', choices=['package', 'default'], default='package')
    args = parser.parse_args()
    stages = args.stages.split(',')
    if len(stages) != 2 or args.rounds < 5:
        parser.error('Agent comparisons require two stages and at least five rounds')
    output = allocation.BASE / args.label
    output.mkdir(parents=True)
    versions = allocation.versions
    fixtures = ['backend/server/agent_snapshot_performance_test.go']
    builds = {}
    for stage in stages:
        alias = args.label + '-' + stage
        allocation.fixtures(alias, stage, fixtures, 'instrumented')
        builds[stage] = versions.read_json(allocation.BASE / alias / 'builds.json')['versions']['C']
    records = []
    for repeat in range(args.rounds):
        for stage in stages[repeat % len(stages):] + stages[:repeat % len(stages)]:
            directory = output / f'{repeat+1}-{stage}'
            directory.mkdir()
            build = builds[stage]
            env = versions.environment() | {
                'ELYSIA_AGENT_SNAPSHOT_OUTPUT': str(directory), 'ELYSIA_AGENT_SNAPSHOT_LOG': args.logging,
                'ELYSIA_AGENT_SNAPSHOT_DEFAULT': '1' if args.chain == 'default' else '',
            }
            command = [build['instrumented'], '-test.run=^TestAgentSnapshotPerformance$', '-test.v', '-test.count=1', '-test.timeout=5m']
            record = dict(stage=stage, round=repeat+1, command=command, binarySHA256=build['instrumentedSHA256'],
                          sourceSHA256=build['sourceSHA256'], fixtureHashes=build['benchmarkOverlay'],
                          runnerSHA256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                          environment=versions.RUNTIME, logging=args.logging, chain=args.chain, status='running')
            versions.save_json(directory / 'run.json', record)
            try:
                versions.execute(command, Path(build['source']) / 'backend/server', directory / 'run.log', env)
                record.update(status='passed', metrics=versions.read_json(directory / 'agent.json'))
            except Exception as error:
                record.update(status='failed', error=str(error))
                raise
            finally:
                versions.save_json(directory / 'run.json', record)
                records.append(record)
                versions.save_json(output / 'runs.json', records)
            print(args.label, repeat+1, stage, flush=True)
    comparator = """
import { readFileSync, writeFileSync } from 'node:fs';
import { compareSamples } from './scripts/verification/compare.mjs';
const [folder, before, after] = process.argv.slice(1);
const runs = JSON.parse(readFileSync(folder + '/runs.json'));
const report = {before, after, comparisons: {}};
for (const metric of ['ns','cpuNS','bytes','objects','p50NS','p95NS','recoveryHeap','gcPauseNS']) {
  report.comparisons[metric] = compareSamples(...[before, after].map(stage => runs.filter(row => row.stage === stage).map(row => row.metrics[metric])));
}
writeFileSync(folder + '/comparison.json', JSON.stringify(report, null, 2));
"""
    versions.execute(['node', '--input-type=module', '-e', comparator, str(output), *stages], ROOT, output / 'compare.log')


if __name__ == '__main__':
    main()
