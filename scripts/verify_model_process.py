#!/usr/bin/env python3
"""Prepare frozen model test binaries, then run them against fresh owned PostgreSQL.

Invoke both phases through agcc with the explicit output/process/target/cache
roots covered. Create those private (0700), absolute, symlink-free directories
first. Only --reuse-leased-target permits the standard Core target directory
with owner-only write access, after its current owner releases the build slot.
prepare requires an already-built Core candidate, its SHA-256, a source
binding {"files": {repo_relative_path: sha256}}, and a successful build receipt
{exit_code: 0, source_drift: [], binary_sha256: sha256,
source_binding_sha256: <SHA of that exact manifest>}. prepare writes its temporary
files under output-root/compile-tmp, covered by the same output tracking root.
It uses the installed repository Rust pin and offline locked Cargo; coordinate
the build slot first.

  scripts/verify_model_process.py prepare --output-root ... --target-root ... \
    --core-binary ... --core-sha256 ... --source-binding ... --build-receipt ...
  scripts/verify_model_process.py run --output-root ... --process-root ... \
    --prepared-sha256 <SHA printed by prepare>

run uses no Cargo and accepts no database URL. It executes all six process cases
and all model_http cases in one fresh, identity-checked local fixture. Failure
keeps the cluster and private evidence; success fences and removes only that
fixture. These are development checks until a separate acceptance owner reviews
the frozen inputs and receipts. Synthetic credentials and raw logs stay local.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import secrets
import shutil
import signal
import stat
import subprocess
import sys
import time
import tomllib

sys.dont_write_bytecode = True
REPO = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO / 'tests/architecture_completion'))
from build_lease import private_directory, process_identity
from pg_fixture import Fixture, FixtureError, write_private

HEX = re.compile(r'[0-9a-f]{64}')
PROCESS_TESTS = {
    'independent_process_distinct_requests_compete_for_real_budget',
    'independent_process_personal_permissions_and_idempotency',
    'independent_process_team_payer_fallback_and_revocation',
    'stream_cases::independent_process_missing_usage_preserves_hold_through_natural_expiry',
    'stream_cases::independent_process_sse_paid_and_partial_disconnect_stays_reserved',
    'stream_cases::independent_process_terminal_evidence_recovers_final_transaction_once',
}
PRODUCT_TREES = ('services/core/src', 'services/core/schema', 'contracts/proto')
PRODUCT_FILES = ('services/core/Cargo.toml', 'services/core/Cargo.lock',
                 'services/core/build.rs', 'deployment/postgres/grant-core-runtime.sql')
TEST_TREES = ('services/core/tests/common', 'services/core/tests/model_http_runtime',
              'services/core/tests/model_http_upstream_cost', 'tests/model_process')
TEST_FILES = ('services/core/tests/model_process.rs', 'services/core/tests/model_http.rs',
              'scripts/verify_model_process.py', 'tests/architecture_completion/pg_fixture.py',
              'tests/architecture_completion/build_lease.py', 'mise.toml',
              'services/core/rust-toolchain.toml')


def sha(path: Path) -> str:
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def regular(path: Path) -> Path:
    if not path.is_absolute() or path.resolve() != path:
        raise FixtureError('file path must be absolute and contain no symlinks')
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid():
        raise FixtureError('input must be a regular file owned by this user')
    return path


def relative(name: str) -> Path:
    if not isinstance(name, str):
        raise FixtureError('source binding path must be a string')
    value = PurePosixPath(name)
    if value.is_absolute() or '..' in value.parts or str(value) != name:
        raise FixtureError('source binding path must be canonical and repository relative')
    return REPO / name


def read_json(path: Path) -> dict:
    regular(path)
    data = json.loads(path.read_text())
    if not isinstance(data, dict):
        raise FixtureError('receipt must be a JSON object')
    return data


def record(path: Path, value: dict) -> None:
    write_private(path, json.dumps(value, sort_keys=True, indent=2) + '\n')


def tree(root: Path) -> list[Path]:
    if root.resolve() != root or not root.is_dir():
        raise FixtureError('source directory missing or symlinked: ' + str(root))
    result = []
    for directory, dirs, files in os.walk(root, followlinks=False):
        parent = Path(directory)
        for name in dirs + files:
            if (parent / name).is_symlink():
                raise FixtureError('symlink inside source inputs: ' + str(parent / name))
        dirs[:] = [name for name in dirs if name != '__pycache__']
        result.extend(parent / name for name in files if not name.endswith('.pyc'))
    return result


def snapshot(trees: tuple[str, ...], files: tuple[str, ...]) -> dict[str, str]:
    paths = [relative(name) for name in files]
    for name in trees:
        paths.extend(tree(relative(name)))
    return {str(path.relative_to(REPO)): sha(regular(path)) for path in sorted(set(paths))}


def delta(before: dict, after: dict) -> dict:
    return {
        'modified': sorted(name for name in before.keys() & after.keys() if before[name] != after[name]),
        'missing': sorted(before.keys() - after.keys()),
        'added': sorted(after.keys() - before.keys()),
    }


def changed(value: dict) -> bool:
    return any(value.values())


def is_product(name: str) -> bool:
    return name in PRODUCT_FILES or any(name.startswith(root + '/') for root in PRODUCT_TREES)


def inputs() -> dict:
    # Track absence too: adding a root toolchain override must invalidate a freeze.
    optional = {'rust-toolchain.toml': None}
    override = REPO / 'rust-toolchain.toml'
    if override.exists() or override.is_symlink():
        optional['rust-toolchain.toml'] = sha(regular(override))
    return {'product': snapshot(PRODUCT_TREES, PRODUCT_FILES),
            'tests': snapshot(TEST_TREES, TEST_FILES), 'optional': optional}


def environment() -> dict:
    # All LMM/SQLx/PG/Docker/DB settings and proxy variables are excluded, including
    # the old LMM_MODEL_BIN. Runtime receives only this fixture's synthesized DSN.
    allow = {'HOME', 'PATH', 'LANG', 'LC_ALL', 'LC_CTYPE', 'TZ', 'USER', 'LOGNAME',
             'SHELL', 'XDG_CACHE_HOME', 'XDG_CONFIG_HOME', 'XDG_RUNTIME_DIR',
             'CARGO_HOME', 'RUSTUP_HOME'}
    result = {key: value for key, value in os.environ.items() if key in allow}
    result['PYTHONDONTWRITEBYTECODE'] = '1'
    # Build the locked crypto source, never a host-installed AWS-LC library.
    result['AWS_LC_SYS_USE_SYSTEM'] = '0'
    return result


def output_root(path: Path) -> None:
    private_directory(path)
    if not path.is_relative_to(REPO / '.local-tests') or path == REPO / '.local-tests':
        raise FixtureError('output root must be a bounded directory under repo/.local-tests')


def check_inputs(frozen: dict) -> None:
    if inputs() != frozen:
        raise FixtureError('product or test inputs changed; frozen acceptance invalid')


def reference_check(reference: dict) -> None:
    for name in ('source_binding', 'build_receipt'):
        if sha(regular(Path(reference[name]))) != reference[name + '_sha256']:
            raise FixtureError('original build/source receipt changed')


def artifact_check(output: Path, artifacts: dict) -> None:
    names = {'artifacts/' + name for name in ('lmm-core', 'model_process', 'model_http')}
    actual = {str(path.relative_to(output)) for path in tree(output / 'artifacts')}
    if set(artifacts) != names or actual != names:
        raise FixtureError('frozen artifact collection changed')
    for name, expected in artifacts.items():
        if not isinstance(expected, str) or not HEX.fullmatch(expected):
            raise FixtureError('invalid frozen artifact binding')
        if sha(regular(output / name)) != expected:
            raise FixtureError('frozen artifact changed: ' + name)


def run_command(command: list[str], env: dict, log: Path, timeout: int) -> dict:
    started = time.monotonic()
    fd = os.open(log, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as stream:
        child = subprocess.Popen(command, cwd=REPO / 'services/core', env=env,
                                 stdout=stream, stderr=subprocess.STDOUT,
                                 stdin=subprocess.DEVNULL, start_new_session=True)
        try:
            identity = process_identity(child.pid)
        except FileNotFoundError:
            identity = {'pid': child.pid, 'generation': 'EXITED_BEFORE_OBSERVATION'}
        timed_out = False
        try:
            code = child.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            timed_out = True
            # The group belongs to this one child; no remote resources or PID scan.
            try:
                os.killpg(child.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                code = child.wait(timeout=10)
            except subprocess.TimeoutExpired:
                os.killpg(child.pid, signal.SIGKILL)
                code = child.wait()
    receipt = {'command': command, 'cwd': str(REPO / 'services/core'),
               'child': identity, 'exit_code': code, 'timed_out': timed_out,
               'seconds': round(time.monotonic() - started, 3), 'log': str(log)}
    record(log.with_suffix(log.suffix + '.json'), receipt)
    return receipt


def require_success(result: dict) -> None:
    if result['exit_code'] != 0 or result['timed_out']:
        raise FixtureError('local command failed; inspect private log and actual exit receipt')


def copy_binary(source: Path, target: Path) -> None:
    regular(source)
    fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o500)
    with source.open('rb') as original, os.fdopen(fd, 'wb') as copied:
        shutil.copyfileobj(original, copied)
        copied.flush()
        os.fsync(copied.fileno())
    if sha(source) != sha(target):
        raise FixtureError('binary changed during copying')


def test_list(binary: Path, env: dict, output: Path) -> list[str]:
    result = run_command([str(binary), '--list', '--format', 'terse'], env, output, 30)
    require_success(result)
    text = output.read_text()
    names = re.findall(r'^(.+): test$', text, re.MULTILINE)
    if not names or len(names) != len(set(names)) or re.search(r': benchmark$', text, re.MULTILINE):
        raise FixtureError('test discovery is empty, duplicated, or includes benchmarks')
    return sorted(names)


def prepare(args) -> int:
    output_root(args.output_root)
    if args.reuse_leased_target:
        target = args.target_root
        if target != REPO / 'services/core/target' or target.resolve() != target:
            raise FixtureError('leased target reuse permits only the standard, symlink-free Core target')
        info = target.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o022:
            raise FixtureError('leased target must be owned by this user and not writable by others')
    else:
        private_directory(args.target_root)
        if args.target_root == REPO / 'services/core/target':
            raise FixtureError('standard Core target requires explicit --reuse-leased-target after release')
    if (args.output_root / 'prepared.json').exists():
        raise FixtureError('prepare is one-shot; retain prior receipts and choose a new output root')
    if not HEX.fullmatch(args.core_sha256):
        raise FixtureError('Core SHA-256 must be lowercase hex')
    core = regular(args.core_binary)
    binding = read_json(args.source_binding)
    build = read_json(args.build_receipt)
    files = binding.get('files')
    if not isinstance(files, dict) or not files:
        raise FixtureError('source binding requires a nonempty files map')
    for name, value in files.items():
        relative(name)
        if not isinstance(value, str) or not HEX.fullmatch(value):
            raise FixtureError('invalid bound source digest')
    drift_fields = [build[key] for key in ('source_drift', 'product_source_drift') if key in build]
    if (type(build.get('exit_code')) is not int or build['exit_code'] != 0
            or not drift_fields or any(value != [] for value in drift_fields)
            or build.get('binary_sha256') != args.core_sha256
            or build.get('source_binding_sha256') != sha(args.source_binding)
            or sha(core) != args.core_sha256):
        raise FixtureError('candidate binary/build receipt does not match a successful frozen build')
    frozen = inputs()
    product_drift = delta({name: value for name, value in files.items() if is_product(name)},
                          frozen['product'])
    old_tests = {name: value for name, value in files.items()
                 if name.startswith('services/core/tests/')}
    current_tests = snapshot(('services/core/tests',), ())
    old_test_drift = delta(old_tests, current_tests)
    other = {name: value for name, value in files.items()
             if not is_product(name) and name not in old_tests}
    other_current = {name: sha(regular(relative(name))) for name in other}
    other_drift = delta(other, other_current)
    reference = {'source_binding': str(args.source_binding),
                 'source_binding_sha256': sha(args.source_binding),
                 'build_receipt': str(args.build_receipt),
                 'build_receipt_sha256': sha(args.build_receipt),
                 'candidate_sha256': args.core_sha256,
                 'product_drift': product_drift, 'non_test_reference_drift': other_drift,
                 'old_test_binding_drift': old_test_drift,
                 'old_test_binding_sha256': old_tests,
                 'current_all_tests_sha256': current_tests,
                 'note': 'Old test drift is explicit; current related tests are frozen separately. '
                         'It does not relax product/source or final acceptance assertions.'}
    record(args.output_root / 'reference-comparison.json', reference)
    if changed(product_drift) or changed(other_drift):
        raise FixtureError('product/build reference changed; inspect reference-comparison.json')
    env = environment()
    pin = tomllib.loads((REPO / 'services/core/rust-toolchain.toml').read_text())['toolchain']['channel']
    mise_pin = tomllib.loads((REPO / 'mise.toml').read_text())['tools']['rust']['version']
    if not re.fullmatch(r'\d+\.\d+\.\d+', pin) or pin != mise_pin:
        raise FixtureError('repository Rust pins must agree on an exact installed version')
    # Mise's Rust plugin may expose rustup shims, rather than a tool_home/bin.
    # Resolve only tool locations; product and receipt paths still forbid symlinks.
    rustup = shutil.which('rustup', path=env.get('PATH', os.defpath))
    if not rustup:
        raise FixtureError('installed rustup is required; no toolchain is installed implicitly')
    rustup = regular(Path(rustup).resolve())
    env.update({'RUSTUP_AUTO_INSTALL': '0', 'RUSTUP_TOOLCHAIN': pin})
    def installed(name: str) -> Path:
        located = subprocess.check_output([str(rustup), 'which', '--toolchain', pin, name],
                                          cwd=REPO, env=env, text=True, timeout=30).strip()
        return regular(Path(located).resolve())
    cargo, rustc = installed('cargo'), installed('rustc')
    version = subprocess.check_output([str(rustc), '--version'], env=env, text=True, timeout=30).strip()
    if version.split()[:2] != ['rustc', pin]:
        raise FixtureError('installed rustc differs from repository pin; no auto-install')
    compile_tmp = args.output_root / 'compile-tmp'
    compile_tmp.mkdir(mode=0o700)
    env.update({'PATH': str(cargo.parent) + os.pathsep + env.get('PATH', '/usr/bin:/bin'),
                'TMPDIR': str(compile_tmp),
                'RUSTUP_TOOLCHAIN': pin, 'RUSTUP_AUTO_INSTALL': '0',
                'CARGO_NET_OFFLINE': 'true', 'CARGO_BUILD_JOBS': '2', 'SQLX_OFFLINE': 'true',
                'CARGO_TARGET_DIR': str(args.target_root)})
    record(args.output_root / 'inputs.json', frozen)
    command = [str(cargo), 'test', '--locked', '--offline', '--no-run',
               '--test', 'model_process', '--test', 'model_http', '--message-format=json']
    built = run_command(command, env, args.output_root / 'compile.jsonl', 900)
    require_success(built)
    check_inputs(frozen)
    binaries = {}
    for line in (args.output_root / 'compile.jsonl').read_text().splitlines():
        try:
            message = json.loads(line)
        except json.JSONDecodeError:
            continue
        target = message.get('target', {})
        if (message.get('reason') == 'compiler-artifact' and message.get('executable')
                and target.get('kind') == ['test']):
            name = target.get('name')
            if name not in {'model_process', 'model_http'} or name in binaries:
                raise FixtureError('Cargo reported an extra or duplicated test executable')
            binary = regular(Path(message['executable']))
            if not binary.is_relative_to(args.target_root):
                raise FixtureError('Cargo test executable is outside the explicit target directory')
            binaries[name] = binary
    if set(binaries) != {'model_process', 'model_http'}:
        raise FixtureError('Cargo did not report exactly the two requested test executables')
    artifacts_dir = args.output_root / 'artifacts'
    artifacts_dir.mkdir(mode=0o700)
    for name, source in {'lmm-core': core, **binaries}.items():
        copy_binary(source, artifacts_dir / name)
    artifacts = {'artifacts/' + name: sha(artifacts_dir / name)
                 for name in ('lmm-core', 'model_process', 'model_http')}
    lists = {name: test_list(artifacts_dir / name, env, args.output_root / (name + '-list.log'))
             for name in ('model_process', 'model_http')}
    if set(lists['model_process']) != PROCESS_TESTS:
        raise FixtureError('model_process must contain exactly the six reviewed cases')
    check_inputs(frozen)
    artifact_check(args.output_root, artifacts)
    reference_check(reference)
    if sha(core) != args.core_sha256 or artifacts['artifacts/lmm-core'] != args.core_sha256:
        raise FixtureError('explicit Core candidate changed during prepare')
    prepared = {'format': 'lmm-model-process-prepared-v1', 'result': 'PREPARED',
                'source_inputs': frozen, 'reference': reference, 'artifact_sha256': artifacts,
                'test_lists': lists, 'rustc': version, 'target_root': str(args.target_root),
                'reuse_leased_target': args.reuse_leased_target,
                'tools': {str(cargo): sha(cargo), str(rustc): sha(rustc)},
                'compile': built, 'not_acceptance_signoff': True}
    record(args.output_root / 'prepared.json', prepared)
    print('PREPARED: --prepared-sha256 ' + sha(args.output_root / 'prepared.json'))
    return 0


def run(args) -> int:
    output_root(args.output_root)
    private_directory(args.process_root)
    if len(os.fsencode(args.process_root)) > 35 or any(args.process_root.iterdir()):
        raise FixtureError('process root must be empty, private, and at most 35 bytes long')
    prepared_path = args.output_root / 'prepared.json'
    if not HEX.fullmatch(args.prepared_sha256) or sha(regular(prepared_path)) != args.prepared_sha256:
        raise FixtureError('prepared receipt differs from its explicitly supplied SHA-256')
    prepared = read_json(prepared_path)
    if prepared.get('format') != 'lmm-model-process-prepared-v1' or prepared.get('result') != 'PREPARED':
        raise FixtureError('unsupported or unsuccessful prepare receipt')
    frozen, artifacts, reference = (prepared['source_inputs'], prepared['artifact_sha256'], prepared['reference'])
    check_inputs(frozen)
    artifact_check(args.output_root, artifacts)
    reference_check(reference)
    if artifacts['artifacts/lmm-core'] != reference['candidate_sha256']:
        raise FixtureError('frozen Core does not match the explicitly bound candidate')
    attempt = args.output_root / ('run-' + secrets.token_hex(8))
    attempt.mkdir(mode=0o700)
    env = environment()
    env.update({'TMPDIR': str(args.process_root), 'LMM_MODEL_PROCESS_ROOT': str(args.process_root),
                'LMM_MODEL_PROCESS_CORE_BIN': str(args.output_root / 'artifacts/lmm-core'),
                'LMM_MODEL_PROCESS_CORE_SHA256': artifacts['artifacts/lmm-core']})
    lists = {name: test_list(args.output_root / 'artifacts' / name, env, attempt / (name + '-list.log'))
             for name in ('model_process', 'model_http')}
    if lists != prepared['test_lists'] or set(lists['model_process']) != PROCESS_TESTS:
        raise FixtureError('frozen test discovery differs from prepare')
    fixture = Fixture(attempt)
    # The fixture fixes the Docker host itself; preserve only its fresh private config.
    fixture.env = {**environment(), 'DOCKER_CONFIG': fixture.env['DOCKER_CONFIG']}
    results = {}
    cleanup_state = 'NOT_REQUESTED'
    started = time.monotonic()
    try:
        fixture.start()
        config = dict(line.split('=', 1) for line in (fixture.root / 'postgres.env').read_text().splitlines())
        env['DATABASE_URL'] = (f'postgresql://a09_admin:{config["POSTGRES_PASSWORD"]}'
                               f'@127.0.0.1:{fixture.binding["port"]}/postgres')
        for name in ('model_process', 'model_http'):
            fixture.verify()
            result = run_command([str(args.output_root / 'artifacts' / name),
                                  '--nocapture', '--test-threads=1'], env, fixture.root / (name + '.log'), 900)
            results[name] = result
            text = (fixture.root / (name + '.log')).read_text()
            matches = re.findall(r'^test result: (?:ok|FAILED)\. (\d+) passed; (\d+) failed; (\d+) ignored; '
                                 r'(\d+) measured; (\d+) filtered out;', text, re.MULTILINE)
            if len(matches) == 1:
                result['counts'] = dict(zip(('passed', 'failed', 'ignored', 'measured', 'filtered'),
                                           map(int, matches[0])))
            require_success(result)
            if len(matches) != 1 or tuple(map(int, matches[0])) != (len(lists[name]), 0, 0, 0, 0):
                raise FixtureError('full test count and zero skipped/filtered cases were not confirmed')
        check_inputs(frozen)
        artifact_check(args.output_root, artifacts)
        reference_check(reference)
        if sha(regular(prepared_path)) != args.prepared_sha256:
            raise FixtureError('prepared receipt changed during run')
        fixture.verify()
        cleanup_state = 'UNKNOWN'
        fixture.cleanup()
        cleanup_state = 'CONFIRMED_REMOVED'
        record(attempt / 'acceptance.json', {
            'format': 'lmm-model-process-run-v1', 'result': 'PASS',
            'prepared_sha256': args.prepared_sha256, 'source_inputs': frozen,
            'artifact_sha256': artifacts, 'test_results': results,
            'fixture_root': str(fixture.root), 'fixture_binding': fixture.binding,
            'fixture_identity': fixture.identity, 'cluster_removed': True,
            'cleanup_state': cleanup_state,
            'process_evidence_root': str(args.process_root),
            'seconds': round(time.monotonic() - started, 3),
            'scope': 'real Core OS HTTP/SSE process; owned PG; synthetic accounts/credits; loopback provider',
            'not_run': ['production accounts or upstream providers', 'full repository acceptance'],
            'not_acceptance_signoff': True,
        })
        print('PASS: complete model process and model_http runs; private receipt ' + str(attempt / 'acceptance.json'))
        return 0
    except (FixtureError, OSError, subprocess.SubprocessError) as error:
        drift = None
        try:
            drift = inputs() != frozen
        except (FixtureError, OSError):
            pass
        record(attempt / 'failure.json', {
            'format': 'lmm-model-process-run-v1', 'result': 'FAIL',
            'error_type': type(error).__name__, 'error': str(error),
            'test_results': results, 'source_drift': drift,
            'prepared_sha256': args.prepared_sha256, 'fixture_root': str(fixture.root),
            'cleanup_state': cleanup_state,
            'cluster_removed': (None if cleanup_state == 'UNKNOWN' else cleanup_state == 'CONFIRMED_REMOVED'),
            'seconds': round(time.monotonic() - started, 3),
        })
        print('FAIL: private evidence ' + str(attempt) + '; cleanup ' + cleanup_state, file=sys.stderr)
        return 1


def main() -> int:
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    phases = parser.add_subparsers(dest='phase', required=True)
    prep = phases.add_parser('prepare', help='offline compile and seal; coordinate Cargo slot before invoking')
    execute = phases.add_parser('run', help='no Cargo; fresh owned PG; complete frozen test executables')
    for action in (prep, execute):
        action.add_argument('--output-root', type=Path, required=True)
    prep.add_argument('--target-root', type=Path, required=True)
    prep.add_argument('--reuse-leased-target', action='store_true',
                      help='only standard Core target, after owner releases slot; include it in agcc roots')
    prep.add_argument('--core-binary', type=Path, required=True)
    prep.add_argument('--core-sha256', required=True)
    prep.add_argument('--source-binding', type=Path, required=True)
    prep.add_argument('--build-receipt', type=Path, required=True)
    execute.add_argument('--process-root', type=Path, required=True)
    execute.add_argument('--prepared-sha256', required=True)
    args = parser.parse_args()
    try:
        return prepare(args) if args.phase == 'prepare' else run(args)
    except (FixtureError, OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as error:
        print('BLOCKED: ' + str(error) + '; prior evidence was not rewritten', file=sys.stderr)
        return 2


if __name__ == '__main__':
    raise SystemExit(main())
