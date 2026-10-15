#!/usr/bin/env python3
"""Run the actual Go Store -> Rust Commerce regression in an owned local cluster.

No production URL, existing database or image pull is accepted. Invoke through
agcc with roots for this repository's outputs and Cargo/Go caches. Private logs,
synthetic credentials and receipts stay in .local-tests, never in Git.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time

REPO = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO / 'tests/architecture_completion'))
from build_lease import private_directory
from pg_fixture import Fixture, FixtureError, clean_environment, write_private


def digest(path: Path) -> str:
    with path.open('rb') as stream:
        value = hashlib.file_digest(stream, 'sha256')
    return value.hexdigest()


def sources() -> dict:
    roots = [REPO / 'services/core/src', REPO / 'services/core/tests',
             REPO / 'services/core/schema', REPO / 'contracts/proto',
             REPO / 'services/extensions/internal', REPO / 'services/extensions/cmd',
             REPO / 'tests/commerce', REPO / 'tests/architecture_completion']
    files = [root / name for root, names in [
        (REPO / 'services/core', ['Cargo.toml', 'Cargo.lock', 'build.rs']),
        (REPO / 'services/extensions', ['go.mod', 'go.sum']),
        (REPO, ['mise.toml', 'rust-toolchain.toml']),
    ] for name in names]
    for root in roots:
        files.extend(path for path in root.rglob('*')
                     if path.is_file() and not path.is_symlink()
                     and '__pycache__' not in path.parts)
    return {str(path.relative_to(REPO)): digest(path) for path in sorted(files)
            if path.is_file()}


def run(command: list[str], cwd: Path, env: dict, output: Path, timeout: int) -> None:
    # Logs can contain driver errors; never forward them to a remote service.
    with output.open('xb') as log:
        os.chmod(output, 0o600)
        result = subprocess.run(command, cwd=cwd, env=env, stdout=log,
                                stderr=subprocess.STDOUT, timeout=timeout)
    if result.returncode:
        raise FixtureError(f'local check failed ({result.returncode}); inspect {output}')


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output-root', type=Path, required=True,
                        help='absolute bounded directory under this repo/.local-tests')
    parser.add_argument('--process-root', type=Path, required=True,
                        help='short absolute private directory for Unix sockets and process logs')
    parser.add_argument('--rust-toolchain',
                        help='explicit local fallback only; absent uses the repository pin')
    args = parser.parse_args()
    if not args.output_root.is_absolute():
        parser.error('output root must be absolute')
    output = args.output_root.resolve()
    if not output.is_relative_to(REPO / '.local-tests'):
        parser.error('output must be inside this repo/.local-tests')
    private_directory(output)
    process_root = args.process_root
    private_directory(process_root)
    if len(os.fsencode(process_root)) > 35:
        parser.error('process root must be short enough for the Unix socket limit')
    env = clean_environment()
    # Avoid inherited product settings and unrelated test endpoints/credentials.
    env = {key: value for key, value in env.items()
           if not key.startswith(('LMM_', 'SQLX_', 'RUSTUP_TOOLCHAIN'))}
    if args.rust_toolchain:
        env['RUSTUP_TOOLCHAIN'] = args.rust_toolchain
    before = sources()
    fixture = Fixture(output)
    try:
        binary = fixture.root / 'lmm-extensions'
        # `mise exec` may auto-install unrelated configured tools. Resolve the
        # existing pinned Go tool only; a missing installation is a blocker.
        go_home = subprocess.check_output(['mise', 'where', 'go'], cwd=REPO,
                                          env=env, text=True).strip()
        go = Path(go_home) / 'bin/go'
        if not go.is_absolute() or not go.is_file():
            raise FixtureError('repository-pinned Go installation is unavailable')
        run([str(go), 'build', '-o', str(binary), './cmd/extensions'],
            REPO / 'services/extensions', env, fixture.root / 'go-build.log', 300)
        fixture.start()
        # Only this freshly created, identity-checked loopback cluster is passed
        # to SQLx. No externally supplied DATABASE_URL is ever consumed.
        config = dict(line.split('=', 1) for line in
                      (fixture.root / 'postgres.env').read_text().splitlines())
        env['DATABASE_URL'] = (f'postgresql://a09_admin:{config["POSTGRES_PASSWORD"]}'
                               f'@127.0.0.1:{fixture.binding["port"]}/postgres')
        env['LMM_RELAY_EXTENSION_BIN'] = str(binary)
        env['TMPDIR'] = str(process_root)
        env['CARGO_BUILD_JOBS'] = '2'
        started = time.monotonic()
        run(['cargo', 'test', '--locked', '--test', 'commerce_process', '--',
             '--nocapture', '--test-threads=1'], REPO / 'services/core', env,
            fixture.root / 'commerce-test.log', 900)
        if sources() != before:
            raise FixtureError('tested sources changed during acceptance; result not frozen')
        fixture.cleanup()
        receipt = {
            'format': 'lmm-commerce-process-acceptance-v1', 'result': 'PASS',
            'source_sha256': before, 'extension_sha256': digest(binary),
            'rust_toolchain_override': args.rust_toolchain,
            'process_evidence_root': str(process_root),
            'seconds': round(time.monotonic() - started, 2),
            'cluster_removed': True, 'scope': 'synthetic credits; local product processes only',
        }
        write_private(fixture.root / 'acceptance.json', json.dumps(receipt, sort_keys=True) + '\n')
        print(f'PASS: real Store/Commerce processes; receipt {fixture.root / "acceptance.json"}')
        return 0
    except (FixtureError, OSError, subprocess.SubprocessError) as error:
        # No optimistic cleanup/retry after an uncertain or failed check. The
        # fixture's immutable identity and local private logs remain inspectable.
        print(f'FAIL: {error}; retained local evidence {fixture.root}', file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
