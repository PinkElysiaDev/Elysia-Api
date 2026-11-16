"""Check current standalone production configuration without performance requests."""

import argparse
from datetime import datetime, timezone
import importlib.util
from pathlib import Path
import tempfile

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('versions', ROOT / 'scripts/performance-versions.py')
versions = importlib.util.module_from_spec(spec)
spec.loader.exec_module(versions)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, default=ROOT / 'dist/standalone/elysia-api-windows-amd64.exe')
    parser.add_argument('--source', type=Path, default=ROOT)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    args.output.mkdir(parents=True)
    documents = versions.read_production_packages(args.source)
    compiler = versions.read_production_compiler(args.source)
    results = []
    for scene in ['short', 'cross-stream']:
        output = args.output / scene
        output.mkdir()
        temporary = Path(tempfile.mkdtemp(prefix='elysia-d0-configuration-'))
        port = versions.free_port()
        url = f'http://127.0.0.1:{port}'
        config_path = temporary / 'config.json'
        versions.save_json(config_path, dict(host='127.0.0.1', port=port, panelAccessToken=versions.TOKEN,
            databasePath=str(temporary / 'app.sqlite3'), logDatabasePath=str(temporary / 'logs.sqlite3'),
            secretKeyPath=str(temporary / '.master-key'), logLevel='error', logPersistLevel='error',
            openBrowserOnStart=False, modelCatalog={'enabled': False, 'syncIntervalMinutes': 0},
            agentRemote={'enabled': False}, outbound={'deniedIpRanges': []}))
        children = versions.Children()
        command = [str(args.binary.resolve()), '-config', str(config_path)]
        record = {'scene': scene, 'status': 'running', 'command': command, 'directory': str(temporary),
                  'binarySHA256': versions.digest(args.binary), 'compiler': compiler,
                  'created': datetime.now(timezone.utc).isoformat(), 'performanceRequests': 0}
        try:
            child = children.start(command, temporary, versions.environment() | {'ELYSIA_PARENT_STDIN': '1'}, output / 'gateway.log')
            record['health'] = versions.wait_gateway(url, child)
            # Configuration only: a reserved loopback port cannot contact a real provider.
            upstream = f'http://127.0.0.1:{versions.free_port()}'
            before = versions.configure_production_gateway(url, upstream, versions.SCENARIOS[scene], output,
                       documents, compiler, record['health']['commit'])
            versions.admin(url, 'protocols/reload', 'POST', {})
            after = versions.read_production_configuration(url, documents, compiler,
                    record['health']['commit'], versions.SCENARIOS[scene])
            if before != after:
                raise RuntimeError('reload changed production configuration')
            record.update(status='passed', before=before, after=after)
        except Exception as error:
            record.update(status='failed', error=str(error))
            raise
        finally:
            record['children'] = children.close()
            if any(child['exitCode'] != 0 for child in record['children']):
                record.update(status='failed', error='standalone child did not exit successfully')
            versions.save_json(output / 'smoke.json', record)
            results.append(record)
            versions.save_json(args.output / 'report.json', {'results': results})
        if record['status'] != 'passed':
            raise RuntimeError(record['error'])
        print(scene, 'configuration and reload passed', flush=True)


if __name__ == '__main__':
    main()
