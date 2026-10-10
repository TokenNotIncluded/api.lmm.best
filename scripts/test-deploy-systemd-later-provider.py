"""Local released-history registration tests; no service or remote host access."""
import importlib.util
import hashlib
import io
import json
import os
from pathlib import Path
import tarfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location(
    'existing_deploy_systemd_tests', Path(__file__).with_name('test-deploy-systemd.py'))
existing = importlib.util.module_from_spec(spec)
spec.loader.exec_module(existing)
deploy = existing.deploy


class LaterProviderHistoryTests(unittest.TestCase):
    setUp = existing.CleanupTests.setUp
    write = existing.CleanupTests.write
    state = existing.CleanupTests.state
    workspace = existing.CleanupTests.workspace
    install_chain = existing.CleanupTests.install_chain
    maintenance = existing.CleanupTests.maintenance
    snapshot = existing.CleanupTests.snapshot
    proof = existing.OrdinaryHistoryTests.proof

    def write_json(self, path, value):
        return self.write(path, json.dumps(value, sort_keys=True).encode() + b'\n')

    def make_archive(self, path, files, extra=()):
        with tarfile.open(path, 'w:gz') as archive:
            for name, content in files.items():
                member = tarfile.TarInfo(name)
                member.size = len(content)
                member.mode = 0o644
                archive.addfile(member, io.BytesIO(content))
            for member, content in extra:
                archive.addfile(member, io.BytesIO(content) if content is not None else None)
        path.chmod(0o600)
        return path

    def later_fixture(self):
        self.proof()
        self.origin = self.workspace('later', 'CONFIRMED')
        self.payload = b'official later provider fixture'
        self.revision = '1' * 40
        self.write(deploy.BINARY, self.payload, 0o755)
        self.write(self.origin / 'lmm-api-go', self.payload, 0o755)
        state = deploy.read_state(self.origin)
        state.update(version='0.2.98', sha256=deploy.digest(deploy.BINARY),
                     migrate=False, backup_exclude_tables=[], previous_version='0.2.97')
        self.state(self.origin, state)
        self.cap_root = Path('/var/lib/lmm-api-go-deploy/merchant-capsules/later')
        self.local_cap_root = self.base / 'capsule'
        assets = self.local_cap_root / 'assets'
        assets.mkdir(mode=0o700, parents=True)
        go_asset = self.make_archive(assets / 'candidate.release.tar.gz', {
            'lmm-api-go-0.2.98/REVISION': self.revision.encode() + b'\n',
            'lmm-api-go-0.2.98/lmm-api-go': self.payload})
        go_bundle = self.write(assets / 'candidate.sigstore.json', b'{"synthetic_signed_bundle":true}\n')
        go_package = self.write(assets / 'candidate.pkg.tar.zst', b'official candidate package fixture')
        candidate = {'name': 'lmm-api-go', 'version': '0.2.98-1', 'package_path': 'assets/candidate.pkg.tar.zst',
                     'package_sha256': deploy.digest(go_package),
                     'git_revision': self.revision, 'contract_revision': 'fixture-v1',
                     'payload_sha256': state['sha256'], 'release_asset': 'assets/candidate.release.tar.gz',
                     'release_asset_sha256': deploy.digest(go_asset), 'signature_bundle': 'assets/candidate.sigstore.json',
                     'signature_bundle_sha256': deploy.digest(go_bundle), 'release_tag': 'go-v0.2.98',
                     'workflow': 'release-go.yml'}
        rollback_package = self.write(assets / 'rollback.pkg.tar.zst', b'official rollback package fixture')
        rollback_asset = self.write(assets / 'rollback.release.tar.gz', go_asset.read_bytes())
        rollback_bundle = self.write(assets / 'rollback.sigstore.json', go_bundle.read_bytes())
        rollback = dict(candidate, version='0.2.97-1', release_tag='go-v0.2.97',
                        package_path='assets/rollback.pkg.tar.zst', package_sha256=deploy.digest(rollback_package),
                        release_asset='assets/rollback.release.tar.gz', release_asset_sha256=deploy.digest(rollback_asset),
                        signature_bundle='assets/rollback.sigstore.json', signature_bundle_sha256=deploy.digest(rollback_bundle))
        physical = {key: self.database[key] for key in ('system_identifier', 'database', 'database_oid', 'schema')}
        physical.update(format=1, schema_oid=2200, metadata_sha256='7' * 64, signed_unit_sha256='8' * 64)
        writer = {key: physical[key] for key in ('format', 'system_identifier', 'database', 'database_oid', 'schema', 'schema_oid', 'signed_unit_sha256')}
        writer.update(required_capability='fixture-v1', role=self.database['database_user'],
                      recovery_policy='original-owner-only', candidate={'payload_sha256': state['sha256']},
                      rollback={'payload_sha256': '9' * 64})
        self.plan = {'format': 7, 'deployment_id': 'later', 'schema_mode': 'verify-existing',
                     'go_candidate': candidate, 'go_rollback': rollback,
                     'existing_schema_contract': physical, 'merchant_store_writer': writer}
        self.plan_path = self.write_json(self.base / 'plan.json', self.plan)
        self.capsule = {'format': 1, 'deployment_id': 'later', 'root': str(self.cap_root),
                        'binary': str(deploy.ENTRY), 'service': deploy.SERVICE, 'host': 'fixture-host',
                        'startup_policy': 'per-start', 'schema_mode': 'verify-existing',
                        'controller_plan_sha256': deploy.digest(self.plan_path), 'candidate': candidate,
                        'rollback': rollback, 'existing_schema_contract': physical, 'merchant_store_writer': writer}
        self.cap_path = self.write_json(self.local_cap_root / 'capsule.json', self.capsule)
        web_files = {'dist/index.html': b'later index', 'dist/assets/main.js': b'later web script',
                     'REVISION': b'3' * 40 + b'\n'}
        self.web_files = web_files
        self.web_asset = self.make_archive(self.base / 'web.release.tar.gz', web_files)
        self.web_bundle = self.write(self.base / 'web.sigstore.json', b'{"synthetic_signed_bundle":true}\n')
        self.published = deploy.FRONTEND / 'releases' / '0.1.137'
        (self.published / 'assets').mkdir(mode=0o700, parents=True)
        for name, content in web_files.items():
            if name.startswith('dist/'):
                self.write(self.published / name.removeprefix('dist/'), content, 0o644)
        (deploy.FRONTEND / 'current').unlink()
        (deploy.FRONTEND / 'current').symlink_to('releases/0.1.137')
        self.manifest = {'format': 'lmm-systemd-later-provider-v1', 'release': 'later',
                         'state_sha256': deploy.digest(self.origin / 'state.json'),
                         'capsule_path': str(self.cap_root / 'capsule.json'), 'capsule_sha256': deploy.digest(self.cap_path),
                         'controller_plan_path': str(self.plan_path), 'controller_plan_sha256': deploy.digest(self.plan_path),
                         'frontend_asset': str(self.web_asset), 'frontend_asset_sha256': deploy.digest(self.web_asset),
                         'frontend_bundle': str(self.web_bundle), 'frontend_bundle_sha256': deploy.digest(self.web_bundle),
                         'frontend_version': '0.1.137', 'frontend_target': 'releases/0.1.137',
                         'boot_id': '11111111-2222-3333-4444-555555555555', 'main_pid': 2147483000,
                         'invocation_id': '4' * 32}
        self.manifest_path = self.base / 'later-provider.json'
        self.seal_manifest()
        self.generation = {'boot_id': self.manifest['boot_id'], 'main_pid': self.manifest['main_pid'],
                           'invocation_id': self.manifest['invocation_id'],
                           'installed_sha256': state['sha256'], 'running_sha256': state['sha256'],
                           'process_environment_sha256': '5' * 64, 'loaded_environment_sha256': '6' * 64,
                           'ordered_environment_files': [{'path': str(deploy.ENVIRONMENT),
                                                          'sha256': deploy.digest(deploy.ENVIRONMENT)}]}
        self.real_generation = deploy.later_generation
        self.real_database_status = deploy.later_database_status
        self.real_entry = deploy.later_entry
        self.entry_probe = self.stack.enter_context(patch.object(deploy, 'later_entry', side_effect=lambda manifest, capsule, state, uid=0:
            self.real_entry(manifest, capsule, state, uid=self.uid)))
        self.generation_probe = self.stack.enter_context(patch.object(deploy, 'later_generation', return_value=self.generation))
        self.database_probe = self.stack.enter_context(patch.object(deploy, 'later_database_status', return_value={
            **{key: writer[key] for key in ('system_identifier', 'database', 'database_oid', 'schema', 'schema_oid', 'role')},
            'reserved_count': 0}))
        self.stack.enter_context(patch.object(deploy.socket, 'gethostname', return_value='fixture-host'))
        real_bound = deploy.guardian.bound_file
        self.stack.enter_context(patch.object(deploy.guardian, 'bound_file', side_effect=lambda path, sha, *args, **kwargs:
            real_bound(self.local_path(Path(path)), sha, *args, **kwargs)))
        real_cleanup = deploy.cleanup_path
        self.stack.enter_context(patch.object(deploy, 'cleanup_path', side_effect=lambda path, *args, **kwargs:
            real_cleanup(self.local_path(Path(path)), *args, **kwargs)))
        real_digest = deploy.digest
        self.stack.enter_context(patch.object(deploy, 'digest', side_effect=lambda path: real_digest(self.local_path(Path(path)))))
        real_archive = deploy.later_signed_archive
        self.stack.enter_context(patch.object(deploy, 'later_signed_archive', side_effect=lambda asset, bundle, *args:
            real_archive(self.local_path(Path(asset)), self.local_path(Path(bundle)), *args)))
        self.native_exit = 0
        self.native_stdout = b'merchant_store_capsule=qualified\n'
        self.cosign_exit = 0
        self.executed = []
        self.stack.enter_context(patch.object(deploy.subprocess, 'run', side_effect=self.fake_command))

    def local_path(self, path):
        return self.local_cap_root / path.relative_to(self.cap_root) if path.is_relative_to(self.cap_root) else path

    def fake_command(self, argv, **kwargs):
        self.executed.append(argv)
        self.assertTrue(kwargs.get('capture_output'))
        if argv[0] == 'cosign':
            self.assertEqual(['verify-blob', '--bundle'], argv[1:3])
            self.assertIn('--certificate-identity', argv)
            self.assertIn('https://token.actions.githubusercontent.com', argv)
            return deploy.subprocess.CompletedProcess(argv, self.cosign_exit, b'', b'fixture signature stderr')
        self.assertEqual([str(deploy.BINARY), 'operator', 'production', 'writer-capsule', 'check', '--capsule',
                          self.manifest['capsule_path'], '--capsule-sha256', self.manifest['capsule_sha256']], argv)
        return deploy.subprocess.CompletedProcess(argv, self.native_exit, self.native_stdout, b'')

    def seal_manifest(self):
        self.write_json(self.manifest_path, self.manifest)
        self.history_args.later_provider = self.manifest_path
        self.history_args.later_provider_sha256 = deploy.digest(self.manifest_path)

    def qualify(self):
        return deploy.qualify_later_provider(self.manifest_path.read_bytes(), self.maintenance())

    def reseal_inputs(self):
        self.write_json(self.plan_path, self.plan)
        self.manifest['controller_plan_sha256'] = deploy.digest(self.plan_path)
        self.capsule['controller_plan_sha256'] = self.manifest['controller_plan_sha256']
        self.write_json(self.cap_path, self.capsule)
        self.manifest['capsule_sha256'] = deploy.digest(self.cap_path)
        self.manifest['state_sha256'] = deploy.digest(self.origin / 'state.json')
        self.seal_manifest()

    def test_entry_and_direct_binary_scopes_qualify_the_same_real_signed_alias(self):
        self.later_fixture()
        # Real symlinks normally have mode 0777; only the protected parent and
        # target permissions matter, rather than nonexistent symlink chmod.
        self.assertEqual(0o777, deploy.ENTRY.lstat().st_mode & 0o777)
        proof, _ = self.qualify()
        self.assertEqual({'path': str(deploy.ENTRY), 'target': deploy.BINARY.name,
                          'payload_sha256': deploy.digest(deploy.BINARY),
                          'entry_device': deploy.ENTRY.lstat().st_dev,
                          'entry_inode': deploy.ENTRY.lstat().st_ino,
                          'target_device': deploy.BINARY.lstat().st_dev,
                          'target_inode': deploy.BINARY.lstat().st_ino}, proof['entry'])
        self.capsule['binary'] = str(deploy.BINARY)
        self.reseal_inputs()
        direct, _ = self.qualify()
        self.assertEqual(proof['entry'], direct['entry'])

    def test_entry_scope_rejects_arbitrary_capsule_binary_and_different_parents(self):
        self.later_fixture()
        state = deploy.read_state(self.origin)
        for binary in (str(self.base / 'unrelated'), str(deploy.BINARY.parent / './other')):
            with self.subTest(binary=binary), self.assertRaisesRegex(RuntimeError, 'entry scope differs'):
                deploy.later_entry(self.manifest, dict(self.capsule, binary=binary), state)
        with patch.object(deploy, 'ENTRY', self.base / 'other-bin/lmm-api'), self.assertRaisesRegex(RuntimeError, 'entry scope differs'):
            deploy.later_entry(self.manifest, dict(self.capsule, binary=str(deploy.BINARY)), state)

    def test_entry_rejects_regular_absolute_foreign_and_nonexact_relative_aliases(self):
        self.later_fixture()
        state = deploy.read_state(self.origin)
        foreign = self.write(deploy.BINARY.parent / 'foreign', self.payload, 0o755)
        for target in (str(deploy.BINARY), str(foreign), foreign.name,
                       '../bin/lmm-api-go', 'missing'):
            deploy.ENTRY.unlink()
            deploy.ENTRY.symlink_to(target)
            with self.subTest(target=target), self.assertRaisesRegex(RuntimeError, 'protected relative alias'):
                deploy.later_entry(self.manifest, self.capsule, state)
        deploy.ENTRY.unlink()
        self.write(deploy.ENTRY, self.payload, 0o755)
        with self.assertRaisesRegex(RuntimeError, 'protected relative alias'):
            deploy.later_entry(self.manifest, self.capsule, state)

    def test_entry_rejects_broken_canonical_alias(self):
        self.later_fixture()
        deploy.BINARY.unlink()
        self.assertTrue(deploy.ENTRY.is_symlink())
        self.assertFalse(deploy.ENTRY.exists())
        with self.assertRaises((RuntimeError, OSError)):
            deploy.later_entry(self.manifest, self.capsule, deploy.read_state(self.origin))

    def test_entry_rejects_unsigned_hardlinked_writable_or_symlink_target(self):
        self.later_fixture()
        state = deploy.read_state(self.origin)
        self.write(deploy.BINARY, b'unsigned changed payload', 0o755)
        with self.assertRaisesRegex(RuntimeError, 'signed payload'):
            deploy.later_entry(self.manifest, self.capsule, state)
        self.write(deploy.BINARY, self.payload, 0o755)
        hardlink = self.base / 'binary-hardlink'
        os.link(deploy.BINARY, hardlink)
        self.assertEqual(2, deploy.BINARY.lstat().st_nlink)
        with self.assertRaises(RuntimeError):
            deploy.later_entry(self.manifest, self.capsule, state)
        hardlink.unlink()
        deploy.BINARY.chmod(0o777)
        with self.assertRaises(RuntimeError):
            deploy.later_entry(self.manifest, self.capsule, state)
        deploy.BINARY.chmod(0o755)
        foreign = self.write(self.base / 'other-signed-payload', self.payload, 0o755)
        deploy.BINARY.unlink()
        deploy.BINARY.symlink_to(foreign)
        with self.assertRaises(RuntimeError):
            deploy.later_entry(self.manifest, self.capsule, state)

    def test_entry_rejects_unsafe_parent_and_link_or_target_ownership(self):
        self.later_fixture()
        state = deploy.read_state(self.origin)
        deploy.ENTRY.parent.chmod(0o777)
        with self.assertRaises(RuntimeError):
            deploy.later_entry(self.manifest, self.capsule, state)
        deploy.ENTRY.parent.chmod(0o700)
        actual_lstat = Path.lstat
        for path, field, value in ((deploy.ENTRY, 4, self.uid + 1),
                                   (deploy.ENTRY, 5, os.getgid() + 1),
                                   (deploy.ENTRY, 3, 2),
                                   (deploy.BINARY, 4, self.uid + 1),
                                   (deploy.BINARY, 5, os.getgid() + 1)):
            def foreign_stat(selected, *args, **kwargs):
                info = actual_lstat(selected, *args, **kwargs)
                if selected == path:
                    values = list(info)
                    values[field] = value
                    return os.stat_result(values)
                return info
            with self.subTest(path=path.name, stat_field=field), patch.object(Path, 'lstat', foreign_stat), self.assertRaises(RuntimeError):
                deploy.later_entry(self.manifest, self.capsule, state)

    def test_entry_detects_target_replacement_while_opening_signed_payload(self):
        self.later_fixture()
        actual_open = deploy.os.open
        moved = self.base / 'retained-old-target'
        def replace_opened_target(path, flags, *args, **kwargs):
            descriptor = actual_open(path, flags, *args, **kwargs)
            if path == deploy.BINARY:
                deploy.BINARY.rename(moved)
                self.write(deploy.BINARY, self.payload, 0o755)
            return descriptor
        with patch.object(deploy.os, 'open', side_effect=replace_opened_target), self.assertRaisesRegex(RuntimeError, 'changed while opening'):
            deploy.later_entry(self.manifest, self.capsule, deploy.read_state(self.origin))

    def test_recheck_rejects_same_payload_new_alias_or_target_inode(self):
        self.later_fixture()
        proof, inputs = self.qualify()
        old_link = self.base / 'retained-old-alias'
        deploy.ENTRY.rename(old_link)
        deploy.ENTRY.symlink_to(deploy.BINARY.name)
        self.assertNotEqual(proof['entry']['entry_inode'], deploy.ENTRY.lstat().st_ino)
        with self.assertRaisesRegex(RuntimeError, 'qualified alias changed'):
            deploy.recheck_later_provider(self.manifest_path.read_bytes(), proof, inputs)
        deploy.ENTRY.unlink()
        old_link.rename(deploy.ENTRY)
        old_target = self.base / 'retained-old-binary'
        deploy.BINARY.rename(old_target)
        self.write(deploy.BINARY, self.payload, 0o755)
        self.assertEqual(proof['entry']['payload_sha256'], deploy.digest(deploy.BINARY))
        self.assertNotEqual(proof['entry']['target_inode'], deploy.BINARY.lstat().st_ino)
        with self.assertRaisesRegex(RuntimeError, 'qualified alias changed'):
            deploy.recheck_later_provider(self.manifest_path.read_bytes(), proof, inputs)

    def test_alias_replacement_during_final_qualification_cannot_publish_receipt(self):
        self.later_fixture()
        self.history_args.execute = True
        def change_alias(*args):
            deploy.ENTRY.rename(self.base / 'original-alias')
            deploy.ENTRY.symlink_to(deploy.BINARY.name)
        self.probes.side_effect = change_alias
        with self.assertRaisesRegex(RuntimeError, 'qualified alias changed'):
            deploy.register_released_history(self.history_args)
        self.assertFalse(deploy.history_root().exists())

    def test_later_provider_dry_run_and_registration_keep_financial_history(self):
        self.later_fixture()
        original = self.snapshot(deploy.ROOT)
        preview = deploy.register_released_history(self.history_args)
        self.assertEqual('lmm-systemd-released-history-v2', preview['format'])
        self.assertFalse(deploy.history_root().exists())
        self.history_args.execute = True
        registered = deploy.register_released_history(self.history_args)
        self.assertEqual('lmm-systemd-released-history-v2', registered['format'])
        self.assertEqual({'bridge', 'capture'}, deploy.released_ancestors())
        self.assertEqual(original, self.snapshot(deploy.ROOT))
        saved = deploy.history_root() / 'released/current'
        self.assertEqual({'controller.json', 'confirmation.json', 'receipt.json', 'later-provider.json',
                          'later-qualification.json', 'origin-state.json', 'origin-capsule.json', 'origin-plan.json'},
                         {path.name for path in saved.iterdir()})
        self.assertGreaterEqual(len([argv for argv in self.executed if argv[0] == 'cosign']), 4)
        self.probes.assert_any_call('0.2.98', None)

    def test_manifest_rejects_unknown_duplicate_and_unsafe_fields(self):
        self.later_fixture()
        changed = dict(self.manifest, unknown='not authority')
        with self.assertRaisesRegex(RuntimeError, 'unknown'):
            deploy.later_provider_manifest(json.dumps(changed).encode())
        raw = self.manifest_path.read_bytes().rstrip()
        with self.assertRaisesRegex(RuntimeError, 'duplicate'):
            deploy.later_provider_manifest(raw[:-1] + b',"release":"later"}')
        for key, value in [('main_pid', True), ('invocation_id', 'invalid'), ('boot_id', 'invalid'),
                           ('capsule_path', '/var/lib/lmm-api-go-deploy/merchant-capsules/other/capsule.json'),
                           ('controller_plan_path', '/private/../plan.json'), ('frontend_target', 'releases/../escape'),
                           ('frontend_version', '0.1.137-1'), ('state_sha256', 'A' * 64)]:
            with self.subTest(field=key), self.assertRaises(RuntimeError):
                deploy.later_provider_manifest(json.dumps(dict(self.manifest, **{key: value})).encode())

    def test_origin_inputs_reject_unconfirmed_migrating_and_changed_signed_scope(self):
        self.later_fixture()
        original = [json.loads(path.read_bytes()) for path in (self.origin / 'state.json', self.cap_path, self.plan_path)]
        changes = [(0, 'phase', 'STAGED'), (0, 'migrate', True), (0, 'maintenance_confirmation', True),
                   (0, 'sha256', 'f' * 64), (0, 'version', '0.2.99'), (0, 'release', 'other'),
                   (1, 'format', 2), (1, 'root', '/wrong'), (1, 'binary', '/wrong'),
                   (1, 'service', 'other.service'), (1, 'startup_policy', 'held'),
                   (1, 'controller_plan_sha256', 'f' * 64), (2, 'format', 6), (2, 'deployment_id', 'other')]
        for index, key, value in changes:
            values = json.loads(json.dumps(original))
            values[index][key] = value
            with self.subTest(input=index, field=key), self.assertRaises(RuntimeError):
                deploy.later_provider_inputs(self.manifest, *(json.dumps(value).encode() for value in values))
        for section, key in [('go_candidate', 'payload_sha256'), ('go_rollback', 'signature_bundle_sha256'),
                             ('existing_schema_contract', 'database_oid'), ('merchant_store_writer', 'role')]:
            values = json.loads(json.dumps(original))
            values[2][section][key] = 'changed'
            with self.subTest(section=section, field=key), self.assertRaises(RuntimeError):
                deploy.later_provider_inputs(self.manifest, *(json.dumps(value).encode() for value in values))

    def test_native_check_failure_or_missing_success_marker_prevents_registration(self):
        self.later_fixture()
        for code, stdout in [(1, b'merchant_store_capsule=qualified\n'), (0, b'claimed successful\n')]:
            self.native_exit, self.native_stdout = code, stdout
            with self.subTest(code=code), self.assertRaisesRegex(RuntimeError, 'native qualification failed'):
                deploy.register_released_history(self.history_args)
            self.assertFalse(deploy.history_root().exists())

    def test_original_physical_database_and_business_role_must_match(self):
        self.later_fixture()
        initial = json.loads(json.dumps(self.capsule))
        for key, value in [('system_identifier', '987654321'), ('database', 'another'),
                           ('database_oid', 43), ('schema', 'other')]:
            self.capsule = json.loads(json.dumps(initial))
            self.capsule['existing_schema_contract'][key] = value
            self.plan['existing_schema_contract'] = self.capsule['existing_schema_contract']
            self.reseal_inputs()
            with self.subTest(field=key), self.assertRaisesRegex(RuntimeError, 'released physical database'):
                self.qualify()
        self.capsule = json.loads(json.dumps(initial))
        self.plan['existing_schema_contract'] = self.capsule['existing_schema_contract']
        self.capsule['merchant_store_writer']['role'] = 'another-role'
        self.plan['merchant_store_writer'] = self.capsule['merchant_store_writer']
        self.reseal_inputs()
        with self.assertRaisesRegex(RuntimeError, 'released database role'):
            self.qualify()

    def test_installed_and_running_elf_must_be_the_signed_candidate(self):
        self.later_fixture()
        self.write(deploy.BINARY, b'changed installed generation', 0o755)
        with self.assertRaisesRegex(RuntimeError, 'installed ELF'):
            self.qualify()
        self.write(deploy.BINARY, self.payload, 0o755)
        self.generation_probe.return_value = dict(self.generation, running_sha256='f' * 64)
        with self.assertRaisesRegex(RuntimeError, 'running ELF'):
            self.qualify()

    def test_runtime_generation_and_origin_byte_change_during_qualification_are_rejected(self):
        self.later_fixture()
        self.generation_probe.side_effect = [self.generation, dict(self.generation, invocation_id='a' * 32)]
        with self.assertRaisesRegex(RuntimeError, 'changed during qualification'):
            self.qualify()
        self.generation_probe.side_effect = None
        original = (self.origin / 'state.json').read_bytes()
        self.probes.side_effect = lambda *args: self.write(self.origin / 'state.json', original + b' ')
        with self.assertRaises(RuntimeError):
            self.qualify()

    def test_backend_and_frontend_signature_failure_never_register(self):
        self.later_fixture()
        self.cosign_exit = 1
        with self.assertRaisesRegex(RuntimeError, 'signature failed'):
            self.qualify()
        self.assertFalse(any(argv[0] == str(deploy.BINARY) for argv in self.executed))
        self.cosign_exit = 0
        original_command = self.fake_command
        def web_signature_failure(argv, **kwargs):
            result = original_command(argv, **kwargs)
            if argv[0] == 'cosign' and argv[-1] == str(self.web_asset):
                result.returncode = 1
            return result
        with patch.object(deploy.subprocess, 'run', side_effect=web_signature_failure), self.assertRaisesRegex(RuntimeError, 'signature failed'):
            self.qualify()

    def test_signed_frontend_actual_tree_and_current_link_must_match(self):
        self.later_fixture()
        self.write(self.published / 'assets/main.js', b'tampered actual frontend', 0o644)
        with self.assertRaisesRegex(RuntimeError, 'differs from signed payload'):
            self.qualify()
        self.write(self.published / 'assets/main.js', self.web_files['dist/assets/main.js'], 0o644)
        (deploy.FRONTEND / 'current').unlink()
        (deploy.FRONTEND / 'current').symlink_to('releases/frozen')
        with self.assertRaisesRegex(RuntimeError, 'active frontend changed'):
            self.qualify()

    def test_signed_archive_rejects_traversal_absolute_symlink_and_duplicate_entries(self):
        self.later_fixture()
        malformed = [('dist/../escaped', tarfile.REGTYPE, None), ('/absolute', tarfile.REGTYPE, None),
                     ('dist/back\\slash', tarfile.REGTYPE, None), ('dist/link', tarfile.SYMTYPE, 'index.html'),
                     ('dist/index.html', tarfile.REGTYPE, None)]
        for index, (name, kind, link) in enumerate(malformed):
            entry = tarfile.TarInfo(name)
            entry.type = kind
            entry.size = 0
            entry.linkname = link or ''
            asset = self.make_archive(self.base / ('unsafe-' + str(index) + '.tar.gz'), self.web_files, [(entry, None)])
            with self.subTest(name=name), self.assertRaisesRegex(RuntimeError, 'unsafe entries'):
                deploy.later_signed_archive(asset, self.web_bundle, deploy.digest(asset), deploy.digest(self.web_bundle), '0.1.137', 'web')

    def test_each_held_lock_inode_replacement_blocks_before_receipt_publication(self):
        self.later_fixture()
        real_qualify = deploy.qualify_later_provider
        for name in ('native', 'systemd', 'frontend'):
            selected = Path(self.locks[name])
            def replace_held(*args):
                result = real_qualify(*args)
                selected.unlink()
                self.write(selected, b'replacement inode')
                return result
            with self.subTest(lock=name), patch.object(deploy, 'qualify_later_provider', side_effect=replace_held):
                with self.assertRaisesRegex(RuntimeError, 'held history lock inode changed'):
                    deploy.register_released_history(self.history_args)
            self.assertFalse(deploy.history_root().exists())

    def test_v2_history_survives_later_current_upgrade_and_rejects_stored_evidence_tampering(self):
        self.later_fixture()
        self.history_args.execute = True
        deploy.register_released_history(self.history_args)
        self.write(deploy.BINARY, b'new legitimate installed release', 0o755)
        self.write(deploy.ENVIRONMENT, b'new legitimate current environment\n')
        (deploy.FRONTEND / 'current').unlink()
        (deploy.FRONTEND / 'current').symlink_to('releases/frozen')
        self.generation_probe.reset_mock()
        self.database_probe.reset_mock()
        # Future releases may legitimately replace both entry and target. The
        # immutable registered evidence must never probe that later live alias.
        deploy.ENTRY.unlink()
        self.write(deploy.ENTRY, b'legitimate later service entry', 0o755)
        self.entry_probe.reset_mock()
        self.executed.clear()
        self.assertEqual({'bridge', 'capture'}, deploy.released_ancestors())
        self.generation_probe.assert_not_called()
        self.database_probe.assert_not_called()
        self.entry_probe.assert_not_called()
        self.assertEqual([], self.executed)
        saved = deploy.history_root() / 'released/current'
        for name in ('later-provider.json', 'later-qualification.json', 'origin-state.json', 'origin-capsule.json', 'origin-plan.json'):
            path = saved / name
            original = path.read_bytes()
            self.write(path, original + b' ')
            with self.subTest(file=name), self.assertRaises(RuntimeError):
                deploy.released_ancestors()
            self.write(path, original)
        frozen = deploy.ROOT / 'bridge/state.json'
        self.write(frozen, frozen.read_bytes() + b' ')
        with self.assertRaisesRegex(RuntimeError, 'history evidence changed'):
            deploy.released_ancestors()

    def process_fixture(self):
        proc = self.base / 'fake-proc'
        process = proc / str(self.manifest['main_pid'])
        process.mkdir(mode=0o700, parents=True)
        self.write(process / 'environ', b'SQL_DSN=postgresql://fixture:fixture-password@localhost/fixture\0')
        self.write(process / 'exe', self.payload, 0o755)
        boot = self.write(proc / 'boot_id', self.manifest['boot_id'].encode() + b'\n')
        def local_proc_path(*args):
            path = Path(*args)
            if str(path) == '/proc/sys/kernel/random/boot_id':
                return boot
            if path.is_relative_to('/proc'):
                return proc / path.relative_to('/proc')
            return path
        return process, boot, local_proc_path

    def test_actual_generation_checks_pid_invocation_boot_restart_guard_and_environment(self):
        self.later_fixture()
        process, boot, local_proc_path = self.process_fixture()
        loaded = {'MainPID': str(self.manifest['main_pid']), 'InvocationID': self.manifest['invocation_id'],
                  'NRestarts': '0', 'ControlPID': '0', 'ActiveState': 'active', 'SubState': 'running',
                  'Result': 'success', 'ExecStartPre': 'argv[]=lmm-api-go operator writer-start code=exited status=0',
                  'Environment': 'LMM_SERVICE=fixture'}
        def rendered():
            return '\n'.join(key + '=' + value for key, value in loaded.items())
        with patch.object(deploy, 'Path', side_effect=local_proc_path), \
             patch.object(deploy, 'run', side_effect=lambda *args: rendered()) as command, \
             patch.object(deploy, 'service_environment_files', return_value=[str(deploy.ENVIRONMENT)]):
            generation = self.real_generation(self.manifest)
            self.assertEqual(deploy.digest(deploy.BINARY), generation['running_sha256'])
            self.assertEqual(hashlib.sha256((process / 'environ').read_bytes()).hexdigest(), generation['process_environment_sha256'])
            self.assertEqual([{'path': str(deploy.ENVIRONMENT), 'sha256': deploy.digest(deploy.ENVIRONMENT)}], generation['ordered_environment_files'])
            self.assertEqual(('systemctl', 'show', deploy.SERVICE), command.call_args.args[:3])
            for key, changed in [('MainPID', '123'), ('InvocationID', 'a' * 32), ('NRestarts', '1'),
                                 ('ControlPID', '123'), ('ActiveState', 'failed'), ('SubState', 'exited'),
                                 ('Result', 'timeout'), ('ExecStartPre', 'writer-start code=exited status=1')]:
                original = loaded[key]
                loaded[key] = changed
                with self.subTest(field=key), self.assertRaises(RuntimeError):
                    self.real_generation(self.manifest)
                loaded[key] = original
            self.write(boot, b'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee\n')
            with self.assertRaisesRegex(RuntimeError, 'boot changed'):
                self.real_generation(self.manifest)

    def test_actual_database_read_only_probe_rejects_live_owner_wrong_identity_and_role(self):
        self.later_fixture()
        process, _, local_proc_path = self.process_fixture()
        expected = {**{key: self.capsule['merchant_store_writer'][key]
                       for key in ('system_identifier', 'database', 'database_oid', 'schema', 'schema_oid', 'role')},
                    'reserved_count': 0}
        current = dict(expected)
        status = {'code': 0}
        queries = []
        def query(argv, **kwargs):
            self.assertEqual(['psql', '-X', '-qAt', '-v', 'ON_ERROR_STOP=1', '--command'], argv[:6])
            self.assertIn('BEGIN READ ONLY;', argv[6])
            self.assertIn('; ROLLBACK;', argv[6])
            self.assertIn('pg_catalog.lower(pg_catalog.btrim', argv[6])
            self.assertIn(r'\u3000', argv[6])
            self.assertIn('merchantstoredeploymentfence:%', argv[6])
            self.assertEqual('fixture', kwargs['env']['PGDATABASE'])
            queries.append(argv[6])
            return deploy.subprocess.CompletedProcess(argv, status['code'], json.dumps(current).encode(), b'')
        with patch.object(deploy, 'Path', side_effect=local_proc_path), patch.object(deploy.shutil, 'which', return_value='/mock/psql'), patch.object(deploy.subprocess, 'run', side_effect=query):
            self.assertEqual(expected, self.real_database_status(self.manifest, self.capsule))
            for key, changed in [('reserved_count', 1), ('system_identifier', 'other'), ('database', 'other'),
                                 ('database_oid', 43), ('schema', 'other'), ('schema_oid', 2201), ('role', 'other')]:
                current = dict(expected, **{key: changed})
                with self.subTest(field=key), self.assertRaisesRegex(RuntimeError, 'differs or has a live durable owner'):
                    self.real_database_status(self.manifest, self.capsule)
            current = dict(expected)
            status['code'] = 1
            with self.assertRaisesRegex(RuntimeError, 'actual physical database/owner check failed'):
                self.real_database_status(self.manifest, self.capsule)
            before = len(queries)
            self.write(process / 'environ', (process / 'environ').read_bytes() + b'LOG_SQL_DSN=postgresql://fixture/logs\0')
            with self.assertRaisesRegex(RuntimeError, 'separate unproved log database'):
                self.real_database_status(self.manifest, self.capsule)
            self.assertEqual(before, len(queries))

    def test_lightweight_recheck_rejects_artifact_generation_database_and_frontend_change(self):
        self.later_fixture()
        proof, inputs = self.qualify()
        self.generation_probe.return_value = dict(self.generation, process_environment_sha256='f' * 64)
        with self.assertRaisesRegex(RuntimeError, 'actual generation changed'):
            deploy.recheck_later_provider(self.manifest_path.read_bytes(), proof, inputs)
        self.generation_probe.return_value = self.generation
        original_database = self.database_probe.return_value
        self.database_probe.return_value = dict(original_database, reserved_count=1)
        with self.assertRaisesRegex(RuntimeError, 'database/owner changed'):
            deploy.recheck_later_provider(self.manifest_path.read_bytes(), proof, inputs)
        self.database_probe.return_value = original_database
        for path in proof['artifact_sha256']:
            actual = self.local_path(Path(path))
            original = actual.read_bytes()
            self.write(actual, original + b' changed')
            with self.subTest(artifact=actual.name), self.assertRaisesRegex(RuntimeError, 'qualified signed artifacts changed'):
                deploy.recheck_later_provider(self.manifest_path.read_bytes(), proof, inputs)
            self.write(actual, original)
        self.write(self.published / 'index.html', b'changed between qualification and registration', 0o644)
        with self.assertRaisesRegex(RuntimeError, 'qualified frontend changed'):
            deploy.recheck_later_provider(self.manifest_path.read_bytes(), proof, inputs)

    def test_registration_race_after_snapshot_writes_never_publishes_receipt(self):
        self.later_fixture()
        self.history_args.execute = True
        original_write = deploy.immutable_write
        def generation_change_after_snapshot(path, *args):
            original_write(path, *args)
            if path.name == 'origin-plan.json':
                self.generation_probe.return_value = dict(self.generation, invocation_id='a' * 32)
        with patch.object(deploy, 'immutable_write', side_effect=generation_change_after_snapshot), \
             self.assertRaisesRegex(RuntimeError, 'actual generation changed'):
            deploy.register_released_history(self.history_args)
        saved = deploy.history_root() / 'released/current'
        self.assertTrue((saved / 'origin-plan.json').exists())
        self.assertFalse((saved / 'receipt.json').exists())

    def parent_fixture(self):
        self.later_fixture()
        capsule = json.loads(self.cap_path.read_bytes())
        contract_sha = hashlib.sha256(json.dumps(capsule['merchant_store_writer'], separators=(',', ':')).encode()).hexdigest()
        self.parent_state = self.local_cap_root / 'state'
        self.parent_state.mkdir(mode=0o700)
        self.parent_owner = {'format': 1, 'state': 'ACTIVE', 'purpose': 'portable-deploy',
                             'deployment_id': 'later', 'host': 'fixture-host', 'service': deploy.SERVICE,
                             'plan_sha256': self.manifest['capsule_sha256'], 'contract_sha256': contract_sha,
                             'provider_sha256': self.capsule['candidate']['payload_sha256'],
                             'holder_unit': 'lmm-merchant-portable-later.service', 'nonce': 'a' * 32,
                             'holder_pid': 2147483100, 'holder_invocation_id': 'b' * 32, 'backend_pid': 4321,
                             **{key: capsule['merchant_store_writer'][key]
                                for key in ('system_identifier', 'database', 'database_oid', 'schema', 'schema_oid', 'role')}}
        self.parent_owner_path = self.write_json(self.parent_state / 'portable-released-owner.json', self.parent_owner)
        self.parent_journal_row = {'MESSAGE': 'merchant_store_start=qualified',
                                   '_SYSTEMD_INVOCATION_ID': self.manifest['invocation_id'],
                                   '_SYSTEMD_UNIT': deploy.SERVICE, '_BOOT_ID': self.manifest['boot_id'].replace('-', ''),
                                   '_UID': '0', '_PID': '4322', '__CURSOR': 'fixture-cursor',
                                   '__REALTIME_TIMESTAMP': '1791489600000000'}
        self.parent_journal_bytes = json.dumps(self.parent_journal_row).encode() + b'\n'
        self.manifest.update(startup_mode='ordinary-parent-cas-journal',
                             released_owner_sha256=deploy.digest(self.parent_owner_path),
                             startup_journal_sha256=self.journal_sha(self.parent_journal_row))
        self.parent_origin = {'release': 'later', 'state_sha256': self.manifest['state_sha256'],
                              'capsule_sha256': self.manifest['capsule_sha256'],
                              'released_owner_sha256': self.manifest['released_owner_sha256'],
                              'source_revision': self.capsule['candidate']['git_revision'],
                              'startup_source_sha256': 'c' * 64, 'provider_sha256': self.capsule['candidate']['payload_sha256'],
                              'contract_sha256': contract_sha}
        self.stack.enter_context(patch.object(deploy, 'ORDINARY_PARENT_STARTUP_ORIGIN', self.parent_origin))
        self.parent_held = {'LoadState': 'loaded', 'MainPID': '0', 'InvocationID': self.parent_owner['holder_invocation_id'],
                            'ControlPID': '0', 'ActiveState': 'inactive', 'SubState': 'dead',
                            'Result': 'success', 'ControlGroup': ''}
        self.parent_holder_exit = self.parent_journal_exit = 0
        self.parent_commands = []
        self.stack.enter_context(patch.object(deploy.subprocess, 'run', side_effect=self.parent_command))
        self.seal_manifest()

    def journal_sha(self, row):
        return hashlib.sha256(json.dumps(row, sort_keys=True, separators=(',', ':')).encode()).hexdigest()

    def parent_path(self, *args):
        path = Path(*args)
        if path.is_relative_to('/proc'):
            if str(path) == '/proc/sys/kernel/random/boot_id':
                return self.base / 'fake-proc/boot_id'
            return self.base / 'fake-proc' / path.relative_to('/proc')
        return self.local_path(path)

    def parent_command(self, argv, **kwargs):
        if argv[0] == 'systemctl':
            self.assertEqual(['systemctl', 'show', self.parent_owner['holder_unit'],
                              '--property=LoadState,MainPID,InvocationID,ControlPID,ActiveState,SubState,Result,ControlGroup'], argv)
            self.assertTrue(kwargs['text'])
            self.parent_commands.append(argv)
            return deploy.subprocess.CompletedProcess(argv, self.parent_holder_exit,
                '\n'.join(key + '=' + value for key, value in self.parent_held.items()), '')
        if argv[0] == 'journalctl':
            self.assertEqual(['journalctl', '--no-pager', '-o', 'json',
                              '_SYSTEMD_INVOCATION_ID=' + self.manifest['invocation_id'],
                              'MESSAGE=merchant_store_start=qualified'], argv)
            self.parent_commands.append(argv)
            return deploy.subprocess.CompletedProcess(argv, self.parent_journal_exit, self.parent_journal_bytes, b'')
        return self.fake_command(argv, **kwargs)

    def parent_startup(self):
        with patch.object(deploy, 'Path', side_effect=self.parent_path):
            return deploy.later_parent_startup(self.manifest)

    def reseal_parent_owner(self, raw=None):
        self.write(self.parent_owner_path, raw if raw is not None else json.dumps(self.parent_owner).encode() + b'\n')
        self.manifest['released_owner_sha256'] = deploy.digest(self.parent_owner_path)
        self.parent_origin['released_owner_sha256'] = self.manifest['released_owner_sha256']

    def test_parent_cas_manifest_requires_explicit_exact_reviewed_origin(self):
        self.parent_fixture()
        self.assertEqual(self.manifest, deploy.later_provider_manifest(json.dumps(self.manifest).encode()))
        for key in ('startup_mode', 'released_owner_sha256', 'startup_journal_sha256'):
            changed = dict(self.manifest)
            del changed[key]
            with self.subTest(missing=key), self.assertRaises(RuntimeError):
                deploy.later_provider_manifest(json.dumps(changed).encode())
        for key, value in [('startup_mode', 'trust-parent'), ('released_owner_sha256', 'f' * 64),
                           ('capsule_sha256', 'f' * 64), ('state_sha256', 'f' * 64), ('release', 'another')]:
            with self.subTest(field=key), self.assertRaises(RuntimeError):
                deploy.later_provider_manifest(json.dumps(dict(self.manifest, **{key: value})).encode())

    def test_parent_cas_uses_private_owner_bytes_and_exact_full_journal_entry(self):
        self.parent_fixture()
        original = self.snapshot(self.base)
        result = self.parent_startup()
        self.assertEqual('ordinary-parent-cas-journal', result['mode'])
        self.assertEqual(self.manifest['released_owner_sha256'], result['released_owner_sha256'])
        self.assertEqual(self.manifest['startup_journal_sha256'], result['journal_sha256'])
        self.assertEqual(4322, result['guard_pid'])
        self.assertEqual(self.parent_held, result['holder_final'])
        self.assertEqual(original, self.snapshot(self.base))
        self.assertEqual(['systemctl', 'journalctl'], [argv[0] for argv in self.parent_commands])
        raw = self.parent_owner_path.read_bytes()
        self.write(self.parent_owner_path, raw + b' ')
        with self.assertRaises(RuntimeError):
            self.parent_startup()
        self.write(self.parent_owner_path, raw)
        self.parent_journal_bytes = json.dumps(dict(self.parent_journal_row, __CURSOR='changed')).encode() + b'\n'
        with self.assertRaisesRegex(RuntimeError, 'journal bytes changed'):
            self.parent_startup()

    def test_parent_cas_owner_rejects_wrong_session_scope_shape_and_nonce(self):
        self.parent_fixture()
        original = dict(self.parent_owner)
        changes = [('deployment_id', 'other'), ('host', 'other'), ('service', 'other.service'),
                   ('purpose', 'writer-start'), ('state', 'RELEASED'), ('plan_sha256', 'f' * 64),
                   ('contract_sha256', 'f' * 64), ('provider_sha256', 'f' * 64), ('database_oid', 99),
                   ('schema_oid', 99), ('role', 'other'), ('holder_unit', 'foreign.service'),
                   ('holder_pid', True), ('backend_pid', 1), ('holder_invocation_id', 'not-an-invocation'),
                   ('nonce', 'a' * 64), ('unknown', 'unproved')]
        for key, value in changes:
            self.parent_owner = dict(original, **{key: value})
            self.reseal_parent_owner()
            with self.subTest(field=key), self.assertRaisesRegex(RuntimeError, 'exact native session'):
                self.parent_startup()
        self.parent_owner = original
        raw = json.dumps(original).encode()
        self.reseal_parent_owner(raw[:-1] + b',"nonce":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}')
        with self.assertRaisesRegex(RuntimeError, 'duplicate'):
            self.parent_startup()

    def test_parent_cas_requires_reviewed_source_provider_and_original_writer_contract(self):
        self.parent_fixture()
        original = json.loads(self.cap_path.read_bytes())
        for section, key, value in [(None, 'format', 2), (None, 'host', 'other'),
                                    ('candidate', 'git_revision', 'f' * 40), ('candidate', 'payload_sha256', 'f' * 64),
                                    ('merchant_store_writer', 'role', 'other')]:
            capsule = json.loads(json.dumps(original))
            (capsule if section is None else capsule[section])[key] = value
            self.write_json(self.cap_path, capsule)
            self.manifest['capsule_sha256'] = deploy.digest(self.cap_path)
            self.parent_origin['capsule_sha256'] = self.manifest['capsule_sha256']
            with self.subTest(section=section, field=key), self.assertRaisesRegex(RuntimeError, 'reviewed source/provider|writer contract changed'):
                self.parent_startup()

    def test_parent_cas_rejects_live_holder_owner_socket_and_nonterminal_unit(self):
        self.parent_fixture()
        for name in ('portable-owner.json', 'holder.sock'):
            path = self.write(self.parent_state / name, b'live owner evidence')
            with self.subTest(file=name), self.assertRaisesRegex(RuntimeError, 'live communication evidence'):
                self.parent_startup()
            path.unlink()
            path.symlink_to('missing-live-evidence')
            with self.subTest(dangling=name), self.assertRaisesRegex(RuntimeError, 'live communication evidence'):
                self.parent_startup()
            path.unlink()
        process = self.base / 'fake-proc' / str(self.parent_owner['holder_pid'])
        process.mkdir(mode=0o700, parents=True)
        with self.assertRaisesRegex(RuntimeError, 'holder process is still present'):
            self.parent_startup()
        process.rmdir()
        original = dict(self.parent_held)
        for key, value in [('LoadState', 'failed'), ('MainPID', '123'), ('InvocationID', 'f' * 32),
                           ('ControlPID', '123'), ('ActiveState', 'active'), ('SubState', 'running'),
                           ('Result', 'exit-code'), ('ControlGroup', '/system.slice/still-live')]:
            self.parent_held = dict(original, **{key: value})
            with self.subTest(field=key), self.assertRaisesRegex(RuntimeError, 'not truly terminated'):
                self.parent_startup()
        self.parent_held = original
        self.parent_holder_exit = 1
        with self.assertRaisesRegex(RuntimeError, 'unit inspection failed'):
            self.parent_startup()

    def test_parent_cas_journal_rejects_missing_duplicate_partial_and_foreign_identity(self):
        self.parent_fixture()
        original = dict(self.parent_journal_row)
        for raw in (b'', self.parent_journal_bytes * 2,
                    b'{"MESSAGE":"merchant_store_start=qualified","MESSAGE":"merchant_store_start=qualified"}\n'):
            self.parent_journal_bytes = raw
            with self.subTest(raw=raw[:30]), self.assertRaises(RuntimeError):
                self.parent_startup()
        for key, value in [('MESSAGE', 'prefix merchant_store_start=qualified suffix'),
                           ('_SYSTEMD_INVOCATION_ID', 'f' * 32), ('_SYSTEMD_UNIT', 'foreign.service'),
                           ('_BOOT_ID', 'f' * 32), ('_UID', '1000'), ('_PID', '1'),
                           ('_PID', str(self.manifest['main_pid'])), ('_PID', 4322), ('_PID', 'unproved')]:
            row = dict(original, **{key: value})
            self.parent_journal_bytes = json.dumps(row).encode() + b'\n'
            self.manifest['startup_journal_sha256'] = self.journal_sha(row)
            with self.subTest(field=key, value=value), self.assertRaisesRegex(RuntimeError, 'journal identity differs'):
                self.parent_startup()
        self.parent_journal_bytes = json.dumps(original).encode() + b'\n'
        self.manifest['startup_journal_sha256'] = self.journal_sha(original)
        self.parent_journal_exit = 1
        with self.assertRaisesRegex(RuntimeError, 'journal query failed'):
            self.parent_startup()

    def test_null_execstartpre_requires_explicit_parent_closure_and_still_checks_pg_zero(self):
        self.parent_fixture()
        self.process_fixture()
        loaded = {'MainPID': str(self.manifest['main_pid']), 'InvocationID': self.manifest['invocation_id'],
                  'NRestarts': '0', 'ControlPID': '0', 'ActiveState': 'active', 'SubState': 'running',
                  'Result': 'success', 'ExecStartPre': 'writer-start code=(null) status=0', 'Environment': 'fixture=true'}
        rendered = '\n'.join(key + '=' + value for key, value in loaded.items())
        with patch.object(deploy, 'Path', side_effect=self.parent_path), \
             patch.object(deploy, 'run', return_value=rendered), \
             patch.object(deploy, 'service_environment_files', return_value=[str(deploy.ENVIRONMENT)]):
            ordinary = {key: self.manifest[key] for key in deploy.LATER_PROVIDER_FIELDS}
            with self.assertRaisesRegex(RuntimeError, 'did not complete successfully'):
                self.real_generation(ordinary)
            generation = self.real_generation(self.manifest)
            self.assertEqual('ordinary-parent-cas-journal', generation['startup']['mode'])
        self.generation_probe.return_value = generation
        self.seal_manifest()
        proof, inputs = self.qualify()
        self.assertIn('startup', proof['generation'])
        self.database_probe.assert_called()
        self.database_probe.return_value = dict(proof['actual_database'], reserved_count=1)
        with self.assertRaisesRegex(RuntimeError, 'database/owner changed'):
            deploy.recheck_later_provider(self.manifest_path.read_bytes(), proof, inputs)

    def test_original_provider_v1_registration_remains_compatible(self):
        self.proof()
        original = self.snapshot(deploy.ROOT)
        preview = deploy.register_released_history(self.history_args)
        self.assertEqual('lmm-systemd-released-history-v1', preview['format'])
        self.assertFalse(deploy.history_root().exists())
        self.history_args.execute = True
        result = deploy.register_released_history(self.history_args)
        self.assertEqual('lmm-systemd-released-history-v1', result['format'])
        self.assertEqual({'bridge', 'capture'}, deploy.released_ancestors())
        self.assertEqual(original, self.snapshot(deploy.ROOT))


if __name__ == '__main__':
    unittest.main()
