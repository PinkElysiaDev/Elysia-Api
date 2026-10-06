#!/usr/bin/python3
"""Local-only native integration fixture; never used in a release bundle."""
import json
import os
import signal
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlsplit

root = Path(os.environ['ELYSIA_NATIVE_TEST_DATA'])
assert os.environ.get('ELYSIA_API_OPEN_BROWSER') == 'false', 'native host must suppress automatic browser launch on every start'
config = json.loads((root / 'config.json').read_text())
count = root / 'starts.txt'
count.write_text(str(int(count.read_text() if count.exists() else '0') + 1))
mode = root / 'fixture-mode'
if mode.exists() and mode.read_text() == 'crash':
    sys.exit(23)

shutdown_started = threading.Event()
heartbeat_lock = threading.Lock()
heartbeat_count = 0

def request_shutdown(reason):
    current_mode = mode.read_text() if mode.exists() else ''
    if current_mode == 'stubborn' or shutdown_started.is_set():
        return
    shutdown_started.set()
    def stop():
        if current_mode == 'drain':
            time.sleep(4)
            (root / 'drained').write_text(reason)
        server.shutdown()
    threading.Thread(target=stop, daemon=True).start()

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        if self.path.startswith('/ui/tray-open?'):
            opened = int(parse_qs(urlsplit(self.path).query)['count'][0])
            with heartbeat_lock:
                temporary = root / 'tray-open-count.next'
                temporary.write_text(str(opened))
                temporary.replace(root / 'tray-open-count.txt')
            self.send_response(204)
            self.end_headers()
        elif self.path == '/ui/tray-heartbeat':
            global heartbeat_count
            with heartbeat_lock:
                heartbeat_count += 1
                temporary = root / 'tray-heartbeats.next'
                temporary.write_text(str(heartbeat_count))
                temporary.replace(root / 'tray-heartbeats.txt')
            self.send_response(204)
            self.end_headers()
        elif self.path == '/health':
            self.send_response(503 if mode.exists() and mode.read_text() == 'unhealthy' else 200)
            self.end_headers()
            self.wfile.write(b'{}')
        elif self.path in ['/ui/export', '/ui/export-slow']:
            slow = self.path.endswith('-slow')
            self.send_response(200)
            self.send_header('Content-Type', 'application/octet-stream')
            self.send_header('Content-Disposition', 'attachment; filename="native-export.txt"')
            self.send_header('Content-Length', '10485760' if slow else '3')
            self.end_headers()
            try:
                if slow:
                    for _ in range(10240):
                        self.wfile.write(b'x' * 1024)
                        self.wfile.flush()
                        time.sleep(.01)
                else:
                    self.wfile.write(b'abc')
            except (BrokenPipeError, ConnectionResetError):
                pass
        elif self.path.startswith('/ui/'):
            data = b'''<!doctype html><html><head><title>Native test panel</title>
            <style>body{font:16px -apple-system;background:#f5f5f7;padding:24px}.dark body{background:#10121a;color:white}</style>
            </head><body><main><h1>Elysia API</h1><p>Native window integration fixture</p></main></body></html>'''
            if mode.exists() and mode.read_text() == 'broken-ui':
                data = b'<!doctype html><html><head><title>Broken WebUI</title></head><body><div id="root"></div></body></html>'
            elif mode.exists() and mode.read_text() == 'tray-fixture':
                data = data.replace(b'</body>', b'''<script>
                window.trayTestAllocation = new Uint8Array(32 * 1024 * 1024);
                window.trayTestAllocation.fill(1);
                const opens = Number(localStorage.getItem('elysia-native-tray-opens') || 0) + 1;
                localStorage.setItem('elysia-native-tray-opens', String(opens));
                fetch('/ui/tray-open?count=' + opens).catch(() => {});
                setInterval(() => fetch('/ui/tray-heartbeat').catch(() => {}), 200);
                </script></body>''')
            self.send_response(200)
            self.send_header('Content-Type', 'text/html')
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            self.wfile.write(data)
        elif self.path == '/download':
            self.send_response(200)
            self.send_header('Content-Length', '3')
            self.end_headers()
            self.wfile.write(b'abc')
        elif self.path == '/slow':
            self.send_response(200)
            self.send_header('Content-Length', '10485760')
            self.end_headers()
            try:
                for _ in range(10240):
                    self.wfile.write(b'x' * 1024)
                    self.wfile.flush()
                    time.sleep(.01)
            except (BrokenPipeError, ConnectionResetError):
                pass
        else:
            self.send_response(404)
            self.end_headers()
            self.wfile.write(b'not a dmg')

    def do_POST(self):
        self.send_response(200)
        self.end_headers()
        request_shutdown('HTTP shutdown')

server = ThreadingHTTPServer(('127.0.0.1', config['port']), Handler)
server.daemon_threads = True
signal.signal(signal.SIGTERM, lambda *_: request_shutdown('SIGTERM'))
if os.environ.get('ELYSIA_PARENT_STDIN') == '1':
    def watch_parent():
        while os.read(0, 1024):
            pass
        request_shutdown('parent EOF')
    threading.Thread(target=watch_parent, daemon=True).start()
server.serve_forever(poll_interval=.05)
server.server_close()
