"""Compare pinned standalone gateways and matching test-only counters on Windows."""

import argparse
import ctypes
from ctypes import wintypes
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import socket
import subprocess
import threading
import time
import urllib.error
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parent.parent
RUNNER_SOURCE = Path(__file__).read_bytes()
EVIDENCE = ROOT / '.tmp-dev/three-version-2026-11-04'
TOKEN = 'synthetic-local-profile'
REVISIONS = {'R-build': '9b72829a', 'A': 'b2844846', 'C': 'd7e29817'}
RELEASE_URL = 'https://github.com/PinkElysiaDev/Elysia-Api/releases/download/v1.7.0/elysia-api-windows-amd64.exe'
RELEASE_SHA = '06b878052d2cd9a16f1b7a0be2359257116f6d318945b1fd912645db48644970'
RUNTIME = {'GOMAXPROCS': '2', 'GOGC': '100', 'GOMEMLIMIT': '2GiB', 'CGO_ENABLED': '0', 'GOTOOLCHAIN': 'local'}
SCENARIOS = {
    'short': {'requests': 4096},
    'history': {'requests': 256, 'request': 'history256k'},
    'tools': {'requests': 1024, 'request': 'tools32'},
    'burst': {'requests': 4096, 'workers': 4},
    'stream': {'requests': 128, 'stream': True},
    'image': {'requests': 128, 'image': True},
    'cross-json': {'requests': 512, 'cross': True},
    'cross-stream': {'requests': 128, 'cross': True, 'stream': True},
}
PACKAGES = ['openai-chat', 'openai-responses', 'anthropic', 'google-generate-content']


def read_json(path):
    return json.loads(Path(path).read_text(encoding='utf-8'))


def save_json(path, value):
    Path(path).write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding='utf-8')


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def environment():
    return dict({k: v for k, v in os.environ.items() if not k.startswith(('ELYSIA_', 'GOFLAGS'))}, **RUNTIME)


def execute(command, directory, log, env=None):
    with Path(log).open('wb') as output:
        subprocess.run(command, cwd=directory, env=env or environment(), stdout=output,
                       stderr=subprocess.STDOUT, check=True, creationflags=subprocess.CREATE_NO_WINDOW)


def prepare():
    EVIDENCE.mkdir(parents=True)
    release = EVIDENCE / 'R-release.exe'
    with urllib.request.urlopen(RELEASE_URL, timeout=60) as response:
        release.write_bytes(response.read())
    assert digest(release) == RELEASE_SHA, 'release checksum differs'
    versions = {'R-release': {'binary': str(release), 'sha256': RELEASE_SHA, 'url': RELEASE_URL,
                              'commit': '9b72829a40a6f73cbe82604a6d04e397d8bdf634'}}
    for name, revision in REVISIONS.items():
        directory = EVIDENCE / name
        directory.mkdir()
        commit = subprocess.check_output(['git', 'rev-parse', revision], cwd=ROOT, text=True).strip()
        archive = directory / 'source.zip'
        subprocess.run(['git', 'archive', '--format=zip', '--output=' + str(archive), commit, 'backend'], cwd=ROOT, check=True)
        source = directory / 'source'
        with zipfile.ZipFile(archive) as snapshot:
            snapshot.extractall(source)
        backend = source / 'backend'
        manifest = {str(p.relative_to(source)).replace('\\', '/'): digest(p) for p in sorted(backend.rglob('*')) if p.is_file()}
        save_json(directory / 'source-manifest.json', manifest)
        binary = directory / 'gateway.exe'
        flags = '-s -w -X github.com/elysia-api/backend/server.AppVersion=1.7.0 -X github.com/elysia-api/backend/server.AppCommit=' + commit[:8]
        command = ['go', 'build', '-buildvcs=false', '-pgo=off', '-ldflags', flags, '-o', str(binary), '.']
        execute(command, backend, directory / 'build.log')
        collector = ROOT / 'backend/server/performance_versioned_process_test.go'
        (backend / 'server/performance_versioned_process_test.go').write_bytes(collector.read_bytes())
        instrumented = directory / 'gateway.test.exe'
        test_command = ['go', 'test', '-c', '-buildvcs=false', '-pgo=off', '-ldflags', flags, '-o', str(instrumented), './server']
        execute(test_command, backend, directory / 'build-test.log')
        versions[name] = dict(commit=commit, binary=str(binary), sha256=digest(binary), instrumented=str(instrumented),
                              instrumentedSHA256=digest(instrumented), source=str(source), archiveSHA256=digest(archive),
                              collectorSHA256=digest(collector), build=command, testBuild=test_command)
        print('built', name, flush=True)
    driver = EVIDENCE / 'driver.test.exe'
    execute(['go', 'test', '-c', '-buildvcs=false', '-pgo=off', '-o', str(driver), './server'], ROOT / 'backend', EVIDENCE / 'build-driver.log')
    versions['driver'] = {'binary': str(driver), 'sha256': digest(driver)}
    for version in versions.values():
        version['buildInfo'] = subprocess.check_output(['go', 'version', '-m', version['binary']], text=True)
    save_json(EVIDENCE / 'builds.json', {'runtime': RUNTIME, 'versions': versions,
              'rebuiltAssets': 'Backend git archives retain tracked embed placeholders; release assets remain unmodified. No WebUI requests are measured.'})


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def request(url, method='GET', body=None, extra_headers=None):
    raw = None if body is None else json.dumps(body, ensure_ascii=False).encode()
    headers = {'Authorization': 'Bearer ' + TOKEN, 'Content-Type': 'application/json'}
    headers.update(extra_headers or {})
    req = urllib.request.Request(url, data=raw, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=120) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        raise RuntimeError(f'{method} {url}: HTTP {error.code} {error.read().decode()[:2500]}') from error


def admin(url, path, method='GET', body=None):
    result = request(url + '/api/admin/' + path, method, body)
    if not result['ok']:
        raise RuntimeError(result)
    return result['data']


class Children:
    def __init__(self):
        self.children = []

    def start(self, command, cwd, env, log):
        output = Path(log).open('wb')
        child = subprocess.Popen(command, cwd=cwd, env=env, stdin=subprocess.PIPE, stdout=output,
                                 stderr=subprocess.STDOUT, creationflags=subprocess.CREATE_NO_WINDOW)
        self.children.append((child, output))
        return child

    def close(self):
        results = []
        for child, output in reversed(self.children):
            if child.poll() is None:
                try:
                    child.stdin.write(b'\n')
                    child.stdin.close()
                    child.wait(timeout=30)
                except (OSError, subprocess.TimeoutExpired):
                    child.kill()
                    child.wait(timeout=10)
            output.close()
            results.append({'pid': child.pid, 'exitCode': child.returncode})
        return results


def wait_file(path, child):
    deadline = time.monotonic() + 90
    while not path.exists():
        if child.poll() is not None or time.monotonic() > deadline:
            raise RuntimeError(f'child failed before ready: {path}, exit={child.poll()}')
        time.sleep(.05)
    return read_json(path)


def wait_gateway(url, child):
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        try:
            return request(url + '/health')
        except (urllib.error.URLError, ConnectionError):
            if child.poll() is not None:
                raise RuntimeError(f'gateway exited {child.returncode}')
            time.sleep(.05)
    raise RuntimeError('gateway startup timeout')


def active_protocols(url):
    return admin(url, 'protocols')['loaded']


def configure_gateway(url, upstream, packages, scenario, directory):
    if packages:
        for name in PACKAGES:
            path = EVIDENCE / 'C/source/backend/protocol/builtin/packages' / name
            spec, mapping = read_json(path / 'spec.json'), read_json(path / 'mapping.json')
            original_id = spec['id']
            protocol_id = 'measure-' + original_id
            spec['id'] = protocol_id
            def rename_identity(value):
                if isinstance(value, dict):
                    for key, child in value.items():
                        if key == 'definitionId' and child == original_id:
                            value[key] = protocol_id
                        else:
                            rename_identity(child)
                elif isinstance(value, list):
                    for child in value:
                        rename_identity(child)
            rename_identity(mapping)
            document = {'schemaVersion': 4, 'spec': spec, 'mapping': mapping}
            save_json(directory / (name + '-package.json'), document)
            draft = request(url + f'/api/admin/protocols/{protocol_id}/draft', 'PUT', document)['data']['draft']
            verified = admin(url, f'protocols/{protocol_id}/verify', 'POST', {'draftHash': draft['hash']})
            if not verified['report']['passed']:
                raise RuntimeError(verified['report'])
            # Activation is pinned to the loaded revision, never inferred from a version tag.
            revisions = admin(url, f'protocols/{protocol_id}/revisions')
            save_json(directory / (name + '-revisions.json'), revisions)
            admin(url, f'protocols/{protocol_id}/activate', 'POST', {'revisionHash': verified['revision']['hash'], 'expectedActive': ''})
    target = scenario.get('target') or ('anthropic-messages' if scenario.get('cross') or scenario.get('image') else 'openai-chat-completions')
    base = upstream + ('/v1' if target == 'openai-chat-completions' and not packages else '')
    target_id = 'measure-' + target if packages else target
    source = dict(id='s', name='local', baseUrl=base, apiKey='synthetic', platform='custom:' + target_id,
                  enabled=True, autoFetchModels=False, manualModels=[dict(id='m', name='m', enabled=True,
                  available=True, origin='manual', toolsCapable=True, visionCapable=True)])
    admin(url, 'model-sources', 'POST', source)
    admin(url, 'model-groups', 'POST', dict(id='g', name='grp', enabled=True, models=['s:m'],
          strategy='round-robin', maxRetries=0, toolsCapable=True, visionCapable=True))
    admin(url, 'api-tokens', 'POST', dict(name='profile', token=TOKEN, enabled=True, allowedGroups=['grp']))
    bindings = admin(url, 'protocols/bindings')
    save_json(directory / 'bindings.json', bindings)
    return {'protocols': active_protocols(url), 'bindings': bindings}


class ProcessMemoryCounters(ctypes.Structure):
    _fields_ = [('cb', wintypes.DWORD), ('PageFaultCount', wintypes.DWORD)] + [(name, ctypes.c_size_t) for name in
        ['PeakWorkingSetSize', 'WorkingSetSize', 'QuotaPeakPagedPoolUsage', 'QuotaPagedPoolUsage',
         'QuotaPeakNonPagedPoolUsage', 'QuotaNonPagedPoolUsage', 'PagefileUsage', 'PeakPagefileUsage']]


class ProcessMetrics:
    def __init__(self, pid, gateway, counter_url):
        kernel = ctypes.WinDLL('kernel32', use_last_error=True)
        kernel.OpenProcess.restype = wintypes.HANDLE
        self.handle = kernel.OpenProcess(0x1000 | 0x10, False, pid)
        if not self.handle:
            raise ctypes.WinError(ctypes.get_last_error())
        self.kernel, self.psapi = kernel, ctypes.WinDLL('psapi', use_last_error=True)
        self.kernel.GetProcessTimes.argtypes = [wintypes.HANDLE] + [ctypes.POINTER(wintypes.FILETIME)] * 4
        self.psapi.GetProcessMemoryInfo.argtypes = [wintypes.HANDLE, ctypes.POINTER(ProcessMemoryCounters), wintypes.DWORD]
        self.kernel.CloseHandle.argtypes = [wintypes.HANDLE]
        self.gateway, self.counter_url = gateway, counter_url
        self.base_cpu, self.peak = 0, 0
        self.base_gc = 0
        self.lock = threading.Lock()
        self.started = time.monotonic()
        self.stop = threading.Event()
        self.thread = threading.Thread(target=self.sample, daemon=True)
        self.thread.start()

    def process(self):
        times = [wintypes.FILETIME() for _ in range(4)]
        if not self.kernel.GetProcessTimes(self.handle, *(ctypes.byref(x) for x in times)):
            raise ctypes.WinError(ctypes.get_last_error())
        cpu = sum((x.dwHighDateTime << 32) | x.dwLowDateTime for x in times[2:]) * 100
        memory = ProcessMemoryCounters()
        memory.cb = ctypes.sizeof(memory)
        if not self.psapi.GetProcessMemoryInfo(self.handle, ctypes.byref(memory), memory.cb):
            raise ctypes.WinError(ctypes.get_last_error())
        return cpu, memory.WorkingSetSize

    def sample(self):
        while not self.stop.wait(.02):
            sampled_at = time.monotonic()
            working = self.process()[1]
            with self.lock:
                if sampled_at >= self.started:
                    self.peak = max(self.peak, working)

    def snapshot(self, action):
        if action == 'reset':
            # Diagnostic profiles settle the allocator before their baseline.
            # Timed batches always use reset-without-gc.
            for _ in range(2):
                req = urllib.request.Request(self.gateway + '/debug/pprof/heap?gc=1',
                                             headers={'Authorization': 'Bearer ' + TOKEN})
                with urllib.request.urlopen(req, timeout=30) as response:
                    response.read()
        if action == 'settle':
            deadline = time.monotonic() + 30
            while True:
                writers = admin(self.gateway, 'usage/storage')['writers']['usage']
                if writers.get('failures', 0):
                    raise RuntimeError(writers)
                if not writers.get('queued', 0) and not writers.get('retainedBytes', 0):
                    break
                if time.monotonic() > deadline:
                    raise RuntimeError('usage writer did not settle')
                time.sleep(.01)
        cpu, working = self.process()
        with self.lock:
            if action in ['reset', 'reset-without-gc']:
                self.base_cpu, self.peak = cpu, working
                self.started = time.monotonic()
            peak = max(self.peak, working)
        if self.counter_url:
            counters = request(self.counter_url + '?action=' + ('reset-without-gc' if action == 'reset' else action))
        else:
            memory = admin(self.gateway, 'health')['memory']
            if action in ['reset', 'reset-without-gc']:
                self.base_gc = memory['numGC']
            counters = {'Heap': memory['alloc'], 'GCCycles': memory['numGC'] - self.base_gc}
        counters.update(CPUTimeNS=cpu-self.base_cpu, WorkingSet=working, PeakWorkingSet=peak)
        return counters

    def close(self):
        self.stop.set()
        self.thread.join()
        self.kernel.CloseHandle(self.handle)


def serve_metrics(metrics):
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            from urllib.parse import parse_qs, urlparse
            try:
                body = json.dumps(metrics.snapshot(parse_qs(urlparse(self.path).query).get('action', [''])[0])).encode()
                self.send_response(200)
            except Exception as error:
                body = str(error).encode()
                self.send_response(500)
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *args):
            pass
    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


def run_case(version, scene, phase, mode, output, requests=None, provider_fixture=None, prepare_fixture=None):
    output.mkdir(parents=True)
    builds = read_json(EVIDENCE / 'builds.json')['versions']
    variant = 'C' if version.startswith('C-') else version
    is_instrumented = phase != 'standalone'
    binary = builds[variant]['instrumented' if is_instrumented else 'binary']
    expected_sha = builds[variant]['instrumentedSHA256' if is_instrumented else 'sha256']
    if phase == 'attribution':
        assert variant == 'C', 'call-counter overlay is current-only'
        binary = str(EVIDENCE / 'attribution-build/gateway.test.exe')
        expected_sha = read_json(EVIDENCE / 'attribution-build/build.json')['binarySHA256']
    assert digest(binary) == expected_sha
    packages = version == 'C-package'
    scenario = SCENARIOS[scene]
    env = environment()
    env.update(ELYSIA_CONVERSION_HTTP_EXECUTION='packages', ELYSIA_FLAME_WORKLOAD='image' if scenario.get('image') else 'small',
               ELYSIA_FLAME_MODE=mode, ELYSIA_FLAME_OUTPUT=str(output), ELYSIA_FLAME_SOURCE='' if scenario.get('image') else 'openai-chat-completions',
               ELYSIA_FLAME_TARGET='' if scenario.get('image') else 'anthropic-messages' if scenario.get('cross') else 'openai-chat-completions',
               ELYSIA_FLAME_REQUEST=scenario.get('request', 'short'), ELYSIA_FLAME_STREAM='sse' if scenario.get('stream') else 'json',
               ELYSIA_FLAME_FRAMES='256', ELYSIA_FLAME_FRAME_BYTES='256', ELYSIA_FLAME_REQUESTS=str(requests or scenario['requests']),
               ELYSIA_FLAME_REPEATS='1', ELYSIA_FLAME_WARMUP='8' if scenario.get('image') or scenario.get('stream') else '32',
               ELYSIA_FLAME_SECONDS='15', ELYSIA_FLAME_VERIFY_EXACT='1', ELYSIA_FLAME_RECOVERY_MS='1000',
               ELYSIA_FLAME_FIXED_CPU='1' if mode == 'cpu' else '',
               ELYSIA_CONVERSION_HTTP_MIB='2', ELYSIA_CONVERSION_HTTP_WORKERS=str(scenario.get('workers', 1)),
               ELYSIA_CONVERSION_HTTP_CANDIDATES='1', ELYSIA_CONVERSION_HTTP_LOG='off')
    source_id = 'anthropic-messages' if scenario.get('image') else 'openai-chat-completions'
    ingress_path = '/v1/messages' if scenario.get('image') else '/v1/chat/completions' if packages else '/chat/completions'
    env['ELYSIA_FLAME_GATEWAY_PATH'] = '/gateway/' + ('measure-' if packages else '') + source_id + ingress_path
    if scenario.get('ingress'):
        env['ELYSIA_FLAME_GATEWAY_PATH'] = '/gateway/' + ('measure-' if packages else '') + scenario['ingress'] + '/v1/messages'
    children, metrics, proxy = Children(), None, None
    record = dict(version=version, scenario=scene, phase=phase, mode=mode, binary=binary, binarySHA256=expected_sha,
                  environment=env, status='running', metricsAvailability={
                      'Allocated': phase == 'counters', 'Allocations': phase == 'counters',
                      'GCPauseNS': phase == 'counters', 'GCCPUSeconds': False, 'PeakHeap': False,
                      'Heap': 'boundary only', 'PeakWorkingSet': '20ms OS sampling'})
    record['runnerSHA256'] = hashlib.sha256(RUNNER_SOURCE).hexdigest()
    record['driverSHA256'] = builds['driver']['sha256']
    (output / 'runner.py').write_bytes(RUNNER_SOURCE)
    # Record task variables only; inherited machine credentials are never written.
    record['environment'] = {k: v for k, v in env.items() if k in RUNTIME or k.startswith('ELYSIA_')}
    save_json(output / 'run.json', record)
    try:
        provider_build = provider_fixture or read_json(ROOT / '.tmp-dev/fixed-provider-build/report.json')
        provider_binary = Path(provider_fixture['binary']) if provider_fixture else ROOT / '.tmp-dev/fixed-provider-build/server.test.exe'
        assert digest(provider_binary) == provider_build['binarySHA256']
        ready = output / 'provider-ready.json'
        provider_env = env | {'ELYSIA_CONVERSION_HTTP_ROLE': 'provider', 'ELYSIA_CONVERSION_HTTP_READY': str(ready)}
        provider_test = 'TestConversionHTTPProcess'
        if provider_fixture:
            provider_env['ELYSIA_CONTINUATION_READY'] = str(ready)
            provider_env['ELYSIA_CONTINUATION_MEDIA'] = '1' if scenario.get('continuation_media') else ''
            provider_test = 'TestContinuationPerformanceProvider'
        provider = children.start([str(provider_binary), '-test.run=^' + provider_test + '$', '-test.timeout=20m'],
                    ROOT / '.tmp-dev/fixed-provider-build/source/backend/server', provider_env, output / 'provider.log')
        provider_address = wait_file(ready, provider)
        port = free_port()
        url = f'http://127.0.0.1:{port}'
        config = dict(host='127.0.0.1', port=port, panelAccessToken=TOKEN, databasePath=str(output / 'app.sqlite3'),
                      logDatabasePath=str(output / 'logs.sqlite3'), secretKeyPath=str(output / '.master-key'),
                      logLevel='error', logPersistLevel='error', openBrowserOnStart=False, enablePprof=True,
                      modelCatalog={'enabled': False, 'syncIntervalMinutes': 0}, agentRemote={'enabled': False},
                      outbound={'deniedIpRanges': []}, usageLog={'bodyMaxKB': 0, 'externalizeMedia': False})
        config_path = output / 'config.json'
        save_json(config_path, config)
        gateway_env = environment() | {'ELYSIA_PARENT_STDIN': '1'}
        gateway_ready = output / 'gateway-ready.json'
        if is_instrumented:
            gateway_env.update(ELYSIA_VERSIONED_CONFIG=str(config_path), ELYSIA_VERSIONED_READY=str(gateway_ready))
            command = [binary, '-test.run=^TestVersionedGatewayProcess$', '-test.timeout=20m']
        else:
            command = [binary, '-config', str(config_path)]
        gateway = children.start(command, output, gateway_env, output / 'gateway.log')
        record['health'] = wait_gateway(url, gateway)
        assert record['health']['version'] == '1.7.0'
        assert builds[variant]['commit'].startswith(record['health']['commit']), 'gateway commit differs'
        record['configuration'] = configure_gateway(url, provider_address['URL'], packages, scenario, output)
        admin(url, 'protocols/reload', 'POST', {})
        reloaded = {'protocols': active_protocols(url), 'bindings': admin(url, 'protocols/bindings')}
        save_json(output / 'reloaded-identity.json', reloaded)
        assert reloaded == record['configuration'], 'reload changed active protocol/binding identity'
        if prepare_fixture:
            env.update(prepare_fixture(url, packages, output))
        record['gatewayCommand'] = command
        counter_url = wait_file(gateway_ready, gateway)['Metrics'] if is_instrumented else None
        metrics = ProcessMetrics(gateway.pid, url, counter_url)
        proxy = serve_metrics(metrics)
        gateway_address = {'URL': url, 'Metrics': f'http://127.0.0.1:{proxy.server_port}', 'PID': gateway.pid,
                           'Execution': {'Execution': 'packages' if packages else 'legacy', 'Commit': builds[variant]['commit'],
                                         'Packages': record['configuration']['protocols']}}
        external = output / 'external.json'
        save_json(external, {'provider': provider_address, 'gateway': gateway_address})
        env['ELYSIA_CONVERSION_HTTP_EXTERNAL'] = str(external)
        driver = provider_fixture['binary'] if provider_fixture else builds['driver']['binary']
        record['driverSHA256'] = digest(driver)
        command = [driver, '-test.run=^TestConversionFlamegraph$', '-test.v', '-test.timeout=15m']
        record['driverCommand'] = command
        record['provider'] = {'binary': str(provider_binary), 'sha256': digest(provider_binary), 'identity': provider_address['Execution']}
        save_json(output / 'run.json', record)
        execute(command, ROOT / 'backend/server', output / 'driver.log', env)
        if provider_fixture:
            provider_after = request(provider_address['Metrics'])
            assert provider_after['Recovered'] == (requests or scenario['requests']) + int(env['ELYSIA_FLAME_WARMUP']), 'recovery did not execute for every measured and warmup request'
            record['recovery'] = provider_after
        if phase == 'attribution':
            record['attribution'] = request(counter_url)['Attribution']
        after = {'protocols': active_protocols(url), 'bindings': admin(url, 'protocols/bindings')}
        assert after == record['configuration'], 'active protocol/binding identity drift'
        record['status'] = 'passed'
    except Exception as error:
        record.update(status='failed', error=str(error))
        raise
    finally:
        if proxy:
            proxy.shutdown()
            proxy.server_close()
        if metrics:
            metrics.close()
        record['children'] = children.close()
        if record['status'] == 'passed' and any(child['exitCode'] != 0 for child in record['children']):
            record.update(status='failed', error='a measured child did not exit successfully')
        save_json(output / 'run.json', record)
        if record['status'] != 'passed':
            print('failed evidence:', output, flush=True)
    if record['status'] != 'passed':
        raise RuntimeError(record['error'])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['prepare', 'run'])
    parser.add_argument('--phase', choices=['standalone', 'counters', 'attribution'], default='standalone')
    parser.add_argument('--versions', default='R-release,R-build,A,C-default,C-package')
    parser.add_argument('--scenes', default=','.join(SCENARIOS))
    parser.add_argument('--mode', choices=['measure', 'cpu', 'memory'], default='measure')
    parser.add_argument('--rounds', type=int, default=5)
    parser.add_argument('--requests', type=int)
    parser.add_argument('--label', required=False)
    args = parser.parse_args()
    if args.action == 'prepare':
        prepare()
        return
    versions = args.versions.split(',')
    for scene in args.scenes.split(','):
        for round_index in range(args.rounds):
            order = versions[round_index % len(versions):] + versions[:round_index % len(versions)]
            for version in order:
                if args.phase == 'counters' and version == 'R-release':
                    continue
                name = f'{scene}-{round_index + 1}-{version}'
                output = EVIDENCE / (args.label or args.phase + '-' + args.mode) / name
                print(name, args.phase, args.mode, flush=True)
                run_case(version, scene, args.phase, args.mode, output, args.requests)


if __name__ == '__main__':
    main()
