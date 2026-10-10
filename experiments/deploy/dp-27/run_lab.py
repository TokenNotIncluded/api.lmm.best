#!/usr/bin/env python3
"""Build and measure the health-probe candidate on the local builder.

Not an application benchmark. It runs only loopback requests and local tools.
No Docker daemon, production credentials, node compiler, CI or registry writes.
"""
from __future__ import annotations
import argparse
import gzip
import http.server
import io
import json
import os
import platform
import shutil
import statistics
import subprocess
import tarfile
import threading
import time
from pathlib import Path
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
import distribution as d
import oci_cost

HERE = Path(__file__).resolve().parent
OUT = HERE / 'out'
EVIDENCE = HERE / 'evidence'
CPU = min(os.sched_getaffinity(0))


def measure(command: list[str], stdout: Path, repeats: int = 7) -> dict:
    runs = []
    for _ in range(repeats):
        start = time.perf_counter()
        rss_file = stdout.with_name(stdout.name + '.rss')
        with stdout.open('wb') as stream:
            measured = ['taskset', '-c', str(CPU), '/usr/bin/time', '-f', '%M', '-o', str(rss_file), *command]
            proc = subprocess.Popen(measured, stdout=stream, stderr=subprocess.PIPE)
            # No command in this controlled lab emits a large stderr stream.
            _, status, usage = os.wait4(proc.pid, 0)
            proc.returncode = os.waitstatus_to_exitcode(status)
            error = proc.stderr.read()
            proc.stderr.close()
        elapsed = time.perf_counter() - start
        if proc.returncode:
            raise RuntimeError(f'{command[0]} failed: {error[:2048]!r}')
        runs.append({'wall_seconds': elapsed, 'user_seconds': usage.ru_utime,
                     'system_seconds': usage.ru_stime, 'max_rss_kib': int(rss_file.read_text().strip())})
    return {'runs': runs, 'median_wall_seconds': statistics.median(r['wall_seconds'] for r in runs),
            'median_cpu_seconds': statistics.median(r['user_seconds'] + r['system_seconds'] for r in runs),
            'max_rss_kib': max(r['max_rss_kib'] for r in runs), 'cpu_affinity': [CPU],
            'wall_and_cpu_include_taskset_time_launcher': True,
            'rss_is_command_only_gnu_time': True}


def tar(files: dict[str, bytes]) -> bytes:
    stream = io.BytesIO()
    with tarfile.open(fileobj=stream, mode='w', format=tarfile.USTAR_FORMAT) as tf:
        for name, data in sorted(files.items()):
            info = tarfile.TarInfo(name)
            info.size, info.mtime, info.uid, info.gid = len(data), 0, 0, 0
            info.mode = 0o555 if name.startswith('usr/local/bin/') else 0o444
            tf.addfile(info, io.BytesIO(data))
    return stream.getvalue()


def put(root: Path, data: bytes, media: str) -> dict:
    digest = d.digest(data)
    path = root / 'blobs' / 'sha256' / digest
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
    return {'mediaType': media, 'digest': 'sha256:' + digest, 'size': len(data)}


def make_oci(root: Path, binaries: dict[str, Path], source_commit: str) -> None:
    root.mkdir(parents=True, exist_ok=True)
    (root / 'oci-layout').write_bytes(d.canonical({'imageLayoutVersion': '1.0.0'}))
    base = tar({'DP27-SCOPE.txt': b'Local standalone health probe only. Not lmm-core or lmm-extensions.\n'})
    base_desc = put(root, gzip.compress(base, mtime=0), 'application/vnd.oci.image.layer.v1.tar+gzip')
    index = {'schemaVersion': 2, 'mediaType': 'application/vnd.oci.image.index.v1+json', 'manifests': []}
    for name, path in binaries.items():
        binary_tar = tar({'usr/local/bin/lmm-healthprobe': path.read_bytes()})
        binary_desc = put(root, gzip.compress(binary_tar, mtime=0), 'application/vnd.oci.image.layer.v1.tar+gzip')
        config = {'architecture': 'amd64', 'os': 'linux',
                  'config': {'User': '65532:65532', 'Entrypoint': ['/usr/local/bin/lmm-healthprobe'],
                             'Cmd': ['--version'], 'Labels': {'org.opencontainers.image.revision': source_commit,
                                                            'io.lmm.dp27.scope': 'standalone-health-probe'}},
                  'rootfs': {'type': 'layers', 'diff_ids': ['sha256:' + d.digest(base), 'sha256:' + d.digest(binary_tar)]}}
        config_desc = put(root, d.canonical(config), 'application/vnd.oci.image.config.v1+json')
        manifest = {'schemaVersion': 2, 'mediaType': 'application/vnd.oci.image.manifest.v1+json',
                    'config': config_desc, 'layers': [base_desc, binary_desc]}
        desc = put(root, d.canonical(manifest), 'application/vnd.oci.image.manifest.v1+json')
        desc['annotations'] = {'org.opencontainers.image.ref.name': name}
        desc['platform'] = {'architecture': 'amd64', 'os': 'linux'}
        index['manifests'].append(desc)
    (root / 'index.json').write_bytes(d.canonical(index))


class Health(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200 if self.path == '/health/live' else 404)
        self.send_header('Content-Length', '0')
        self.end_headers()
    def log_message(self, *_):
        pass


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-commit', required=True)
    args = parser.parse_args()
    if platform.machine() != 'x86_64':
        raise SystemExit('This measured fixture requires native linux/amd64; do not substitute emulation.')
    import re
    if not re.fullmatch('[0-9a-f]{40}', args.source_commit):
        raise SystemExit('source commit must be a full SHA')
    # Bind the probe source used below to the declared local code commit.
    for path in sorted((HERE / 'probe').rglob('*')):
        if path.is_file() and path.suffix in {'.go', '.mod'}:
            relative = 'experiments/deploy/dp-27/' + str(path.relative_to(HERE))
            expected = subprocess.check_output(['git', 'rev-parse', args.source_commit + ':' + relative], cwd=HERE, text=True).strip()
            actual = subprocess.check_output(['git', 'hash-object', str(path)], cwd=HERE, text=True).strip()
            if actual != expected:
                raise SystemExit('Probe source differs from declared commit: ' + relative)
    OUT.mkdir(exist_ok=True)
    EVIDENCE.mkdir(exist_ok=True)
    for sub in ('bin', 'debug', 'payloads', 'signed'):
        (OUT / sub).mkdir(exist_ok=True)
    environment = os.environ | {'GOTOOLCHAIN': 'local', 'GOWORK': 'off', 'GOPROXY': 'off', 'CGO_ENABLED': '0'}
    build_times = {}
    builds = [('probe-v1', '.', '-s -w -X main.revision=dp27-probe-v1'),
              ('probe-v2', '.', '-s -w -X main.revision=dp27-probe-v2'),
              ('probe-http', './full', '-s -w -X main.revision=dp27-probe-v1')]
    for name, package, flags in builds:
        start = time.perf_counter()
        subprocess.run(['go', 'build', '-trimpath', '-buildvcs=false', '-ldflags=' + flags,
                        '-o', str(OUT / 'bin' / name), package], cwd=HERE / 'probe', env=environment, check=True)
        build_times[name] = time.perf_counter() - start
    subprocess.run(['go', 'build', '-trimpath', '-buildvcs=false', '-o', str(OUT / 'debug' / 'probe.full'), '.'],
                   cwd=HERE / 'probe', env=environment, check=True)
    overhead = measure(['/bin/true'], OUT / 'runner-overhead')
    binaries = {name: OUT / 'bin' / name for name, _, _ in builds}
    old, new = binaries['probe-v1'], binaries['probe-v2']
    compressed = {}
    for name, command in {
        'gzip-6': ['gzip', '-n', '-6', '-c', str(new)],
        'zstd-3': ['zstd', '-q', '-3', '-T1', '-c', str(new)],
        'xz-6': ['xz', '-6', '--threads=1', '-c', str(new)],
        'zstd-patch': ['zstd', '-q', '-3', '-T1', '--patch-from=' + str(old), '-c', str(new)],
    }.items():
        payload = OUT / 'payloads' / name
        enc = measure(command, payload, repeats=3)
        decode = {'gzip-6': ['gzip', '-d', '-c', str(payload)],
                  'zstd-3': ['zstd', '-d', '-q', '--memory=64MB', '-c', str(payload)],
                  'xz-6': ['xz', '-d', '--memlimit-decompress=64MiB', '-c', str(payload)],
                  'zstd-patch': ['zstd', '-d', '-q', '--memory=64MB', '--patch-from=' + str(old), '-c', str(payload)]}[name]
        decoded = OUT / 'decoded'
        dec = measure(decode, decoded)
        assert d.hash_file(decoded) == d.hash_file(new)
        compressed[name] = {'payload_bytes': payload.stat().st_size,
                            'ratio': payload.stat().st_size / new.stat().st_size,
                            'encode': enc, 'decode': dec, 'roundtrip_sha256': d.hash_file(decoded)}
    (OUT / 'decoded').unlink()
    server = http.server.ThreadingHTTPServer(('127.0.0.1', 8081), Health)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    runtime = {}
    try:
        for name, path in binaries.items():
            runtime[name] = {'file_bytes': path.stat().st_size, 'sha256': d.hash_file(path),
                             'start_and_version': measure([str(path), '--version'], OUT / 'probe-stdout'),
                             'start_and_health': measure([str(path), '8081'], OUT / 'probe-stdout')}
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=3)
    key = Ed25519PrivateKey.generate()  # never written to disk; not a production key
    stage_results = {}
    for encoding, filename in [('gzip', 'gzip-6'), ('zstd-patch', 'zstd-patch')]:
        payload = OUT / 'payloads' / filename
        manifest = {'schema': 1, 'component': 'probe', 'version': 'v0.0.0-dp27.2',
                    'repository': d.REPOSITORY, 'commit': args.source_commit, 'target': d.target_platform(),
                    'builder': 'dp27-local-unsigned-builder-identity', 'encoding': encoding,
                    'payload_sha256': d.hash_file(payload), 'payload_bytes': payload.stat().st_size,
                    'result_sha256': d.hash_file(new), 'result_bytes': new.stat().st_size}
        if encoding == 'zstd-patch':
            manifest.update(base_sha256=d.hash_file(old), base_bytes=old.stat().st_size)
        raw, signature, public = d.sign_manifest(manifest, key)
        (OUT / 'signed' / (filename + '.json')).write_bytes(raw)
        (OUT / 'signed' / (filename + '.sig')).write_bytes(signature)
        (OUT / 'signed' / 'TEST-ONLY-public-key.raw').write_bytes(public)
        root = OUT / ('stage-' + filename)
        if root.exists():
            shutil.rmtree(root)
        root.mkdir(mode=0o700)
        shutil.copyfile(old, root / 'running-v1')
        (root / 'current').symlink_to('running-v1')
        stage_results[filename] = d.stage(root, raw, signature, public, lambda p=payload: p.open('rb'),
                                          component='probe', commit=args.source_commit, base=root / 'running-v1')
        assert os.readlink(root / 'current') == 'running-v1'
        assert d.hash_file(root / 'current') == d.hash_file(old)
    layout = OUT / 'oci'
    make_oci(layout, binaries, args.source_commit)
    images = {name: oci_cost.inspect(layout, name) for name in binaries}
    oci = {'first_probe_v1': oci_cost.cost([], [images['probe-v1']]),
           'first_http_probe': oci_cost.cost([], [images['probe-http']]),
           'single_probe_update': oci_cost.cost([images['probe-v1']], [images['probe-v2']]),
           'after_pre_pull': oci_cost.cost([images['probe-v1'], images['probe-v2']], [images['probe-v2']])}
    # Sign the OCI index. The embedded trust key is for reproducing THIS lab,
    # not proof of an approved production builder or registry provenance.
    raw_index = (layout / 'index.json').read_bytes()
    (OUT / 'signed' / 'oci-index.sig').write_bytes(key.sign(raw_index))
    def text(path):
        try:
            return Path(path).read_text().strip()
        except OSError:
            return None
    report = {'scope': 'standalone Go health probe; NOT actual Rust/Go application images',
              'source_commit': args.source_commit,
              'base_commit': '72667564c0431754d4856dc2e0db55f360bd2745',
              'environment': {'kernel': platform.release(), 'machine': platform.machine(),
                              'go': subprocess.check_output(['go', 'version'], text=True).strip(),
                              'cpu_max': text('/sys/fs/cgroup/cpu.max'),
                              'memory_max': text('/sys/fs/cgroup/memory.max'),
                              'measurement_cpu_affinity': [CPU],
                              'cpu_affinity_is_not_1c1g_vm': True,
                              'upx_available': bool(shutil.which('upx')),
                              'docker_available': bool(shutil.which('docker')),
                              'rustc_available': bool(shutil.which('rustc'))},
              'builder_cached_build_wall_seconds': build_times, 'runner_overhead': overhead,
              'probe_binaries': runtime, 'compression': compressed,
              'staging_local_filesystem': stage_results, 'oci_probe_only': oci,
              'test_public_key_sha256': d.digest(public),
              'unmeasured': ['application_glibc_vs_musl', 'application_first_pull', 'single_go_application_update',
                             'single_rust_application_update', 'docker_snapshotter_disk_peak',
                             'docker_pull_network_bytes', '1c1g_application_startup_and_rss',
                             'production_ca_dns_timezone', 'actual_core_admin_savings', 'upx_runtime_tradeoffs']}
    (EVIDENCE / 'measurements.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    for path in sorted(OUT.rglob('*')):
        if path.is_file() and not path.is_symlink() and not any(p.startswith('stage-') for p in path.relative_to(OUT).parts):
            print(d.hash_file(path), path.relative_to(OUT))
    print(json.dumps({'compression_bytes': {k: v['payload_bytes'] for k, v in compressed.items()},
                      'probe_bytes': {k: v['file_bytes'] for k, v in runtime.items()},
                      'report': str(EVIDENCE / 'measurements.json')}, indent=2))

if __name__ == '__main__':
    main()
