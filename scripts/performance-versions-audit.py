"""Supplement timed checks with exact request assertions; never compare its timings."""

import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
from pathlib import Path
import sys
import threading
import urllib.request

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location('versions', ROOT / 'scripts/performance-versions.py')
versions = importlib.util.module_from_spec(spec)
spec.loader.exec_module(versions)


def check_request(scene, document):
    assert document['model'] == 'm', 'model routing changed'
    messages = document['messages']
    if scene == 'history':
        assert len(messages) == 64, 'history message boundary changed'
        for index, message in enumerate(messages):
            assert message['role'] == ('assistant' if index % 2 else 'user'), 'history role changed'
            content = message['content']
            if isinstance(content, list):
                assert len(content) == 1 and content[0]['type'] == 'text'
                content = content[0]['text']
            assert content == 'h' * 4096, 'history payload changed'
    elif scene == 'tools':
        assert len(document['tools']) == 32
        schema = {'type': 'object', 'properties': {'query': {'type': 'string'}}, 'required': ['query']}
        for index, tool in enumerate(document['tools']):
            assert tool['type'] == 'function'
            assert tool['function']['name'] == f'lookup_{index}'
            assert tool['function']['parameters'] == schema, 'tool schema changed'
    elif scene == 'image':
        assert len(messages) == 2
        assert messages[0]['role'] == 'assistant' and messages[1]['role'] == 'user'
        assert messages[0]['content'] == [{'type': 'tool_use', 'id': 'c', 'name': 'lookup', 'input': {}}]
        result, = messages[1]['content']
        assert result['type'] == 'tool_result' and result['tool_use_id'] == 'c'
        image, = result['content']
        assert image == {'type': 'image', 'source': {'type': 'base64', 'media_type': 'image/png', 'data': 'A' * (2 << 20)}}


def main():
    configure = versions.configure_gateway
    for scene in ['history', 'tools', 'image']:
        for version in ['R-release', 'R-build', 'A', 'C-default', 'C-package']:
            output = versions.EVIDENCE / 'semantic-audit' / f'{scene}-{version}'
            checks, servers = [], []

            def audited_configuration(url, upstream, packages, scenario, directory):
                class Handler(BaseHTTPRequestHandler):
                    def do_POST(self):
                        try:
                            raw = self.rfile.read(int(self.headers['Content-Length']))
                            document = json.loads(raw)
                            check_request(scene, document)
                            if not checks:
                                versions.save_json(directory / 'first-upstream-request.json', document)
                            checks.append({'path': self.path, 'sha256': hashlib.sha256(raw).hexdigest()})
                            request = urllib.request.Request(upstream + self.path, data=raw,
                                headers={'Content-Type': 'application/json'}, method='POST')
                            with urllib.request.urlopen(request, timeout=120) as response:
                                body = response.read()
                                self.send_response(response.status)
                                self.send_header('Content-Type', response.headers['Content-Type'])
                                self.send_header('Content-Length', str(len(body)))
                                self.end_headers()
                                self.wfile.write(body)
                        except Exception as error:
                            checks.append({'error': repr(error)})
                            self.send_response(500)
                            self.end_headers()

                    def log_message(self, *args):
                        pass

                proxy = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
                servers.append(proxy)
                threading.Thread(target=proxy.serve_forever, daemon=True).start()
                return configure(url, f'http://127.0.0.1:{proxy.server_port}', packages, scenario, directory)

            versions.configure_gateway = audited_configuration
            try:
                print(scene, version, 'semantic audit', flush=True)
                versions.run_case(version, scene, 'standalone', 'measure', output, requests=4)
            finally:
                for server in servers:
                    server.shutdown()
                    server.server_close()
                versions.save_json(output / 'audit.json', {
                    'purpose': 'Exact request semantics only. Proxy overhead invalidates performance comparisons.',
                    'passed': bool(checks) and all('error' not in check for check in checks), 'requests': checks})


if __name__ == '__main__':
    main()
