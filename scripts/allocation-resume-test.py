"""Identity-only tests; never start a gateway or contact a provider."""
import copy
import hashlib
import importlib.util
from pathlib import Path
import unittest
import tempfile
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('resume', Path(__file__).with_name('allocation-resume.py'))
resume = importlib.util.module_from_spec(spec)
spec.loader.exec_module(resume)


class ResumeIdentityTest(unittest.TestCase):
    def test_micro_uses_each_test_packages_working_directory(self):
        allocation = resume.allocation
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            stage = base / 'candidate'
            stage.mkdir()
            build = dict(source=str(base / 'source'), protocol='protocol.exe',
                         instrumented='server.exe', builtin='builtin.exe')
            allocation.versions.save_json(stage / 'builds.json', {'versions': {'C': build}})
            directories = []
            def execute(command, cwd, log):
                directories.append(cwd)
                log.write_text('', encoding='utf-8')
            with patch.object(allocation, 'BASE', base), patch.object(allocation.versions, 'execute', execute):
                allocation.micro(['candidate'], 'measure', '^BenchmarkFixture$', 1, 1)
            self.assertEqual(directories, [base / 'source/backend' / package for package in
                                          ['protocol', 'server', 'protocol/builtin']])

    def test_current_matrix_keeps_both_chains_and_rotates_conditions(self):
        allocation = resume.allocation
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            calls = []
            def run_case(version, scene, phase, mode, output):
                output.mkdir(parents=True)
                calls.append((allocation.versions.EVIDENCE.name, version))
                allocation.versions.save_json(output / 'run.json', {'status': 'passed'})
            with patch.object(allocation, 'BASE', base), patch.object(allocation.versions, 'run_case', run_case):
                allocation.matrix(['parent', 'candidate'], 'measure', ['short'], 2, 'standalone', False)
            first = [('parent', 'C-default'), ('parent', 'C-package'), ('candidate', 'C-default'), ('candidate', 'C-package')]
            self.assertEqual(calls, first + first[1:] + first[:1])

    def test_identity_and_environment(self):
        build = {'C': {'sha256': 'binary', 'instrumentedSHA256': 'counter'}, 'driver': {'sha256': 'driver'}}
        record = dict(stage='candidate', scenario='short', phase='standalone', mode='measure', version='C-package',
                      binarySHA256='binary', driverSHA256='driver', runnerSHA256=hashlib.sha256(resume.versions.RUNNER_SOURCE).hexdigest(), environment=dict(resume.versions.RUNTIME))
        resume.check_completed(record, 'candidate', 'short', 'standalone', build)
        for key in ['stage', 'scenario', 'phase', 'mode', 'version', 'binarySHA256', 'driverSHA256', 'runnerSHA256']:
            changed = record | {key: 'different'}
            with self.subTest(key=key), self.assertRaises(RuntimeError):
                resume.check_completed(changed, 'candidate', 'short', 'standalone', build)
        changed = copy.deepcopy(record)
        changed['environment']['GOGC'] = '200'
        with self.assertRaises(RuntimeError):
            resume.check_completed(changed, 'candidate', 'short', 'standalone', build)
        record.update(phase='counters', binarySHA256='counter')
        resume.check_completed(record, 'candidate', 'short', 'counters', build)
        build['A'] = {'sha256': 'historical', 'instrumentedSHA256': 'historical-counter'}
        record.update(stage='A', version='A', binarySHA256='historical-counter')
        resume.check_completed(record, 'A', 'short', 'counters', build, 'A')
        with self.assertRaises(RuntimeError):
            resume.check_completed(record, 'A', 'short', 'counters', build, 'C-default')


if __name__ == '__main__':
    unittest.main()
