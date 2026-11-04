"""Identity-only tests; never start a gateway or contact a provider."""
import copy
import hashlib
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('resume', Path(__file__).with_name('allocation-resume.py'))
resume = importlib.util.module_from_spec(spec)
spec.loader.exec_module(resume)


class ResumeIdentityTest(unittest.TestCase):
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
