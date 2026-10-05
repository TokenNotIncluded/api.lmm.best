"""Offline failure injection: no SSH, service or database access."""
import importlib.util
import json
import os
import array
import fcntl
import socket
import signal
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
            return {'identity': {'system': 'synthetic', 'oid': 1, 'schema': 'public'}}
        if action == 'baseline':
            self.baselines += 1
            if self.drift == self.baselines:
                return {'fingerprints': {'pricing': 'changed'}, 'tables': {'users': 1}}
            return BASELINE
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

class PrimitiveTests(unittest.TestCase):
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
        for bad in [good.replace('finished=2','finished=1'),good.replace('dropped=0','dropped=1'),
                    good.replace('server exited',''),good+'\n'+good,
                    good+'\nbatch update started',good+'\npanic']:
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
