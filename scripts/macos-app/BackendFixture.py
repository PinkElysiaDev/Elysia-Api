#!/usr/bin/python3
"""Local-only native integration fixture; never used in a release bundle."""
import json
import os
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

root = Path(os.environ['ELYSIA_NATIVE_TEST_DATA'])
config = json.loads((root / 'config.json').read_text())
count = root / 'starts.txt'
count.write_text(str(int(count.read_text() if count.exists() else '0') + 1))
mode = root / 'fixture-mode'
if mode.exists() and mode.read_text() == 'crash':
    sys.exit(23)

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        if self.path == '/health':
            self.send_response(503 if mode.exists() and mode.read_text() == 'unhealthy' else 200)
            self.end_headers()
            self.wfile.write(b'{}')
        elif self.path.startswith('/ui/'):
            data = b'''<!doctype html><html><head><title>Native test panel</title>
            <style>body{font:16px -apple-system;background:#f5f5f7;padding:24px}.dark body{background:#10121a;color:white}</style>
            </head><body><h1>Elysia API</h1><p>Native window integration fixture</p></body></html>'''
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
        threading.Thread(target=self.server.shutdown, daemon=True).start()

server = ThreadingHTTPServer(('127.0.0.1', config['port']), Handler)
server.serve_forever(poll_interval=.05)
server.server_close()
