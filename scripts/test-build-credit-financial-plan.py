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
