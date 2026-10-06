"""Offline failure injection: no SSH, service or database access."""
import importlib.util
import json
import os
import array
import fcntl
import socket
import signal
import stat
import subprocess
import sys
import time
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch, Mock

spec = importlib.util.spec_from_file_location('cluster', Path(__file__).with_name('deploy-shared-postgres.py'))
c = importlib.util.module_from_spec(spec)
spec.loader.exec_module(c)

GOOD = b'synthetic-full-dump'
DIGEST = __import__('hashlib').sha256(GOOD).hexdigest()
BASELINE = {'fingerprints': {'pricing': 'same', 'quota': 'same'}, 'tables': {'users': 1}}
RESTORED = {'actual_restore': True, 'candidate_and_all_n1_verified': True, 'fingerprints_unchanged': True}


def plan():
    nodes = [{'name': n, 'ssh': n, 'hostname': n, 'script': '/p/script.py', 'helper': '/p/helper.py',
              'plan': '/p/plan.json', 'candidate': '/p/candidate', 'provider_sha256': 'a'*64,
              'version': '1.2.3', 'invocation': 'b'*32, 'rollback': '/p/'+n,
              'probes': [{'url': 'http://127.0.0.1/closed', 'body_sha256': 'c'*64}]} for n in ('alpha', 'beta')]
    return {'format': 1, 'id': 'fixture-001', 'confirmation': 'fixture.example',
            'script_sha256': 'a'*64, 'helper_sha256': 'a'*64, 'source_sha': 'b'*40,
            'candidate': {'path': '/p/candidate', 'sha256': 'a'*64}, 'nodes': nodes,
            'database_owner': 'alpha', 'public_probes': nodes[0]['probes'],
            'database_backup': {'transport': 'local_peer', 'os_user': 'postgres', 'database_role': 'postgres',
                                'socket_directory': '/run/postgresql', 'port': 5432},
            'expected_units': {k: '1000' for k in c.PRICING_KEYS[:3]}, 'rehearsal_root': '/tmp/fixture-only'}


class Fake:
    def __init__(self, p, n, digest, work, events, failure, drift):
        self.node, self.events, self.failure, self.drift = n, events, failure, drift
        self.baselines = 0

    def call(self, action, data=None):
        self.events.append((self.node['name'], action))
        if (self.node['name'], action) == self.failure:
            raise c.GateFailed('injected-secret-must-not-be-printed')
        if action == 'preflight':
            value = {'identity': {'system': 'synthetic', 'oid': 1, 'schema': 'public'}}
            if self.node['name'] == 'alpha':
                value['backup_preflight'] = {'passed': True, 'full_schema_dump_exit': 0,
                                             'binding': {'identity': value['identity']}}
            return value
        if action == 'baseline':
            self.baselines += 1
            if self.drift == self.baselines:
                value = {'fingerprints': {'pricing': 'changed'}}
            else:
                value = {'fingerprints': BASELINE['fingerprints']}
            if self.node['name'] == 'alpha':
                value['tables'] = BASELINE['tables']
            return value
        if action == 'backup':
            return {'path': '/p/database.dump', 'sha256': DIGEST}
        return {'passed': True}

    def close(self):
        self.events.append((self.node['name'], 'channel-closed-lock-guardian-retained'))


class CoordinatorTests(unittest.TestCase):
    def execute(self, *, failure=None, restore=RESTORED, drift=None):
        events = []
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            old = root / 'native-state.json'
            old.write_bytes(b'immutable-existing-native-state')
            def connect(p, n, digest, work):
                return Fake(p, n, digest, work, events, failure, drift)
            def tool(args, **kw):
                self.assertEqual('scp', args[0])
                c.private_write(Path(args[-1]), GOOD)
                return b''
            def rehearse(*args):
                events.append(('clone', 'actual-restore-and-cli-checks'))
                if isinstance(restore, Exception):
                    raise restore
                return restore
            with patch.object(c, 'probe_all'), patch.object(c, 'command', tool):
                try:
                    result = c.coordinate(plan(), 'd'*64, root, connect, rehearse)
                    error = None
                except c.GateFailed as e:
                    result, error = False, e
            self.assertEqual(b'immutable-existing-native-state', old.read_bytes())
            records = [json.loads(v) for v in (root/'events.jsonl').read_text().splitlines()]
            self.assertNotIn('injected-secret', json.dumps(records))
            return result, events, records, error

    def test_success_order_and_normal_gate_boundary(self):
        ok, events, records, error = self.execute()
        self.assertTrue(ok)
        self.assertIsNone(error)
        self.assertLess(events.index(('beta', 'stop')), events.index(('alpha', 'backup')))
        self.assertLess(events.index(('clone', 'actual-restore-and-cli-checks')), events.index(('alpha', 'apply')))
        self.assertLess(events.index(('beta', 'verify')), events.index(('alpha', 'start')))
        self.assertEqual(1, events.count(('alpha', 'apply')))
        self.assertEqual('complete', records[-1]['event'])

    def test_each_pre_apply_gate_fail_stops_without_apply_or_restart(self):
        for failure in [('alpha','preflight'), ('beta','preflight'), ('alpha','stop'),
                        ('beta','stop'), ('alpha','check'), ('beta','check'),
                        ('alpha','baseline'), ('alpha','backup'), ('alpha','arm')]:
            with self.subTest(failure=failure):
                ok, events, records, error = self.execute(failure=failure)
                self.assertFalse(ok)
                self.assertFalse(any(a in ('apply','start','release') for _,a in events))
                self.assertEqual('recovery-required', records[-1]['event'])

    def test_actual_restore_or_any_clone_proof_failure_blocks_apply(self):
        for bad in [c.GateFailed('restore-failed'), {},
                    {**RESTORED,'actual_restore':False},
                    {**RESTORED,'candidate_and_all_n1_verified':False},
                    {**RESTORED,'fingerprints_unchanged':False}]:
            with self.subTest(bad=bad):
                ok, events, _, _ = self.execute(restore=bad)
                self.assertFalse(ok)
                self.assertFalse(any(a in ('apply','start','release') for _,a in events))

    def test_late_database_or_money_drift_never_restarts(self):
        for drift in (2,3):
            ok, events, _, _ = self.execute(drift=drift)
            self.assertFalse(ok)
            self.assertFalse(any(a in ('start','release') for _,a in events))
            if drift==2:
                self.assertNotIn(('alpha','apply'),events)

    def test_ambiguous_apply_or_n1_failure_never_replays_or_restarts(self):
        for failure in [('alpha','apply'), ('alpha','verify'), ('beta','verify')]:
            ok, events, _, _ = self.execute(failure=failure)
            self.assertFalse(ok)
            self.assertEqual(1, events.count(('alpha','apply')))
            self.assertFalse(any(a in ('start','release') for _,a in events))

    def test_restart_failure_keeps_guardians_and_ingress_closed(self):
        ok, events, _, _ = self.execute(failure=('beta','start'))
        self.assertFalse(ok)
        self.assertFalse(any(a=='release' for _,a in events))

    def test_missing_backup_preflight_or_nonowner_fullcounts_never_stops_or_applies(self):
        original = Fake.call
        for bad in ('preflight', 'baseline'):
            def altered(agent, action, data=None):
                value = original(agent, action, data)
                if bad == 'preflight' and action == 'preflight' and agent.node['name'] == 'alpha':
                    value.pop('backup_preflight')
                if bad == 'baseline' and action == 'baseline' and agent.node['name'] == 'beta':
                    value['tables'] = {}
                return value
            with self.subTest(bad=bad), patch.object(Fake, 'call', altered):
                ok, events, _, _ = self.execute()
                self.assertFalse(ok)
                self.assertFalse(any(a in ('apply', 'start', 'release') for _,a in events))
                if bad == 'preflight':
                    self.assertFalse(any(a == 'stop' for _,a in events))

    def test_owner_full_table_drift_after_backup_still_blocks_apply(self):
        original = Fake.call
        def altered(agent, action, data=None):
            value = original(agent, action, data)
            if agent.node['name'] == 'alpha' and action == 'baseline' and agent.baselines == 2:
                value['tables'] = {'users': 2}
            return value
        with patch.object(Fake, 'call', altered):
            ok, events, _, _ = self.execute()
        self.assertFalse(ok)
        self.assertNotIn(('alpha', 'apply'), events)


class PeerTests(unittest.TestCase):
    def peer(self, root):
        cfg = {**plan()['database_backup'], 'socket_directory': str(root)}
        identity = {'system': '123', 'oid': 42, 'schema': 'public', 'database': 'synthetic', 'version': '180006'}
        return c.PeerDatabase(cfg, identity, root)

    def test_explicit_environment_cannot_inherit_password_service_or_application_dsn(self):
        with tempfile.TemporaryDirectory() as d, patch.dict(os.environ, {
                'PGPASSWORD': 'secret-sentinel', 'PGSERVICE': 'unexpected-service',
                'SQL_DSN': 'postgresql://secret-sentinel', 'PGPASSFILE': '/secret-sentinel'}):
            peer = self.peer(Path(d))
            self.assertEqual(['runuser','-u','postgres','--','/usr/bin/env','-i'], peer.prefix[:6])
            self.assertEqual('/dev/null', peer.env['PGPASSFILE'])
            self.assertNotIn('secret-sentinel', ' '.join(peer.prefix))
            # Exercise the same explicit env-i boundary without changing OS UID.
            result = c.peer_output(peer.prefix[4:] + ['/usr/bin/env'], None, Path(d)/'environment.stderr', 10, capture=True)
            child = dict(v.split('=',1) for v in result.decode().splitlines())
            self.assertEqual(peer.env, child)
            self.assertIn('row_security=off', child['PGOPTIONS'])
            self.assertIn('default_transaction_read_only=on', child['PGOPTIONS'])

    def test_effective_peer_identity_and_role_must_match(self):
        with tempfile.TemporaryDirectory() as d:
            peer = self.peer(Path(d))
            role = {'current_user':'postgres','session_user':'postgres','superuser':True,'bypass_rls':True}
            with patch.object(c,'database_identity',return_value=peer.identity),patch.object(peer,'query',return_value=c.encode(role)):
                binding = peer.binding()
                self.assertEqual(peer.identity,binding['identity'])
                self.assertNotIn('superuser',binding['identity'])
                peer.verify(binding)
            for field in ('system','database','oid','schema','version'):
                changed={**peer.identity,field:'other'}
                with self.subTest(field=field),patch.object(c,'database_identity',return_value=changed):
                    with self.assertRaises(c.GateFailed):peer.binding()
            with patch.object(c,'database_identity',return_value=peer.identity),patch.object(peer,'query',return_value=c.encode({**role,'current_user':'other'})):
                with self.assertRaises(c.GateFailed):peer.binding()

    def test_catalog_count_and_full_schema_dump_are_real_pre_stop_gates(self):
        capabilities = dict.fromkeys(('missing_table_select','missing_schema_usage','missing_sequence_select',
                                     'rls_without_bypass','missing_large_object_select'),0)
        for failure in [*capabilities, 'counts', 'schema']:
            with self.subTest(failure=failure),tempfile.TemporaryDirectory() as d:
                peer = self.peer(Path(d));value={**capabilities}
                if failure in value:value[failure]=1
                with patch.object(peer,'verify'),patch.object(peer,'query',return_value=c.encode(value)),\
                     patch.object(c,'database_table_counts',side_effect=c.GateFailed('read-denied') if failure=='counts' else None,return_value={'public.users':1}),\
                     patch.object(c,'peer_output',side_effect=c.GateFailed('schema-denied') if failure=='schema' else None) as dump:
                    with self.assertRaises(c.GateFailed):peer.preflight({})
                    self.assertFalse((Path(d)/'backup-preflight.json').exists())
                    if failure!='schema':dump.assert_not_called()
        with tempfile.TemporaryDirectory() as d:
            peer = self.peer(Path(d))
            with patch.object(peer,'verify'),patch.object(peer,'query',return_value=c.encode(capabilities)),\
                 patch.object(c,'database_table_counts',return_value={'public.users':1}),patch.object(c,'peer_output') as dump:
                self.assertTrue(peer.preflight({})['passed'])
                args=dump.call_args.args
                self.assertIsNone(args[1])
                self.assertEqual(['pg_dump','--no-password','--schema-only'],args[0][-3:])

    def test_root_owned_exclusive_stdout_preserves_private_modes_and_bytes(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d);root.chmod(0o700)
            output,log=root/'database.dump',root/'dump.stderr'
            c.peer_output([sys.executable,'-c',"import sys;sys.stdout.buffer.write(b'exact-custom-payload')"],output,log,10)
            self.assertEqual(b'exact-custom-payload',output.read_bytes())
            self.assertEqual(0o600,stat.S_IMODE(output.stat().st_mode))
            self.assertEqual(0o600,stat.S_IMODE(log.stat().st_mode))
            self.assertEqual(0o700,stat.S_IMODE(root.stat().st_mode))
            self.assertEqual(1,output.stat().st_nlink)
            for name in ('existing','symlink'):
                path=root/name
                if name=='existing':path.write_bytes(b'untouched')
                else:path.symlink_to(output)
                with patch.object(c.subprocess,'Popen') as child,self.assertRaises(OSError):
                    c.peer_output(['not-started'],path,root/(name+'.stderr'),10)
                child.assert_not_called()
            self.assertEqual(b'untouched',(root/'existing').read_bytes())

    def test_partial_failure_timeout_and_invalid_list_never_seal_backup(self):
        for mode in ('failed','timeout','list'):
            with self.subTest(mode=mode),tempfile.TemporaryDirectory() as d:
                root=Path(d);peer=self.peer(root)
                code="import sys,time;sys.stdout.buffer.write(b'partial');sys.stdout.flush();"
                code += 'time.sleep(30)' if mode=='timeout' else ('sys.exit(2)' if mode=='failed' else '')
                peer.prefix=[sys.executable,'-c',code]
                agent=c.NodeAgent.__new__(c.NodeAgent);agent.p=plan();agent.n=agent.p['nodes'][0]
                agent.peer=peer;agent.backup_binding={};agent.db_env={};agent.guard=lambda *v:None;agent.dump_sha=None
                original=c.peer_output
                def short(args,output,log,timeout,*v,**kw):return original(args,output,log,0.1 if mode=='timeout' else timeout,*v,**kw)
                with patch.object(peer,'verify'),patch.object(c,'no_database_clients'),patch.object(c,'peer_output',side_effect=short),\
                     patch.object(c,'command',side_effect=c.GateFailed('invalid-custom-list') if mode=='list' else None) as listing:
                    with self.assertRaises(c.GateFailed):agent.action('backup',None)
                    self.assertIsNone(agent.dump_sha)
                    self.assertEqual(b'partial',(root/'database.dump').read_bytes())
                    if mode!='list':listing.assert_not_called()

    def test_peer_monetary_visibility_must_match_application_before_baseline(self):
        a=c.NodeAgent.__new__(c.NodeAgent);a.p=plan();a.n=a.p['nodes'][0];a.work=Path('/unused')
        a.db_env={};a.backup_binding={};a.guard=lambda *v:None;a.peer=Mock()
        a.peer.fingerprint.return_value={'quota':'filtered'}
        with patch.object(c,'no_database_clients'),patch.object(c,'fingerprints',return_value={'quota':'all'}):
            with self.assertRaises(c.GateFailed):a.action('baseline',None)
        a.peer.counts.assert_not_called()

    def test_preflight_does_not_read_money_before_all_writers_are_stopped(self):
        a=c.NodeAgent.__new__(c.NodeAgent);a.p=plan();a.n=a.p['nodes'][0]
        a.guard=lambda *v:None;a.identity={'database':'synthetic'};a.backup_binding={};a.peer=Mock()
        a.peer.preflight.return_value={'passed':True}
        with patch.object(c,'fingerprints',side_effect=AssertionError('inflight money is not stable yet')):
            self.assertEqual({'identity':a.identity,'backup_preflight':{'passed':True}},a.action('preflight',None))
        a.peer.preflight.assert_called_once_with({})
        a.peer.fingerprint.assert_not_called()

    def test_background_member_cannot_continue_writing_a_sealed_dump(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d);output=root/'database.dump'
            code="import os,time;pid=os.fork();" \
                 "\ntime.sleep(2) if pid==0 else None\n" \
                 "os.write(1,b'late-write') if pid==0 else None\n"
            with self.assertRaises(c.GateFailed):
                c.peer_output([sys.executable,'-c',code],output,root/'stderr',0.1)
            before=output.read_bytes()
            time.sleep(0.15)
            self.assertEqual(before,output.read_bytes())
            self.assertNotIn(b'late-write',before)

class PrimitiveTests(unittest.TestCase):
    def test_full_invocation_periodic_flush_is_not_shutdown_evidence(self):
        messages=['quota dashboard flush: persisted=9 failed=0 dropped=0',
                  'quota dashboard flush: persisted=8 failed=0 dropped=0',
                  'received signal: terminated',
                  'refund_tasks execution_complete=true accepted=2 finished=2 active=0 failed=0 (execution completion is not financial success)',
                  'quota dashboard flush: persisted=1 failed=0 dropped=0','server exited']
        def records(values):
            return [dict(MESSAGE=m,_PID='456',_SYSTEMD_INVOCATION_ID='e'*32,
                         _BOOT_ID='a'*32,__MONOTONIC_TIMESTAMP=str(100+i)) for i,m in enumerate(values)]
        def raw(rows):return b'\n'.join(c.encode(v) for v in rows)+b'\n'
        original=raw(records(messages))
        window,evidence=c.shutdown_window(original,'456','e'*32)
        self.assertNotIn(b'persisted=9',window)
        self.assertEqual(__import__('hashlib').sha256(original).hexdigest(),evidence['full_invocation_journal_sha256'])
        self.assertEqual(102,evidence['shutdown_start_monotonic_us'])
        coloured=records(messages)
        coloured[0]['MESSAGE']=list(b'\x1b[32mnon-financial runtime entry\x1b[0m')
        c.shutdown_window(raw(coloured),'456','e'*32)
        for bad_message in (None,[256],[-1],[True],[255]):
            rows=records(messages);rows[3]['MESSAGE']=bad_message
            with self.subTest(message=bad_message),self.assertRaises(c.GateFailed):c.shutdown_window(raw(rows),'456','e'*32)
        bads=[messages[:2]+messages[3:],messages[:-1],messages+[messages[2]],messages+[messages[-1]],
              messages[:4]+[messages[3]]+messages[4:],messages[:5]+[messages[4]]+messages[5:]]
        for bad in bads:
            with self.subTest(messages=bad),self.assertRaises(c.GateFailed):c.shutdown_window(raw(records(bad)),'456','e'*32)
        for field,value in [('_PID','789'),('_SYSTEMD_INVOCATION_ID','f'*32),('_BOOT_ID','b'*32),('__MONOTONIC_TIMESTAMP','1')]:
            rows=records(messages);rows[3][field]=value
            with self.subTest(field=field),self.assertRaises(c.GateFailed):c.shutdown_window(raw(rows),'456','e'*32)

    def test_real_fd_transfer_retains_all_locks_after_sender_eof(self):
        with tempfile.TemporaryDirectory() as d:
            paths = [str(Path(d)/str(i)) for i in range(3)]
            fds = [os.open(p,os.O_RDWR|os.O_CREAT,0o600) for p in paths]
            for fd in fds:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
            sender,receiver=socket.socketpair()
            pid=os.fork()
            if pid==0:
                try:
                    sender.close()
                    for fd in fds:os.close(fd)
                    received=c.receive_guardian_locks(receiver,paths)
                    receiver.close()
                    while received:signal.pause()
                finally:os._exit(1)
            receiver.close()
            try:
                sender.sendmsg([b'LOCKS'],[(socket.SOL_SOCKET,socket.SCM_RIGHTS,array.array('i',fds).tobytes())])
                sender.settimeout(5)
                self.assertEqual(b'HELD',sender.recv(32))
                sender.close()
                for fd in fds:os.close(fd)
                fds=[]
                for p in paths:
                    fd=os.open(p,os.O_RDWR)
                    try:
                        with self.assertRaises(BlockingIOError):fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
                    finally:os.close(fd)
            finally:
                os.kill(pid,signal.SIGTERM);os.waitpid(pid,0)
                sender.close()
                for fd in fds:os.close(fd)
            for p in paths:
                fd=os.open(p,os.O_RDWR)
                try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
                finally:os.close(fd)

    def test_partial_rpc_line_has_real_deadline(self):
        r,w=os.pipe()
        try:
            os.write(w,b'{"ok":true')
            with self.assertRaises(c.GateFailed):c.response_line(r,0.02)
        finally:os.close(r);os.close(w)

    def test_plan_or_helper_drift_blocks_apply_before_any_tool(self):
        a=c.NodeAgent.__new__(c.NodeAgent);a.p=plan();a.n=a.p['nodes'][0]
        a.plan_digest='d'*64;a.armed=True;a.dispatched=False
        for changed in (a.n['plan'],a.n['script'],a.n['helper']):
            with self.subTest(changed=changed),patch.object(c,'regular',side_effect=lambda path,*v:c.require(path!=changed,'changed')),patch.object(c,'command') as tool:
                with self.assertRaises(c.GateFailed):a.action('apply',None)
                tool.assert_not_called()
                self.assertFalse(a.dispatched)

    def test_recovery_journal_cannot_reuse_pid_only_shutdown(self):
        u={'ExecMainPID':'456','InvocationID':'e'*32}
        old='received signal: terminated\nrefund_tasks execution_complete=true accepted=0 finished=0 active=0 failed=0 (execution completion is not financial success)\nquota dashboard flush: persisted=0 failed=0 dropped=0\nserver exited'
        def journal(args,**kw):
            return b'server exited' if '_SYSTEMD_INVOCATION_ID='+'e'*32 in args else old.encode()
        with patch.object(c,'unit',return_value=u),patch.object(c,'stopped'),patch.object(c,'command',side_effect=journal) as tool:
            with self.assertRaises(c.GateFailed):c.recovery_stopped({})
            self.assertIn('_PID=456',tool.call_args.args[0])

    def test_recovery_verifies_every_node_before_restart_and_retains_lock_on_failure(self):
        for failed in ('verify','start'):
            calls=[]
            def ssh(args,**kw):
                name=args[args.index('python3')-1];phase=args[args.index('--phase')+1]
                calls.append((name,phase))
                if name=='beta' and phase==failed:raise c.GateFailed('failure')
                if phase=='verify':return c.encode({'verified':True,'rollback_verified':True,'identity':{'database':'fixture'},'fingerprints':{'same':True}})
                return c.encode({'old_writer_ready':True} if phase=='start' else {'maintenance_locks_released':True})
            with tempfile.TemporaryDirectory() as d,patch.object(c,'probe_all'),patch.object(c,'command',side_effect=ssh):
                with self.assertRaises(c.GateFailed):c.recover(plan(),'d'*64,Path(d),'attempt-001')
            self.assertFalse(any(phase=='release' for _,phase in calls))
            if failed=='verify':self.assertFalse(any(phase=='start' for _,phase in calls))

    def test_plan_rejects_credentials_unknown_fields_and_unsafe_paths(self):
        for change in ({'password':'secret'}, {'candidate':{'path':'/p/../secret','sha256':'a'*64}},
                       {'nodes':plan()['nodes'][:1]}, {'expected_units':{}}):
            p=plan();p.update(change)
            with self.assertRaises(c.GateFailed):c.validate_plan(p)
        self.assertEqual(plan(),c.validate_plan(plan()))

    def test_correct_price_policy_and_token_fields_are_preserved(self):
        self.assertIn('billing_setting.billing_mode',c.PRICING_KEYS)
        self.assertIn('billing_setting.billing_expr',c.PRICING_KEYS)
        self.assertIn('GroupRatio',c.PRICING_KEYS)
        self.assertIn('ModerationEnabled',c.PRICING_KEYS)
        self.assertIn('violation_fee.enabled',c.PRICING_KEYS)
        with patch.object(c,'psql',side_effect=[c.encode({k:'1000' for k in c.PRICING_KEYS[:3]}),b'one',b'two']) as query:
            c.fingerprints({},plan()['expected_units'])
        self.assertIn('remain_quota',query.call_args_list[-1].args[1])
        self.assertIn('used_quota',query.call_args_list[-1].args[1])

    def test_shutdown_requires_refunds_flush_and_clean_completion(self):
        good='received signal: terminated\nrefund_tasks execution_complete=true accepted=2 finished=2 active=0 failed=0 (execution completion is not financial success)\nquota dashboard flush: persisted=1 failed=0 dropped=0\nserver exited'
        c.validate_shutdown(good)
        # Normal native accepts no flush report when the dashboard batch is
        # empty. No synthetic persisted=0 report is added to the evidence.
        empty=good.replace('quota dashboard flush: persisted=1 failed=0 dropped=0\n','')
        c.validate_shutdown(empty)
        for bad in [good.replace('finished=2','finished=1'),good.replace('dropped=0','dropped=1'),
                    good.replace('server exited',''),good+'\n'+good,
                    good+'\nbatch update started',good+'\npanic',
                    good.replace('failed=0 dropped=0','failed=1 dropped=0'),
                    good+'\nquota dashboard flush: persisted=2 failed=0 dropped=0',
                    empty.replace('refund_tasks execution_complete=true accepted=2 finished=2 active=0 failed=0 (execution completion is not financial success)\n','')]:
            with self.assertRaises(c.GateFailed):c.validate_shutdown(bad)

    def test_apply_dispatch_is_irreversible_even_after_failed_tool(self):
        n=c.NodeAgent.__new__(c.NodeAgent);n.p=plan();n.n=n.p['nodes'][0]
        n.did_stop=True;n.dump_sha=DIGEST;n.armed=False;n.dispatched=False
        n.guard=lambda *a:None;n.migrate=Mock(side_effect=c.GateFailed('ambiguous-tool-failure'));n.work=Path('/unused')
        data={'dump_sha256':DIGEST,'candidate_sha256':n.p['candidate']['sha256'],'rehearsal_passed':True}
        with patch.object(c,'regular'):
            n.action('arm',data)
            with self.assertRaises(c.GateFailed):n.action('apply',None)
            self.assertTrue(n.dispatched);self.assertFalse(n.armed)
            with self.assertRaises(c.GateFailed):n.action('arm',data)
            with self.assertRaises(c.GateFailed):n.action('apply',None)
            self.assertEqual(1,n.migrate.call_count)

    def test_actual_migration_environment_mismatch_prevents_exec(self):
        p=plan();p['nodes'][0]['candidate']='/p/candidate'
        helper=type('H',(),{'database_environment_from_values':lambda self,v:{}})()
        with tempfile.TemporaryDirectory() as d:
            f=Path(d)/'identity';f.write_text('{"database":"A"}')
            with patch.object(c,'regular'),patch.object(c,'module',return_value=helper),\
                 patch.dict(os.environ,{'NODE_TYPE':'master','SQL_DSN':'postgresql://secret-sentinel'}),\
                 patch.object(c,'database_identity',return_value={'database':'B'}),\
                 patch.object(c.os,'execv') as execute:
                with self.assertRaises(c.GateFailed):c.bound_migrate(p,'alpha',f,'/p/candidate','apply')
                execute.assert_not_called()

    def test_normal_subcommand_sets_master_after_environment_files(self):
        a=c.NodeAgent.__new__(c.NodeAgent);a.p=plan();a.n=a.p['nodes'][0]
        a.work=Path('/p/work');a.env_files=['/p/normal.env'];a.db_env={};a.guard=lambda *v:None;a.plan_digest='a'*64
        with patch.object(c,'no_database_clients'),patch.object(c,'sha',return_value='a'*64),patch.object(c,'command') as run:
            a.migrate('/p/candidate','apply','candidate-apply')
        args=run.call_args.args[0]
        self.assertGreater(args.index('NODE_TYPE=master'),args.index('EnvironmentFile=/p/normal.env'))
        self.assertIn('_bound-migrate',args)


if __name__=='__main__':unittest.main()
