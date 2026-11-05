"""Authenticated carrier/session HTTP supplements using the existing process runner."""

import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parent.parent
BASE = ROOT / '.tmp-dev/allocation-optimization-2026-11-04'
spec = importlib.util.spec_from_file_location('versions', ROOT / 'scripts/performance-versions.py')
versions = importlib.util.module_from_spec(spec)
spec.loader.exec_module(versions)


def prepare_fixture(scene):
    def prepare(url, packages, output):
        session = 'isolated-recovery' if scene == 'session' else ''
        messages = [dict(role='assistant' if index % 2 else 'user', content='x' * 4096) for index in range(63)]
        if scene == 'media-carrier':
            messages[0]['content'] = [dict(type='text', text='x' * 4096),
                dict(type='image', source=dict(type='base64', media_type='image/png', data='A' * (2 << 20)))]
        body = dict(model='grp', max_tokens=64, messages=messages)
        path = '/gateway/' + ('measure-' if packages else '') + 'anthropic-messages/v1/messages'
        response = versions.request(url + path, 'POST', body, {'x-elysia-session-id': session})
        if 'elysia-continuation.v1.' not in json.dumps(response):
            raise RuntimeError('initial response did not issue a continuation')
        versions.save_json(output / 'initial-response.json', response)
        content = [dict(type='text', text='hello')] if scene == 'session' else response['content']
        messages += [dict(role='assistant', content=content), dict(role='user', content='continue')]
        fixture = output / 'continuation-fixture.json'
        versions.save_json(fixture, {'Body': json.dumps(body, ensure_ascii=False, separators=(',', ':')), 'Session': session})
        versions.save_json(output / 'fixture-identity.json', {
            'fixtureSHA256': versions.digest(fixture), 'scene': scene,
            'historyMessages': 64, 'newUserMessages': 1,
            'recoveryProof': 'Separate provider requires the restored signed-text signature on every follow-up.',
        })
        return {'ELYSIA_FLAME_CONTINUATION_FIXTURE': str(fixture), 'ELYSIA_FLAME_SOURCE': ''}
    return prepare


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stages', required=True)
    parser.add_argument('--label', required=True)
    parser.add_argument('--driver', default='recovery-driver')
    parser.add_argument('--scenes', default='carrier,session')
    parser.add_argument('--rounds', type=int, default=5)
    parser.add_argument('--phase', choices=['standalone', 'counters'], default='standalone')
    parser.add_argument('--mode', choices=['measure', 'cpu', 'memory'], default='measure')
    parser.add_argument('--requests', type=int, default=256)
    args = parser.parse_args()
    stages = args.stages.split(',')
    if any(not re.fullmatch(r'[a-z0-9-]+', stage) for stage in stages + [args.driver]):
        parser.error('invalid frozen stage')
    fixture_build = versions.read_json(BASE / args.driver / 'builds.json')['versions']['C']
    provider = {'binary': fixture_build['instrumented'], 'binarySHA256': fixture_build['instrumentedSHA256']}
    for scene in args.scenes.split(','):
        if scene not in ['carrier', 'session', 'media-carrier']:
            parser.error('scene must be carrier, session or media-carrier')
        scenario = 'recovery-' + scene
        versions.SCENARIOS[scenario] = {'requests': args.requests, 'target': 'google-generate-content', 'ingress': 'anthropic-messages', 'continuation_media': scene == 'media-carrier'}
        for repeat in range(args.rounds):
            order = stages[repeat % len(stages):] + stages[:repeat % len(stages)]
            for stage in order:
                versions.EVIDENCE = BASE / stage
                output = BASE / args.label / f'{scenario}-{repeat+1}-{stage}'
                print(args.label, scene, repeat+1, stage, flush=True)
                versions.run_case('C-package', scenario, args.phase, args.mode, output, provider_fixture=provider, prepare_fixture=prepare_fixture(scene))
                record = versions.read_json(output / 'run.json')
                record.update(stage=stage, recoveryRunnerSHA256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest())
                versions.save_json(output / 'run.json', record)


if __name__ == '__main__':
    main()
