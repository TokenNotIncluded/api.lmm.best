#!/usr/bin/env python3
"""Three fake-owner/fake-clock cases; no host files, guardian or production IO."""
import importlib.util
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

SOURCE=Path(__file__).with_name('wait-ubuntu-financial-release.py')
spec=importlib.util.spec_from_file_location('ubuntu_wait',SOURCE)
wait=importlib.util.module_from_spec(spec); spec.loader.exec_module(wait)


class FakeClock:
    def __init__(self): self.now=0.0; self.sleeps=[]
    def clock(self): return self.now
    def sleep(self,seconds):
        if not 0 < seconds <= 0.2: raise AssertionError('unexpected poll interval')
        self.sleeps.append(seconds); self.now+=seconds


class FakeOwner:
    def __init__(self):
        self.events=[]; self.work=Path('/never/synthetic-owner-work')
        self.state={'phase':'MAINTENANCE_CONFIRMED','maintenance_admission_closed':True}
        self.maintenance={'stage':'post','transition_id':'synthetic-transition'}
        self.saved=[]
    def same(self,work,state,maintenance=None):
        assert work is self.work and state is self.state
        if maintenance is not None: assert maintenance is self.maintenance
    def original_reopen(self,work,state,maintenance):
        self.same(work,state,maintenance); self.events.append('reopen')
    def close_maintenance_admission(self,work,state,maintenance):
        self.same(work,state,maintenance); self.events.append('close')
        assert state['phase']=='MAINTENANCE_CONFIRMED'
        state['maintenance_admission_closed']=True
    def save(self,work,state):
        self.same(work,state); self.events.append('save'); self.saved.append(dict(state))


class UbuntuWaitTests(unittest.TestCase):
    def setUp(self):
        guard=patch('subprocess.run',side_effect=AssertionError('real process forbidden'))
        guard.start(); self.addCleanup(guard.stop)
        self.owner=FakeOwner(); self.clock=FakeClock()
    def call(self,check,evidence):
        return wait.wait_after_reopen(self.owner,self.owner.work,self.owner.state,self.owner.maintenance,
            clock=self.clock.clock,sleep=self.clock.sleep,check_baton=check,write_evidence=evidence)

    def test_legal_baton_waits_then_returns_without_creating_confirmation(self):
        answers=iter((False,False,True)); observed=[]
        def check():
            observed.append(self.owner.state['phase']); return next(answers)
        result=self.call(check,lambda:self.owner.events.append('evidence'))
        self.assertIsNone(result); self.assertEqual(self.owner.events,['reopen','evidence'])
        self.assertEqual(observed,['MAINTENANCE_CONFIRMED']*3)
        self.assertEqual(self.owner.state,{'phase':'MAINTENANCE_CONFIRMED','maintenance_admission_closed':True})
        self.assertEqual(self.owner.saved,[]); self.assertEqual(self.clock.sleeps,[0.2,0.2])

    def test_180_second_timeout_closes_saves_rollback_required_and_raises(self):
        with self.assertRaisesRegex(TimeoutError,'Arch-confirmation-baton-deadline'):
            self.call(lambda:False,lambda:self.owner.events.append('evidence'))
        self.assertEqual(wait.DEADLINE_SECONDS,180)
        self.assertAlmostEqual(self.clock.now,180,places=7)
        self.assertEqual(self.owner.events,['reopen','evidence','close','save'])
        self.assertEqual(self.owner.saved,[{'phase':'ROLLBACK_REQUIRED','maintenance_admission_closed':True}])
        self.assertNotIn('maintenance_admission_reopened',self.owner.state)

    def test_bad_baton_or_evidence_failure_closes_saves_and_preserves_exception(self):
        for failing in ('baton','evidence'):
            with self.subTest(failing=failing):
                self.owner=FakeOwner(); self.clock=FakeClock(); checks=[]
                def check():
                    checks.append(True)
                    if failing=='baton': raise RuntimeError('synthetic invalid baton')
                    return True
                def evidence():
                    self.owner.events.append('evidence')
                    if failing=='evidence': raise OSError('synthetic exclusive evidence refusal')
                with self.assertRaisesRegex(RuntimeError if failing=='baton' else OSError,
                        'synthetic invalid baton' if failing=='baton' else 'synthetic exclusive evidence refusal'):
                    self.call(check,evidence)
                self.assertEqual(self.owner.events,['reopen','evidence','close','save'])
                self.assertEqual(self.owner.saved,[{'phase':'ROLLBACK_REQUIRED','maintenance_admission_closed':True}])
                self.assertEqual(checks,[True] if failing=='baton' else [])
                self.assertEqual(self.clock.sleeps,[])


if __name__=='__main__': unittest.main()
