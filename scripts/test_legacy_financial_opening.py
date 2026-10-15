"""Development checks for the bounded adapter; fresh-PG acceptance is separate."""
import copy
from decimal import Decimal
import importlib.util
import io
from pathlib import Path
import unittest
from unittest.mock import patch

import test_migrate_legacy_financial as facts

F = facts.MODULE
SPEC = importlib.util.spec_from_file_location("opening_adapter",Path(__file__).with_name("legacy_financial_opening.py"))
O = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(O)


def payload(a=19,f=3,aid=1):
    return {"account_id":aid,"affiliate_units":f,"available_units":a,"mapping_sha256":"b"*64,
            "migration_id":"synthetic-opening","observed_at":1791740633,"reserved_units":0,
            "source_archive_sha256":"a"*64,"source_entity_key":"legacy.users/"+str(aid),"source_row_sha256":"c"*64}


class OpeningProjectionTests(unittest.TestCase):
    def test_debt_and_signed_affiliate_remain_separate(self):
        result=O.expected_readback(F,[payload(-19,-3)])
        self.assertEqual(result['positions'][0]['balances'],[
            {'bucket':'affiliate','balance_units':-3,'revision':1},
            {'bucket':'debt','balance_units':-19,'revision':1},
            {'bucket':'reserved','balance_units':0,'revision':0},
            {'bucket':'wallet','balance_units':0,'revision':0}])
        self.assertEqual(result['clearing'],{'balance_units':22,'revision':1})
        self.assertEqual(result['positions'][0]['journal']['amount_units'],22)

    def test_zero_net_inputs_still_have_gross_journal_but_no_clearing_leg(self):
        result=O.expected_readback(F,[payload(19,-19)])
        j=result['positions'][0]['journal']
        self.assertEqual(j['amount_units'],38)
        self.assertEqual({e['bucket'] for e in j['entries']},{'wallet','affiliate'})
        self.assertEqual(result['clearing'],{'balance_units':0,'revision':0})
        self.assertIsNone(O.expected_readback(F,[payload(0,0)])['positions'][0]['journal'])

    def test_clearing_records_each_intermediate_balance_and_revision(self):
        result=O.expected_readback(F,[payload(19,3),payload(-10,-2,2),payload(7,-7,3)])
        legs=[e for p in result['positions'] for e in (p['journal'] or {}).get('entries',[]) if e['bucket']=='clearing']
        self.assertEqual([(e['delta_units'],e['balance_after_units'],e['balance_revision']) for e in legs],[(-22,-22,1),(12,-10,2)])
        self.assertEqual(result['clearing'],{'balance_units':-10,'revision':2})

    def test_noninteger_and_nonzero_reservations_are_rejected(self):
        for value in (True,None,1,'0',0.0):
            p=payload();p['reserved_units']=value
            with self.subTest(value=value),self.assertRaises(F.MappingError):
                O.expected_readback(F,[p])

    def test_typed_projection_hash_binds_original_row_and_operation_id(self):
        baseline=payload()
        for changes in ({'source_row_sha256':'d'*64},{'migration_id':'another-opening'},{'affiliate_units':4}):
            changed={**baseline,**changes}
            self.assertNotEqual(F.digest(O.expected_readback(F,[baseline])),F.digest(O.expected_readback(F,[changed])))


class OpeningReviewTests(unittest.TestCase):
    def setUp(self):
        self.plan=F.plan_financial(facts.tables(),{71:1},source_sha256='a'*64)
        self.proof={'source_export_sha256':'c'*64,'selected_table_counts':{'users':1,'options':0},
                    'opening_table_rows_sha256':{'users':'d'*64,'options':'e'*64}}
        self.evidence={'format_version':1,'scope':'opening-reservation-evidence','source_archive_sha256':'a'*64,
            'mapping_sha256':self.plan['mapping_sha256'],'source_export_sha256':'c'*64,
            'exported_table_counts':self.proof['selected_table_counts'],
            'positions':[{'source_user_id':71,'reserved_units':0}],
            'table_assessments':[{'table':t,'source_rows_sha256':h,'opening_holds':'proven-none',
                                 'review_basis':'Synthetic full row review; no holds present.'} for t,h in self.proof['opening_table_rows_sha256'].items()]}
        self.review={'format_version':1,'scope':'opening-economic-policy-review','status':'independently-reviewed-zero-reserved',
            'source_archive_sha256':'a'*64,'mapping_sha256':self.plan['mapping_sha256'],
            'reservation_evidence_file_sha256':'f'*64,'excluded_families':O.EXCLUDED,
            'reviewer':'independent-synthetic-reviewer','review_basis':'Synthetic source row and hold semantics explicitly reviewed.'}

    def validate(self,e=None,r=None):
        return O.validate_review(F,self.plan,self.proof,self.evidence if e is None else e,self.review if r is None else r,evidence_sha256='f'*64)

    def test_actual_bound_complete_review_is_accepted_without_inventing_history(self):
        result=self.validate()
        self.assertEqual(result['positions'],[{'source_user_id':71,'reserved_units':0}])

    def test_well_formed_hash_alone_does_not_supply_table_evidence(self):
        e=copy.deepcopy(self.evidence);e['table_assessments']=[]
        with self.assertRaisesRegex(F.MappingError,'reservation_table_review_incomplete'):self.validate(e=e)

    def test_changed_original_rows_missing_coverage_and_unknown_rights_fail(self):
        for change in ({'source_rows_sha256':'0'*64},{'opening_holds':'unknown'},{'review_basis':'yes'}):
            e=copy.deepcopy(self.evidence);e['table_assessments'][0].update(change)
            with self.subTest(change=change),self.assertRaises(F.MappingError):self.validate(e=e)
        e=copy.deepcopy(self.evidence);e['exported_table_counts']={'users':1}
        with self.assertRaises(F.MappingError):self.validate(e=e)

    def test_wrong_review_file_hash_or_source_or_unreviewed_status_fail(self):
        for change in ({'reservation_evidence_file_sha256':'0'*64},{'source_archive_sha256':'0'*64},{'status':'pending'},{'format_version':True}):
            r={**self.review,**change}
            with self.subTest(change=change),self.assertRaises(F.MappingError):self.validate(r=r)

    def test_both_count_sets_require_nonnegative_integers_and_complete_keys(self):
        for side in ('evidence','proof'):
            for value in (False,True,Decimal('0.0'),0.0,'0',-1,None):
                evidence,proof=copy.deepcopy(self.evidence),copy.deepcopy(self.proof)
                counts=evidence['exported_table_counts'] if side=='evidence' else proof['selected_table_counts']
                counts['options']=value
                with self.subTest(side=side,value=value),self.assertRaises(F.MappingError):
                    O.validate_review(F,self.plan,proof,evidence,self.review,evidence_sha256='f'*64)
        for counts in ({'users':1},{'users':1,'options':0,'extra_table':0}):
            evidence=copy.deepcopy(self.evidence);evidence['exported_table_counts']=counts
            with self.subTest(counts=counts),self.assertRaises(F.MappingError):self.validate(e=evidence)


class OpeningTypedContractTests(unittest.TestCase):
    def setUp(self):
        self.contract={'identity_version':4,'identity_fingerprint_sha256':'a'*64,
                       'component_fingerprints':{'identity':'a'*64},'catalog_query_sha256':'b'*64,
                       'oauth_catalog_query_sha256':'d'*64}
        self.target={'status':'initialized-identity-only-opening-target','application_or_worker_started':False,
                     'exclusive_offline_resource_lease_required':True,
                     'connection':{'role':'opening_o','login_role':'opening_i','set_role':'opening_o','database':'opening_db','port':5432},
                     'instance_identity':{'role':'opening_o','session_user':'opening_i','database':'opening_db','database_owner':'opening_o',
                       'effective_role_can_login':False,'database_oid':123,'port':5432,'cluster_system_identifier':'123456',
                       'server_version_num':'180006','postmaster_started_at':'2026-10-15T00:00:00+00:00','server_instance_nonce':'synthetic-private-marker'},
                     'contract':{**self.contract,'catalog_fingerprint_sha256':'c'*64},
                     'identity_import':{'migration_id':'synthetic-identity'},'runtime_role':'opening_r'}

    def test_current_integer_version_accepted_but_numerically_equal_other_types_rejected(self):
        self.assertEqual(O.target_contract(F,self.target,self.contract)['database_oid'],123)
        for value in (Decimal('4.0'),4.0,'4',True,None):
            target=copy.deepcopy(self.target);target['contract']['identity_version']=value
            with self.subTest(value=value),self.assertRaises(F.MappingError):O.target_contract(F,target,self.contract)

    def test_instance_version_allows_pg_setting_text_but_never_numeric_coercion(self):
        target=copy.deepcopy(self.target);target['instance_identity']['server_version_num']=180006
        O.target_contract(F,target,self.contract)
        for value in (Decimal('180006.0'),180006.0,True,'0180006','180006.0'):
            target=copy.deepcopy(self.target);target['instance_identity']['server_version_num']=value
            with self.subTest(value=value),self.assertRaises(F.MappingError):O.target_contract(F,target,self.contract)

    def test_old_or_changed_oauth_catalog_contract_is_rejected(self):
        for value in (None, 'e'*64):
            target=copy.deepcopy(self.target);target['contract']['oauth_catalog_query_sha256']=value
            with self.subTest(value=value),self.assertRaises(F.MappingError):O.target_contract(F,target,self.contract)

    def test_typed_approval_comparison_preserves_nested_integer_truth(self):
        approved={'target':{'identity_version':4},'table_counts':{'empty':0},'reviewed':True}
        self.assertTrue(O.typed_equal(F,approved,copy.deepcopy(approved)))
        for changed in ({'target':{'identity_version':Decimal('4.0')},'table_counts':{'empty':0},'reviewed':True},
                        {'target':{'identity_version':4},'table_counts':{'empty':False},'reviewed':True},
                        {'target':{'identity_version':4},'table_counts':{'empty':0},'reviewed':1}):
            self.assertEqual(approved,changed)  # The original Python equality hazard.
            self.assertFalse(O.typed_equal(F,approved,changed))

    def test_snapshot_table_counts_and_sequence_counts_are_strict_integers(self):
        snapshot={'core_console.options':{'rows':0,'sha256':'d'*64},'sequence-state':{
            name:{'owner':'opening_o','definition':{'seqincrement':1,'seqcache':1,'seqmin':1,'seqmax':F.BIGINT_MAX,'seqcycle':False},
                  'last_value':1,'is_called':False} for name in O.TOUCHED}}
        O.validate_sequences(F,snapshot,owner='opening_o')
        for value in (False,Decimal('0.0'),0.0,'0',-1,None):
            changed=copy.deepcopy(snapshot);changed['core_console.options']['rows']=value
            with self.subTest(table_rows=value),self.assertRaises(F.MappingError):O.validate_sequences(F,changed,owner='opening_o')
        for value in (True,Decimal('1.0'),1.0,'1'):
            changed=copy.deepcopy(snapshot);changed['sequence-state'][sorted(O.TOUCHED)[0]]['definition']['seqcache']=value
            with self.subTest(seqcache=value),self.assertRaises(F.MappingError):O.validate_sequences(F,changed,owner='opening_o')


class OpeningEntryTests(unittest.TestCase):
    def test_incomplete_opening_input_refused_before_source_or_output(self):
        argv=['financial','--source-receipt','/absent','--identity-map','/absent','--identity-manifest','/absent',
              '--output','/must-not-write','--migration-id','synthetic','--apply-opening']
        err=io.StringIO()
        with patch('sys.argv',argv),patch('sys.stderr',err),patch.object(F.IDENTITY,'load_receipt') as read:
            self.assertEqual(F.main(),1)
        read.assert_not_called()
        self.assertIn('opening_frozen_reviewed_inputs_required',err.getvalue())

    def test_unresolved_original_attempt_never_reports_no_target_writes(self):
        argv=['financial','--source-receipt','/absent','--identity-map','/absent','--identity-manifest','/absent',
              '--output','/must-not-write','--migration-id','synthetic','--reconcile-opening',
              '--opening-attempt','/private/original/opening-attempt.private.json']
        err=io.StringIO()
        with patch('sys.argv',argv),patch('sys.stderr',err):
            self.assertEqual(F.main(),1)
        failure=F.parse_json(err.getvalue())
        self.assertEqual(failure['status'],'opening-outcome-unknown')
        self.assertFalse(failure['automatic_retry'])
        self.assertEqual(failure['original_attempt'],'/private/original/opening-attempt.private.json')


if __name__=='__main__':
    unittest.main()
