"""Synthetic author contracts only. No Core, PostgreSQL, network or compiler.

FD execution uses the already-installed Python ELF to prove Linux plumbing.
DTO fixtures test parsing/binding, never substitute for real check-db acceptance.
"""
from contextlib import ExitStack
from pathlib import Path
import copy,hashlib,importlib.util,json,os,shutil,subprocess,sys,tempfile,unittest
from unittest.mock import patch

SOURCE=Path(__file__).resolve().parents[1]/'scripts/core-ops.py'
spec=importlib.util.spec_from_file_location('core_ops',SOURCE);ops=importlib.util.module_from_spec(spec);sys.modules[spec.name]=ops;spec.loader.exec_module(ops)

class Contracts(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(prefix='core-ops-unit-',dir=os.environ['TMPDIR']);self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name);self.root.chmod(0o700);self.install=self.root/'install';self.install.mkdir(mode=0o755)
        self.journal_dir=self.root/'journal';self.journal_dir.mkdir(mode=0o700)
        self.operation='core-ops-'+'a'*32;machine,boot=ops.host_identity();self.boot=boot
        self.incarnation={'name':'synthetic','oid':'12345','server_address':None,'server_port':None,
                          'postmaster_started_at_unix_micros':'1','session_user':'synthetic_runtime','current_user':'synthetic_runtime','server_version_num':180006}
        compiled={'format_version':1,'scope':'compiled-core-schema-contract','components':[{'name':'identity','raw_schema_sha256':'1'*64}],
                  'identity':{'version':4,'fingerprint_sha256':'1'*64},'installer_contract_sql_sha256':'2'*64,
                  'catalog':{'query_utf8':'SELECT synthetic','query_sha256':ops.digest(b'SELECT synthetic'),'query_hash_domain':'sha256-raw-utf8-v1','fingerprint_hash_domain':'sha256-u64be-byte-length-prefixed-utf8-v1','definition_order':'postgresql-c-collation'}}
        schema=self.write(self.install/'schema.json',compiled,mode=0o644)
        receipt=self.write(self.install/'build.json',{'scope':'synthetic unit only'},mode=0o644)
        source=self.write(self.install/'source.json',{'scope':'synthetic unit only'},mode=0o644)
        contract=self.write(self.install/'contract.md',b'Synthetic contract reference only\n',mode=0o644)
        self.config=self.write(self.root/'runtime.toml',b'version=1\n# Synthetic only; never passed to Core\n')
        binary=Path(sys.executable).resolve();binary_sha=hashlib.sha256(binary.read_bytes()).hexdigest()
        physical={'version':1,'scope':'operator_record_only_not_live_physical_proof','installation_id':'synthetic',
                  'machine_id_sha256':machine,'boot_id':boot,'postgres_system_identifier':'123456789','pgdata':'/synthetic/unopened/pgdata',
                  'pg_pid':12345,'pg_start_ticks':'12345','pg_executable_sha256':'3'*64,'nonce':'4'*32,'public_incarnation':self.incarnation}
        physical_path=self.write(self.root/'physical.json',physical)
        self.input={'version':1,'scope':ops.SCOPE,'operation_id':self.operation,'installation_id':'synthetic',
                    'host':{'machine_id_sha256':machine,'expected_uid':os.getuid(),'boot_id_at_plan':boot},
                    'core':{'binary_file':str(binary),'binary_sha256':binary_sha,'source_manifest':self.ref(source),
                            'build_receipt':self.ref(receipt),'schema_manifest':self.ref(schema),'check_db_contract':self.ref(contract)},
                    'database':{'runtime_config_file':str(self.config),'runtime_config_sha256':ops.digest(self.config.read_bytes()),
                                'expected_public_incarnation':self.incarnation,'expected_catalog_sha256':'5'*64,
                                'expected_required_capabilities':{k:False for k in ops.CAPABILITIES}},
                    'physical_binding':self.ref(physical_path),'journal':{'directory':str(self.journal_dir),
                      'file':str(self.journal_dir/(self.operation+'.journal.json')),'envelope_version':1}}
        self.spec_path=self.write(self.root/'spec.json',self.input)

    def write(self,path,value,mode=0o600):
        path.write_bytes(value if type(value) is bytes else ops.canonical(value));path.chmod(mode);return path
    def ref(self,path):return {'file':str(path),'sha256':ops.digest(path.read_bytes())}
    def plan(self):
        self.write(self.spec_path,self.input);path=self.root/'plan.json';response=ops.write_plan(str(self.spec_path),str(path))
        return path,response['plan_sha256'],json.loads(path.read_bytes())
    def journal(self,plan,sha,state='SUCCEEDED'):
        ticks=Path('/proc/self/stat').read_text().rsplit(')',1)[1].split()[19]
        value={'version':1,'operation_id':self.operation,'plan_sha256':sha,'generation':1,'state':state,
               'holder':{'boot_id':self.boot,'pid':os.getpid(),'start_ticks':ticks},
               'installation_identity':{'installation_id':plan['installation_id'],'machine_id_sha256':plan['host']['machine_id_sha256'],
                                        'public_incarnation_sha256':ops.digest(ops.canonical(self.incarnation))}}
        path=self.write(Path(plan['journal']['file']),value);return path,value
    def parent_observation(self,inputs):
        ticks=Path('/proc/self/stat').read_text().rsplit(')',1)[1].split()[19]
        return ops.ObservedExecution(os.getpid(),self.boot,ticks,inputs.binary.sha256)
    def dto(self,inputs):
        value={'version':1,'protocol':ops.PROTOCOL,'status':'validated','scope':'runtime_configured_capabilities',
                'compiled_schema_manifest_sha256':inputs.schema.sha256,'compiled_schema_manifest_hash_domain':'sha256-exact-schema-manifest-stdout-v1',
                'components':inputs.schema.json()['components'],'catalog':{'status':'validated','fingerprint_sha256':'5'*64,'fingerprint_hash_domain':'sha256-u64be-byte-length-prefixed-utf8-v1','matches_installed_receipt':True},
                'runtime_authority':{'status':'validated','required_capabilities':self.input['database']['expected_required_capabilities']},
                'database':{**self.incarnation,'observed_at_unix_micros':'2'},
                'observation':{'transaction_read_only':True,'transaction_isolation':'repeatable_read','snapshot':'single_repeatable_read_transaction'},
                'system_identifier':{'status':'not_observed','value':None},
                'execution':{'pid':os.getpid(),'boot_id':self.boot,'start_ticks':self.parent_observation(inputs).start_ticks,'executable_sha256':inputs.binary.sha256},
                'not_checked':ops.NOT_CHECKED}
        return copy.deepcopy(value)

    def test_plan_allows_public_installed_manifest_and_root_owned_elf(self):
        path,sha,plan=self.plan();self.assertEqual(path.stat().st_mode&0o777,0o600)
        self.assertEqual(ops.digest(path.read_bytes()),sha);self.assertEqual(plan['scope'],ops.SCOPE)
        self.assertFalse(Path(plan['journal']['file']).exists())
    def test_plan_never_overwrites_or_creates_journal(self):
        path,sha,plan=self.plan();before=path.read_bytes()
        with self.assertRaises(FileExistsError):ops.write_plan(str(self.spec_path),str(path))
        self.assertEqual(path.read_bytes(),before);self.assertFalse(Path(plan['journal']['file']).exists())
    def test_output_outside_backups_rejected_before_any_creation(self):
        outside='/not-allowed-core-ops-plan.json'
        with self.assertRaisesRegex(ops.Refused,'OUTPUT_ONLY_BACKUPS'):ops.write_plan(str(self.spec_path),outside)
        self.assertFalse(Path(outside).exists())
    def test_public_runtime_config_rejected(self):
        self.config.chmod(0o644)
        with self.assertRaisesRegex(ops.Refused,'PRIVATE_OWNER_MODE_REQUIRED'):self.plan()
    def test_public_spec_rejected(self):
        self.spec_path.chmod(0o644)
        with self.assertRaisesRegex(ops.Refused,'PRIVATE_OWNER_MODE_REQUIRED'):ops.write_plan(str(self.spec_path),str(self.root/'plan.json'))
    def test_public_private_parent_rejected(self):
        self.root.chmod(0o755)
        with self.assertRaisesRegex(ops.Refused,'PRIVATE_PARENT_REQUIRED'):self.plan()
    def test_group_writable_installed_manifest_rejected(self):
        Path(self.input['core']['schema_manifest']['file']).chmod(0o664)
        with self.assertRaisesRegex(ops.Refused,'TRUSTED_INSTALL_OWNER_MODE_REQUIRED'):self.plan()
    def test_symlink_leaf_rejected(self):
        target=self.root/'link';target.symlink_to(self.spec_path)
        with self.assertRaises(OSError):ops.Opened.open(str(target),private=True)
    def test_symlink_ancestor_rejected(self):
        target=self.root/'linked-install';target.symlink_to(self.install,target_is_directory=True)
        with self.assertRaises(OSError):ops.Opened.open(str(target/'schema.json'))
    def test_dot_relative_and_control_paths_rejected(self):
        for value in ['relative','/a/../b','/a/./b','/a//b','/a/secret\nfile','/a/C1\u0085file','/a/C1\u009ffile','/']:
            with self.subTest(value=value):
                with self.assertRaises(ops.Refused):ops.path_value(value)
    def test_regular_fifo_rejected_without_blocking(self):
        path=self.root/'fifo';os.mkfifo(path,0o600)
        with self.assertRaisesRegex(ops.Refused,'REGULAR_FILE_REQUIRED'):ops.Opened.open(str(path),private=True)
    def test_duplicate_unknown_and_nonfinite_json_rejected(self):
        for raw in [b'{"x":1,"x":2}',b'{"x":NaN}',b'"\xed\xa0\x80"']:
            with self.subTest(raw=raw):
                with self.assertRaises(ops.Refused):ops.decode(raw)
        bad={**self.input,'override':'unsafe'}
        with self.assertRaisesRegex(ops.Refused,'CLOSED_FIELDS_REQUIRED'):ops.validate_spec(bad)
    def test_boolean_integer_and_noncanonical_numbers_rejected(self):
        for value in ['01','-0','1e2',1,True]:
            with self.subTest(value=value):
                with self.assertRaises(ops.Refused):ops.decimal(value,-100,100)
        with self.assertRaises(ops.Refused):ops.uint(True)
        self.assertEqual(ops.decimal('-9223372036854775808',-(1<<63),(1<<63)-1),'-9223372036854775808')
    def test_signed_times_and_u32_oid_boundaries(self):
        for oid in ['1','4294967295']:
            value={**self.incarnation,'oid':oid,'postmaster_started_at_unix_micros':'-1'};ops.incarnation(value)
        for oid in ['0','4294967296','01']:
            with self.assertRaises(ops.Refused):ops.incarnation({**self.incarnation,'oid':oid})
    def test_endpoint_half_or_invalid_ip_rejected(self):
        for address,port in [(None,5432),('127.0.0.1',None),('127.0.0.1/garbage',5432),('https://host',5432)]:
            with self.subTest(address=address,port=port):
                with self.assertRaises(ops.Refused):ops.incarnation({**self.incarnation,'server_address':address,'server_port':port})
    def test_missing_physical_identity_rejected(self):
        p=Path(self.input['physical_binding']['file']);value=json.loads(p.read_bytes());value.pop('postgres_system_identifier');self.write(p,value)
        self.input['physical_binding']=self.ref(p)
        with self.assertRaisesRegex(ops.Refused,'CLOSED_FIELDS_REQUIRED'):self.plan()
    def test_boot_or_machine_mismatch_blocks(self):
        self.input['host']['boot_id_at_plan']='00000000-0000-0000-0000-000000000001'
        with self.assertRaisesRegex(ops.Refused,'BOOT_ID_CHANGED'):self.plan()
    def test_held_fd_detects_same_inode_rewrite(self):
        with ops.Opened.open(str(self.config),private=True) as held:
            self.write(self.config,b'version=2\n')
            with self.assertRaisesRegex(ops.Refused,'OPEN_FILE_CHANGED'):held.verify()
    def test_private_parent_permission_drift_is_rejected(self):
        with ops.Opened.open(str(self.config),private=True) as held:
            self.root.chmod(0o755)
            with self.assertRaisesRegex(ops.Refused,'PRIVATE_PARENT_REQUIRED'):held.verify()
    def test_held_fd_detects_path_replacement_even_equal_bytes(self):
        with ops.Opened.open(str(self.config),private=True) as held:
            other=self.write(self.root/'replacement',self.config.read_bytes());os.replace(other,self.config)
            # Replacing a linked pathname first changes the original inode's
            # ctime/link metadata, so the held-FD guard rejects even earlier.
            with self.assertRaisesRegex(ops.Refused,'OPEN_FILE_CHANGED'):held.verify()
    def test_plan_source_digest_mismatch_blocks(self):
        self.input['core']['source_manifest']['sha256']='0'*64
        with self.assertRaisesRegex(ops.Refused,'FILE_DIGEST_MISMATCH'):self.plan()
    def test_plan_hash_mismatch_rejected(self):
        path,sha,plan=self.plan()
        with ExitStack() as stack:
            with self.assertRaisesRegex(ops.Refused,'FILE_DIGEST_MISMATCH'):ops.read_plan(str(path),'0'*64,stack)
    def test_changed_consumer_identity_invalidates_plan(self):
        path,sha,plan=self.plan();plan['operator']['script_sha256']='0'*64;self.write(path,plan)
        with ExitStack() as stack:
            with self.assertRaisesRegex(ops.Refused,'OPERATOR_IMPLEMENTATION_CHANGED'):ops.read_plan(str(path),ops.digest(path.read_bytes()),stack)
    def test_journal_unknown_state_and_unknown_fields_rejected(self):
        path,sha,plan=self.plan();jp,value=self.journal(plan,sha);value['state']='READY_FOR_RESTORE'
        with self.assertRaisesRegex(ops.Refused,'JOURNAL_STATE_UNKNOWN'):ops.validate_journal(value,plan,sha)
        value['state']='SUCCEEDED';value['override']=True
        with self.assertRaisesRegex(ops.Refused,'CLOSED_FIELDS_REQUIRED'):ops.validate_journal(value,plan,sha)
    def test_journal_other_id_or_plan_never_adopted(self):
        path,sha,plan=self.plan();jp,value=self.journal(plan,sha);value['operation_id']='core-ops-'+'b'*32
        with self.assertRaisesRegex(ops.Refused,'JOURNAL_PLAN_MISMATCH'):ops.validate_journal(value,plan,sha)
    def test_unknown_reconcile_does_not_execute_core_or_write(self):
        path,sha,plan=self.plan();jp,value=self.journal(plan,sha,'UNKNOWN');old=jp.read_bytes()
        with patch.object(ops,'probe',side_effect=AssertionError('must not invoke Core')):
            response,code=ops.inspect_plan(str(path),sha,journal_sha=ops.digest(old),reconcile=True)
        self.assertEqual(code,3);self.assertIn('JOURNAL_UNKNOWN',response['codes']);self.assertFalse(response['takeover_authorized']);self.assertEqual(jp.read_bytes(),old)
    def test_reconcile_missing_journal_refuses_without_creation(self):
        path,sha,plan=self.plan()
        with self.assertRaisesRegex(ops.Refused,'NO_JOURNAL'):ops.inspect_plan(str(path),sha,journal_sha='0'*64,reconcile=True)
        self.assertFalse(Path(plan['journal']['file']).exists())
    def test_succeeded_journal_does_not_make_physical_or_restore_ready(self):
        path,sha,plan=self.plan();jp,value=self.journal(plan,sha)
        with patch.object(ops,'probe',side_effect=ops.Refused('CHECK_DB_DATABASE_UNAVAILABLE',3)):
            response,code=ops.inspect_plan(str(path),sha)
        self.assertEqual(code,3);self.assertFalse(response['restore_ready']);self.assertIn('PHYSICAL_IDENTITY_UNVERIFIED',response['codes']);self.assertEqual(response['core_probe'],'NOT_VALIDATED')
    def test_probe_full_dto_contract_is_only_parsing_fixture(self):
        with ExitStack() as stack:
            inputs=ops.open_inputs(self.input,stack);dto=self.dto(inputs)
            self.assertEqual(ops.validate_probe(ops.canonical(dto),0,inputs,self.parent_observation(inputs),self.boot),dto)
    def test_probe_unknown_or_missing_fields_rejected(self):
        with ExitStack() as stack:
            inputs=ops.open_inputs(self.input,stack)
            for change in ['extra','missing']:
                dto=self.dto(inputs)
                if change=='extra':dto['restore_ready']=True
                else:dto.pop('system_identifier')
                with self.assertRaisesRegex(ops.Refused,'CLOSED_FIELDS_REQUIRED'):ops.validate_probe(ops.canonical(dto),0,inputs,self.parent_observation(inputs),self.boot)
    def test_probe_huge_or_truncated_output_rejected(self):
        with ExitStack() as stack:
            inputs=ops.open_inputs(self.input,stack)
            for raw in [b'x'*(ops.MAX_JSON+1),b'{"version":1']:
                with self.assertRaises(ops.Refused):ops.validate_probe(raw,0,inputs,self.parent_observation(inputs),self.boot)
    def test_probe_wrong_execution_or_database_binding_rejected(self):
        with ExitStack() as stack:
            inputs=ops.open_inputs(self.input,stack)
            for section,field,value in [('execution','pid',os.getpid()+1),('execution','executable_sha256','0'*64),('database','oid','12346')]:
                dto=self.dto(inputs);dto[section][field]=value
                with self.subTest(section=section,field=field):
                    with self.assertRaises(ops.Refused):ops.validate_probe(ops.canonical(dto),0,inputs,self.parent_observation(inputs),self.boot)
    def test_probe_configured_capabilities_not_all_safe(self):
        with ExitStack() as stack:
            inputs=ops.open_inputs(self.input,stack);dto=self.dto(inputs);dto['runtime_authority']['required_capabilities']['models']=True
            with self.assertRaisesRegex(ops.Refused,'RUNTIME_REQUIREMENTS_MISMATCH'):ops.validate_probe(ops.canonical(dto),0,inputs,self.parent_observation(inputs),self.boot)
    def test_probe_wrong_snapshot_and_domain_rejected(self):
        with ExitStack() as stack:
            inputs=ops.open_inputs(self.input,stack)
            for section,field,value in [('observation','transaction_read_only',False),('observation','transaction_read_only',1),('catalog','fingerprint_hash_domain','sorted-json'),('system_identifier','status','verified')]:
                dto=self.dto(inputs);dto[section][field]=value
                with self.assertRaises(ops.Refused):ops.validate_probe(ops.canonical(dto),0,inputs,self.parent_observation(inputs),self.boot)
    def test_failure_codes_bind_exact_exit_and_never_leak_raw_error(self):
        with ExitStack() as stack:
            inputs=ops.open_inputs(self.input,stack)
            dto={'version':1,'protocol':ops.PROTOCOL,'status':'rejected','scope':'runtime_configured_capabilities','code':'CHECK_DB_RUNTIME_UNSAFE'}
            with self.assertRaisesRegex(ops.Refused,'CHECK_DB_RUNTIME_UNSAFE'):ops.validate_probe(ops.canonical(dto),4,inputs,self.parent_observation(inputs),self.boot)
            with self.assertRaisesRegex(ops.Refused,'INVALID_PROBE_FAILURE'):ops.validate_probe(ops.canonical(dto),3,inputs,self.parent_observation(inputs),self.boot)
            dto['code']='SECRET_DSN_PASSWORD'
            with self.assertRaisesRegex(ops.Refused,'INVALID_PROBE_FAILURE'):ops.validate_probe(ops.canonical(dto),3,inputs,self.parent_observation(inputs),self.boot)
    def test_fd_execution_uses_real_installed_python_and_self_exe_hash(self):
        binary=self.input['core']['binary_file']
        with ops.Opened.open(binary,executable=True) as held:
            command='from pathlib import Path; import os,hashlib,json; print(json.dumps({"pid":os.getpid(),"exe":hashlib.sha256(Path("/proc/self/exe").read_bytes()).hexdigest()}))'
            raw,code,execution=ops.execute_fd(held,[binary,'-B','-c',command]);observed=ops.decode(raw)
            self.assertEqual(code,0);self.assertEqual(observed['pid'],execution.pid);self.assertEqual(observed['exe'],held.sha256)
            self.assertIs(type(execution),ops.ObservedExecution)
    def test_real_owned_process_start_ticks_forgery_is_rejected(self):
        with ExitStack() as stack:
            inputs=ops.open_inputs(self.input,stack);dto=self.dto(inputs)
            command=('from pathlib import Path;import os,hashlib,json;v='+repr(dto)+';'
                'v["execution"]={"pid":os.getpid(),"boot_id":Path("/proc/sys/kernel/random/boot_id").read_text().strip(),'
                '"start_ticks":Path("/proc/self/stat").read_text().rsplit(")",1)[1].split()[19],'
                '"executable_sha256":hashlib.sha256(Path("/proc/self/exe").read_bytes()).hexdigest()};'
                'print(json.dumps(v,separators=(",",":")))')
            raw,code,parent=ops.execute_fd(inputs.binary,[str(inputs.binary.path),'-B','-c',command])
            actual=ops.validate_probe(raw,code,inputs,parent,self.boot)
            self.assertEqual(actual['execution']['start_ticks'],parent.start_ticks)
            actual['execution']['start_ticks']=str(int(parent.start_ticks)+1)
            with self.assertRaisesRegex(ops.Refused,'EXECUTION_BINDING_MISMATCH'):
                ops.validate_probe(ops.canonical(actual),code,inputs,parent,self.boot)
    def test_reaped_child_cannot_supply_parent_observation(self):
        with ops.Opened.open(self.input['core']['binary_file'],executable=True) as binary:
            child=subprocess.Popen([str(binary.path),'-B','-c','pass'],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            self.assertEqual(child.wait(timeout=5),0)
            with self.assertRaisesRegex(ops.Refused,'EXECUTION_OBSERVATION_UNAVAILABLE'):ops.observe_child(child,binary)
    def test_plain_pid_cannot_replace_explicit_parent_witness(self):
        with ExitStack() as stack:
            inputs=ops.open_inputs(self.input,stack)
            with self.assertRaisesRegex(ops.Refused,'EXECUTION_PARENT_OBSERVATION_REQUIRED'):
                ops.validate_probe(ops.canonical(self.dto(inputs)),0,inputs,os.getpid(),self.boot)
    def test_raw_child_stderr_is_discarded_even_for_invalid_probe(self):
        with ExitStack() as stack:
            inputs=ops.open_inputs(self.input,stack)
            raw,code,execution=ops.execute_fd(inputs.binary,[str(inputs.binary.path),'-B','-c','import sys;sys.stderr.write("SECRET_DSN_PASSWORD");print("{}");sys.exit(3)'])
            with self.assertRaises(ops.Refused) as caught:ops.validate_probe(raw,code,inputs,execution,self.boot)
            self.assertNotIn('SECRET_DSN_PASSWORD',str(caught.exception))
            self.assertNotIn(b'SECRET_DSN_PASSWORD',raw)
    def test_fd_path_swap_cannot_execute_replacement(self):
        binary=self.root/'selected-elf';shutil.copyfile(self.input['core']['binary_file'],binary);binary.chmod(0o700)
        real_popen=subprocess.Popen;observations=[];marker=self.root/'replacement-was-executed'
        with ops.Opened.open(str(binary),executable=True) as held:
            def swap_then_spawn(*args,**kwargs):
                replacement=self.write(self.root/'replacement-elf',('#!/usr/bin/python3\nfrom pathlib import Path\nPath('+repr(str(marker))+').write_text("untrusted")\n').encode(),mode=0o700)
                os.replace(replacement,binary);observations.append(kwargs['executable']);return real_popen(*args,**kwargs)
            with patch.object(ops.subprocess,'Popen',side_effect=swap_then_spawn):
                with self.assertRaisesRegex(ops.Refused,'OPEN_FILE_CHANGED'):
                    ops.execute_fd(held,[str(binary),'-B','-c','print("original executable")'])
            self.assertEqual(observations,[f'/proc/self/fd/{held.fd}'])
            self.assertFalse(marker.exists())
    def test_output_cap_and_timeout_only_cancel_owned_python_child(self):
        with ops.Opened.open(self.input['core']['binary_file'],executable=True) as held:
            with self.assertRaisesRegex(ops.Refused,'PROBE_OUTPUT_BOUND'):
                ops.execute_fd(held,[str(held.path),'-B','-c','print("x"*200000)'])
            with self.assertRaisesRegex(ops.Refused,'PROBE_TIMEOUT'):
                ops.execute_fd(held,[str(held.path),'-B','-c','import time;time.sleep(5)'],timeout=.03)
    def test_unsupported_commands_and_secret_arguments_redacted(self):
        p=subprocess.run([sys.executable,'-B',str(SOURCE),'restore','--secret','DO_NOT_PRINT_SECRET'],capture_output=True,env={'PATH':'/usr/bin','LANG':'C.UTF-8'},cwd='/')
        self.assertEqual(p.returncode,2);self.assertNotIn(b'DO_NOT_PRINT_SECRET',p.stdout+p.stderr);self.assertFalse(p.stderr)
    def test_real_sigint_inspection_boundary_returns_safe_json(self):
        code=('import importlib.util,sys,os,signal;'
              's=importlib.util.spec_from_file_location("sigint_owned",'+repr(str(SOURCE))+');'
              'o=importlib.util.module_from_spec(s);sys.modules[s.name]=o;s.loader.exec_module(o);'
              'o.inspect_plan=lambda *a,**k:os.kill(os.getpid(),signal.SIGINT);'
              'raise SystemExit(o.main(["status","--plan","/private/synthetic","--expect-plan-sha256","'+'0'*64+'"]))')
        child=subprocess.run([sys.executable,'-B','-c',code],capture_output=True,timeout=5)
        self.assertEqual(child.returncode,3);self.assertFalse(child.stderr)
        value=json.loads(child.stdout);self.assertEqual(value['code'],'OPERATOR_INTERRUPTED');self.assertEqual(value['result'],'REJECTED')

if __name__=='__main__':unittest.main()
