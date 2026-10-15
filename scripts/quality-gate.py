#!/usr/bin/env python3
"""Local quality checks. No network service, CI dispatch, or approval authority."""
from __future__ import annotations

import argparse
from collections import Counter
from dataclasses import dataclass
from datetime import date, datetime, timezone
import difflib
import hashlib
import json
import os
import platform
from pathlib import Path, PurePosixPath
import re
import shlex
import signal
import subprocess
import sys
import time
from urllib.parse import parse_qs, urlsplit

ROOT = Path(__file__).resolve().parents[1]
CORE = 'services/core'
EXT = 'services/extensions'
CLI = 'apps/cli'
PYTHON = sys.executable
SOURCE_SUFFIXES = {'.py', '.go', '.rs', '.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs'}
RULES = {
    'ignore-directive': r'@ts-(?:ignore|nocheck)|(?:eslint|oxlint)-disable|\bnolint\b|\bnoqa\b|#\s*\[\s*allow\b',
    'test-selection': r'\b(?:test|it|describe)\s*\.\s*(?:skip|skipIf|skipUnless|only|todo|runIf)\b|\bt\.Skip(?:f|Now)?\s*\(|#\s*\[\s*ignore\b|@(?:unittest\.)?skip(?:If|Unless)?\b|pytest\.mark\.(?:skip|skipif|xfail)\b',
    'empty-catch': r'\bcatch\s*(?:\([^)]*\))?\s*\{\s*\}',
    'unfinished-implementation': r'\b(?:todo|unimplemented)!\s*\(|\braise\s+NotImplementedError\b',
    'test-dependent-behavior': r'cfg!\s*\(\s*test\s*\)|\b(?:NODE_ENV|PYTEST_CURRENT_TEST|LMM_\w*TEST\w*|LMM_\w*FIXTURE\w*)\b',
    'temporary-code': r'\b(?:TODO|FIXME|HACK|TEMPORARY)\b',
}
PG_ENV = {
    f'{EXT}/internal/modules/store/pgtest': 'LMM_STORE_TEST_DSN',
    f'{EXT}/internal/modules/payments/pgtest': 'PAYMENT_TEST_DSN',
    f'{EXT}/internal/modules/assistant/pgtest': 'LMM_MK09_TEST_DSN',
}


def digest(raw: bytes) -> str:
    return hashlib.sha256(raw).hexdigest()


def git(root: Path, *args: str) -> bytes:
    result = subprocess.run(['git', '--no-replace-objects', *args], cwd=root,
                            capture_output=True, timeout=30, check=False)
    if result.returncode:
        raise ValueError('Git read failed: ' + shlex.join(args[:2]))
    return result.stdout


def unique_object(pairs: list) -> dict:
    value = {}
    for key, item in pairs:
        if key in value:
            raise ValueError('duplicate JSON key: ' + key)
        value[key] = item
    return value


def read_json(raw: str) -> dict:
    def invalid_constant(value: str) -> None:
        raise ValueError('non-standard JSON constant: ' + value)
    result = json.loads(raw, object_pairs_hook=unique_object, parse_constant=invalid_constant)
    if not isinstance(result, dict):
        raise ValueError('expected a JSON object')
    return result


def paths(root: Path) -> list[str]:
    raw = git(root, 'ls-files', '-z', '--cached', '--others', '--exclude-standard')
    return sorted(set(x.decode('utf-8') for x in raw.split(b'\0') if x))


def cli_submodule(root: Path) -> str | None:
    """Only the extracted CLI must be an initialized, clean, pinned child."""
    entries = git(root, 'ls-files', '--stage', '-z', '--', CLI).split(b'\0')
    pin = None
    for entry in entries:
        if not entry:
            continue
        metadata, name = entry.split(b'\t', 1)
        mode, sha, stage = metadata.split()
        if name.decode('utf-8') == CLI and mode == b'160000':
            if stage != b'0':
                raise ValueError('resolve CLI submodule conflicts before checking')
            pin = sha.decode('ascii')
    if pin is None:
        return None
    child = root / CLI
    if not (child / '.git').exists():
        raise ValueError('CLI submodule is not initialized: ' + CLI)
    top = Path(git(child, 'rev-parse', '--show-toplevel').decode().strip()).resolve()
    if top != child.resolve():
        raise ValueError('CLI submodule is not initialized: ' + CLI)
    head = git(child, 'rev-parse', '--verify', 'HEAD^{commit}').decode().strip()
    if head != pin:
        raise ValueError('CLI submodule HEAD differs from the parent pin: ' + CLI)
    if git(child, 'status', '--porcelain=v1', '--untracked-files=all', '--ignore-submodules=none'):
        raise ValueError('CLI submodule has uncommitted or untracked source: ' + CLI)
    return pin


def index_entries(root: Path) -> dict[str, tuple[str, str]]:
    entries = {}
    for raw in git(root, 'ls-files', '--stage', '-z').split(b'\0'):
        if not raw:
            continue
        metadata, name = raw.split(b'\t', 1)
        mode, sha, stage = metadata.decode('ascii').split()
        if stage != '0':
            raise ValueError('resolve unmerged index entries before checking')
        entries[name.decode('utf-8')] = (mode, sha)
    return entries


def index_source(root: Path, name: str, entries: dict) -> str:
    if name not in entries:
        return ''
    mode, sha = entries[name]
    if mode not in {'100644', '100755'}:
        raise ValueError('cannot inspect non-regular index source: ' + name)
    if int(git(root, 'cat-file', '-s', sha)) > 2_000_000:
        raise ValueError('source exceeds inspection limit; review separately: ' + name)
    return git(root, 'cat-file', 'blob', sha).decode('utf-8')


def snapshot(root: Path) -> dict:
    """Bind tracked, staged, unstaged and non-ignored untracked source, plus modes."""
    entries = index_entries(root)
    cli_pin = cli_submodule(root)
    items = {}
    for name in paths(root):
        path = root / name
        if path.is_symlink():
            items[name] = ['symlink', os.readlink(path)]
        elif path.is_file():
            with path.open('rb') as stream:
                value = hashlib.file_digest(stream, 'sha256').hexdigest()
            items[name] = [path.stat().st_mode & 0o777, value]
        else:
            items[name] = ['deleted-or-submodule']
    submodules = git(root, 'submodule', 'status', '--recursive').decode('utf-8')
    status = git(root, 'status', '--porcelain=v1', '--untracked-files=all', '--ignore-submodules=none').decode()
    return {'sha256': digest(json.dumps([items, entries, submodules, status], sort_keys=True).encode()),
            'file_count': len(items), 'submodules': submodules, 'cli_submodule': cli_pin}


def sensitive(name: str) -> bool:
    leaf = PurePosixPath(name).name.lower()
    return (name.startswith(('.github/', '.quality/', 'scripts/', 'contracts/'))
            or leaf in {'justfile', 'package.json', 'cargo.toml', 'cargo.lock', 'go.mod',
                        'go.sum', 'bun.lock', 'turbo.json', 'mise.toml', 'mise.lock', '.gitignore', '.gitattributes'}
            or any(word in leaf for word in ('test', 'spec', 'lint', 'baseline', 'coverage', 'tsconfig')))


def source_text(root: Path, name: str) -> str:
    path = root / name
    if path.is_symlink():
        raise ValueError('cannot inspect symlink source: ' + name)
    if not path.exists():
        return ''
    if not path.is_file() or not path.resolve().is_relative_to(root):
        raise ValueError('cannot inspect non-regular source: ' + name)
    if path.stat().st_size > 2_000_000:
        raise ValueError('source exceeds inspection limit; review separately: ' + name)
    return path.read_text(encoding='utf-8')


def signals(name: str, text: str) -> list[dict]:
    rules = dict(RULES)
    if name.endswith(('.ts', '.tsx')):
        rules['broad-any'] = r'\bany\b'
    output = []
    lines = text.splitlines()
    for rule, pattern in rules.items():
        for match in re.finditer(pattern, text, re.MULTILINE):
            # Do not persist source snippets, which can contain secrets.
            line = text.count('\n', 0, match.start()) + 1
            context = lines[line - 1].strip()
            key = digest((name + '\0' + rule + '\0' + context).encode())
            output.append({'id': key, 'rule': rule, 'path': name, 'line': line})
    return output


def inspect_change(root: Path, base: str) -> dict:
    entries = index_entries(root)
    base_paths = set(x.decode() for x in git(root, 'ls-tree', '-r', '--name-only', '-z', base).split(b'\0') if x)
    changed = set(x.decode() for x in git(root, 'diff', '--no-ext-diff', '--no-renames', '--name-only', '-z', base, '--').split(b'\0') if x)
    changed.update(x.decode() for x in git(root, 'diff', '--cached', '--no-ext-diff', '--no-renames',
                                            '--name-only', '-z', base, '--').split(b'\0') if x)
    changed.update(x.decode() for x in git(root, 'ls-files', '-z', '--others', '--exclude-standard').split(b'\0') if x)
    result = {'changed_files': sorted(changed), 'protected_changes': [], 'new_signals': [],
              'removed_assertion_files': [], 'high_risk_changes': [], 'existing_signals_in_changed_files': 0}
    for name in sorted(changed):
        if sensitive(name):
            result['protected_changes'].append(name)
        if name.startswith((CORE + '/src/', CORE + '/schema/', EXT + '/internal/modules/payments/',
                            EXT + '/internal/modules/assistant/', EXT + '/internal/modules/store/')):
            result['high_risk_changes'].append(name)
        if PurePosixPath(name).suffix not in SOURCE_SUFFIXES and not name.endswith('package.json'):
            continue
        before = git(root, 'show', base + ':' + name).decode('utf-8') if name in base_paths else ''
        old_signals = Counter(item['id'] for item in signals(name, before))
        result['existing_signals_in_changed_files'] += sum(old_signals.values())
        combined = {}
        for layer, after in (('index', index_source(root, name, entries)),
                             ('worktree', source_text(root, name))):
            old = old_signals.copy()
            occurrences = Counter()
            for item in signals(name, after):
                if old[item['id']]:
                    old[item['id']] -= 1
                else:
                    # Merge matching occurrences, not all occurrences of the same signal.
                    occurrences[item['id']] += 1
                    key = (item['id'], occurrences[item['id']])
                    combined.setdefault(key, dict(item, layers=[]))['layers'].append(layer)
            if re.search(r'(?:test|spec)', name, re.I):
                removed = '\n'.join(line[1:] for line in difflib.unified_diff(before.splitlines(), after.splitlines())
                                    if line.startswith('-') and not line.startswith('---'))
                if (re.search(r'\b(?:assert\w*|expect|require|Fatalf?|Errorf?)\b', removed)
                        and name not in result['removed_assertion_files']):
                    result['removed_assertion_files'].append(name)
            if name.endswith('package.json') and after:
                data = read_json(after)
                runtime = data.get('dependencies', {})
                development = data.get('devDependencies', {})
                previous = read_json(before) if before else {}
                old_duplicates = set(previous.get('dependencies', {})) & set(previous.get('devDependencies', {}))
                for dependency in sorted((set(runtime) & set(development)) - old_duplicates):
                    key = ('duplicate-direct-dependency', dependency)
                    combined.setdefault(key, {'rule': 'duplicate-direct-dependency', 'path': name,
                                              'dependency': dependency, 'layers': []})['layers'].append(layer)
        result['new_signals'].extend(combined.values())
    return result


def debt_records(root: Path, today: date) -> list[dict]:
    data = read_json((root / '.quality/debt.json').read_text())
    if set(data) != {'version', 'baseline_revision', 'records'} or data['version'] != 1:
        raise ValueError('invalid debt registry format')
    if not re.fullmatch('[0-9a-f]{40}', data['baseline_revision']):
        raise ValueError('debt baseline must name a full source SHA')
    if not isinstance(data['records'], list):
        raise ValueError('records must be an array')
    ids = set()
    required = {'id', 'kind', 'status', 'paths', 'owner', 'reviewer', 'issue', 'opened',
                'expires', 'reason', 'risk', 'alternative_validation', 'exit_condition', 'evidence'}
    for entry in data['records']:
        if not isinstance(entry, dict) or set(entry) != required:
            raise ValueError('invalid debt/exception fields')
        for key in required - {'paths', 'evidence'}:
            if not isinstance(entry[key], str) or not entry[key].strip():
                raise ValueError('empty debt field: ' + key)
        if entry['id'] in ids:
            raise ValueError('duplicate debt ID')
        ids.add(entry['id'])
        if entry['kind'] not in {'debt', 'exception'} or entry['status'] not in {'open', 'resolved'}:
            raise ValueError('invalid debt kind/status')
        if entry['risk'] not in {'normal', 'critical'}:
            raise ValueError('invalid debt risk')
        if entry['kind'] == 'exception' and entry['risk'] == 'critical':
            raise ValueError('critical correctness cannot be exempted')
        if not isinstance(entry['paths'], list) or not entry['paths']:
            raise ValueError('exact scoped paths are required')
        for name in entry['paths']:
            if (not isinstance(name, str) or not name or name == '.'
                    or PurePosixPath(name).is_absolute() or '..' in PurePosixPath(name).parts
                    or any(c in name for c in '*?[\\')):
                raise ValueError('global or wildcard exception scope is forbidden')
        opened, expires = date.fromisoformat(entry['opened']), date.fromisoformat(entry['expires'])
        if opened > today:
            raise ValueError('debt opening date cannot be in the future')
        if not 0 < (expires - opened).days <= 30:
            raise ValueError('debt review window must be 1..30 days')
        if entry['status'] == 'open' and today >= expires:
            raise ValueError('expired debt/exception: ' + entry['id'])
        if not isinstance(entry['evidence'], str):
            raise ValueError('evidence must be a string')
        if entry['status'] == 'resolved' and not entry['evidence'].strip():
            raise ValueError('resolved debt requires evidence')
        if entry['reviewer'] == entry['owner']:
            raise ValueError('the author cannot be the independent reviewer')
    return data['records']


@dataclass(frozen=True)
class Check:
    name: str
    command: tuple[str, ...]
    cwd: str = '.'
    needs_env: tuple[str, ...] = ()
    after: tuple[str, ...] = ()
    tests: str = ''


def plan(root: Path, level: str, changed: list[str], output: Path) -> list[Check]:
    checks = [Check('gate-self-tests', (PYTHON, '-B', 'scripts/test-quality-gate.py'), tests='unittest'),
              Check('source-boundaries', (PYTHON, '-B', 'scripts/test-core-boundaries.py'), tests='unittest')]
    if level == 'scan':
        return []
    all_paths = level == 'full' or any(sensitive(path) for path in changed)
    affected = lambda path: all_paths or any(x == path or x.startswith(path + '/') for x in changed)
    for path, name in ((CORE, 'core'), (CLI, 'cli')):
        if affected(path):
            checks.append(Check(name + '-format', ('cargo', 'fmt', '--all', '--check'), path))
    if affected(EXT):
        checks.append(Check('go-format', ('bash', 'scripts/check-go-format.sh', EXT)))
    if affected('apps/web'):
        checks.append(Check('web-format', ('bun', 'run', '--filter', '@lmm/web', 'format:check')))
    if level == 'quick':
        return checks
    # Fixed real core regression, even for a docs-only change. No path-only acceptance.
    checks += [Check('core-regression', ('cargo', 'test', '--locked', '--lib'), CORE, ('DATABASE_URL',), tests='rust'),
               Check('routing-regression', ('cargo', 'test', '--locked', '--test', 'routing'), CORE, ('DATABASE_URL',), tests='rust'),
               # Charging contracts now live in the unfiltered library suite above.
               # Keep this separate gate on the real transactional ledger integration.
               Check('charging-regression', ('cargo', 'test', '--locked', '--test', 'assembled_billing'), CORE, ('DATABASE_URL',), tests='rust'),
               Check('extensions-regression', ('go', 'test', '-mod=readonly', '-race', '-count=1', '-json', './...'), EXT, tests='go')]
    if level == 'full' or affected('contracts') or affected(EXT):
        checks.append(Check('protocol-generated', ('bash', 'scripts/generate-core-protocol.sh', '--check')))
    if level != 'full':
        if affected('apps/web'):
            checks += [Check('web-types', ('bun', 'run', 'typecheck:web')),
                       Check('web-tests', ('bun', 'run', 'test:web'), tests='text')]
        for path, key in PG_ENV.items():
            if affected(str(PurePosixPath(path).parent)):
                checks.append(Check(path.replace('/', '-') + '-tests',
                                    ('go', 'test', '-mod=readonly', '-race', '-count=1', '-json', './...'),
                                    path, (key,), tests='go'))
        return checks
    for name in ('check-docs-brand.py', 'test_docs_brand.py', 'test-docs-microservices.py',
                 'test-distribution.py', 'test-local-release-tests.py'):
        checks.append(Check(name, (PYTHON, '-B', 'scripts/' + name), tests='unittest' if name.startswith('test') else ''))
    checks.append(Check('workflow-topology', ('node', '--test', 'scripts/workflow-topology.test.mjs'), tests='text'))
    for path, name in ((CORE, 'core'), (CLI, 'cli')):
        checks += [Check(name + '-lint', ('cargo', 'clippy', '--locked', '--all-targets', '--all-features', '--', '-D', 'warnings'), path),
                   Check(name + '-dependencies', ('cargo', 'tree', '--locked', '--duplicates'), path),
                   Check(name + '-build', ('cargo', 'build', '--locked', '--release'), path)]
    checks.append(Check('extension-host', ('go', 'build', '-mod=readonly', '-trimpath', '-o', str(output / 'lmm-extensions'), './cmd/extensions'), EXT))
    checks.append(Check('core-all-tests', ('cargo', 'test', '--locked', '--all-targets', '--all-features', '--no-fail-fast'), CORE,
                        ('DATABASE_URL',), ('extension-host',), 'rust'))
    checks.append(Check('cli-tests', ('cargo', 'test', '--locked', '--all-targets', '--all-features'), CLI, tests='rust'))
    # go test ./... does not traverse nested modules. Discover every production module.
    modules = sorted({EXT, *PG_ENV, *(str(p.parent.relative_to(root)) for p in (root / EXT).rglob('go.mod'))})
    for path in modules:
        key = path.replace('/', '-')
        env = (PG_ENV[path],) if path in PG_ENV else ()
        checks += [Check(key + '-dependencies', ('go', 'mod', 'verify'), path),
                   Check(key + '-vet', ('go', 'vet', '-mod=readonly', './...'), path)]
        if path != EXT:
            checks.append(Check(key + '-tests', ('go', 'test', '-mod=readonly', '-race', '-count=1', '-json', './...'), path, env, tests='go'))
    checks += [Check('legal-process', (PYTHON, '-B', 'scripts/test-legal-process.py'), after=('extension-host',), tests='unittest'),
               Check('events-rust-fixture', ('cargo', 'build', '--locked', '--example', 'protocol-events-fixture'), CORE),
               Check('events-go-fixture', ('go', 'build', '-mod=readonly', '-o', str(output / 'events-go'), '.'), 'contracts/proto/tests/go'),
               Check('events-process', (PYTHON, '-B', 'contracts/proto/tests/process_recovery.py'), needs_env=('DATABASE_URL',), after=('events-rust-fixture', 'events-go-fixture')),
               Check('rpc-docker', (PYTHON, '-B', 'scripts/test-core-rpc-docker.py'))]
    for name in ('lint:web', 'typecheck:web', 'test:web', 'build:web'):
        checks.append(Check(name, ('bun', 'run', name), tests='text' if name == 'test:web' else ''))
    checks += [Check('web-bundle', ('bun', 'run', '--filter', '@lmm/web', 'bundle:check'), after=('build:web',)),
               Check('web-unused-dependencies', ('bun', 'run', '--filter', '@lmm/web', 'knip'))]
    return checks


def database_problem(key: str, env: dict) -> str | None:
    if env.get('LMM_QUALITY_DISPOSABLE') != '1':
        return 'requires explicit LMM_QUALITY_DISPOSABLE=1 and disposable local PostgreSQL'
    raw = env.get(key, '')
    if not raw:
        return 'missing test environment variable: ' + key
    try:
        url = urlsplit(raw)
        query = parse_qs(url.query, strict_parsing=True)
        if (url.scheme not in {'postgres', 'postgresql'} or url.hostname not in {'127.0.0.1', 'localhost'}
                or url.fragment or any(k not in {'sslmode'} for k in query) or not url.path.startswith('/')):
            return 'test DSN must use loopback and cannot override its host or service'
        if key == 'PAYMENT_TEST_DSN' and url.path != '/lmm_payments_test':
            return 'payments require database lmm_payments_test'
        if key == 'LMM_STORE_TEST_DSN' and not url.path.endswith('_test'):
            return 'store requires a fresh database ending in _test'
        if url.port is not None and not 1 <= url.port <= 65535:
            return 'invalid test database port'
    except ValueError:
        return 'invalid test database URL; value omitted'
    return None


def stop_group(process: subprocess.Popen) -> None:
    for sig, timeout in ((signal.SIGTERM, 3), (signal.SIGKILL, 3)):
        try:
            os.killpg(process.pid, sig)
        except ProcessLookupError:
            break
        try:
            process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            continue
        # The group may contain children after its leader exits.
        if sig == signal.SIGTERM:
            continue
        break
    process.wait(timeout=3)


def test_result(kind: str, log: Path) -> str | None:
    if not kind:
        return None
    passes = 0
    skipped = False
    with log.open(errors='replace') as stream:
        for line in stream:
            line = re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]', '', line)
            if kind == 'go' and line.startswith('{'):
                event = read_json(line)
                if event.get('Test'):
                    passes += event.get('Action') == 'pass'
                    skipped |= event.get('Action') == 'skip'
            elif kind == 'rust':
                match = re.search(r'test result: ok\. (\d+) passed; \d+ failed; (\d+) ignored', line)
                if match:
                    passes += int(match[1])
                    skipped |= int(match[2]) > 0
            else:
                match = re.search(r'(?:Ran (\d+) tests?\b|# tests\s+(\d+)\b|^\s*(\d+) pass\b)', line)
                if match:
                    passes += int(next(value for value in match.groups() if value is not None))
                skipped |= bool(re.search(r'(?:# skipped\s+[1-9]|\b[1-9]\d* skip\b|skipped=[1-9])', line))
    if skipped:
        return 'test runner reported skipped/ignored tests; independent scope review required'
    if passes == 0:
        return 'no executed passing tests found in required suite'
    return None


def execute(root: Path, check: Check, output: Path, env: dict, prior: dict, timeout: float) -> dict:
    result = {'name': check.name, 'command': list(check.command), 'cwd': check.cwd, 'status': 'not-run'}
    for name in check.after:
        if prior.get(name) != 'passed':
            return dict(result, status='blocked', reason='prerequisite did not pass: ' + name)
    for key in check.needs_env:
        reason = database_problem(key, env)
        if reason:
            return dict(result, status='blocked', reason=reason)
    directory = root / check.cwd
    if not directory.is_dir():
        return dict(result, status='blocked', reason='source directory is missing')
    log = output / (check.name.replace(':', '-') + '.log')
    started = time.monotonic()
    process = None
    try:
        with log.open('xb') as stream:
            os.chmod(log, 0o600)
            process = subprocess.Popen(check.command, cwd=directory, env=env, stdin=subprocess.DEVNULL,
                                       stdout=stream, stderr=subprocess.STDOUT, start_new_session=True)
            try:
                code = process.wait(timeout=timeout)
                result.update(status='passed' if code == 0 else 'failed', exit_code=code)
            except subprocess.TimeoutExpired:
                stop_group(process)
                result.update(status='timed-out', reason='process group stopped after check deadline')
            except KeyboardInterrupt:
                stop_group(process)
                result.update(status='interrupted', reason='operator interruption')
        if result['status'] == 'passed':
            reason = test_result(check.tests, log)
            if reason:
                result.update(status='review-required', reason=reason)
    except (OSError, ValueError) as error:
        result.update(status='blocked', reason=type(error).__name__ + ': unable to execute or read check evidence')
    result['seconds'] = round(time.monotonic() - started, 3)
    if log.exists():
        with log.open('rb') as stream:
            result['log_sha256'] = hashlib.file_digest(stream, 'sha256').hexdigest()
        result['log'] = log.name
    return result


def conclusion(report: dict) -> tuple[str, int]:
    checks = report.get('checks', [])
    allowed = {'passed', 'failed', 'timed-out', 'blocked', 'not-run', 'interrupted', 'review-required'}
    if not isinstance(checks, list) or any(not isinstance(item, dict) or item.get('status') not in allowed for item in checks):
        return 'failed', 1
    statuses = {item['status'] for item in checks}
    if report.get('errors') or statuses & {'failed', 'timed-out'}:
        return 'failed', 1
    if ('checks' in report and not checks) or report.get('plan_only') or statuses & {'blocked', 'not-run', 'interrupted'}:
        return 'blocked', 2
    change = report.get('change', {})
    if (report.get('environment_overrides') or statuses & {'review-required'} or change.get('new_signals') or change.get('protected_changes')
            or change.get('removed_assertion_files') or change.get('high_risk_changes')):
        return 'review-required', 3
    return 'passed', 0


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('level', choices=('scan', 'quick', 'related', 'full'))
    parser.add_argument('--base', required=True, help='reviewed ancestor SHA; includes committed and working-tree changes')
    parser.add_argument('--output', required=True, help='new private directory OUTSIDE the checkout')
    parser.add_argument('--plan', action='store_true', help='record unexecuted checks, never a passing result')
    args = parser.parse_args(argv)
    root, output = ROOT.resolve(), Path(args.output).resolve()
    if output.is_relative_to(root):
        parser.error('output must be outside the checkout')
    try:
        output.mkdir(mode=0o700, parents=False, exist_ok=False)
    except OSError:
        parser.error('output must be a new directory with an existing parent')
    report = {'format': 1, 'profile': args.level, 'recorded_utc': datetime.now(timezone.utc).isoformat(),
              'plan_only': args.plan, 'checks': [], 'errors': [], 'acceptance': 'not independently verified',
              'environment': {'system': platform.platform(), 'python': platform.python_version(),
                              'logical_cpus': os.cpu_count()}}
    try:
        base = git(root, 'rev-parse', '--verify', args.base + '^{commit}').decode().strip()
        head = git(root, 'rev-parse', 'HEAD').decode().strip()
        git(root, 'merge-base', '--is-ancestor', base, head)
        if git(root, 'ls-files', '-u'):
            raise ValueError('resolve merge conflicts before checking')
        report.update(base=base, head=head, before=snapshot(root))
        report['change'] = inspect_change(root, base)
        report['debt'] = debt_records(root, datetime.now(timezone.utc).date())
        checks = plan(root, args.level, report['change']['changed_files'], output)
        checks.insert(0, Check('diff-whitespace', ('git', 'diff', '--check', base, '--')))
        env = dict(os.environ, GOWORK='off', GOFLAGS='-mod=readonly', GOTOOLCHAIN='local',
                   PYTHONDONTWRITEBYTECODE='1', PYTHONOPTIMIZE='0', LMM_RELAY_EXTENSION_BIN=str(output / 'lmm-extensions'),
                   LMM_EXTENSION_TEST_BIN=str(output / 'lmm-extensions'), MK06_GO_FIXTURE=str(output / 'events-go'))
        # Record possible runner overrides by name/hash, never by secret value.
        report['environment_overrides'] = {name: digest(os.environ[name].encode()) for name in
            ('RUSTFLAGS', 'CARGO_ENCODED_RUSTFLAGS', 'GOFLAGS', 'NODE_OPTIONS', 'BUN_OPTIONS',
             'PYTHONPATH', 'PYTHONOPTIMIZE', 'PYTEST_ADDOPTS') if os.environ.get(name)}
        # Environment values, including DSNs, are never embedded in the report.
        for key in ('SQL_DSN', 'LOG_SQL_DSN', 'LMM_CORE_DATABASE_URL', 'LMM_CORE_DATABASE_URL_FILE'):
            env.pop(key, None)
        prior = {}
        interrupted = False
        for check in checks:
            if args.plan or interrupted:
                result = {'name': check.name, 'command': list(check.command), 'cwd': check.cwd, 'status': 'not-run'}
            else:
                result = execute(root, check, output, env, prior, {'scan': 30, 'quick': 120, 'related': 600, 'full': 1800}[args.level])
            report['checks'].append(result)
            prior[check.name] = result['status']
            print(check.name + ': ' + result['status'], flush=True)
            interrupted |= result['status'] == 'interrupted'
        if args.level == 'full':
            for record in report['debt']:
                if record['risk'] == 'critical' and record['status'] == 'open':
                    report['checks'].append({'name': record['id'], 'status': 'blocked',
                                              'reason': 'open critical correctness debt; no baseline waiver'})
            report['checks'].append({'name': 'resource-and-business-acceptance', 'status': 'not-run',
                                      'reason': 'Independent process/load/resource and real-funds acceptance required; see docs/quality.md. No manual claim is imported as a pass.'})
        report['after'] = snapshot(root)
        if report['before'] != report['after']:
            report['errors'].append('source changed during checks; evidence is stale')
    except KeyboardInterrupt:
        report['checks'].append({'name': 'operator-interruption', 'status': 'interrupted'})
    except (OSError, ValueError, TypeError, KeyError, UnicodeError, subprocess.SubprocessError) as error:
        report['errors'].append(type(error).__name__ + ': ' + str(error))
    status, code = conclusion(report)
    report.update(status=status, exit_code=code)
    target = output / 'report.json'
    with target.open('x') as stream:
        os.chmod(target, 0o600)
        json.dump(report, stream, indent=2, ensure_ascii=False)
        stream.write('\n')
    print(str(target) + ': ' + status)
    return code


if __name__ == '__main__':
    raise SystemExit(main(sys.argv[1:]))
