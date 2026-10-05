#!/usr/bin/env python3
"""Offline controller durability and owner reconciliation tests; no production IO."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

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


@unittest.skipUnless(os.getenv('CREDIT_FINANCIAL_RUNNER_REAL_PG') == '1', 'explicit isolated local PostgreSQL opt-in')
class RealCloneTests(unittest.TestCase):
    def test_full_archive_restore_owned_clone_identity_and_readonly_verification(self):
        self.assertNotEqual(os.geteuid(), 0)
        with tempfile.TemporaryDirectory(prefix='cr-pg-', dir=Path.home()/'.cache') as temporary:
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
