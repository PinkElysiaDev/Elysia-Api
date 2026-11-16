"""Exercise production measurement setup without processes, builds or network calls."""

import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parent.parent


def load_script(name):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts' / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


versions = load_script('performance-versions')
allocation = load_script('allocation-optimization')


class ProductionConfigurationTest(unittest.TestCase):
    def setUp(self):
        self.documents = {}
        for protocol_id, path in [('openai-chat-completions', '/candidate/chat'), ('anthropic-messages', '/candidate/messages')]:
            operations = {name: {'kind': 'generate', 'method': 'POST', 'path': path, 'transport': transport}
                          for name, transport in [('generate', 'http_json'), ('stream', 'sse')]}
            self.documents[protocol_id] = {'schemaVersion': 4, 'spec': {'id': protocol_id, 'operations': operations},
                                           'mapping': {'coreVersion': 2, 'dslVersion': 2,
                                                       'directions': {name: {} for name in versions.CORE_DIRECTIONS}}}
        self.loaded = {name: name + '-revision' for name in self.documents}
        self.compiler = 'compiler-current'
        self.responses = {
            'protocols': {'runtimeReady': True, 'loaded': self.loaded,
                          'active': [{'protocolId': key, 'revisionHash': value} for key, value in self.loaded.items()]},
            'protocols/schema': {'packageVersion': 4, 'compilerVersion': self.compiler},
            'protocols/enabled': {'items': [{'id': name, 'revision': revision, 'preset': True,
                                            'directions': sorted(versions.CORE_DIRECTIONS), 'capabilities': {'text': True}}
                                           for name, revision in self.loaded.items()]},
        }
        for name, document in self.documents.items():
            self.responses[f'protocols/{name}/revisions/{self.loaded[name]}'] = {
                'revision': {'hash': self.loaded[name], 'definition': copy.deepcopy(document)},
                'report': {'passed': True, 'kind': 'offline', 'compilerVersion': self.compiler,
                           'definitionHash': self.loaded[name], 'samplesHash': name + '-samples'},
            }
        self.report = {'passed': True, 'kind': 'offline', 'compilerVersion': self.compiler,
                       'sourceHash': self.loaded['openai-chat-completions'], 'targetHash': self.loaded['anthropic-messages'],
                       'sourceSamplesHash': 'openai-chat-completions-samples', 'targetSamplesHash': 'anthropic-messages-samples',
                       'contextHash': 'context', 'policyHash': 'policy', 'transport': 'sse'}
        self.binding = {'kind': 'source', 'sourceId': 's', 'binding': {'protocolId': 'anthropic-messages',
                        'revisionHash': self.loaded['anthropic-messages'], 'transports': ['http_json', 'sse']},
                        'combinations': [self.report]}
        self.responses['protocols/bindings'] = [self.binding]
        self.calls = []

    def admin(self, url, path, method='GET', body=None):
        self.calls.append((path, method, copy.deepcopy(body)))
        if method != 'GET':
            return {}
        return copy.deepcopy(self.responses[path])

    def configuration(self):
        with patch.object(versions, 'admin', self.admin):
            return versions.read_production_configuration('http://127.0.0.1', self.documents, self.compiler,
                                                          'frozen-commit', {'cross': True, 'stream': True})

    def test_production_uses_separate_pinned_binding_and_declared_paths(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(versions, 'admin', self.admin):
            result = versions.configure_production_gateway('http://127.0.0.1', 'http://127.0.0.1:9001',
                     {'cross': True, 'stream': True}, Path(directory), self.documents, self.compiler, 'frozen-commit')
        writes = [(path, method, body) for path, method, body in self.calls if method != 'GET']
        self.assertEqual([entry[:2] for entry in writes], [('model-sources', 'POST'), ('protocols/bindings', 'PUT'),
                                                        ('model-groups', 'POST'), ('api-tokens', 'POST')])
        self.assertEqual(writes[0][2]['baseUrl'], 'http://127.0.0.1:9001')
        self.assertNotIn('platform', writes[0][2])
        self.assertNotIn('protocolBinding', writes[0][2])
        self.assertEqual(writes[1][2]['binding']['revisionHash'], self.loaded['anthropic-messages'])
        self.assertEqual(result['route']['ingressPath'], '/gateway/openai-chat-completions/candidate/chat')
        self.assertEqual(result['route']['upstreamPath'], '/candidate/messages')
        self.assertEqual(result['execution']['Execution'], 'production')
        self.assertEqual(result['execution']['Commit'], 'frozen-commit')
        self.assertTrue(result['execution']['Policies'])

    def test_rejects_stale_or_different_defaults(self):
        path = 'protocols/openai-chat-completions/revisions/' + self.loaded['openai-chat-completions']
        changes = [
            ('not-ready', lambda: self.responses['protocols'].update(runtimeReady=False)),
            ('old-compiler', lambda: self.responses['protocols/schema'].update(compilerVersion='old')),
            ('activation-drift', lambda: self.responses['protocols']['active'][0].update(revisionHash='other')),
            ('legacy-definition', lambda: self.responses[path]['revision'].update(definition={'schemaVersion': 2})),
            ('modified-mapping', lambda: self.responses[path]['revision']['definition']['mapping'].update(native={'preserve': False})),
            ('failed-verification', lambda: self.responses[path]['report'].update(passed=False)),
            ('upstream-only', lambda: self.responses[path]['report'].update(kind='upstream')),
            ('stale-verification', lambda: self.responses[path]['report'].update(compilerVersion='old')),
        ]
        original = copy.deepcopy(self.responses)
        for name, change in changes:
            with self.subTest(name=name):
                self.responses = copy.deepcopy(original)
                change()
                with self.assertRaises(RuntimeError):
                    self.configuration()

    def test_rejects_stale_binding_and_wrong_transport_evidence(self):
        changes = [
            ('unbound', lambda: self.binding.update(unbound=True)),
            ('stale-revision', lambda: self.binding['binding'].update(revisionHash='old')),
            ('failed-combination', lambda: self.report.update(passed=False)),
            ('wrong-transport', lambda: self.report.update(transport='http_json')),
            ('stale-samples', lambda: self.report.update(sourceSamplesHash='old')),
            ('missing-policy', lambda: self.report.update(policyHash='')),
        ]
        original = copy.deepcopy(self.binding)
        for name, change in changes:
            with self.subTest(name=name):
                self.binding = copy.deepcopy(original)
                self.report = self.binding['combinations'][0]
                self.responses['protocols/bindings'] = [self.binding]
                change()
                with self.assertRaises(RuntimeError):
                    self.configuration()

    def test_historical_default_configuration_is_unchanged(self):
        for name, scenario in versions.SCENARIOS.items():
            self.calls.clear()
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory, patch.object(versions, 'admin', self.admin):
                result = versions.configure_gateway('http://127.0.0.1', 'http://127.0.0.1:9001', False, scenario, Path(directory))
            source = self.calls[0][2]
            target = 'anthropic-messages' if scenario.get('cross') or scenario.get('image') else 'openai-chat-completions'
            self.assertEqual(source['platform'], 'custom:' + target)
            self.assertEqual(source['baseUrl'], 'http://127.0.0.1:9001' + ('/v1' if target == 'openai-chat-completions' else ''))
            self.assertEqual([call[:2] for call in self.calls], [('model-sources', 'POST'), ('model-groups', 'POST'),
                             ('api-tokens', 'POST'), ('protocols/bindings', 'GET'), ('protocols', 'GET')])
            self.assertEqual(set(result), {'protocols', 'bindings'})


class CutoverHarnessTest(unittest.TestCase):
    def test_fixed_workload_counts(self):
        self.assertEqual({name: scene['requests'] for name, scene in versions.SCENARIOS.items()},
                         {'short': 4096, 'history': 256, 'tools': 1024, 'burst': 4096, 'stream': 128,
                          'image': 128, 'cross-json': 512, 'cross-stream': 128})
        self.assertEqual(versions.RUNTIME, {'GOMAXPROCS': '2', 'GOGC': '100', 'GOMEMLIMIT': '2GiB',
                                           'CGO_ENABLED': '0', 'GOTOOLCHAIN': 'local'})

    def test_five_rounds_alternate_d0_and_production_without_changing_counts(self):
        calls = []
        conditions = [('backend-p0-default', Path('baseline'), 'C-default'),
                      ('candidate-production', Path('candidate'), 'C-production')]

        def run_case(version, scene, phase, mode, output, driver_cwd):
            calls.append((version, scene, phase, mode, allocation.versions.EVIDENCE, output.name, driver_cwd))

        with patch.object(allocation.versions, 'run_case', run_case), patch.object(allocation.versions, 'read_json', return_value={}), \
             patch.object(allocation.versions, 'save_json'), patch('builtins.print'):
            allocation.run_matrix(conditions, 'label', list(versions.SCENARIOS), 5, 'standalone', Path('frozen-driver-fixtures'))
        self.assertEqual(len(calls), 80)
        for offset, scene in enumerate(versions.SCENARIOS):
            for repeat in range(5):
                pair = calls[offset * 10 + repeat * 2:offset * 10 + repeat * 2 + 2]
                expected = ['C-default', 'C-production'] if repeat % 2 == 0 else ['C-production', 'C-default']
                self.assertEqual([call[0] for call in pair], expected)
                self.assertTrue(all(call[1:4] == (scene, 'standalone', 'measure') for call in pair))
                self.assertTrue(all(call[6] == Path('frozen-driver-fixtures') for call in pair))

    def test_frozen_preflight_rejects_modified_source(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            stage = root / 'candidate'
            source = stage / 'C/source'
            source.mkdir(parents=True)
            (source / 'fixture.go').write_text('frozen source', encoding='utf-8')
            manifest = {'fixture.go': versions.digest(source / 'fixture.go')}
            build = {'source': str(source), 'sourceSHA256': hashlib.sha256(json.dumps(manifest, sort_keys=True).encode()).hexdigest(),
                     'buildInfo': {name: 'fixture: go1.27.2\n' for name in ['binary', 'instrumented']}}
            for name, hash_key in [('binary', 'sha256'), ('instrumented', 'instrumentedSHA256')]:
                binary = stage / (name + '.exe')
                binary.write_bytes(name.encode())
                build[name], build[hash_key] = str(binary), versions.digest(binary)
            driver = stage / 'driver.exe'
            driver.write_bytes(b'fixed driver')
            versions.save_json(stage / 'source-manifest.json', manifest)
            versions.save_json(stage / 'builds.json', {'runtime': versions.RUNTIME, 'versions': {'C': build,
                               'driver': {'binary': str(driver), 'sha256': versions.digest(driver)}}})
            with patch.object(allocation, 'BASE', root), patch.object(allocation, 'FIXED_DRIVER_SHA256', versions.digest(driver)):
                self.assertEqual(allocation.verify_frozen_stage('candidate'), build)
                (source / 'fixture.go').write_text('edited source', encoding='utf-8')
                with self.assertRaisesRegex(RuntimeError, 'frozen source differs'):
                    allocation.verify_frozen_stage('candidate')


if __name__ == '__main__':
    unittest.main()
