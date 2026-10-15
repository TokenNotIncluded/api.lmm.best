#!/usr/bin/env python3
"""AGPL-3.0-or-later. Bounded Core operator inspection, never maintenance authority.

Python stdlib only. JSON/private-file patterns follow scripts/lmm-admin.py.
Only plan writes a new file. status/reconcile never create journals, locks or logs.
There are deliberately no maintenance, backup, restore, takeover or retry commands.
"""
from __future__ import annotations
from dataclasses import asdict, dataclass
from pathlib import Path
from contextlib import ExitStack
import argparse, datetime, hashlib, ipaddress, json, os, re, selectors, stat
import subprocess, sys, time, unicodedata, uuid

BACKUPS = Path('/home/lightjunction/Backups')
MAX_JSON = 1 << 20
MAX_BINARY = 1 << 30
MAX_STDOUT = 128 << 10
MAX_STDERR = 16 << 10
PROTOCOL = 'core.check-db.v1'
SCOPE = 'core-inspection-v1'
CAPABILITIES = {'models','events','payments','console','commerce','web_identity','oauth2'}
NOT_CHECKED = ['maintenance_fence','restore_readiness','backup_integrity',
               'physical_system_identifier','external_services','historical_migration']
IDENTITY_FIELDS = {'name','oid','server_address','server_port','postmaster_started_at_unix_micros',
                   'session_user','current_user','server_version_num'}
SPEC_FIELDS = {'version','scope','operation_id','installation_id','host','core','database','physical_binding','journal'}
STATES = {'PLANNED','IN_PROGRESS','SUCCEEDED','FAILED','UNKNOWN'}
FAILURES = {
    2:{'CHECK_DB_ARGUMENTS_INVALID','CHECK_DB_CONFIG_REQUIRED','CHECK_DB_LEGACY_ENV_FORBIDDEN','CHECK_DB_CONFIG_INVALID'},
    3:{'CHECK_DB_DATABASE_UNAVAILABLE','CHECK_DB_SCHEMA_UNVERIFIED','CHECK_DB_AUTHORITY_UNVERIFIED','CHECK_DB_OBSERVATION_UNAVAILABLE','CHECK_DB_EXECUTION_UNAVAILABLE'},
    4:{'CHECK_DB_RUNTIME_UNSAFE','CHECK_DB_RUNTIME_INCOMPLETE'},
    5:{'CHECK_DB_OUTPUT_UNAVAILABLE'},
}

class Refused(Exception):
    def __init__(self, code: str, exit_code: int = 2):
        self.code, self.exit_code = code, exit_code
        super().__init__(code)

def require(ok, code, exit_code=2):
    if not ok: raise Refused(code, exit_code)

def closed(value, keys):
    require(type(value) is dict and set(value)==set(keys),'CLOSED_FIELDS_REQUIRED')
    return value

def digest(raw): return hashlib.sha256(raw).hexdigest()

def sha_value(value):
    require(type(value) is str and re.fullmatch('[0-9a-f]{64}',value) is not None,'SHA256_REQUIRED')
    return value

def decimal(value, minimum, maximum):
    require(type(value) is str and re.fullmatch(r'0|[1-9][0-9]*|-[1-9][0-9]*',value) is not None,'CANONICAL_DECIMAL_REQUIRED')
    n=int(value);require(minimum<=n<=maximum,'DECIMAL_RANGE');return value

def uint(value, minimum=1, maximum=(1<<64)-1):
    require(type(value) is int and minimum<=value<=maximum,'INTEGER_RANGE');return value

def uuid_value(value):
    try: valid=type(value) is str and str(uuid.UUID(value))==value
    except (ValueError,AttributeError): valid=False
    require(valid,'CANONICAL_UUID_REQUIRED');return value

def identifier(value):
    require(type(value) is str and re.fullmatch('[a-z][a-z0-9_-]{0,63}',value) is not None,'INSTALLATION_ID_REQUIRED')
    return value

def operation_id(value):
    require(type(value) is str and re.fullmatch('core-ops-[0-9a-f]{32}',value) is not None,'OPERATION_ID_REQUIRED')
    return value

def canonical(value):
    try: raw=json.dumps(value,sort_keys=True,separators=(',',':'),ensure_ascii=False,allow_nan=False).encode('utf8')+b'\n'
    except (TypeError,ValueError,UnicodeError,RecursionError): raise Refused('INVALID_JSON') from None
    require(len(raw)<=MAX_JSON,'JSON_SIZE_BOUND');return raw

def decode(raw):
    require(len(raw)<=MAX_JSON,'JSON_SIZE_BOUND')
    def unique(pairs):
        result={}
        for key,value in pairs:
            require(key not in result,'DUPLICATE_JSON_FIELD');result[key]=value
        return result
    try:
        value=json.loads(raw.decode('utf8'),object_pairs_hook=unique,
                         parse_constant=lambda _: (_ for _ in ()).throw(Refused('NONFINITE_JSON')))
        canonical(value);return value
    except (UnicodeError,ValueError,RecursionError): raise Refused('INVALID_JSON') from None

def path_value(value):
    require(type(value) is str and value.startswith('/') and value==str(Path(value)) and value!='/'
            and all(part not in ['.','..'] for part in value.split('/'))
            and not any(unicodedata.category(c)=='Cc' for c in value),'CANONICAL_ABSOLUTE_PATH_REQUIRED')
    return Path(value)

def stable(info):
    return (info.st_dev,info.st_ino,info.st_mode,info.st_uid,info.st_gid,info.st_size,info.st_mtime_ns,info.st_ctime_ns)

def _trusted_directory(info):
    require(stat.S_ISDIR(info.st_mode) and info.st_uid in [0,os.getuid()] and not info.st_mode&0o022,'UNTRUSTED_ANCESTOR')

def open_parent(path, *, private):
    """Walk from / using openat O_NOFOLLOW; retain the actual parent FD."""
    path=path_value(str(path));fd=os.open('/',os.O_RDONLY|os.O_DIRECTORY|os.O_CLOEXEC)
    try:
        _trusted_directory(os.fstat(fd))
        for part in path.parts[1:-1]:
            child=os.open(part,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW|os.O_CLOEXEC,dir_fd=fd)
            os.close(fd);fd=child;_trusted_directory(os.fstat(fd))
        if private:
            info=os.fstat(fd)
            require(info.st_uid==os.getuid() and stat.S_IMODE(info.st_mode)==0o700,'PRIVATE_PARENT_REQUIRED')
        return fd
    except Exception:
        os.close(fd);raise

def fd_hash(fd, maximum):
    info=os.fstat(fd);require(stat.S_ISREG(info.st_mode) and 0<=info.st_size<=maximum,'REGULAR_FILE_SIZE_BOUND')
    os.lseek(fd,0,os.SEEK_SET);h=hashlib.sha256();count=0
    while chunk:=os.read(fd,65536):
        count+=len(chunk);require(count<=maximum,'REGULAR_FILE_SIZE_BOUND');h.update(chunk)
    require(count==info.st_size and stable(info)==stable(os.fstat(fd)),'FILE_CHANGED_DURING_READ',3)
    os.lseek(fd,0,os.SEEK_SET);return h.hexdigest()

@dataclass
class Opened:
    path: Path
    parent: int
    fd: int
    info: os.stat_result
    sha256: str
    maximum: int
    private: bool

    @classmethod
    def open(cls, path, *, private=False, executable=False, expected=None):
        path=path_value(path);parent=open_parent(path,private=private);fd=None
        try:
            fd=os.open(path.name,os.O_RDONLY|os.O_NONBLOCK|os.O_NOFOLLOW|os.O_CLOEXEC,dir_fd=parent)
            info=os.fstat(fd)
            require(stat.S_ISREG(info.st_mode),'REGULAR_FILE_REQUIRED')
            if private:
                require(info.st_uid==os.getuid() and stat.S_IMODE(info.st_mode) in [0o400,0o600],'PRIVATE_OWNER_MODE_REQUIRED')
            else:
                require(info.st_uid in [0,os.getuid()] and not info.st_mode&0o022,'TRUSTED_INSTALL_OWNER_MODE_REQUIRED')
            if executable:
                require(info.st_mode&0o111!=0 and os.read(fd,4)==b'\x7fELF','TRUSTED_ELF_REQUIRED')
            maximum=MAX_BINARY if executable else MAX_JSON
            hashed=fd_hash(fd,maximum)
            if expected is not None:require(hashed==sha_value(expected),'FILE_DIGEST_MISMATCH',3)
            value=cls(path,parent,fd,info,hashed,maximum,private);value.verify();return value
        except Exception:
            if fd is not None:os.close(fd)
            os.close(parent);raise

    def verify(self):
        require(stable(self.info)==stable(os.fstat(self.fd)),'OPEN_FILE_CHANGED',3)
        # A rename/replacement of any ancestor or leaf invalidates the result.
        parent=open_parent(self.path,private=self.private)
        try:
            require(os.fstat(parent).st_dev==os.fstat(self.parent).st_dev and os.fstat(parent).st_ino==os.fstat(self.parent).st_ino,'PARENT_CHANGED',3)
            current=os.stat(self.path.name,dir_fd=parent,follow_symlinks=False)
            require(stable(current)==stable(self.info),'PATH_REPLACED',3)
        finally:os.close(parent)
        require(fd_hash(self.fd,self.maximum)==self.sha256,'OPEN_FILE_DIGEST_CHANGED',3)

    def raw(self):
        require(self.maximum==MAX_JSON,'JSON_FILE_REQUIRED');self.verify()
        raw=os.read(self.fd,MAX_JSON+1);os.lseek(self.fd,0,os.SEEK_SET)
        require(digest(raw)==self.sha256,'OPEN_FILE_CHANGED',3);self.verify();return raw

    def json(self):return decode(self.raw())
    def close(self):os.close(self.fd);os.close(self.parent)
    def __enter__(self):return self
    def __exit__(self,*_):self.close()

def file_ref(value):
    closed(value,{'file','sha256'});path_value(value['file']);sha_value(value['sha256']);return value

def incarnation(value):
    closed(value,IDENTITY_FIELDS)
    for key in ['name','session_user','current_user']:
        require(type(value[key]) is str and 0<len(value[key].encode('utf8'))<=63 and '\0' not in value[key],'PG_IDENTIFIER_REQUIRED')
    decimal(value['oid'],1,(1<<32)-1)
    decimal(value['postmaster_started_at_unix_micros'],-(1<<63),(1<<63)-1)
    uint(value['server_version_num'],maximum=(1<<31)-1)
    require(value['session_user']==value['current_user'],'RUNTIME_ROLE_IDENTITY_MISMATCH',3)
    if value['server_address'] is None:require(value['server_port'] is None,'ENDPOINT_PAIR_REQUIRED')
    else:
        require(type(value['server_address']) is str,'ENDPOINT_ADDRESS_REQUIRED')
        try:ipaddress.ip_interface(value['server_address'])
        except ValueError:raise Refused('ENDPOINT_ADDRESS_REQUIRED') from None
        uint(value['server_port'],maximum=65535)
    return value

def capabilities(value):
    closed(value,CAPABILITIES);require(all(type(v) is bool for v in value.values()),'CAPABILITY_BOOLEANS_REQUIRED');return value

def validate_spec(value):
    closed(value,SPEC_FIELDS);require(type(value['version']) is int and value['version']==1 and value['scope']==SCOPE,'SPEC_PROTOCOL_REQUIRED')
    operation_id(value['operation_id']);identifier(value['installation_id'])
    host=closed(value['host'],{'machine_id_sha256','expected_uid','boot_id_at_plan'})
    sha_value(host['machine_id_sha256']);uint(host['expected_uid'],minimum=0,maximum=(1<<32)-1);uuid_value(host['boot_id_at_plan'])
    core=closed(value['core'],{'binary_file','binary_sha256','source_manifest','build_receipt','schema_manifest','check_db_contract'})
    path_value(core['binary_file']);sha_value(core['binary_sha256'])
    for k in ['source_manifest','build_receipt','schema_manifest','check_db_contract']:file_ref(core[k])
    database=closed(value['database'],{'runtime_config_file','runtime_config_sha256','expected_public_incarnation','expected_catalog_sha256','expected_required_capabilities'})
    path_value(database['runtime_config_file']);sha_value(database['runtime_config_sha256']);sha_value(database['expected_catalog_sha256'])
    incarnation(database['expected_public_incarnation']);capabilities(database['expected_required_capabilities'])
    file_ref(value['physical_binding'])
    journal=closed(value['journal'],{'directory','file','envelope_version'})
    directory=path_value(journal['directory']);path_value(journal['file'])
    require(journal['file']==str(directory/(value['operation_id']+'.journal.json')) and type(journal['envelope_version']) is int and journal['envelope_version']==1,'JOURNAL_PATH_PROTOCOL_REQUIRED')
    return value

def validate_physical(value,spec):
    closed(value,{'version','scope','installation_id','machine_id_sha256','boot_id','postgres_system_identifier','pgdata','pg_pid','pg_start_ticks','pg_executable_sha256','nonce','public_incarnation'})
    require(type(value['version']) is int and value['version']==1 and value['scope']=='operator_record_only_not_live_physical_proof','PHYSICAL_REFERENCE_PROTOCOL')
    require(value['installation_id']==spec['installation_id'] and value['machine_id_sha256']==spec['host']['machine_id_sha256'] and value['boot_id']==spec['host']['boot_id_at_plan'],'PHYSICAL_REFERENCE_HOST_MISMATCH',3)
    decimal(value['postgres_system_identifier'],1,(1<<64)-1);path_value(value['pgdata'])
    uint(value['pg_pid'],maximum=(1<<31)-1);decimal(value['pg_start_ticks'],1,(1<<64)-1);sha_value(value['pg_executable_sha256'])
    require(type(value['nonce']) is str and re.fullmatch('[0-9a-f]{32,64}',value['nonce']) is not None,'PHYSICAL_NONCE_REQUIRED')
    require(incarnation(value['public_incarnation'])==spec['database']['expected_public_incarnation'],'PHYSICAL_REFERENCE_DATABASE_MISMATCH',3)

def host_identity():
    with Opened.open('/etc/machine-id') as machine:machine_sha=machine.sha256
    raw=Path('/proc/sys/kernel/random/boot_id').read_bytes();require(len(raw)<=64,'BOOT_ID_SIZE')
    return machine_sha,uuid_value(raw.decode('ascii').strip())

def operator_identity():
    # The script digest pins the exact consumer implementation for this plan.
    with Opened.open(str(Path(__file__).resolve())) as script:script_sha=script.sha256
    with Opened.open(str(Path(sys.executable).resolve()),executable=True) as python:python_sha=python.sha256
    return {'script_sha256':script_sha,'python_executable_sha256':python_sha,'python_version':sys.version.split()[0]}

@dataclass
class Inputs:
    spec: dict
    binary: Opened
    config: Opened
    schema: Opened
    files: list[Opened]

    def verify(self):
        for f in self.files:f.verify()

def open_inputs(spec,stack):
    validate_spec(spec);machine,boot=host_identity()
    require(spec['host']['expected_uid']==os.getuid() and spec['host']['machine_id_sha256']==machine,'HOST_IDENTITY_MISMATCH',3)
    require(spec['host']['boot_id_at_plan']==boot,'BOOT_ID_CHANGED',3)
    files=[]
    def opened(path,sha,**kwargs):
        f=stack.enter_context(Opened.open(path,expected=sha,**kwargs));files.append(f);return f
    core=spec['core'];binary=opened(core['binary_file'],core['binary_sha256'],executable=True)
    refs={k:opened(core[k]['file'],core[k]['sha256']) for k in ['source_manifest','build_receipt','schema_manifest','check_db_contract']}
    db=spec['database'];config=opened(db['runtime_config_file'],db['runtime_config_sha256'],private=True)
    physical=opened(spec['physical_binding']['file'],spec['physical_binding']['sha256'],private=True)
    validate_physical(physical.json(),spec)
    # Validate the journal parent even when the journal itself does not exist.
    parent=open_parent(Path(spec['journal']['file']),private=True);os.close(parent)
    schema=refs['schema_manifest'];compiled=schema.json()
    closed(compiled,{'format_version','scope','components','identity','installer_contract_sql_sha256','catalog'})
    require(type(compiled['format_version']) is int and compiled['format_version']==1 and compiled['scope']=='compiled-core-schema-contract','COMPILED_MANIFEST_REQUIRED')
    validate_components(compiled['components'])
    identity=closed(compiled['identity'],{'version','fingerprint_sha256'});uint(identity['version'],maximum=(1<<31)-1);sha_value(identity['fingerprint_sha256'])
    sha_value(compiled['installer_contract_sql_sha256'])
    catalog=closed(compiled['catalog'],{'query_utf8','query_sha256','query_hash_domain','fingerprint_hash_domain','definition_order'})
    require(type(catalog['query_utf8']) is str and sha_value(catalog['query_sha256'])==digest(catalog['query_utf8'].encode('utf8')) and catalog['query_hash_domain']=='sha256-raw-utf8-v1' and catalog['fingerprint_hash_domain']=='sha256-u64be-byte-length-prefixed-utf8-v1' and catalog['definition_order']=='postgresql-c-collation','COMPILED_CATALOG_CONTRACT_REQUIRED')
    return Inputs(spec,binary,config,schema,files)

def validate_components(value):
    require(type(value) is list and 1<=len(value)<=64,'COMPILED_COMPONENTS_REQUIRED');names=[]
    for item in value:
        closed(item,{'name','raw_schema_sha256'});identifier(item['name']);sha_value(item['raw_schema_sha256']);names.append(item['name'])
    require(len(names)==len(set(names)),'DUPLICATE_COMPONENT');return value

def write_plan(spec_path,output):
    with ExitStack() as stack:
        source=stack.enter_context(Opened.open(spec_path,private=True));spec=source.json();inputs=open_inputs(spec,stack)
        output=path_value(output);require(output.is_relative_to(BACKUPS) and output!=BACKUPS,'OUTPUT_ONLY_BACKUPS')
        parent=open_parent(output,private=True)
        try:
            plan={**spec,'created_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'operator':operator_identity()}
            raw=canonical(plan);source.verify();inputs.verify()
            fd=os.open(output.name,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW|os.O_CLOEXEC,0o600,dir_fd=parent)
            try:
                with os.fdopen(os.dup(fd),'wb') as target:target.write(raw);target.flush()
                os.fsync(fd)
            finally:os.close(fd)
            os.fsync(parent)
            # No unlink/overwrite/retry on an uncertain publication.
            with Opened.open(str(output),private=True,expected=digest(raw)):pass
            return {'version':1,'scope':SCOPE,'operation_id':spec['operation_id'],'result':'PLAN_WRITTEN','plan_sha256':digest(raw),'writes_plan_only':True,'service_or_database_mutation':False}
        finally:os.close(parent)

def read_plan(path,expected,stack):
    f=stack.enter_context(Opened.open(path,private=True,expected=expected));plan=f.json()
    closed(plan,SPEC_FIELDS|{'created_at_utc','operator'})
    require(type(plan['created_at_utc']) is str,'PLAN_TIME_REQUIRED')
    try:when=datetime.datetime.fromisoformat(plan['created_at_utc']);require(when.utcoffset()==datetime.timedelta(0),'PLAN_TIME_REQUIRED')
    except ValueError:raise Refused('PLAN_TIME_REQUIRED') from None
    closed(plan['operator'],{'script_sha256','python_executable_sha256','python_version'})
    require(plan['operator']==operator_identity(),'OPERATOR_IMPLEMENTATION_CHANGED',3)
    spec={k:plan[k] for k in SPEC_FIELDS};validate_spec(spec)
    return f,plan,open_inputs(spec,stack)

def validate_journal(value,plan,plan_sha):
    closed(value,{'version','operation_id','plan_sha256','generation','state','holder','installation_identity'})
    require(type(value['version']) is int and value['version']==1,'JOURNAL_PROTOCOL_REQUIRED')
    require(value['operation_id']==plan['operation_id'] and value['plan_sha256']==plan_sha,'JOURNAL_PLAN_MISMATCH',3)
    uint(value['generation']);require(type(value['state']) is str and value['state'] in STATES,'JOURNAL_STATE_UNKNOWN')
    holder=closed(value['holder'],{'boot_id','pid','start_ticks'});uuid_value(holder['boot_id']);uint(holder['pid'],maximum=(1<<31)-1);decimal(holder['start_ticks'],1,(1<<64)-1)
    installation=closed(value['installation_identity'],{'installation_id','machine_id_sha256','public_incarnation_sha256'})
    expected={'installation_id':plan['installation_id'],'machine_id_sha256':plan['host']['machine_id_sha256'],'public_incarnation_sha256':digest(canonical(plan['database']['expected_public_incarnation']))}
    require(installation==expected,'JOURNAL_INSTALLATION_MISMATCH',3)
    return value

def holder_observation(holder):
    # A mismatch/absence is merely an observation, never proof authorizing CAS.
    _,boot=host_identity()
    if holder['boot_id']!=boot:return 'BOOT_CHANGED'
    try:
        proc=Path('/proc')/str(holder['pid']);raw=(proc/'stat').read_bytes()
        require(len(raw)<=65536,'HOLDER_OBSERVATION_UNAVAILABLE',3)
        ticks=raw.decode().rsplit(')',1)[1].split()[19]
        return 'MATCH' if ticks==holder['start_ticks'] and proc.stat().st_uid==os.getuid() else 'INCARNATION_DIFFERENT'
    except (OSError,ValueError,IndexError,UnicodeError):return 'NOT_OBSERVED'

@dataclass(frozen=True)
class ObservedExecution:
    """Explicit parent observation of this owned Popen child, never DTO input."""
    pid: int
    boot_id: str
    start_ticks: str
    executable_sha256: str

def _child_stat(child):
    # The child has not been waited/reaped; its PID cannot be recycled here.
    raw=Path('/proc',str(child.pid),'stat').read_bytes()
    require(len(raw)<=65536,'EXECUTION_OBSERVATION_UNAVAILABLE',3)
    parts=raw.decode('utf8').rsplit(')',1)
    require(len(parts)==2 and parts[0].split(' ',1)[0]==str(child.pid),'EXECUTION_OBSERVATION_UNAVAILABLE',3)
    tail=parts[1].split()
    require(len(tail)>19 and tail[0] not in ['Z','X','x'],'EXECUTION_OBSERVATION_UNAVAILABLE',3)
    return decimal(tail[19],1,(1<<64)-1)

def observe_child(child,binary):
    """Read only this Popen PID's stat/exe while it is still observable.

    No SIGSTOP, handshake, hidden PID map, payload-derived time or fallback.
    A command that exits before observation is unverified, even if its DTO claims
    all four fields. No other process's argv, environment or account is read.
    """
    try:
        ticks=_child_stat(child);_,boot=host_identity()
        # /proc/PID/exe is the kernel's intentional symlink to the actually
        # executing inode, distinct from untrusted filesystem path inputs.
        fd=os.open(f'/proc/{child.pid}/exe',os.O_RDONLY|os.O_CLOEXEC)
        try:
            current=os.fstat(fd)
            require((current.st_dev,current.st_ino)==(binary.info.st_dev,binary.info.st_ino),'EXECUTION_OBSERVATION_UNAVAILABLE',3)
            require(_child_stat(child)==ticks,'EXECUTION_OBSERVATION_UNAVAILABLE',3)
            # Complete both live process/image observations before hashing the
            # held actual-exe FD. The FD remains valid if the observed process
            # naturally exits during hashing; that is not a guessed observation.
            after=os.stat(f'/proc/{child.pid}/exe')
            require((after.st_dev,after.st_ino)==(current.st_dev,current.st_ino),'EXECUTION_OBSERVATION_UNAVAILABLE',3)
            executable_sha=fd_hash(fd,MAX_BINARY)
            require(executable_sha==binary.sha256,'EXECUTION_OBSERVATION_UNAVAILABLE',3)
        finally:os.close(fd)
        return ObservedExecution(child.pid,boot,ticks,executable_sha)
    except (OSError,UnicodeError,ValueError,IndexError,Refused):
        raise Refused('EXECUTION_OBSERVATION_UNAVAILABLE',3) from None

def validate_probe(raw,returncode,inputs,parent_execution,boot):
    value=decode(raw)
    if returncode!=0:
        closed(value,{'version','protocol','status','scope','code'})
        require(type(value['version']) is int and value['version']==1 and value['protocol']==PROTOCOL and value['status']=='rejected' and value['scope']=='runtime_configured_capabilities' and type(value['code']) is str and value['code'] in FAILURES.get(returncode,set()),'INVALID_PROBE_FAILURE')
        raise Refused(value['code'],3)
    closed(value,{'version','protocol','status','scope','compiled_schema_manifest_sha256','compiled_schema_manifest_hash_domain','components','catalog','runtime_authority','database','observation','system_identifier','execution','not_checked'})
    require(type(value['version']) is int and value['version']==1 and value['protocol']==PROTOCOL and value['status']=='validated' and value['scope']=='runtime_configured_capabilities','INVALID_PROBE_PROTOCOL')
    require(value['compiled_schema_manifest_hash_domain']=='sha256-exact-schema-manifest-stdout-v1' and sha_value(value['compiled_schema_manifest_sha256'])==inputs.schema.sha256,'COMPILED_MANIFEST_MISMATCH',3)
    require(validate_components(value['components'])==inputs.schema.json()['components'],'COMPILED_COMPONENTS_MISMATCH',3)
    catalog=closed(value['catalog'],{'status','fingerprint_sha256','fingerprint_hash_domain','matches_installed_receipt'})
    require(catalog['status']=='validated' and catalog['matches_installed_receipt'] is True and catalog['fingerprint_hash_domain']=='sha256-u64be-byte-length-prefixed-utf8-v1','CATALOG_OBSERVATION_REQUIRED')
    require(sha_value(catalog['fingerprint_sha256'])==inputs.spec['database']['expected_catalog_sha256'],'CATALOG_MISMATCH',3)
    authority=closed(value['runtime_authority'],{'status','required_capabilities'})
    require(authority['status']=='validated' and capabilities(authority['required_capabilities'])==inputs.spec['database']['expected_required_capabilities'],'RUNTIME_REQUIREMENTS_MISMATCH',3)
    database=closed(value['database'],IDENTITY_FIELDS|{'observed_at_unix_micros'})
    decimal(database['observed_at_unix_micros'],-(1<<63),(1<<63)-1)
    actual={k:database[k] for k in IDENTITY_FIELDS}
    require(incarnation(actual)==inputs.spec['database']['expected_public_incarnation'],'DATABASE_INCARNATION_MISMATCH',3)
    observation=closed(value['observation'],{'transaction_read_only','transaction_isolation','snapshot'})
    require(observation['transaction_read_only'] is True and observation['transaction_isolation']=='repeatable_read' and observation['snapshot']=='single_repeatable_read_transaction','READ_ONLY_SNAPSHOT_REQUIRED')
    require(closed(value['system_identifier'],{'status','value'})=={'status':'not_observed','value':None},'PHYSICAL_IDENTITY_PROTOCOL_REQUIRED')
    execution=closed(value['execution'],{'pid','boot_id','start_ticks','executable_sha256'})
    uint(execution['pid'],maximum=(1<<31)-1);uuid_value(execution['boot_id']);decimal(execution['start_ticks'],1,(1<<64)-1);sha_value(execution['executable_sha256'])
    require(type(parent_execution) is ObservedExecution,'EXECUTION_PARENT_OBSERVATION_REQUIRED',3)
    require(parent_execution.boot_id==boot and parent_execution.executable_sha256==inputs.binary.sha256 and execution==asdict(parent_execution),'EXECUTION_BINDING_MISMATCH',3)
    require(value['not_checked']==NOT_CHECKED,'PROBE_SCOPE_LIMITS_CHANGED')
    return value

def execute_fd(binary,argv,*,timeout=20.0):
    """Bounded POSIX plumbing; argv is fixed by probe(), never caller JSON."""
    require(sys.platform.startswith('linux') and Path('/proc/self/fd').is_dir(),'FD_EXECUTION_UNAVAILABLE',3)
    binary.verify()
    try:
        child=subprocess.Popen(argv,executable=f'/proc/self/fd/{binary.fd}',pass_fds=(binary.fd,),
            stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE,cwd='/',
            env={'PATH':'/usr/bin','LANG':'C.UTF-8','LC_ALL':'C.UTF-8'},close_fds=True)
    except OSError:raise Refused('FD_EXECUTION_UNAVAILABLE',3) from None
    output=bytearray();stderr_bytes=0;deadline=time.monotonic()+timeout
    try:
        execution=observe_child(child,binary)
        with selectors.DefaultSelector() as selector:
            for stream,label in [(child.stdout,'stdout'),(child.stderr,'stderr')]:
                os.set_blocking(stream.fileno(),False);selector.register(stream,selectors.EVENT_READ,label)
            while selector.get_map():
                require(time.monotonic()<deadline,'PROBE_TIMEOUT',3)
                for key,_ in selector.select(min(.05,max(0,deadline-time.monotonic()))):
                    chunk=os.read(key.fileobj.fileno(),65536)
                    if not chunk:selector.unregister(key.fileobj);continue
                    if key.data=='stdout':output.extend(chunk);require(len(output)<=MAX_STDOUT,'PROBE_OUTPUT_BOUND',3)
                    else:stderr_bytes+=len(chunk);require(stderr_bytes<=MAX_STDERR,'PROBE_OUTPUT_BOUND',3)
            code=child.wait(timeout=max(.001,deadline-time.monotonic()))
        binary.verify()
        return bytes(output),code,execution
    except subprocess.TimeoutExpired:raise Refused('PROBE_TIMEOUT',3) from None
    finally:
        # Normal cancellation only of the owned readonly child. No kill fallback.
        if child.poll() is None:
            child.terminate()
            try:child.wait(timeout=2)
            except subprocess.TimeoutExpired:pass
        child.stdout.close();child.stderr.close()

def probe(inputs):
    inputs.verify();_,boot=host_identity()
    raw,code,execution=execute_fd(inputs.binary,[str(inputs.binary.path),'--config',str(inputs.config.path),'check-db'])
    inputs.verify();return validate_probe(raw,code,inputs,execution,boot)

def inspect_plan(path,expected,*,journal_sha=None,reconcile=False):
    with ExitStack() as stack:
        plan_file,plan,inputs=read_plan(path,expected,stack);codes=[];journal=None
        try:journal_file=stack.enter_context(Opened.open(plan['journal']['file'],private=True,expected=journal_sha))
        except FileNotFoundError:
            require(not reconcile,'NO_JOURNAL',3);journal_file=None;codes.append('NO_JOURNAL')
        if journal_file is not None:
            journal=validate_journal(journal_file.json(),plan,plan_file.sha256)
            observed=holder_observation(journal['holder'])
            if observed!='MATCH':codes.append('HOLDER_'+observed)
            if journal['state']!='SUCCEEDED':codes.append('JOURNAL_'+journal['state'])
        # Reconcile reads an unresolved record without issuing another probe.
        if reconcile and codes:
            result=None
        else:
            try:result=probe(inputs)
            except Refused as error:
                if error.exit_code==2:raise
                codes.append(error.code);result=None
        # This slice never opens PGDATA or claims live system-ID proof.
        codes.append('PHYSICAL_IDENTITY_UNVERIFIED')
        plan_file.verify();inputs.verify()
        if journal_file is not None:journal_file.verify()
        response={'version':1,'scope':SCOPE,'operation_id':plan['operation_id'],
            'result':'BLOCKED','codes':sorted(set(codes)),
            'plan_sha256':plan_file.sha256,'core_probe':'VALIDATED_PUBLIC_SCOPE' if result is not None else 'NOT_VALIDATED',
            'journal':None if journal is None else {'state':journal['state'],'generation':journal['generation'],'sha256':journal_file.sha256},
            'service_or_database_mutation':False,'restore_ready':False,'takeover_authorized':False}
        return response,3

class Parser(argparse.ArgumentParser):
    def error(self,_):raise Refused('ARGUMENTS_INVALID')

def main(argv=None):
    parser=Parser(description='Read-only Core operator inspection. No maintenance/backup/restore commands.')
    subs=parser.add_subparsers(dest='command',required=True)
    p=subs.add_parser('plan');p.add_argument('--input',required=True);p.add_argument('--out',required=True)
    for name in ['status','reconcile']:
        p=subs.add_parser(name);p.add_argument('--plan',required=True);p.add_argument('--expect-plan-sha256',required=True)
        if name=='reconcile':p.add_argument('--expect-journal-sha256',required=True)
    try:
        args=parser.parse_args(argv)
        if args.command=='plan':response=write_plan(args.input,args.out);code=0
        else:
            response,code=inspect_plan(args.plan,sha_value(args.expect_plan_sha256),journal_sha=sha_value(args.expect_journal_sha256) if args.command=='reconcile' else None,reconcile=args.command=='reconcile')
    except Refused as error:response={'version':1,'scope':SCOPE,'result':'REJECTED','code':error.code,'service_or_database_mutation':False};code=error.exit_code
    except KeyboardInterrupt:
        response={'version':1,'scope':SCOPE,'result':'REJECTED','code':'OPERATOR_INTERRUPTED','service_or_database_mutation':False};code=3
    except (OSError,ValueError,TypeError,KeyError,IndexError,UnicodeError,RecursionError):
        response={'version':1,'scope':SCOPE,'result':'REJECTED','code':'INPUT_OR_PLATFORM_UNAVAILABLE','service_or_database_mutation':False};code=3
    try:sys.stdout.buffer.write(canonical(response));sys.stdout.buffer.flush()
    except (OSError,KeyboardInterrupt):return 3
    return code

if __name__=='__main__':raise SystemExit(main())
