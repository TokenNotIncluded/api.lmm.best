#!/usr/bin/env python3
"""Offline builder contract checks; no SSH, processes or databases are started."""
import copy
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import sys
import tempfile
import unittest

sys.dont_write_bytecode = True
HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('builder', HERE / 'build-credit-financial-plan.py')
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)


class BuilderTests(unittest.TestCase):
    def setUp(self):
        cache = Path.home() / '.cache'
        cache.mkdir(mode=0o700, exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(prefix='financial-plan-', dir=cache)
        self.addCleanup(self.temporary.cleanup)
        self.work = Path(self.temporary.name)
        self.seed = json.loads((HERE / 'fixtures/credit-financial-plan-seed.example.json').read_text())
        schema = Path(os.getenv('CREDIT_FINANCIAL_RUNNER_SCHEMA', HERE / 'run-credit-financial-maintenance.py'))
        if not schema.is_file():
            self.skipTest('set CREDIT_FINANCIAL_RUNNER_SCHEMA to the reviewed runner in this separate worktree')
        self.seed['controller'] = self.bind(schema)
        for key in ('provider', 'verifier', 'generator', 'fingerprint_generator'):
            file = self.work / key
            file.write_bytes((key + '-fixture').encode())
            self.seed[key] = self.bind(file)
        self.seed['generator_helpers'] = []
        for node in self.seed['nodes']:
            node['helpers']['runner']['sha256'] = self.seed['controller']['sha256']
            node['post_intent']['sha256'] = builder.digest(builder.canonical({'stage': 'post', 'node': node['name']}))
            if node['deployment_tool'] == 'native':
                for owner in node['owners'].values():
                    owner['operator']['sha256'] = self.seed['provider']['sha256']
                for operation in node['command_overrides'].values():
                    argv = operation['argv']
                    for flag in ('--probe-binary-sha256', '--operator-binary-sha256'):
                        argv[argv.index(flag) + 1] = self.seed['provider']['sha256']
                post_argv = node['command_overrides']['post_apply']['argv']
                post_argv[post_argv.index('--maintenance-handoff-sha256') + 1] = node['post_intent']['sha256']
        self.intent_file = self.work / 'intent.json'
        self.intent = builder.canonical(builder.make_intent(self.seed))
        builder.write_once(self.intent_file, self.intent)

    @staticmethod
    def bind(file):
        return {'path': str(file), 'sha256': builder.digest(file.read_bytes())}

    def plan(self, seed=None):
        return builder.make_plan(seed or self.seed, self.intent_file, self.intent)

    def late_seed(self, bound=False):
        seed = copy.deepcopy(self.seed)
        node = seed['nodes'][0]
        owner = node['owners']['prebridge']
        command = node['command_overrides']['prebridge_apply']
        command['argv'][0] = owner['workspace'] + '/staging/lmm-api'
        argv = command['argv']
        ordered_flags = ('--workspace', '--operator-user', '--go-package', '--go-package-sha256',
                         '--go-rollback-package', '--go-rollback-sha256', '--web-package', '--web-package-sha256',
                         '--web-rollback-package', '--web-rollback-sha256', '--probe-binary', '--probe-binary-sha256',
                         '--operator-binary', '--operator-binary-sha256', '--expected-version', '--observation-seconds',
                         '--maintenance-handoff', '--maintenance-handoff-sha256')
        command['argv'] = argv[:4] + [item for flag in ordered_flags for item in (flag, builder.flag_value(argv, flag))] + ['--go-changed']
        owner['operator']['path'] = command['argv'][0]
        owner['apply_contract'] = copy.deepcopy(command)
        owner['stage_handoff'] = None
        if bound:
            owner['stage_handoff'] = {'path': '/var/lib/lmm-api-go-deploy/handoffs/' + '6' * 64 + '.json', 'sha256': '6' * 64}
            for flag, key in (('--maintenance-handoff', 'path'), ('--maintenance-handoff-sha256', 'sha256')):
                command['argv'][command['argv'].index(flag) + 1] = owner['stage_handoff'][key]
        else:
            owner['operator'] = owner['staged_plan'] = None
            del node['command_overrides']['prebridge_apply']
        return seed

    def runner(self):
        spec = importlib.util.spec_from_file_location('test_builder_runner', self.seed['controller']['path'])
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        return module

    def test_late_partial_has_only_real_capture_post_owners_and_preserves_base(self):
        seed = self.late_seed()
        before = builder.canonical(seed)
        plan = self.plan(seed)
        native = plan['nodes'][0]
        self.assertEqual(plan['prebridge_staging'], 'pending')
        self.assertEqual(set(native['commands']), set(builder.OPERATIONS) - set(builder.PREBRIDGE_OPERATIONS))
        stage = native['prebridge_stage']
        self.assertEqual(stage['workspace'], seed['nodes'][0]['owners']['prebridge']['workspace'])
        self.assertEqual(stage['capture_workspace'], seed['nodes'][0]['owners']['capture']['workspace'])
        self.assertTrue(all(stage[key] is None for key in ('operator', 'staged_plan', 'stage_handoff')))
        self.assertFalse(any(artifact['path'].startswith(stage['workspace'] + '/') for artifact in native['artifacts']))
        self.assertEqual(native['handoff'], seed['nodes'][0]['handoff'])
        self.assertEqual(builder.flag_value(stage['apply_contract']['argv'], '--maintenance-handoff'), '{handoff_path}')
        self.assertEqual(builder.flag_value(stage['apply_contract']['argv'], '--observation-seconds'), '120')
        self.assertEqual(builder.canonical(seed), before)

    def test_late_full_is_exact_runner_refinement_and_only_adds_real_owner_artifacts(self):
        pending = self.plan(self.late_seed())
        seed = self.late_seed(bound=True)
        full = self.plan(seed)
        self.assertEqual(full['prebridge_staging'], 'bound')
        self.assertEqual(self.runner().prebridge_projection(full), pending)
        native = full['nodes'][0]
        stage = native['prebridge_stage']
        self.assertEqual(native['handoff'], pending['nodes'][0]['handoff'])
        self.assertEqual(stage['apply_contract'], pending['nodes'][0]['prebridge_stage']['apply_contract'])
        self.assertEqual(native['commands']['prebridge_apply'], stage['apply_contract'])
        self.assertEqual({key: native['commands'][key] for key in builder.PREBRIDGE_OPERATIONS},
                         self.runner().prebridge_commands(stage))
        added = [artifact for artifact in native['artifacts'] if artifact not in pending['nodes'][0]['artifacts']]
        self.assertEqual(added, [stage['operator'], stage['staged_plan']])
        self.assertNotIn(stage['stage_handoff'], native['artifacts'])

    def test_late_bound_apply_cannot_drift_packages_observation_edge_policy_or_timeout(self):
        for flag, replacement in (('--go-package-sha256', '7' * 64), ('--go-rollback-sha256', '8' * 64),
                                  ('--web-package-sha256', '9' * 64), ('--expected-version', 'different'),
                                  ('--observation-seconds', '180')):
            with self.subTest(flag=flag):
                seed = self.late_seed(bound=True)
                argv = seed['nodes'][0]['command_overrides']['prebridge_apply']['argv']
                argv[argv.index(flag) + 1] = replacement
                with self.assertRaisesRegex(builder.InvalidSeed, 'immutable contract'):
                    self.plan(seed)
        for mutation in ('edge', 'timeout'):
            with self.subTest(mutation=mutation):
                seed = self.late_seed(bound=True)
                command = seed['nodes'][0]['command_overrides']['prebridge_apply']
                if mutation == 'edge':
                    command['argv'].append('--preserve-edge-policy')
                else:
                    command['timeout_seconds'] += 1
                with self.assertRaisesRegex(builder.InvalidSeed, 'immutable contract'):
                    self.plan(seed)

    def test_late_bindings_are_atomic_and_real_stopped_handoff_is_required(self):
        seed = self.late_seed()
        seed['nodes'][0]['owners']['prebridge']['staged_plan'] = self.seed['nodes'][0]['owners']['prebridge']['staged_plan']
        with self.assertRaisesRegex(builder.InvalidSeed, 'all pending or all bound'):
            self.plan(seed)
        for mutation in ('base_argv', 'wrong_path'):
            with self.subTest(mutation=mutation):
                seed = self.late_seed(bound=True)
                node = seed['nodes'][0]
                if mutation == 'base_argv':
                    argv = node['command_overrides']['prebridge_apply']['argv']
                    argv[argv.index('--maintenance-handoff') + 1] = node['handoff']['path']
                else:
                    node['owners']['prebridge']['stage_handoff']['path'] = node['handoff']['path']
                with self.assertRaisesRegex(builder.InvalidSeed, 'handoff'):
                    self.plan(seed)

    def test_late_contract_rejects_provider_paths_digest_missing_observation_and_backup_flags(self):
        for flag, value in (('--probe-binary', '/usr/bin/lmm-api-go'), ('--operator-binary', '/usr/bin/lmm-api-go'),
                            ('--operator-binary-sha256', '1' * 64), ('--observation-seconds', '119')):
            with self.subTest(flag=flag):
                seed = self.late_seed()
                argv = seed['nodes'][0]['owners']['prebridge']['apply_contract']['argv']
                argv[argv.index(flag) + 1] = value
                with self.assertRaises(builder.InvalidSeed):
                    self.plan(seed)
        seed = self.late_seed()
        argv = seed['nodes'][0]['owners']['prebridge']['apply_contract']['argv']
        index = argv.index('--observation-seconds')
        del argv[index:index + 2]
        with self.assertRaisesRegex(builder.InvalidSeed, 'observation-seconds'):
            self.plan(seed)
        for flag in ('--with-backups', '--with-backups=false', '--controller-backup-receipt', '--backup-dir', '--release-plan-sha256'):
            with self.subTest(flag=flag):
                seed = self.late_seed()
                seed['nodes'][0]['owners']['prebridge']['apply_contract']['argv'].extend([flag, 'unused'])
                with self.assertRaisesRegex(builder.InvalidSeed, 'backup-disabled'):
                    self.plan(seed)

    def test_late_pending_has_no_dispatch_override_and_full_derived_commands_are_fixed(self):
        seed = self.late_seed()
        seed['nodes'][0]['command_overrides']['prebridge_apply'] = self.seed['nodes'][0]['command_overrides']['prebridge_apply']
        with self.assertRaisesRegex(builder.InvalidSeed, 'pending prebridge'):
            self.plan(seed)
        seed = self.late_seed(bound=True)
        seed['nodes'][0]['command_overrides']['prebridge_confirm'] = {'argv': ['/usr/bin/arbitrary'], 'timeout_seconds': 1}
        with self.assertRaisesRegex(builder.InvalidSeed, 'cannot be overridden'):
            self.plan(seed)

    def test_late_contract_uses_normal_go_order_and_preserves_edge_policy(self):
        seed = self.late_seed()
        contract = seed['nodes'][0]['owners']['prebridge']['apply_contract']
        contract['argv'].append('--preserve-edge-policy')
        pending = self.plan(seed)
        seed = self.late_seed(bound=True)
        seed['nodes'][0]['owners']['prebridge']['apply_contract']['argv'].append('--preserve-edge-policy')
        seed['nodes'][0]['command_overrides']['prebridge_apply']['argv'].append('--preserve-edge-policy')
        self.assertEqual(self.runner().prebridge_projection(self.plan(seed)), pending)
        argv = seed['nodes'][0]['owners']['prebridge']['apply_contract']['argv']
        index = argv.index('--observation-seconds')
        pair = argv[index:index + 2]
        del argv[index:index + 2]
        argv.extend(pair)
        with self.assertRaisesRegex(builder.InvalidSeed, 'canonical order'):
            self.plan(seed)

    def test_late_expected_packages_cannot_be_outside_the_real_staging_directory(self):
        for flag in ('--go-package', '--go-rollback-package', '--web-package', '--web-rollback-package'):
            for location in ('outside', 'nested'):
                with self.subTest(flag=flag, location=location):
                    seed = self.late_seed()
                    owner = seed['nodes'][0]['owners']['prebridge']
                    argv = owner['apply_contract']['argv']
                    argv[argv.index(flag) + 1] = '/tmp/candidate.pkg.tar.zst' if location == 'outside' else owner['workspace'] + '/staging/nested/candidate.pkg.tar.zst'
                    with self.assertRaisesRegex(builder.InvalidSeed, 'direct expected staging files'):
                        self.plan(seed)

    def test_complete_plan_passes_actual_runner_validator_and_exact_commands(self):
        plan = self.plan()
        self.assertEqual(plan['transition_intent_sha256'], builder.digest(self.intent))
        self.assertEqual(plan['database']['peer_role'], 'postgres')
        self.assertEqual(plan['database']['runtime_role'], 'lmm_api')
        self.assertEqual(plan['clone']['role'], 'lmm_api')
        self.assertEqual(plan['fingerprint_generator'], self.seed['fingerprint_generator'])
        native, systemd = plan['nodes']
        self.assertEqual(set(native['commands']), set(builder.OPERATIONS))
        self.assertEqual(native['commands']['guardian_start']['argv'], ['/usr/bin/systemctl', 'start', native['guardian_unit']])
        for key in ('prebridge_confirm', 'post_confirm'):
            self.assertNotIn('--wait', native['commands'][key]['argv'])
            self.assertNotIn('--json', native['commands'][key]['argv'])
            self.assertIn('--wait', systemd['commands'][key]['argv'])
            self.assertIn('--json', systemd['commands'][key]['argv'])
        apply = native['commands']['post_apply']['argv']
        retry = native['commands']['post_retry']['argv']
        self.assertEqual(apply[:3] + apply[4:], retry[:3] + retry[4:])
        self.assertEqual(retry[3], 'maintenance-retry')
        self.assertEqual(systemd['commands']['post_retry']['argv'][2], 'status')
        self.assertEqual(native['commands']['seal_post']['argv'][4], native['post_intent']['path'])
        self.assertIn(native['post_intent'], native['artifacts'])
        self.assertNotEqual(native['post_intent']['sha256'], native['handoff']['sha256'])
        status = native['commands']['post_status']['argv']
        self.assertEqual(builder.flag_value(status, '--staged-plan-sha256'), self.seed['nodes'][0]['owners']['post']['staged_plan']['sha256'])
        substitutions = {key: 'sealed-value' for key in ('handoff_path', 'handoff_sha256', 'base_handoff_path', 'base_handoff_sha256',
            'all_closed_path', 'all_closed_sha256', 'global_confirmation_path', 'global_confirmation_sha256',
            'receipt_path', 'receipt_sha256', 'handoff_output_path')}
        for node in plan['nodes']:
            for command in node['commands'].values():
                for arg in command['argv']:
                    arg.format_map(substitutions)

    def test_intent_binds_exact_writer_inventory_and_database(self):
        seed = copy.deepcopy(self.seed)
        seed['database']['peer_role'] = 'another_peer'
        with self.assertRaisesRegex(builder.InvalidSeed, 'intent differs'):
            self.plan(seed)
        seed = copy.deepcopy(self.seed)
        seed['nodes'][1]['hostname'] = 'changed-host'
        with self.assertRaisesRegex(builder.InvalidSeed, 'intent differs'):
            self.plan(seed)

    def test_origin_resolve_address_is_preserved_and_actual_runner_rejects_invalid_ip(self):
        seed = copy.deepcopy(self.seed)
        seed['nodes'][0]['probe_resolve_address'] = '8.8.8.8'
        seed['nodes'][0]['probe_urls'] = ['https://api.lmm.best/api/status', 'https://api.lmm.best/v1/models']
        plan = self.plan(seed)
        self.assertEqual(plan['nodes'][0]['probe_resolve_address'], '8.8.8.8')
        self.assertNotIn('probe_resolve_address', plan['nodes'][1])
        seed['nodes'][0]['probe_resolve_address'] = 'not-an-ip'
        with self.assertRaises((ValueError, RuntimeError)):
            self.plan(seed)

    def test_normal_owner_immutable_package_parameters_are_required_and_preserved(self):
        seed = copy.deepcopy(self.seed)
        supplied = seed['nodes'][0]['command_overrides']['post_apply']['argv']
        supplied += ['--preserve-edge-policy']
        plan = self.plan(seed)
        self.assertIn('--preserve-edge-policy', plan['nodes'][0]['commands']['post_apply']['argv'])
        supplied[supplied.index('--go-rollback-sha256') + 1] = '1' * 64
        with self.assertRaisesRegex(builder.InvalidSeed, 'same package'):
            self.plan(seed)
        seed = copy.deepcopy(self.seed)
        del seed['nodes'][0]['command_overrides']['capture']
        with self.assertRaisesRegex(builder.InvalidSeed, 'normal-owner'):
            self.plan(seed)
        seed = copy.deepcopy(self.seed)
        supplied = seed['nodes'][0]['command_overrides']['post_apply']['argv']
        supplied[supplied.index('--maintenance-handoff-sha256') + 1] = seed['nodes'][0]['handoff']['sha256']
        with self.assertRaisesRegex(builder.InvalidSeed, 'reviewed staging intent'):
            self.plan(seed)

    def test_private_outputs_are_canonical_exclusive_and_source_inputs_unchanged(self):
        output = self.work / 'plan.json'
        content = builder.canonical(self.plan())
        builder.write_once(output, content)
        self.assertEqual(stat.S_IMODE(output.stat().st_mode), 0o600)
        self.assertEqual(output.read_bytes(), builder.canonical(json.loads(content)))
        self.assertEqual(self.intent_file.read_bytes(), self.intent)
        with self.assertRaises(FileExistsError):
            builder.write_once(output, b'overwrite')
        self.assertEqual(output.read_bytes(), content)
        with self.assertRaises(builder.InvalidSeed):
            builder.decode(b'{"source_sha":"one","source_sha":"two"}')

    def test_publisher_and_seals_use_real_bound_remote_helpers(self):
        plan = self.plan()
        for source, node in zip(self.seed['nodes'], plan['nodes']):
            publisher = node['commands']['publish_receipt']['argv']
            self.assertEqual(publisher[:3], ['/usr/bin/python3', source['helpers']['runner']['path'], '_publish-receipt'])
            seal = node['commands']['seal_post']['argv']
            self.assertEqual(builder.flag_value(seal, '--handoff-sha256'), source['post_intent']['sha256'])
            self.assertEqual(builder.flag_value(seal, '--workspace'), source['owners']['prebridge']['workspace'])
            self.assertEqual(builder.flag_value(node['commands']['post_apply']['argv'], '--maintenance-handoff'), '{handoff_path}')

    def test_unknown_variables_are_rejected_before_frozen_backup_or_release(self):
        seed = copy.deepcopy(self.seed)
        seed['backup_commands']['offhost_copy']['argv'].append('{backup_size}')
        with self.assertRaisesRegex(builder.InvalidSeed, 'unsupported runtime substitution'):
            self.plan(seed)
        seed = copy.deepcopy(self.seed)
        seed['nodes'][0]['cleanup'] = {'argv': ['/usr/bin/cleanup', '{receipt_path}'], 'timeout_seconds': 30}
        with self.assertRaisesRegex(builder.InvalidSeed, 'unsupported runtime substitution'):
            self.plan(seed)

    def test_cli_two_passes_create_private_bound_outputs_without_overwriting(self):
        seed_file = self.work / 'seed.json'
        builder.write_once(seed_file, builder.canonical(self.seed))
        intent = self.work / 'new-intent.json'
        plan_file = self.work / 'runner-plan.json'
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(builder.main(['intent', '--seed', str(seed_file), '--output', str(intent)]), 0)
            self.assertEqual(builder.main(['plan', '--seed', str(seed_file), '--intent', str(intent), '--output', str(plan_file)]), 0)
        plan = json.loads(builder.read_private(plan_file))
        self.assertEqual(plan['intent'], self.bind(intent))
        before = plan_file.read_bytes()
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(builder.main(['plan', '--seed', str(seed_file), '--intent', str(intent), '--output', str(plan_file)]), 1)
        self.assertEqual(plan_file.read_bytes(), before)


if __name__ == '__main__':
    unittest.main()
