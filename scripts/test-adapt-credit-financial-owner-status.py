#!/usr/bin/env python3
"""Offline native owner output/status adaptation; no real controller state or SSH."""
import sys
sys.dont_write_bytecode = True

import copy
import gzip
import atexit
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch


def import_adapter():
    path = Path(__file__).with_name('adapt-credit-financial-owner-status.py')
    spec = importlib.util.spec_from_file_location('financial_owner_status_adapter', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


adapter = import_adapter()
SOURCE = '7af4bf9e56055a9a283b6545433e271a84b60026'
PLAN_SHA = '095347319e48ce3e2400fb1031a611455ed1230b2ad80606e7e5cc7b702617ce'
PROVIDER_SHA = '19dd6ff9d514cfea8338ba0c37b9a62d8ed1e10ddf77c7ba0f41d69a4eacdec9'
ORIGINAL_SHA = '8e5a894f3ca806a1c9f86641e3ac96d2b0de310acc251d5f30a5439b285c611b'
SEALED_SOURCE = Path('/home/lightjunction/.cache/api-release-preparation-20261006/final/source/scripts/run-credit-financial-maintenance.py')
# Recovery authority is immutable. Never substitute the current live runner or
# change the adapter's fixed hash when new deployment support changes that file.
_fixture_dir = tempfile.TemporaryDirectory(prefix='sealed-financial-source-')
atexit.register(_fixture_dir.cleanup)
ORIGINAL_SOURCE = Path(_fixture_dir.name) / 'original.py'
ORIGINAL_SOURCE.write_bytes(gzip.decompress((Path(__file__).parent / 'fixtures/financial-runner-8e5a894f.py.gz').read_bytes()))
ORIGINAL_SOURCE.chmod(0o600)
original = adapter.load_original(ORIGINAL_SOURCE, ORIGINAL_SHA)
AdaptedController = adapter.adapter_controller(original)
OPERATIONS = ('post_apply', 'post_confirm')
ALL_MUTATIONS = ('prebridge_apply', 'prebridge_confirm', *OPERATIONS)


class SourceSealTests(unittest.TestCase):
    def setUp(self):
        cache = Path.home() / '.cache'
        cache.mkdir(mode=0o700, parents=True, exist_ok=True)
        temporary = tempfile.TemporaryDirectory(prefix='owner-status-source-', dir=cache)
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        self.path = self.directory / 'original.py'
        self.path.write_bytes(ORIGINAL_SOURCE.read_bytes())
        self.path.chmod(0o600)

    def test_sealed_original_loads_and_execution_release_methods_are_inherited(self):
        self.assertEqual(ORIGINAL_SHA, hashlib.sha256(self.path.read_bytes()).hexdigest())
        loaded = adapter.load_original(self.path, ORIGINAL_SHA)
        cls = adapter.adapter_controller(loaded)
        self.assertEqual((loaded.Controller,), cls.__bases__)
        self.assertIs(cls.execute, loaded.Controller.execute)
        self.assertIs(cls.release, loaded.Controller.release)
        self.assertIs(cls.resume, loaded.Controller.resume)
        self.assertIs(cls.finish_release, loaded.Controller.finish_release)

    def test_source_hash_mismatch_rejects_before_compilation(self):
        self.path.write_bytes(self.path.read_bytes() + b'\n# changed source\n')
        with patch('builtins.compile', side_effect=AssertionError('unsealed source must not compile')):
            with self.assertRaises(adapter.AdapterFailed):
                adapter.load_original(self.path, ORIGINAL_SHA)

    def test_source_symlink_rejected(self):
        link = self.directory / 'link.py'
        link.symlink_to(self.path)
        with self.assertRaises(adapter.AdapterFailed):
            adapter.load_original(link, ORIGINAL_SHA)

    def test_source_multiple_hardlinks_rejected(self):
        os.link(self.path, self.directory / 'second.py')
        with self.assertRaises(adapter.AdapterFailed):
            adapter.load_original(self.path, ORIGINAL_SHA)

    def test_source_nonregular_type_rejected(self):
        with self.assertRaises(adapter.AdapterFailed):
            adapter.load_original(self.directory, ORIGINAL_SHA)

    def test_source_invalid_digest_type_rejected(self):
        for value in (None, True, 123, '', 'A' * 64, 'a' * 63):
            with self.subTest(expected=value), self.assertRaises(adapter.AdapterFailed):
                adapter.load_original(self.path, value)

    def test_unsealed_imported_module_cannot_create_an_adapter(self):
        with self.assertRaises(adapter.AdapterFailed):
            adapter.adapter_controller(SimpleNamespace(Controller=original.Controller))


class AdapterOperationTests(unittest.TestCase):
    def setUp(self):
        cache = Path.home() / '.cache'
        cache.mkdir(mode=0o700, parents=True, exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(prefix='owner-status-contract-', dir=cache)
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name)
        self.case = 0
        self.new_case()

    def new_case(self):
        self.case += 1
        self.work = self.base / str(self.case)
        self.work.mkdir(mode=0o700)
        self.calls = []
        self.outcomes = {}
        self.current = {'prebridge': 'AWAITING_CONFIRMATION', 'post': 'AWAITING_CONFIRMATION'}
        commands = {}
        self.workspaces = {stage: '/var/lib/lmm-api-go-deploy/work/fixture-' + stage for stage in ('prebridge', 'post')}
        for stage, workspace in self.workspaces.items():
            for verb in ('apply', 'confirm', 'status'):
                commands[stage + '_' + verb] = {'argv': [workspace + '/staging/lmm-api', 'operator', 'production', verb,
                    '--workspace', workspace, '--maintenance-handoff', '{handoff_path}',
                    '--maintenance-handoff-sha256', '{handoff_sha256}', '--json'], 'timeout_seconds': 30}
        commands['prebridge_stop'] = {'argv': ['/fixture/native-owner', 'prebridge_stop'], 'timeout_seconds': 30}
        commands['maintenance_release'] = {'argv': ['/fixture/native-owner', 'maintenance_release'], 'timeout_seconds': 30}
        self.native = {'name': 'arch', 'ssh': 'synthetic-arch', 'deployment_tool': 'native', 'commands': commands,
                       'handoff': {'path': '/fixture/base-handoff.json', 'sha256': 'a' * 64},
                       'post_intent': {'path': '/fixture/post-intent.json', 'sha256': 'f' * 64}}
        self.systemd = {'name': 'ubuntu', 'ssh': 'synthetic-ubuntu', 'deployment_tool': 'systemd',
                        'commands': {operation: {'argv': ['/fixture/systemd-owner', operation], 'timeout_seconds': 30}
                                     for operation in (*ALL_MUTATIONS, 'post_status')}}
        self.plan = {'source_sha': SOURCE, 'transition_id': 'synthetic-episode', 'transition_intent_sha256': 'b' * 64,
                     'provider': {'path': '/fixture/provider', 'sha256': PROVIDER_SHA}, 'nodes': [self.native, self.systemd]}
        self.controller = AdaptedController(self.plan, PLAN_SHA, self.work, executor=self.executor)
        self.controller.state.update(phase='ALL_NODES_CONFIRMED', stopped_handoffs={
            'arch': {'path': '/fixture/arch-handoff.json', 'sha256': 'c' * 64},
            'ubuntu': {'path': '/fixture/ubuntu-handoff.json', 'sha256': 'd' * 64}}, business_plan_sha256='e' * 64)

    def phase(self, operation):
        return 'AWAITING_CONFIRMATION' if operation.endswith('_apply') else 'MAINTENANCE_CONFIRMED'

    def raw(self, operation):
        return {'format': 0, 'deployment_id': '', 'phase': self.phase(operation), 'version': '0.2.82'}

    def status(self, operation):
        stage = operation.split('_', 1)[0]
        phase = self.current[stage] if operation.endswith('_status') else self.phase(operation)
        value = {'format': 2, 'deployment_id': Path(self.workspaces[stage]).name, 'phase': phase, 'version': '0.2.82',
                 'transition_id': self.plan['transition_id'], 'transition_intent_sha256': self.plan['transition_intent_sha256'],
                 'provider_sha256': PROVIDER_SHA, 'handoff_sha256': self.controller.state['stopped_handoffs']['arch']['sha256'],
                 'maintenance_stage': stage, 'fixture_durable_only': 'preserve-me'}
        if phase == 'MAINTENANCE_CONFIRMED':
            value['maintenance_confirmation'] = True
        return value

    def executor(self, argv, **options):
        # This executor only interprets argv. It never starts SSH or any child.
        self.assertEqual(['/usr/bin/ssh', '-T'], argv[:2])
        tokens = shlex.split(argv[3])
        node = 'arch' if argv[2] == self.native['ssh'] else 'ubuntu'
        if node == 'arch' and tokens[:3][1:] == ['operator', 'production']:
            workspace = tokens[tokens.index('--workspace') + 1]
            stage = 'prebridge' if workspace.endswith('prebridge') else 'post'
            operation = stage + '_' + tokens[3]
        else:
            operation = tokens[1]
        label = node + '-' + operation
        self.calls.append((label, list(argv), dict(options)))
        outcome = self.outcomes.get(label)
        if isinstance(outcome, BaseException):
            raise outcome
        if outcome is not None:
            if outcome[0] == 0 and node == 'arch' and operation in ALL_MUTATIONS:
                self.current[operation.split('_', 1)[0]] = self.phase(operation)
            return subprocess.CompletedProcess(argv, outcome[0], outcome[1], outcome[2])
        if node == 'arch' and operation in ALL_MUTATIONS:
            self.current[operation.split('_', 1)[0]] = self.phase(operation)
            value = self.raw(operation)
        elif node == 'arch' and operation.endswith('_status'):
            value = self.status(operation)
        else:
            value = {'phase': 'FROZEN' if operation == 'prebridge_stop' else self.phase(operation),
                     'transition_id': self.plan['transition_id'], 'transition_intent_sha256': self.plan['transition_intent_sha256'],
                     'maintenance_stage': 'post', 'maintenance_confirmation': True, 'fixture_systemd': 'unchanged'}
        return subprocess.CompletedProcess(argv, 0, original.encode(value), b'')

    def set_output(self, label, value, code=0, stderr=b''):
        body = value if isinstance(value, bytes) else original.encode(value)
        self.outcomes[label] = (code, body, stderr)
        return body

    def labels(self):
        return [call[0] for call in self.calls]

    def logs(self):
        return sorted(self.work.glob('*.log'))

    def reject_raw(self, value, operation='post_confirm'):
        self.set_output('arch-' + operation, value)
        with self.assertRaises(Exception):
            self.controller.node_operation(self.native, operation, self.phase(operation))
        self.assertEqual(['arch-' + operation], self.labels())
        self.assertEqual(1, len(self.logs()))

    def reject_status(self, value, operation='post_confirm'):
        self.set_output('arch-post_status' if operation.startswith('post_') else 'arch-prebridge_status', value)
        with self.assertRaises(Exception):
            self.controller.node_operation(self.native, operation, self.phase(operation))
        self.assertEqual(['arch-' + operation, 'arch-' + operation.split('_', 1)[0] + '_status'], self.labels())
        self.assertEqual(2, len(self.logs()))

    def test_native_two_post_operations_return_durable_status_with_exact_two_logs(self):
        for operation in OPERATIONS:
            with self.subTest(operation=operation):
                self.new_case()
                raw = self.set_output('arch-' + operation, self.raw(operation), stderr=b'raw-warning\n')
                status = self.status(operation)
                status_raw = self.set_output('arch-' + operation.split('_', 1)[0] + '_status', status, stderr=b'status-warning\n')
                value = self.controller.node_operation(self.native, operation, self.phase(operation))
                self.assertEqual(status, value)
                self.assertEqual(['arch-' + operation, 'arch-' + operation.split('_', 1)[0] + '_status'], self.labels())
                self.assertEqual([raw + b'raw-warning\n', status_raw + b'status-warning\n'], [file.read_bytes() for file in self.logs()])
                self.assertEqual([0o600, 0o600], [file.stat().st_mode & 0o777 for file in self.logs()])

    def test_native_handoff_substitution_is_preserved(self):
        value = self.controller.node_operation(self.native, 'post_apply', variables={'fixture': 'unused'})
        self.assertEqual('AWAITING_CONFIRMATION', value['phase'])
        self.assertIsNone(self.calls[0][2]['input'])
        self.assertIsNone(self.calls[1][2]['input'])
        for _label, argv, _options in self.calls:
            self.assertIn('c' * 64, argv[3])

    def test_nonempty_matching_raw_identity_is_accepted_without_projection(self):
        value = self.status('post_confirm')
        self.set_output('arch-post_confirm', value)
        self.assertEqual(value, self.controller.node_operation(self.native, 'post_confirm', 'MAINTENANCE_CONFIRMED'))
        self.assertEqual(2, len(self.logs()))

    def test_systemd_operations_and_other_native_operations_delegate_unchanged(self):
        for operation in ALL_MUTATIONS:
            with self.subTest(operation=operation):
                self.new_case()
                value = self.controller.node_operation(self.systemd, operation, self.phase(operation))
                self.assertEqual('unchanged', value['fixture_systemd'])
                self.assertEqual(['ubuntu-' + operation], self.labels())
                self.assertEqual(1, len(self.logs()))
        self.new_case()
        value = self.controller.node_operation(self.native, 'prebridge_stop', 'FROZEN')
        self.assertEqual('FROZEN', value['phase'])
        self.assertEqual(['arch-prebridge_stop'], self.labels())

    def test_prebridge_and_maintenance_release_bare_results_are_not_adapted(self):
        for operation in ('prebridge_apply', 'prebridge_confirm', 'maintenance_release'):
            with self.subTest(operation=operation):
                self.new_case()
                phase = 'CONFIRMED' if operation == 'maintenance_release' else self.phase(operation)
                self.set_output('arch-' + operation, dict(self.raw(operation), phase=phase))
                with self.assertRaises(original.GateFailed):
                    self.controller.node_operation(self.native, operation, phase)
                self.assertEqual(['arch-' + operation], self.labels())
                self.assertEqual(1, len(self.logs()))

    def test_native_body_or_handoff_override_is_rejected_before_execution(self):
        for options in ({'body': b'{"synthetic":"stdin"}'}, {'variables': {'handoff_sha256': 'f' * 64}}):
            with self.subTest(options=options):
                self.new_case()
                with self.assertRaises(original.GateFailed):
                    self.controller.node_operation(self.native, 'post_apply', **options)
                self.assertEqual([], self.calls)
                self.assertEqual([], self.logs())

    def test_original_release_reconfirms_and_reads_status_even_if_already_confirmed(self):
        self.current['post'] = 'MAINTENANCE_CONFIRMED'
        publication = []

        def publish(name, value, intent_phase=None):
            publication.append((name, value, intent_phase, list(self.labels())))
            return {'path': '/fixture/all-nodes-confirmed.json', 'sha256': 'f' * 64}

        with patch.object(self.controller, 'barriers'), patch.object(self.controller, 'guardians'), patch.object(self.controller, 'publish_receipt', side_effect=publish), patch.object(self.controller, 'finish_release') as finish:
            self.controller.release()
        self.assertEqual(['arch-post_status', 'ubuntu-post_status', 'arch-post_confirm', 'arch-post_status', 'ubuntu-post_confirm'], self.labels())
        self.assertEqual(1, len(publication))
        self.assertEqual('RELEASE_INTENT', publication[0][2])
        self.assertEqual(self.labels(), publication[0][3])
        finish.assert_called_once_with(initial=True)
        self.assertIs(AdaptedController.release, original.Controller.release)

    def test_nonzero_mutation_keeps_raw_log_and_never_reads_status_or_replays(self):
        raw = self.set_output('arch-post_apply', self.raw('post_apply'), code=7, stderr=b'executor failed\n')
        with self.assertRaises(original.GateFailed):
            self.controller.node_operation(self.native, 'post_apply')
        self.assertEqual(['arch-post_apply'], self.labels())
        self.assertEqual([raw + b'executor failed\n'], [file.read_bytes() for file in self.logs()])

    def test_timeout_mutation_keeps_unknown_outcome_log_and_never_reads_status(self):
        self.outcomes['arch-post_apply'] = subprocess.TimeoutExpired(['synthetic'], 30, output=b'partial mutation output', stderr=b'timeout diagnostic')
        with self.assertRaises(original.GateFailed):
            self.controller.node_operation(self.native, 'post_apply')
        self.assertEqual(['arch-post_apply'], self.labels())
        self.assertEqual([b'partial mutation outputtimeout diagnostic'], [file.read_bytes() for file in self.logs()])

    def test_invalid_raw_json_and_wrong_json_types_stop_before_status(self):
        for value in (b'{', b'null', b'[]', b'42', b'"text"'):
            with self.subTest(raw=value):
                self.new_case()
                self.reject_raw(value)

    def test_duplicate_raw_json_keys_stop_before_status(self):
        self.reject_raw(b'{"phase":"MAINTENANCE_CONFIRMED","phase":"MAINTENANCE_CONFIRMED","version":"0.2.82"}')

    def test_raw_phase_and_version_must_match_the_operation(self):
        for key, value in (('phase', 'AWAITING_CONFIRMATION'), ('phase', ''), ('version', '0.2.81'), ('version', None)):
            with self.subTest(key=key, value=value):
                self.new_case()
                self.reject_raw(dict(self.raw('post_confirm'), **{key: value}))

    def test_nonempty_raw_identity_mismatch_is_not_filled_from_status(self):
        for key in ('deployment_id', 'transition_id', 'transition_intent_sha256', 'handoff_sha256', 'provider_sha256', 'maintenance_stage'):
            with self.subTest(key=key):
                self.new_case()
                self.set_output('arch-post_confirm', dict(self.raw('post_confirm'), **{key: 'conflicting-raw-identity'}))
                with self.assertRaises(original.GateFailed):
                    self.controller.node_operation(self.native, 'post_confirm', 'MAINTENANCE_CONFIRMED')
                self.assertEqual(1, self.labels().count('arch-post_confirm'))
                self.assertLessEqual(self.labels().count('arch-post_status'), 1)

    def test_status_phase_mismatch_rejects_without_replaying_mutation(self):
        self.reject_status(dict(self.status('post_confirm'), phase='AWAITING_CONFIRMATION'))

    def test_status_version_mismatch_rejects(self):
        self.reject_status(dict(self.status('post_confirm'), version='0.2.81'))

    def test_status_format_is_exact_integer_two(self):
        for value in (0, True, 2.0, '2', None):
            with self.subTest(format=value):
                self.new_case()
                self.reject_status(dict(self.status('post_confirm'), format=value))

    def test_status_transition_intent_provider_and_handoff_are_exact(self):
        for key in ('transition_id', 'transition_intent_sha256', 'provider_sha256', 'handoff_sha256'):
            for remove in (False, True):
                with self.subTest(key=key, missing=remove):
                    self.new_case()
                    value = self.status('post_confirm')
                    if remove:
                        value.pop(key)
                    else:
                        value[key] = 'wrong'
                    self.reject_status(value)

    def test_status_stage_and_workspace_deployment_identity_are_exact(self):
        for key, value in (('maintenance_stage', 'prebridge'), ('deployment_id', 'different-workspace')):
            with self.subTest(key=key):
                self.new_case()
                self.reject_status(dict(self.status('post_confirm'), **{key: value}))

    def test_confirmation_requires_real_boolean_true_and_apply_cannot_be_confirmed(self):
        for value in (False, 1, 'true', None):
            with self.subTest(confirmation=value):
                self.new_case()
                self.reject_status(dict(self.status('post_confirm'), maintenance_confirmation=value))
        self.new_case()
        self.reject_status(dict(self.status('post_apply'), maintenance_confirmation=True), 'post_apply')

    def test_reopened_admission_or_reported_failure_rejects(self):
        for changes in ({'maintenance_admission_reopened': True}, {'maintenance_admission_reopened': 1}, {'failure': 'failed-health'}):
            with self.subTest(changes=changes):
                self.new_case()
                self.reject_status(dict(self.status('post_confirm'), **changes))

    def test_failed_status_preserves_both_logs_and_does_not_retry_mutation(self):
        raw = self.set_output('arch-post_confirm', self.raw('post_confirm'), stderr=b'raw diagnostic')
        status = self.set_output('arch-post_status', self.status('post_confirm'), code=9, stderr=b'status failed')
        with self.assertRaises(original.GateFailed):
            self.controller.node_operation(self.native, 'post_confirm', 'MAINTENANCE_CONFIRMED')
        self.assertEqual(['arch-post_confirm', 'arch-post_status'], self.labels())
        self.assertEqual([raw + b'raw diagnostic', status + b'status failed'], [file.read_bytes() for file in self.logs()])

    def test_invalid_or_duplicate_status_json_preserves_logs_without_retry(self):
        for value in (b'{', b'[]', b'{"transition_id":"x","transition_id":"x"}'):
            with self.subTest(status=value):
                self.new_case()
                self.reject_status(value)

    def test_missing_stopped_handoff_is_rejected_before_mutation(self):
        self.controller.state['stopped_handoffs'].pop('arch')
        with self.assertRaises(original.GateFailed):
            self.controller.node_operation(self.native, 'post_apply')
        self.assertEqual([], self.calls)
        self.assertEqual([], self.logs())

    def test_status_workspace_and_operator_prefix_cannot_drift(self):
        for index, value in ((0, '/fixture/different-owner'), (5, '/var/lib/lmm-api-go-deploy/work/different')):
            with self.subTest(index=index):
                self.new_case()
                self.native['commands']['post_status']['argv'][index] = value
                with self.assertRaises(original.GateFailed):
                    self.controller.node_operation(self.native, 'post_apply')
                self.assertEqual([], self.calls)
                self.assertEqual([], self.logs())


if __name__ == '__main__':
    unittest.main(verbosity=2)
