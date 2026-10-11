#!/usr/bin/env python3
"""Tests of the gate, not evidence of application behavior."""
from __future__ import annotations

from datetime import date
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest

SOURCE = Path(__file__).with_name('quality-gate.py')
spec = importlib.util.spec_from_file_location('quality_gate', SOURCE)
gate = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = gate
spec.loader.exec_module(gate)


def registry() -> dict:
    return {'version': 1, 'baseline_revision': 'a' * 40, 'records': []}


def record() -> dict:
    return {'id': 'DEBT-1', 'kind': 'debt', 'status': 'open', 'paths': ['apps/web/src/example.ts'],
            'owner': 'author', 'reviewer': 'pending', 'issue': 'https://example.invalid/issue/1',
            'opened': '2026-10-01', 'expires': '2026-10-20', 'reason': 'A confirmed gap',
            'risk': 'normal', 'alternative_validation': 'Run the unchanged contract tests',
            'exit_condition': 'Replace the temporary branch and confirm its callers', 'evidence': ''}


class GateTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name) / 'repo'
        self.root.mkdir()
        self.out = Path(self.temporary.name) / 'logs'
        self.out.mkdir()
        self.run_git('init', '-q')
        self.run_git('config', 'user.name', 'Gate fixture')
        self.run_git('config', 'user.email', 'fixture@example.invalid')
        self.write('.quality/debt.json', json.dumps(registry()))
        self.write('apps/web/src/a.test.ts', 'expect(actual).toBe(expected)\n')
        self.commit()
        self.base = self.run_git('rev-parse', 'HEAD').strip()

    def run_git(self, *args):
        return subprocess.run(['git', *args], cwd=self.root, text=True,
                              capture_output=True, check=True).stdout

    def write(self, name, value):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(value)
        return path

    def commit(self):
        self.run_git('add', '.')
        self.run_git('commit', '-q', '-m', 'fixture')

    def add_cli_submodule(self):
        origin = Path(self.temporary.name) / 'cli-origin'
        origin.mkdir()
        for args in (('init', '-q'), ('config', 'user.name', 'CLI fixture'),
                     ('config', 'user.email', 'fixture@example.invalid')):
            subprocess.run(['git', '-C', str(origin), *args], check=True, capture_output=True)
        (origin / 'source.rs').write_text('fn main() {}\n')
        (origin / '.gitignore').write_text('/target/\n')
        subprocess.run(['git', '-C', str(origin), 'add', '.'], check=True, capture_output=True)
        subprocess.run(['git', '-C', str(origin), '-c', 'commit.gpgsign=false',
                        'commit', '-qm', 'CLI fixture'], check=True, capture_output=True)
        self.run_git('-c', 'protocol.file.allow=always', 'submodule', 'add', '-q', str(origin), gate.CLI)
        self.child = self.root / gate.CLI
        self.child_git('config', 'user.name', 'CLI fixture')
        self.child_git('config', 'user.email', 'fixture@example.invalid')
        self.commit()
        return self.child_git('rev-parse', 'HEAD').strip()

    def child_git(self, *args):
        return subprocess.run(['git', '-C', str(self.child), *args], text=True,
                              capture_output=True, check=True).stdout

    def test_committed_staged_unstaged_and_untracked_changes_are_all_checked(self):
        self.write('apps/web/src/committed.ts', 'const a: any = 1\n')
        self.commit()
        self.write('apps/web/src/staged.ts', 'const b: any = 2\n')
        self.run_git('add', '.')
        self.write('apps/web/src/a.test.ts', 'test.only("changed", () => {})\n')
        self.write('apps/web/src/untracked.ts', 'try {} catch (e) {\n}\n')
        change = gate.inspect_change(self.root, self.base)
        self.assertEqual(len(change['changed_files']), 4)
        self.assertEqual({item['rule'] for item in change['new_signals']},
                         {'broad-any', 'test-selection', 'empty-catch'})
        self.assertEqual(change['removed_assertion_files'], ['apps/web/src/a.test.ts'])
        self.assertEqual(gate.conclusion({'change': change}), ('review-required', 3))

    def test_baseline_does_not_hide_additional_occurrences(self):
        path = 'apps/web/src/value.ts'
        self.write(path, 'let value: any\n')
        self.commit()
        base = self.run_git('rev-parse', 'HEAD').strip()
        self.write(path, '\nlet value: any\n')
        self.assertEqual(gate.inspect_change(self.root, base)['new_signals'], [])
        self.write(path, 'let value: any\nlet value: any\n')
        result = gate.inspect_change(self.root, base)
        self.assertEqual(result['existing_signals_in_changed_files'], 1)
        self.assertEqual(len(result['new_signals']), 1)

    def test_deleted_assertions_and_gate_configuration_need_review(self):
        (self.root / 'apps/web/src/a.test.ts').unlink()
        self.write('.gitignore', '*\n')
        change = gate.inspect_change(self.root, self.base)
        self.assertIn('apps/web/src/a.test.ts', change['removed_assertion_files'])
        # A tracked ignore-file change cannot hide itself.
        self.run_git('add', '-f', '.gitignore')
        self.assertIn('.gitignore', gate.inspect_change(self.root, self.base)['protected_changes'])
        self.assertTrue(gate.sensitive('apps/web/tsconfig.json'))

    def test_snapshot_binds_untracked_content_and_file_modes(self):
        first = gate.snapshot(self.root)
        path = self.write('new.py', 'print(1)\n')
        second = gate.snapshot(self.root)
        self.assertNotEqual(first, second)
        path.write_text('print(2)\n')
        self.assertNotEqual(second, gate.snapshot(self.root))
        before_mode = gate.snapshot(self.root)
        path.chmod(0o755)
        self.assertNotEqual(before_mode, gate.snapshot(self.root))

    def test_cli_submodule_accepts_parent_pin_and_ignored_build_output(self):
        pin = self.add_cli_submodule()
        before = gate.snapshot(self.root)
        (self.child / 'target').mkdir()
        (self.child / 'target/lmm').write_text('ignored build output\n')
        self.assertEqual(before, gate.snapshot(self.root))
        self.assertEqual(before['cli_submodule'], pin)

    def test_cli_submodule_rejects_uninitialized_checkout(self):
        self.add_cli_submodule()
        self.run_git('submodule', 'deinit', '-f', '--', gate.CLI)
        with self.assertRaisesRegex(ValueError, 'not initialized'):
            gate.snapshot(self.root)

    def test_cli_submodule_rejects_head_different_from_parent_pin(self):
        self.add_cli_submodule()
        (self.child / 'source.rs').write_text('fn main() { println!("changed"); }\n')
        self.child_git('add', '.')
        self.child_git('-c', 'commit.gpgsign=false', 'commit', '-qm', 'Different CLI revision')
        with self.assertRaisesRegex(ValueError, 'HEAD differs from the parent pin'):
            gate.snapshot(self.root)

    def test_cli_submodule_rejects_unstaged_staged_and_untracked_source(self):
        self.add_cli_submodule()
        for kind in ('unstaged', 'staged', 'untracked'):
            with self.subTest(kind=kind):
                name = 'new.rs' if kind == 'untracked' else 'source.rs'
                path = self.child / name
                path.write_text('changed source\n')
                if kind == 'staged':
                    self.child_git('add', name)
                with self.assertRaisesRegex(ValueError, 'uncommitted or untracked source'):
                    gate.snapshot(self.root)
                if kind == 'untracked':
                    path.unlink()
                else:
                    self.child_git('reset', '--hard', 'HEAD')

    def test_symlink_source_is_not_followed(self):
        outside = self.out / 'secret.ts'
        outside.write_text('private')
        (self.root / 'linked.ts').symlink_to(outside)
        with self.assertRaises(ValueError):
            gate.inspect_change(self.root, self.base)

    def test_duplicate_json_and_duplicate_dependencies_are_not_silent(self):
        for raw in ('{"a":1,"a":2}', '{"a":NaN}'):
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                gate.read_json(raw)
        self.write('package.json', json.dumps({'dependencies': {'one': '1'}, 'devDependencies': {'one': '1'}}))
        change = gate.inspect_change(self.root, self.base)
        self.assertEqual(change['new_signals'][0]['rule'], 'duplicate-direct-dependency')

    def test_debt_expiry_scope_and_independent_review_are_validated(self):
        good = record()
        mutations = [{'opened': '2026-10-12'}, {'expires': '2026-10-10'}, {'expires': '2027-10-10'}, {'paths': ['**/*']},
                     {'paths': ['.']}, {'paths': ['../other']}, {'reviewer': 'author'},
                     {'kind': 'exception', 'risk': 'critical'}, {'status': 'resolved'},
                     {'unknown': 'value'}]
        for change in mutations:
            with self.subTest(change=change):
                data = registry()
                data['records'] = [dict(good, **change)]
                self.write('.quality/debt.json', json.dumps(data))
                with self.assertRaises(ValueError):
                    gate.debt_records(self.root, date(2026, 10, 11))
        data['records'] = [good]
        self.write('.quality/debt.json', json.dumps(data))
        self.assertEqual(gate.debt_records(self.root, date(2026, 10, 11)), [good])

    def test_related_has_fixed_core_regression_and_full_includes_nested_modules(self):
        related = gate.plan(self.root, 'related', ['docs/new.md'], self.out)
        self.assertTrue({'core-regression', 'extensions-regression', 'routing-regression',
                         'charging-regression'} <= {c.name for c in related})
        new_module = gate.EXT + '/internal/modules/future/pgtest'
        self.write(new_module + '/go.mod', 'module fixture\n')
        full = gate.plan(self.root, 'full', ['docs/new.md'], self.out)
        tested_dirs = {c.cwd for c in full if c.tests == 'go'}
        self.assertTrue(set(gate.PG_ENV) | {new_module} <= tested_dirs)
        for check in related:
            if check.name in {'core-regression', 'routing-regression', 'charging-regression'}:
                self.assertEqual(check.needs_env, ('DATABASE_URL',))
        self.assertTrue({'protocol-generated', 'core-all-tests', 'events-process', 'rpc-docker',
                         'typecheck:web'} <= {c.name for c in full})
        self.assertEqual(len(full), len({c.name for c in full}))

    def test_database_guard_rejects_missing_remote_and_override_destinations(self):
        examples = [({}, 'DATABASE_URL'),
                    ({'DATABASE_URL': 'postgres://u:p@127.0.0.1/postgres'}, 'DATABASE_URL'),
                    ({'LMM_QUALITY_DISPOSABLE': '1', 'DATABASE_URL': 'postgres://u:secret@production.invalid/db'}, 'DATABASE_URL'),
                    ({'LMM_QUALITY_DISPOSABLE': '1', 'DATABASE_URL': 'postgres://u:secret@localhost/db?host=production.invalid'}, 'DATABASE_URL'),
                    ({'LMM_QUALITY_DISPOSABLE': '1', 'PAYMENT_TEST_DSN': 'postgres://u:secret@localhost/live'}, 'PAYMENT_TEST_DSN')]
        for env, key in examples:
            with self.subTest(key=key):
                reason = gate.database_problem(key, env)
                self.assertIsNotNone(reason)
                self.assertNotIn('secret', reason)
        env = {'LMM_QUALITY_DISPOSABLE': '1', 'PAYMENT_TEST_DSN': 'postgres://u:p@127.0.0.1/lmm_payments_test?sslmode=disable'}
        self.assertIsNone(gate.database_problem('PAYMENT_TEST_DSN', env))

    def test_real_process_success_failure_missing_tool_and_blocked_database(self):
        cases = [('ok', (sys.executable, '-c', 'print("executed")'), 'passed'),
                 ('fail', (sys.executable, '-c', 'raise SystemExit(7)'), 'failed'),
                 ('missing', ('/definitely-missing-quality-tool',), 'blocked')]
        for name, cmd, status in cases:
            with self.subTest(name=name):
                result = gate.execute(self.root, gate.Check(name, cmd), self.out, dict(os.environ), {}, 5)
                self.assertEqual(result['status'], status)
                self.assertEqual((self.out / result['log']).stat().st_mode & 0o777, 0o600)
        target = self.out / 'must-not-exist'
        command = (sys.executable, '-c', f'open({str(target)!r}, "w").close()')
        blocked = gate.execute(self.root, gate.Check('database', command, needs_env=('DATABASE_URL',)), self.out, {}, {}, 5)
        self.assertEqual(blocked['status'], 'blocked')
        self.assertFalse(target.exists())

    def test_timeout_stops_child_process_group(self):
        pid_file = self.out / 'child.pid'
        code = ('import subprocess,time; from pathlib import Path; '
                'p=subprocess.Popen(["' + sys.executable + '","-c","import time;time.sleep(60)"]); '
                f'Path({str(pid_file)!r}).write_text(str(p.pid)); time.sleep(60)')
        check = gate.Check('timeout', (sys.executable, '-c', code))
        result = gate.execute(self.root, check, self.out, dict(os.environ), {}, 3)
        self.assertEqual(result['status'], 'timed-out')
        child = int(pid_file.read_text())
        status_path = Path(f'/proc/{child}/status')
        if status_path.exists():
            self.assertIn('State:\tZ', status_path.read_text())

    def test_empty_and_skipped_test_suites_do_not_count_as_passed(self):
        log = self.out / 'tests.jsonl'
        log.write_text('{"Action":"pass","Package":"fixture"}\n')
        self.assertIn('no executed', gate.test_result('go', log))
        log.write_text('{"Action":"skip","Test":"T","Package":"fixture"}\n')
        self.assertIn('skipped', gate.test_result('go', log))
        log.write_text('{"Action":"pass","Test":"T","Package":"fixture"}\n')
        self.assertIsNone(gate.test_result('go', log))
        log.write_text('test result: ok. 0 passed; 0 failed; 1 ignored\n')
        self.assertIn('skipped', gate.test_result('rust', log))
        log.write_text('Ran 0 tests in 0.000s\n')
        self.assertIn('no executed', gate.test_result('unittest', log))
        log.write_text('Ran 3 tests in 0.000s\n')
        self.assertIsNone(gate.test_result('unittest', log))
        log.write_text('# skipped 2\n')
        self.assertIn('skipped', gate.test_result('text', log))

    def test_status_aggregation_never_turns_partial_results_green(self):
        for status, expected in [('failed', 1), ('timed-out', 1), ('blocked', 2),
                                 ('not-run', 2), ('interrupted', 2), ('review-required', 3)]:
            with self.subTest(status=status):
                self.assertEqual(gate.conclusion({'checks': [{'status': 'passed'}, {'status': status}]})[1], expected)
        self.assertEqual(gate.conclusion({'checks': [{'status': 'unknown'}]})[1], 1)
        self.assertEqual(gate.conclusion({'checks': []})[1], 2)
        self.assertEqual(gate.conclusion({'plan_only': True})[1], 2)
        self.assertEqual(gate.conclusion({'errors': ['changed source']})[1], 1)

    def test_cli_rejects_source_changes_during_execution(self):
        self.write('scripts/quality-gate.py', SOURCE.read_text())
        self.write('scripts/test-quality-gate.py',
                   'from pathlib import Path; Path("changed.py").write_text("changed")\n')
        self.commit()
        output = self.out / 'changed-run'
        result = subprocess.run([sys.executable, '-B', str(self.root / 'scripts/quality-gate.py'),
                                 'quick', '--base', self.base, '--output', str(output)],
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 1)
        report = json.loads((output / 'report.json').read_text())
        self.assertIn('source changed during checks; evidence is stale', report['errors'])

    def test_generated_protocol_check_rejects_orphaned_generated_files(self):
        # Compiler/installer/formatter fixtures isolate the real comparison script.
        # This is not a protobuf generation or business acceptance test.
        script = self.write('scripts/generate-core-protocol.sh',
                            SOURCE.with_name('generate-core-protocol.sh').read_text())
        self.write('contracts/proto/lmm/core/v1/control.proto', 'syntax = "proto3";\n')
        generated = 'services/extensions/internal/corepb/'
        self.write(generated + 'control.pb.go', 'package corepb\n')
        tools = self.root / 'fixture-tools'
        tools.mkdir()
        for name in ('go', 'gofmt'):
            file = tools / name
            file.write_text('#!/bin/sh\nexit 0\n')
            file.chmod(0o755)
        compiler = tools / 'protoc'
        compiler.write_text('#!' + sys.executable + '\n'
                            'import sys; from pathlib import Path\n'
                            'out=next(a.split("=",1)[1] for a in sys.argv if a.startswith("--go_out="))\n'
                            'Path(out,"internal/corepb/control.pb.go").write_text("package corepb\\n")\n')
        compiler.chmod(0o755)
        env = dict(os.environ, PATH=str(tools) + os.pathsep + os.environ['PATH'])
        command = ['bash', str(script), '--check']
        success = subprocess.run(command, env=env, capture_output=True, text=True)
        self.assertEqual(success.returncode, 0, success.stderr)
        orphan = self.write(generated + 'retired.pb.go', 'package corepb\n')
        rejected = subprocess.run(command, env=env, capture_output=True, text=True)
        self.assertNotEqual(rejected.returncode, 0, 'orphaned bindings incorrectly accepted')
        self.assertTrue(orphan.exists(), 'a check must not delete source to turn green')

    def test_cli_plan_records_no_execution_and_refuses_overwriting_evidence(self):
        self.write('scripts/quality-gate.py', SOURCE.read_text())
        self.commit()
        output = self.out / 'run'
        args = [sys.executable, '-B', str(self.root / 'scripts/quality-gate.py'), 'full',
                '--base', self.base, '--output', str(output), '--plan']
        result = subprocess.run(args, capture_output=True, text=True)
        self.assertEqual(result.returncode, 2, result.stderr)
        report = json.loads((output / 'report.json').read_text())
        self.assertEqual(report['status'], 'blocked')
        self.assertTrue(all(c['status'] == 'not-run' for c in report['checks']))
        self.assertEqual((output / 'report.json').stat().st_mode & 0o777, 0o600)
        original = (output / 'report.json').read_bytes()
        self.assertNotEqual(subprocess.run(args, capture_output=True).returncode, 0)
        self.assertEqual((output / 'report.json').read_bytes(), original)


if __name__ == '__main__':
    unittest.main()
