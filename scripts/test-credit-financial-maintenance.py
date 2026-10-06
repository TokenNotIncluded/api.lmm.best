#!/usr/bin/env python3
"""Offline controller durability and owner reconciliation tests; no production IO."""
import importlib.util
import copy
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location('financial_runner', Path(__file__).with_name('run-credit-financial-maintenance.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class FakeController(runner.Controller):
    def __init__(self, work, executor=None):
        nodes = [{'name': name, 'ssh': name, 'deployment_tool': 'native', 'guardian_unit': name+'-guardian.service',
                  'receipt_directory': '/root/credit-receipts', 'commands': {}} for name in ('arch', 'ubuntu')]
        plan = {'transition_id': 'episode-1', 'transition_intent_sha256': '1'*64, 'provider': {'path': '/sealed/provider', 'sha256': '2'*64},
                'nodes': nodes, 'clone': {'database': 'credit_rebase_clone_test', 'schema': 'financial_test', 'role': 'lmm_api', 'port': 25671},
                'regression': {'source_directory': '/sealed/source', 'source_sha': '3'*40, 'packages': ['./model'], 'run': '^TestCredit'}}
        super().__init__(plan, 'a'*64, work, executor=executor)
        self.calls = []
        self.responses = {}
        self.seal_value = {}
        self.state['guardian_generations'] = {'arch': 123, 'ubuntu': 456}

    def frozen_gates(self):
        self.calls.append('frozen-gates')

    def barriers(self):
        self.calls.append('barriers')

    def guardians(self):
        self.calls.append('guardians')

    def verify_artifacts(self):
        self.calls.append('artifacts')

    def business_seal(self):
        return self.seal_value

    def fingerprint(self, seal, target, mode):
        self.calls.append(target+'-fingerprint-'+mode)
        if self.responses.get(target+'-fingerprint-'+mode) == 'fail':
            raise runner.GateFailed('synthetic-fingerprint-mismatch')
        return {'path': '/sealed/'+target+'-'+mode+'.tsv', 'sha256': 'f'*64}

    def sql(self, label, binding):
        self.calls.append(label)
        if label == 'production-financial-dispatch':
            # Persistence, including the immutable intent, must precede the
            # only financial SQL. Inspect the file as another process would.
            intent = runner.decode((self.work/'financial-dispatch-intent.json').read_bytes())
            self.assert_intent = intent['production_sql_sha256'] == binding['sha256']
            assert runner.decode(self.state_path.read_bytes())['phase'] == 'DISPATCH_INTENT'
        if self.responses.get(label) == 'fail':
            raise runner.GateFailed('synthetic-failure')
        return b''

    def node_operation(self, node, operation, phase=None, **options):
        self.calls.append((node['name'], operation))
        value = {'transition_id': self.plan['transition_id'], 'transition_intent_sha256': self.plan['transition_intent_sha256'],
                 'maintenance_stage': 'post', 'maintenance_confirmation': True,
                 'phase': self.responses.get((node['name'], operation), phase or 'MAINTENANCE_CONFIRMED')}
        if phase:
            runner.require(value['phase'] == phase, 'synthetic-owner-phase-mismatch')
        if operation == 'publish_receipt':
            value['receipt_sha256'] = runner.digest(options['body'])
        return value

    def execute(self, label, operation, **options):
        if self.executor:
            return super().execute(label, operation, **options)
        self.calls.append(label)
        if label.endswith('guardian-before-release-unit'):
            return b'MainPID=0\nActiveState=inactive\n'
        return b''


class ControllerTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='credit-financial-controller-')
        self.work = Path(self.temporary.name)
        self.controller = FakeController(self.work)
        self.controller.executor = None
        self.controller.seal_value = {key: {'path': '/sealed/'+key, 'sha256': 'b'*64}
            for key in ('before_sql', 'after_sql', 'production_sql')}
        self.controller.state.update(business_plan_sha256='c'*64, business_seal={'path': '/sealed/business.json', 'sha256': 'd'*64})

    def tearDown(self):
        self.temporary.cleanup()

    def test_origin_barriers_bind_each_node_address_with_verified_tls(self):
        body = b'lmm-credit-transition:episode-1'
        probe = {'url': 'https://api.lmm.best/api/status', 'body_sha256': runner.digest(body)}
        self.controller.plan['public_probes'] = []
        addresses = ('192.0.2.1', '192.0.2.2')
        for node, address in zip(self.controller.plan['nodes'], addresses):
            node.update(probes=[probe], probe_resolve_address=address)
        observed = []
        def executor(argv, **options):
            observed.append(argv)
            self.assertEqual(argv[0:2], ['/usr/bin/curl', '-q'])
            self.assertEqual(argv[-1], probe['url'])
            self.assertEqual(argv[argv.index('--noproxy')+1], '*')
            self.assertEqual(argv[argv.index('--write-out')+1], '\n%{http_code}')
            self.assertNotIn('-k', argv)
            self.assertNotIn('--insecure', argv)
            self.assertNotIn('--location', argv)
            return subprocess.CompletedProcess(argv, 0, body+b'\n503', b'')
        self.controller.executor = executor
        runner.Controller.barriers(self.controller)
        self.assertEqual([argv[argv.index('--resolve')+1] for argv in observed],
                         ['api.lmm.best:443:'+address for address in addresses])
        self.assertEqual(len(list(self.work.glob('*origin-barrier*.log'))), 2)

    def test_origin_barrier_rejects_tls_failure_and_wrong_body(self):
        body = b'lmm-credit-transition:episode-1'
        self.controller.plan['public_probes'] = []
        self.controller.plan['nodes'] = [dict(self.controller.plan['nodes'][0], probe_resolve_address='192.0.2.1',
            probes=[{'url': 'https://api.lmm.best/api/status', 'body_sha256': runner.digest(body)}])]
        for code, output in ((60, b''), (0, body+b'\n200'), (0, b'wrong\n503'), (0, b'x'*8193+b'\n503')):
            self.controller.executor = lambda argv, **options: subprocess.CompletedProcess(argv, code, output, b'')
            with self.assertRaises(runner.GateFailed):
                runner.Controller.barriers(self.controller)
        self.assertNotIn('production-financial-dispatch', self.controller.calls)

    def test_origin_probe_requires_exact_address_and_hostname(self):
        probe = {'url': 'https://api.lmm.best/api/status'}
        for address in ('node.example', '127.0.0.1', '0.0.0.0', '224.0.0.1', 123):
            with self.assertRaises(runner.GateFailed):
                runner.resolved_probe_operation(probe, address)
        for url in ('http://api.lmm.best/api/status', 'https://192.0.2.1/api/status',
                    'https://api.lmm.best:8443/api/status', 'https://user@api.lmm.best/api/status',
                    'https://api.lmm.best/api/status?token=secret', 'https://api.lmm.best/other'):
            with self.assertRaises(runner.GateFailed):
                runner.resolved_probe_operation({'url': url}, '192.0.2.1')

    def test_hostname_inventory_uses_mandatory_python_and_rejects_wrong_host(self):
        for key in ('controller', 'intent', 'generator', 'verifier', 'fingerprint_generator'):
            self.controller.plan[key] = {'path': '/sealed/'+key, 'sha256': '1'*64}
        self.controller.plan['generator_helpers'] = []
        self.controller.plan['nodes'] = [dict(self.controller.plan['nodes'][0], hostname='arch-dmit', artifacts=[])]
        def executor(argv, **options):
            self.assertEqual(argv[:3], ['/usr/bin/ssh', '-T', 'arch'])
            self.assertEqual(shlex.split(argv[3]), ['/usr/bin/python3', '-c', 'import socket; print(socket.gethostname())'])
            return subprocess.CompletedProcess(argv, 0, b'arch-dmit\n', b'')
        self.controller.executor = executor
        with mock.patch.object(runner, 'read_bound', return_value=b''):
            runner.Controller.verify_artifacts(self.controller)
            self.controller.plan['nodes'][0]['hostname'] = 'wrong-host'
            with self.assertRaises(runner.GateFailed):
                runner.Controller.verify_artifacts(self.controller)

    def test_dispatch_intent_is_durable_before_sql_and_never_replayed(self):
        self.controller.persist('REHEARSED')
        self.controller.responses['production-financial-dispatch'] = 'fail'
        with self.assertRaises(runner.GateFailed):
            self.controller.apply()
        self.assertTrue(self.controller.assert_intent)
        self.assertEqual(self.controller.state['phase'], 'AMBIGUOUS')
        with self.assertRaises(runner.GateFailed):
            self.controller.apply()
        self.assertEqual(self.controller.calls.count('production-financial-dispatch'), 1)
        reconstructed = runner.Controller(self.controller.plan, 'a'*64, self.work)
        self.assertEqual(reconstructed.state['phase'], 'AMBIGUOUS')

    def test_inspect_distinguishes_commit_no_commit_and_disagreement(self):
        self.write_dispatch_intent()
        for after, before, expected in ((None, None, 'APPLIED'), ('fail', None, 'NOT_APPLIED'), ('fail', 'fail', 'AMBIGUOUS')):
            self.controller.persist('AMBIGUOUS')
            self.controller.responses.update({'inspect-after': after, 'inspect-before': before})
            self.controller.inspect()
            self.assertEqual(self.controller.state['phase'], expected)
        self.assertNotIn('production-financial-dispatch', self.controller.calls)

    def write_dispatch_intent(self):
        intent = {'format': 'lmm-credit-financial-dispatch-v1', 'transition_id': self.controller.plan['transition_id'],
                  'transition_intent_sha256': self.controller.plan['transition_intent_sha256'],
                  'business_plan_sha256': self.controller.state['business_plan_sha256'],
                  'business_seal_sha256': self.controller.state['business_seal']['sha256'],
                  'production_sql_sha256': self.controller.seal_value['production_sql']['sha256'], 'created_at': 123}
        runner.write_once(self.work/'financial-dispatch-intent.json', runner.encode(intent))

    def test_inspect_recovers_durable_intent_before_checkpoint_without_dispatch(self):
        self.controller.persist('REHEARSED')
        self.write_dispatch_intent()
        self.controller.inspect()
        self.assertEqual(self.controller.state['phase'], 'APPLIED')
        self.assertNotIn('production-financial-dispatch', self.controller.calls)

    def test_abandoned_checkpoint_is_preserved_without_blocking_new_checkpoint(self):
        abandoned = self.work/'state.json.next.999.123'
        runner.write_once(abandoned, b'{"phase":"UNKNOWN"}')
        self.controller.persist('CAPTURE_INTENT')
        self.controller.persist('CAPTURED')
        self.assertEqual(abandoned.read_bytes(), b'{"phase":"UNKNOWN"}')
        self.assertEqual(runner.decode(self.controller.state_path.read_bytes())['phase'], 'CAPTURED')

    def test_declared_values_pass_but_unplanned_fulltable_mutation_is_ambiguous(self):
        self.controller.persist('REHEARSED')
        self.controller.responses['production-fingerprint-after'] = 'fail'
        with self.assertRaises(runner.GateFailed):
            self.controller.apply()
        self.assertEqual(self.controller.state['phase'], 'AMBIGUOUS')
        self.assertEqual(self.controller.calls.count('production-financial-dispatch'), 1)

    def test_post_resume_confirms_pending_without_reapplying_completed_node(self):
        self.controller.persist('POST_DEPLOYMENT_INTENT')
        self.controller.responses[('arch', 'post_status')] = 'MAINTENANCE_CONFIRMED'
        self.controller.responses[('ubuntu', 'post_status')] = 'AWAITING_CONFIRMATION'
        self.controller.resume()
        self.assertEqual(self.controller.state['phase'], 'ALL_NODES_CONFIRMED')
        self.assertNotIn(('arch', 'post_apply'), self.controller.calls)
        self.assertNotIn(('ubuntu', 'post_apply'), self.controller.calls)
        self.assertIn(('ubuntu', 'post_confirm'), self.controller.calls)

    def test_post_resume_only_uses_supported_prearm_retry(self):
        self.controller.persist('POST_DEPLOYMENT_INTENT')
        self.controller.responses[('arch', 'post_status')] = 'MAINTENANCE_PREARM_FAILED'
        self.controller.resume()
        self.assertIn(('arch', 'post_retry'), self.controller.calls)
        self.assertNotIn(('arch', 'post_apply'), self.controller.calls)

    def test_post_resume_refuses_rollback_required(self):
        self.controller.persist('POST_DEPLOYMENT_INTENT')
        self.controller.responses[('arch', 'post_status')] = 'ROLLBACK_REQUIRED'
        with self.assertRaises(runner.GateFailed):
            self.controller.resume()
        self.assertNotIn(('arch', 'post_retry'), self.controller.calls)
        self.assertEqual(self.controller.state['phase'], 'POST_DEPLOYMENT_INTENT')

    def test_partial_release_skips_confirmed_node_and_inactive_guardian(self):
        body = runner.encode({'format': 'synthetic-release'})
        receipt = self.work/'all-nodes-confirmed.json'
        runner.write_once(receipt, body)
        self.controller.persist('RELEASE_INTENT', global_release={'path': str(receipt), 'sha256': runner.digest(body)})
        self.controller.responses[('arch', 'post_status')] = 'CONFIRMED'
        self.controller.resume()
        self.assertEqual(self.controller.state['phase'], 'RELEASED')
        self.assertNotIn(('arch', 'maintenance_release'), self.controller.calls)
        self.assertIn(('ubuntu', 'maintenance_release'), self.controller.calls)
        self.assertNotIn('barriers', self.controller.calls)
        self.assertNotIn('arch-guardian-release', self.controller.calls)

    def test_release_rechecks_every_owner_before_opening_first_node(self):
        self.controller.persist('ALL_NODES_CONFIRMED', confirmations_sha256='e'*64)
        self.controller.responses[('ubuntu', 'post_status')] = 'ROLLBACK_REQUIRED'
        with self.assertRaises(runner.GateFailed):
            self.controller.release()
        self.assertNotIn(('arch', 'maintenance_release'), self.controller.calls)
        self.assertFalse((self.work/'all-nodes-confirmed.json').exists())

    def test_release_intent_precedes_receipt_transport(self):
        self.controller.persist('ALL_NODES_CONFIRMED', confirmations_sha256='e'*64)
        original = self.controller.node_operation
        def verify(node, operation, phase=None, **options):
            if operation == 'publish_receipt':
                self.assertEqual(runner.decode(self.controller.state_path.read_bytes())['phase'], 'RELEASE_INTENT')
            return original(node, operation, phase, **options)
        self.controller.node_operation = verify
        self.controller.release()
        self.assertEqual(self.controller.state['phase'], 'RELEASED')

    def test_readonly_reconcile_retains_partial_intent(self):
        self.controller.persist('STOP_INTENT')
        self.controller.responses[('arch', 'capture_status')] = 'FROZEN'
        self.controller.responses[('ubuntu', 'capture_status')] = 'ADMISSION_CLOSED'
        self.controller.reconcile()
        self.assertEqual(self.controller.state['phase'], 'STOP_INTENT')
        self.assertTrue(self.controller.state['reconciled_owner_states'])
        self.assertNotIn(('ubuntu', 'stop'), self.controller.calls)

    def test_repeated_evidence_has_unique_fsync_logs(self):
        def executor(argv, **options):
            return subprocess.CompletedProcess(argv, 0, stdout=b'proof', stderr=b'')
        actual = runner.Controller(self.controller.plan, 'a'*64, self.work, executor)
        operation = {'argv': ['/usr/bin/true'], 'timeout_seconds': 1}
        actual.execute('repeat-gate', operation)
        actual.execute('repeat-gate', operation)
        resumed = runner.Controller(self.controller.plan, 'a'*64, self.work, executor)
        resumed.execute('repeat-gate', operation)
        self.assertEqual(len(list(self.work.glob('*-repeat-gate.log'))), 3)

    def test_receipts_have_distinct_paths_and_exact_hash_idempotence(self):
        class ReceiptController(FakeController):
            def node_operation(inner, node, operation, phase=None, **options):
                return {'receipt_sha256': runner.digest(options['body'])}
        controller = ReceiptController(self.work)
        closed = controller.publish_receipt('all-admission-closed.json', {'format': 'closed'})
        released = controller.publish_receipt('all-nodes-confirmed.json', {'format': 'released'})
        self.assertNotEqual(closed['path'], released['path'])
        self.assertEqual(controller.publish_receipt('all-admission-closed.json', {'format': 'closed'}), closed)
        with self.assertRaises(runner.GateFailed):
            controller.publish_receipt('all-admission-closed.json', {'format': 'changed'})

    def test_clone_sql_always_uses_owned_local_socket_and_scrubbed_environment(self):
        observed = []
        def executor(argv, **options):
            observed.append((argv, options))
            return subprocess.CompletedProcess(argv, 0, stdout=b'', stderr=b'')
        controller = runner.Controller(self.controller.plan, 'a'*64, self.work, executor)
        controller.clone_sql('clone-test', b'SELECT 1;')
        argv, options = observed[0]
        self.assertEqual(argv[0], '/usr/bin/psql')
        self.assertNotIn('/usr/bin/ssh', argv)
        self.assertEqual(argv[argv.index('-h')+1], str(self.work/'local-clone/socket'))
        self.assertEqual(options['input'], b'SET ROLE "lmm_api";\nSELECT 1;')
        self.assertNotIn('SQL_DSN', options['env'])
        self.assertNotIn('LMM_CREDIT_TRANSITION_PLAN', options['env'])
        self.assertNotIn('LOG_SQL_DSN', options['env'])

    def test_clone_generation_rejects_tcp_or_foreign_data_directory(self):
        directory, data, socket = self.controller.clone_paths()
        data.mkdir(mode=0o700, parents=True)
        socket.mkdir(mode=0o700)
        file = data/'postmaster.pid'
        file.write_text('123\n'+str(data)+'\n456\n25671\n'+str(socket)+'\n\n')
        self.assertEqual(self.controller.clone_generation()['pid'], 123)
        file.write_text('123\n'+str(data)+'\n456\n25671\n'+str(socket)+'\n127.0.0.1\n')
        with self.assertRaises(runner.GateFailed):
            self.controller.clone_generation()


class LatePrebridgeTests(unittest.TestCase):
    def setUp(self):
        fixture_spec = importlib.util.spec_from_file_location('financial_builder_fixture',
            Path(__file__).with_name('test-build-credit-financial-plan.py'))
        fixtures = importlib.util.module_from_spec(fixture_spec)
        fixture_spec.loader.exec_module(fixtures)
        self.fixture = fixtures.BuilderTests('test_complete_plan_passes_actual_runner_validator_and_exact_commands')
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        self.addCleanup(self.fixture.temporary.cleanup)
        self.work = self.fixture.work / 'runner'
        self.work.mkdir(mode=0o700)
        seed = copy.deepcopy(self.fixture.seed)
        native = seed['nodes'][0]
        stage = native['owners']['prebridge']
        argv = native['command_overrides']['prebridge_apply']['argv']
        argv[0] = stage['workspace'] + '/staging/lmm-api'
        for flag, value in (('--maintenance-handoff', '{handoff_path}'),
                            ('--maintenance-handoff-sha256', '{handoff_sha256}')):
            argv[argv.index(flag) + 1] = value
        handoff = []
        for flag in ('--maintenance-handoff', '--maintenance-handoff-sha256'):
            index = argv.index(flag)
            handoff.extend(argv[index:index+2])
            del argv[index:index+2]
        argv.remove('--go-changed')
        if '--web-changed=false' in argv:
            argv.remove('--web-changed=false')
        if '--observation-seconds' in argv:
            index = argv.index('--observation-seconds')
            del argv[index:index+2]
        argv.extend(['--observation-seconds', '120'] + handoff + ['--go-changed'])
        stage['apply_contract'] = copy.deepcopy(native['command_overrides'].pop('prebridge_apply'))
        stage.update(operator=None, staged_plan=None, stage_handoff=None)
        self.partial = self.fixture.plan(seed)
        self.old_hash = runner.digest(runner.encode(self.partial))
        self.controller = runner.Controller(self.partial, self.old_hash, self.work)
        self.calls = []
        self.controller.verify_artifacts = lambda: self.calls.append('artifacts')
        self.controller.frozen_gates = lambda: self.calls.append('frozen-gates')
        self.controller.barriers = lambda: self.calls.append('barriers')
        self.controller.guardians = lambda: self.calls.append('guardians')
        self.controller.initial_guardians = lambda: self.calls.append('initial-guardians')
        self.controller.verify_closed_receipt = lambda: self.calls.append('closure-receipt')
        self.controller.execute = lambda label, operation, **options: self.calls.append(label) or b''
        self.controller.publish_receipt = lambda *a, **kw: {'path': '/sealed/closure', 'sha256': 'c'*64}
        self.controller.seal_stopped = lambda stage: self.calls.append(('seal', stage))
        self.controller.node_operation = lambda node, operation, phase=None, **kw: self.calls.append((node['name'], operation)) or {}
        self.stopped = {'path': '/root/receipts/prebridge-stopped-handoff.json', 'sha256': 'd'*64}
        self.controller.state['stopped_handoffs'] = {node['name']: copy.deepcopy(self.stopped) for node in self.partial['nodes']}
        self.controller.state['all_admission_closed'] = {'path': '/sealed/closure', 'sha256': 'c'*64}

    def bound_plan(self):
        candidate = copy.deepcopy(self.partial)
        candidate['prebridge_staging'] = 'bound'
        node = candidate['nodes'][0]
        stage = node['prebridge_stage']
        stage['operator'] = {'path': stage['workspace'] + '/staging/lmm-api', 'sha256': candidate['provider']['sha256']}
        stage['staged_plan'] = {'path': stage['workspace'] + '/staging/release-plan.json', 'sha256': 'e'*64}
        stage['stage_handoff'] = {'path': '/var/lib/lmm-api-go-deploy/handoffs/' + self.stopped['sha256'] + '.json',
                                 'sha256': self.stopped['sha256']}
        node['artifacts'].extend([stage['operator'], stage['staged_plan']])
        node['commands'].update(runner.prebridge_commands(stage))
        return candidate

    def candidate_file(self, candidate, name='refined.json'):
        file = self.fixture.work / name
        body = runner.encode(candidate)
        runner.write_once(file, body)
        return {'path': str(file), 'sha256': runner.digest(body)}

    def test_partial_has_no_unavailable_prebridge_artifacts_and_prepare_pauses_after_stop(self):
        self.assertEqual(self.partial['prebridge_staging'], 'pending')
        stage = self.partial['nodes'][0]['prebridge_stage']
        self.assertFalse(any(item['path'].startswith(stage['workspace'] + '/') for item in self.partial['nodes'][0]['artifacts']))
        self.controller.prepare()
        self.assertEqual(self.controller.state['phase'], 'FROZEN_AWAITING_PREBRIDGE_STAGE')
        self.assertIn(('arch', 'capture'), self.calls)
        self.assertIn(('arch', 'stop'), self.calls)
        self.assertIn(('seal', 'prebridge'), self.calls)
        self.assertFalse(any(isinstance(call, tuple) and call[1].startswith('prebridge_') for call in self.calls))
        with self.assertRaises(runner.GateFailed):
            self.controller.prepare()
        with self.assertRaises(runner.GateFailed):
            self.controller.resume()

    def test_refine_is_once_only_durable_and_continue_does_not_replay_capture_or_old_stop(self):
        candidate = self.bound_plan()
        binding = self.candidate_file(candidate)
        self.controller.persist('FROZEN_AWAITING_PREBRIDGE_STAGE')
        self.controller.verify_native_prebridge = lambda node: {'node': node['name']}
        self.controller.refine_prebridge(binding)
        self.assertEqual(self.controller.state['plan_sha256'], binding['sha256'])
        self.assertEqual(self.controller.state['phase'], 'FROZEN')
        receipt = runner.decode(runner.read_bound(self.controller.state['prebridge_refinement']))
        self.assertEqual(receipt['original_plan_sha256'], self.old_hash)
        self.assertEqual(receipt['stopped_handoffs'], self.controller.state['stopped_handoffs'])
        resumed = runner.Controller(candidate, binding['sha256'], self.work)
        self.assertEqual(resumed.state['phase'], 'FROZEN')
        with self.assertRaises(runner.GateFailed):
            self.controller.refine_prebridge(binding)
        self.calls.clear()
        self.controller.continue_prepare()
        self.assertEqual(self.controller.state['phase'], 'FINAL_FROZEN')
        self.assertEqual(self.calls[:2], ['closure-receipt', 'frozen-gates'])
        self.assertIn(('arch', 'prebridge_apply'), self.calls)
        self.assertIn(('arch', 'prebridge_stop'), self.calls)
        self.assertNotIn(('arch', 'capture'), self.calls)
        self.assertNotIn(('arch', 'stop'), self.calls)
        self.assertNotIn('production-financial-dispatch', self.calls)
        with self.assertRaises(runner.GateFailed):
            self.controller.continue_prepare()
        with self.assertRaises(runner.GateFailed):
            runner.Controller(self.partial, self.old_hash, self.work)

    def test_refinement_cannot_change_source_provider_nodes_database_intent_capture_post_or_packages(self):
        self.controller.persist('FROZEN_AWAITING_PREBRIDGE_STAGE')
        base = self.bound_plan()
        def change_package(plan):
            contract = plan['nodes'][0]['prebridge_stage']['apply_contract']['argv']
            contract[contract.index('--go-package-sha256') + 1] = 'a'*64
            plan['nodes'][0]['commands']['prebridge_apply']['argv'] = copy.deepcopy(contract)
        mutations = (
            lambda p: p.update(source_sha='4'*40),
            lambda p: p['provider'].update(sha256='a'*64),
            lambda p: p['nodes'][0].update(hostname='different-host'),
            lambda p: p['database'].update(peer_role='another_peer'),
            lambda p: p.update(transition_intent_sha256='a'*64),
            lambda p: p['nodes'][0]['commands']['capture']['argv'].append('--changed'),
            lambda p: p['nodes'][0]['commands']['post_apply']['argv'].append('--changed'),
            lambda p: p['nodes'][0]['handoff'].update(path='/root/changed-base.json'),
            change_package,
            lambda p: p['nodes'][0]['prebridge_stage']['apply_contract'].update(timeout_seconds=901),
        )
        for index, mutation in enumerate(mutations):
            with self.subTest(index=index):
                candidate = copy.deepcopy(base)
                mutation(candidate)
                with self.assertRaises((runner.GateFailed, OSError)):
                    self.controller.refine_prebridge(self.candidate_file(candidate, 'bad-' + str(index) + '.json'))
                self.assertEqual(self.controller.state['phase'], 'FROZEN_AWAITING_PREBRIDGE_STAGE')
                self.assertFalse((self.work / 'prebridge-refinement.json').exists())

    def test_refinement_drift_gate_failure_preserves_old_plan_and_waiting_boundary(self):
        self.controller.persist('FROZEN_AWAITING_PREBRIDGE_STAGE')
        candidate = self.bound_plan()
        self.controller.frozen_gates = lambda: (_ for _ in ()).throw(runner.GateFailed('guardian-generation-changed'))
        with self.assertRaises(runner.GateFailed):
            self.controller.refine_prebridge(self.candidate_file(candidate))
        self.assertEqual(self.controller.state['plan_sha256'], self.old_hash)
        self.assertEqual(self.controller.state['phase'], 'FROZEN_AWAITING_PREBRIDGE_STAGE')
        self.assertFalse((self.work / 'prebridge-refinement.json').exists())

    def test_continue_frozen_gate_failure_dispatches_no_bridge_or_financial_operation(self):
        self.controller.plan = self.bound_plan()
        self.controller.state.update(phase='FROZEN', prebridge_refinement={'path': '/proof', 'sha256': 'f'*64})
        self.controller.frozen_gates = lambda: (_ for _ in ()).throw(runner.GateFailed('unknown-database-client'))
        self.calls.clear()
        with self.assertRaises(runner.GateFailed):
            self.controller.continue_prepare()
        self.assertEqual(self.controller.state['phase'], 'FROZEN')
        self.assertFalse(any(isinstance(call, tuple) for call in self.calls))
        self.assertNotIn('production-financial-dispatch', self.calls)

    def test_closure_receipt_requires_original_local_and_each_remote_exact_bytes(self):
        body = runner.encode({'format': 'lmm-credit-all-admission-closed-v1',
                              'transition_id': self.partial['transition_id'],
                              'transition_intent_sha256': self.partial['transition_intent_sha256'],
                              'all_origins_closed': True, 'nodes': [node['name'] for node in self.partial['nodes']]})
        file = self.work / 'all-admission-closed.json'
        runner.write_once(file, body)
        self.controller.state['all_admission_closed'] = {'path': str(file), 'sha256': runner.digest(body)}
        observed = []
        def remote(n, binding, label):
            observed.append((n['name'], binding))
            return body
        self.controller.remote_bound_bytes = remote
        runner.Controller.verify_closed_receipt(self.controller)
        self.assertEqual(len(observed), len(self.partial['nodes']))
        self.assertTrue(all(binding['sha256'] == runner.digest(body) for _, binding in observed))
        self.controller.remote_bound_bytes = lambda *a: b'changed'
        with self.assertRaisesRegex(runner.GateFailed, 'remote-admission-closure-changed'):
            runner.Controller.verify_closed_receipt(self.controller)
        file.write_bytes(b'changed')
        with self.assertRaisesRegex(runner.GateFailed, 'bound-file-changed'):
            runner.Controller.verify_closed_receipt(self.controller)

    def actual_stage_fixture(self):
        candidate = self.bound_plan()
        node = candidate['nodes'][0]
        stage = node['prebridge_stage']
        receipt = {'format': 'lmm-credit-maintenance-capture-v1', 'phase': 'FROZEN',
                   'transition_id': candidate['transition_id'], 'transition_intent_sha256': candidate['transition_intent_sha256'],
                   'pid': 123, 'invocation_id': 'a'*32}
        receipt_bytes = runner.encode(receipt)
        stopped = {'format': 'lmm-credit-maintenance-handoff-v1', 'stage': 'prebridge', 'deployment_tool': 'native',
                   'transition_id': candidate['transition_id'], 'transition_intent_sha256': candidate['transition_intent_sha256'],
                   'provider_sha256': candidate['provider']['sha256'], 'prepare_config_sha256': node['prepare_config']['sha256'],
                   'previous_deployment_id': Path(stage['capture_workspace']).name,
                   'capture_receipt_path': stage['capture_workspace'] + '/state/maintenance-capture.FROZEN.json',
                   'capture_receipt_sha256': runner.digest(receipt_bytes),
                   'stopped_writer': {'pid': 123, 'invocation_id': 'a'*32}}
        stopped_bytes = runner.encode(stopped)
        sha = runner.digest(stopped_bytes)
        self.stopped['sha256'] = sha
        self.controller.state['stopped_handoffs']['arch'] = copy.deepcopy(self.stopped)
        stage['stage_handoff'] = {'path': '/var/lib/lmm-api-go-deploy/handoffs/' + sha + '.json', 'sha256': sha}
        argv = stage['apply_contract']['argv']
        actual = {'format': 6, 'deployment_id': Path(stage['workspace']).name, 'target_alias': node['ssh'],
                  'expected_host': node['hostname'], 'go_changed': True, 'web_changed': False, 'with_backups': False,
                  'operator_user': runner.flag_value(argv, '--operator-user'),
                  'expected_version': runner.flag_value(argv, '--expected-version'),
                  'observation_seconds': int(runner.flag_value(argv, '--observation-seconds')),
                  'preserve_edge_policy': '--preserve-edge-policy' in argv,
                  'maintenance_handoff': dict(stopped, path='/local/sealed-stop.json', sha256=sha)}
        for key, pf, hf in (('go_candidate', '--go-package', '--go-package-sha256'),
                            ('go_rollback', '--go-rollback-package', '--go-rollback-sha256'),
                            ('web_candidate', '--web-package', '--web-package-sha256'),
                            ('web_rollback', '--web-rollback-package', '--web-rollback-sha256')):
            actual[key] = {'package_path': '/local/' + Path(runner.flag_value(argv, pf)).name,
                           'package_sha256': runner.flag_value(argv, hf)}
        actual['go_candidate'].update(git_revision=candidate['source_sha'], payload_sha256=candidate['provider']['sha256'])
        for key, flag in (('probe_binary', '--probe-binary'), ('operator_binary', '--operator-binary')):
            actual[key] = {'path': '/local/provider', 'sha256': runner.flag_value(argv, flag + '-sha256')}
        # Exactly the normal-owner target argument order from its sealed plan.
        normalized = runner.encode(actual)
        stage['staged_plan']['sha256'] = runner.digest(normalized)
        values = {self.stopped['path']: stopped_bytes, stage['stage_handoff']['path']: stopped_bytes,
                  stopped['capture_receipt_path']: receipt_bytes, stage['staged_plan']['path']: normalized}
        def remote_read(n, binding, label):
            data = values[binding['path']]
            runner.require(runner.digest(data) == binding['sha256'], 'test-remote-bound-hash')
            return data
        self.controller.plan = candidate
        self.controller.remote_bound_bytes = remote_read
        self.controller.node_operation = lambda n, op, phase=None: dict(receipt, capture_receipt_path=stopped['capture_receipt_path'],
                                                                       capture_receipt_sha256=stopped['capture_receipt_sha256'])
        return node, actual, values

    def test_actual_normal_stage_requires_formal_stop_capture_and_exact_full_argv(self):
        node, actual, values = self.actual_stage_fixture()
        self.assertEqual(self.controller.verify_native_prebridge(node)['node'], node['name'])
        original_status = self.controller.node_operation
        self.controller.node_operation = lambda *a, **kw: dict(original_status(*a, **kw), capture_receipt_sha256='f'*64)
        with self.assertRaisesRegex(runner.GateFailed, 'capture-frozen-evidence-changed'):
            self.controller.verify_native_prebridge(node)
        self.controller.node_operation = original_status
        actual['go_rollback']['package_sha256'] = '1'*64
        raw = runner.encode(actual)
        node['prebridge_stage']['staged_plan']['sha256'] = runner.digest(raw)
        values[node['prebridge_stage']['staged_plan']['path']] = raw
        with self.assertRaisesRegex(runner.GateFailed, 'immutable-argv'):
            self.controller.verify_native_prebridge(node)
        node['prebridge_stage']['stage_handoff']['sha256'] = 'f'*64
        with self.assertRaisesRegex(runner.GateFailed, 'formal-frozen-handoff'):
            self.controller.verify_native_prebridge(node)


@unittest.skipUnless(os.getenv('CREDIT_FINANCIAL_RUNNER_REAL_PG') == '1', 'explicit isolated local PostgreSQL opt-in')
class RealCloneTests(unittest.TestCase):
    def test_full_archive_restore_owned_clone_identity_and_readonly_verification(self):
        self.assertNotEqual(os.geteuid(), 0)
        cache = Path.home()/'.cache'
        cache.mkdir(mode=0o700, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='cr-pg-', dir=cache) as temporary:
            base = Path(temporary)
            origin, socket, work = base/'origin', base/'origin-socket', base/'controller'
            socket.mkdir(mode=0o700)
            work.mkdir(mode=0o700)
            environment = {'PATH': '/usr/bin:/bin', 'HOME': str(Path.home()), 'LANG': 'C.UTF-8'}
            def run(argv, body=None):
                return subprocess.run(argv, input=body, capture_output=True, env=environment, check=True).stdout
            run(['/usr/bin/initdb', '-D', str(origin), '--encoding=UTF8', '--locale=C.UTF-8', '--auth-local=trust', '--auth-host=reject'])
            run(['/usr/bin/pg_ctl', '-D', str(origin), '-l', str(base/'origin.log'), '-o', '-c listen_addresses= -c shared_buffers=16MB -k '+str(socket)+' -p 25672', '-w', 'start'])
            controller = FakeController(work, executor=subprocess.run)
            try:
                bootstrap = b'CREATE ROLE lmm_api LOGIN; CREATE SCHEMA financial_test AUTHORIZATION lmm_api; CREATE TABLE financial_test.wallets(id bigint PRIMARY KEY,amount bigint,history bigint); ALTER TABLE financial_test.wallets OWNER TO lmm_api; INSERT INTO financial_test.wallets VALUES(1,6710363,123456),(2,13420726,234567);'
                run(['/usr/bin/psql', '-XqAt', '-v', 'ON_ERROR_STOP=1', '-h', str(socket), '-p', '25672', '-d', 'postgres'], bootstrap)
                archive = run(['/usr/bin/pg_dump', '--format=custom', '-h', str(socket), '-p', '25672', '-d', 'postgres'])
                backup = work/'final-full-database.dump'
                runner.write_once(backup, archive)
                controller.persist('FULL_BACKUP_CREATED', backup={'path': str(backup), 'sha256': runner.digest(archive)}, backup_frozen_state_sha256='f'*64)
                controller.clone()
                self.assertEqual(controller.state['phase'], 'CLONE_RESTORED')
                self.assertEqual(controller.state['clone_table_count'], 1)
                self.assertNotEqual(controller.state['clone_identity']['database'], 'postgres')
                before = controller.clone_sql('local-before', b'BEGIN READ ONLY; SELECT count(*) FROM wallets WHERE amount IN (6710363,13420726) AND history IN (123456,234567); COMMIT;')
                self.assertEqual(before.strip(), b'2')
                controller.clone_sql('synthetic-clone-apply', b'BEGIN; UPDATE wallets SET amount=amount/7; COMMIT;')
                after = controller.clone_sql('local-after', b'BEGIN READ ONLY; SELECT count(*) FROM wallets WHERE amount IN (958623,1917246) AND history IN (123456,234567); COMMIT;')
                self.assertEqual(after.strip(), b'2')
                origin_values = run(['/usr/bin/psql', '-XqAt', '-h', str(socket), '-p', '25672', '-d', 'postgres'], b'SELECT count(*) FROM financial_test.wallets WHERE amount IN (6710363,13420726);')
                self.assertEqual(origin_values.strip(), b'2')
                _, clone_data, _ = controller.clone_paths()
                self.assertEqual(controller.clone_identity(), controller.state['clone_identity'])
            finally:
                _, clone_data, _ = controller.clone_paths()
                if (clone_data/'postmaster.pid').exists():
                    run(['/usr/bin/pg_ctl', '-D', str(clone_data), '-m', 'fast', '-w', 'stop'])
                run(['/usr/bin/pg_ctl', '-D', str(origin), '-m', 'fast', '-w', 'stop'])


if __name__ == '__main__':
    unittest.main()
