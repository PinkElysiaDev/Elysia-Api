"""Test offline rendering coordination without launching pprof or providers."""
import hashlib
import importlib.util
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location('summary', Path(__file__).with_name('performance-profile-summary.py'))
summary = importlib.util.module_from_spec(spec)
spec.loader.exec_module(summary)


class RenderTest(unittest.TestCase):
    def test_windows_viewer_cleanup_includes_its_children(self):
        child = Mock(pid=123)
        with patch.object(summary.os, 'name', 'nt'), patch.object(summary.subprocess, 'run') as run:
            summary.stop_viewer(child)
        self.assertEqual(run.call_args.args[0], ['taskkill', '/PID', '123', '/T', '/F'])
        child.terminate.assert_not_called()
        child.wait.assert_called_once_with(timeout=5)

    def test_independent_outputs_and_stable_manifest_order(self):
        barrier = threading.Barrier(4, timeout=5)
        def render(arguments, destination):
            barrier.wait()
            destination.write_text(arguments[0], encoding='utf-8')
        with tempfile.TemporaryDirectory() as directory, patch.object(summary, 'flame', render):
            jobs = [([str(i)], Path(directory) / f'{i}.html', {'file': f'{i}.html'}) for i in range(8)]
            manifest = summary.render_flames(jobs)
            self.assertEqual([row['file'] for row in manifest], [f'{i}.html' for i in range(8)])
            self.assertEqual([row['sha256'] for row in manifest], [hashlib.sha256(str(i).encode()).hexdigest() for i in range(8)])

    def test_failure_does_not_publish_partial_index(self):
        with patch.object(summary, 'flame', side_effect=RuntimeError('viewer failed')):
            with self.assertRaisesRegex(RuntimeError, 'viewer failed'):
                summary.render_flames([([], Path('unused.html'), {'file': 'unused.html'})])


if __name__ == '__main__':
    unittest.main()
