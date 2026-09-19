#!/usr/bin/env python3
import importlib.util
import json
import io
import hashlib
import subprocess
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('recovery', Path(__file__).with_name('reconcile-frontend-package.py'))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class RecoveryTests(unittest.TestCase):
    def test_tree_digest_ignores_mtime_but_rejects_content_and_symlinks(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            file = root / 'index.html'
            file.write_text('same')
            before = MODULE.tree_digest(root)
            os.utime(file, (1, 1))
            self.assertEqual(MODULE.tree_digest(root), before)
            file.write_text('changed')
            self.assertNotEqual(MODULE.tree_digest(root), before)
            (root / 'link').symlink_to(file)
            with self.assertRaisesRegex(RuntimeError, 'unsafe entry'):
                MODULE.tree_digest(root)

    def test_health_checks_arch_origin_and_distinct_public_frontend(self):
        origin_body, public_body = b'arch-old', b'ubuntu-new'
        origin_hash = hashlib.sha256(origin_body).hexdigest()
        public_hash = hashlib.sha256(public_body).hexdigest()
        class Opener:
            def open(self, request, **kwargs):
                url = request if isinstance(request, str) else request.full_url
                if '/api/' in url:
                    return io.BytesIO(json.dumps({'success': True, 'data': {'version': '0.2.51'}}).encode())
                return io.BytesIO(public_body)
        with patch.object(MODULE.urllib.request, 'build_opener', return_value=Opener()), patch.object(MODULE.subprocess, 'run') as command:
            command.return_value = subprocess.CompletedProcess([], 0, origin_body, b'')
            MODULE.health(origin_hash, public_hash)
            self.assertIn('api.lmm.best:443:45.59.187.63', command.call_args.args[0])
            with self.assertRaisesRegex(RuntimeError, 'public frontend'):
                MODULE.health(origin_hash, origin_hash)
            command.return_value = subprocess.CompletedProcess([], 0, public_body, b'')
            with self.assertRaisesRegex(RuntimeError, 'Arch frontend'):
                MODULE.health(origin_hash, public_hash)

    def exercise(self, fail_health=False, drift=False):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            web, work = root / 'frontend', root / 'work'
            old = web / MODULE.OLD_TARGET
            old.mkdir(parents=True)
            work.mkdir()
            (old / 'index.html').write_text('old page')
            (web / 'current').symlink_to(MODULE.OLD_TARGET)
            (web / '.deployment-transactions').mkdir()
            source = root / 'package-dist'
            source.mkdir()
            (source / 'index.html').write_text('new page')
            revision = 'a' * 40
            target = 'releases/0.1.76-1.g' + revision[:12]
            plan = {'old_tree_sha256': MODULE.tree_digest(old), 'old_index_sha256': MODULE.digest(old / 'index.html'), 'public_index_sha256': MODULE.digest(source / 'index.html')}
            (work / 'plan.json').write_text(json.dumps(plan))
            events = []
            installed = [MODULE.OLD_PACKAGE]
            health_count = [0]
            real_read = Path.read_text
            real_exists = Path.exists

            def read(path, *args, **kwargs):
                if str(path) == '/etc/hostname':
                    return 'arch-dmit\n'
                if str(path) == '/usr/share/doc/lmm-api-web-bin/REVISION':
                    return revision
                return real_read(path, *args, **kwargs)

            def exists(path):
                if str(path) == '/var/lib/lmm-api-go-deploy/transaction.lock':
                    return False
                return real_exists(path)

            def run(*args):
                events.append(args)
                if args[:2] == ('/usr/bin/pacman', '-Q'):
                    return installed[0] if args[2] == 'lmm-api-web-bin' else 'lmm-api-go-bin 0.2.51-1'
                if args[:2] == ('/usr/bin/pacman', '-U'):
                    if args[-1].endswith('candidate'):
                        installed[0] = MODULE.NEW_PACKAGE
                        MODULE.shutil.copytree(source, web / target)
                        (web / 'current').unlink()
                        (web / 'current').symlink_to(target)
                        (web / '.deployment-transactions' / (target.split('/')[-1] + '.json')).write_text(json.dumps({'phase': 'CONFIRMED', 'previous': MODULE.OLD_TARGET.split('/')[-1]}))
                    else:
                        installed[0] = MODULE.OLD_PACKAGE
                return ''

            def verify(plan, label):
                events.append(('verify', label))
                return work / label

            def health(index, public_index=None):
                events.append(('health', index))
                health_count[0] += 1
                if fail_health and health_count[0] == 2:
                    raise RuntimeError('public frontend differs')

            def frontend(*args):
                events.append(('native-frontend', *args))
                self.assertEqual(args[:2], ('rollback', '--release'))
                (web / 'current').unlink()
                (web / 'current').symlink_to('releases/' + args[2])

            real_tree = MODULE.tree_digest
            def tree(path):
                return real_tree(source if str(path) == '/usr/share/lmm-api-web/frontend-dist' else path)

            if drift:
                (old / 'index.html').write_text('unplanned change')
            with patch.object(MODULE, 'ROOT', web), patch.object(MODULE, 'WORK', work), \
                 patch.object(MODULE, 'run', run), patch.object(MODULE, 'health', health), \
                 patch.object(MODULE, 'verified_package', verify), patch.object(MODULE, 'frontend_command', frontend), \
                 patch.object(MODULE, 'tree_digest', tree), patch.object(Path, 'read_text', read), \
                 patch.object(Path, 'exists', exists), patch.object(MODULE.shutil, 'disk_usage', return_value=type('Space', (), {'free': 8*1024**3, 'used': 10*1024**3, 'total': 20*1024**3})()):
                if fail_health or drift:
                    with self.assertRaises(RuntimeError):
                        MODULE.apply(plan, work / 'helper.py')
                else:
                    self.assertEqual(MODULE.apply(plan, work / 'helper.py')['phase'], 'CONFIRMED')
                state = json.loads((work / 'state.json').read_text()) if (work / 'state.json').exists() else None
            if drift:
                self.assertIsNone(state)
                self.assertFalse(any(args[0] == '/usr/bin/systemd-run' or args[:2] == ('/usr/bin/pacman', '-U') for args in events))
                return
            timer = next(i for i, args in enumerate(events) if args[0] == '/usr/bin/systemd-run')
            install = next(i for i, args in enumerate(events) if args[:2] == ('/usr/bin/pacman', '-U'))
            self.assertLess(timer, install)
            self.assertLess(events.index(('verify', 'rollback')), timer)
            self.assertEqual(state['phase'], 'ROLLED_BACK' if fail_health else 'CONFIRMED')
            self.assertEqual(installed[0], MODULE.OLD_PACKAGE if fail_health else MODULE.NEW_PACKAGE)
            self.assertEqual(os.readlink(web / 'current'), MODULE.OLD_TARGET if fail_health else target)
            self.assertFalse(any('lmm-api.service' in args for args in events))

    def test_confirmed_recovery_keeps_backend_running(self):
        self.exercise()

    def test_failed_public_acceptance_restores_package_and_original_frontend(self):
        self.exercise(fail_health=True)

    def test_frontend_drift_is_rejected_before_timer_or_install(self):
        self.exercise(drift=True)


if __name__ == '__main__':
    unittest.main()
