#!/usr/bin/env bash
# Read-only checkpoint and kernel cgroup evidence; no service or database writes.
set -euo pipefail
set +x
: "${LMM_OPS_REPORT:?Use the reviewed owner incident workflow}"
python3 - "$LMM_OPS_REPORT" <<'PY'
import hashlib
import json
import os
from pathlib import Path
import stat
import subprocess
import sys

ROOT = Path('/var/lib/lmm-api-go-deploy/work/release-go-v0.2.51-35116594330-attempt-1/state')
OLD = ROOT / 'incident-343-schema-recovery'
RETRY = OLD / 'backup-retry-1'
FAILURE = 'failed service cgroup is not verifiably empty'


def private_read(path, limit=1048576):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_nlink != 1 or info.st_mode & 0o077 or info.st_size > limit:
            raise ValueError('unsafe_private_file')
        return source.read(limit + 1)


def kernel_read(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0:
            raise ValueError('unsafe_kernel_file')
        data = source.read(8193)
        if len(data) > 8192:
            raise ValueError('oversized_kernel_metadata')
        return data.decode('ascii')


def metadata(path):
    try:
        info = path.lstat()
        return {'exists': True, 'regular': stat.S_ISREG(info.st_mode), 'bytes': info.st_size,
                'root_owned': info.st_uid == 0, 'private': info.st_mode & 0o077 == 0,
                'single_link': info.st_nlink == 1}
    except FileNotFoundError:
        return {'exists': False}


def properties(raw):
    result = {}
    for line in raw.decode('ascii').splitlines():
        key, sep, value = line.partition('=')
        if not sep or key in result:
            raise ValueError('invalid_unit_properties')
        result[key] = value
    allowed = {'LoadState','ActiveState','SubState','MainPID','ControlPID','ControlGroup','Result','NRestarts'}
    if set(result) != allowed:
        raise ValueError('missing_unit_properties')
    output = {}
    for key in ('MainPID','ControlPID','NRestarts'):
        if not result[key].isascii() or not result[key].isdecimal() or len(result[key]) > 12:
            raise ValueError('invalid_unit_pid')
        output[key] = int(result[key])
    enums = {'LoadState': {'loaded','not-found','error','masked'},
             'ActiveState': {'active','inactive','failed','activating','deactivating'},
             'SubState': {'dead','failed','running','start-pre','start','start-post','stop','stop-sigterm','stop-sigkill','stop-post','auto-restart','exited'},
             'Result': {'success','exit-code','signal','core-dump','timeout','watchdog','start-limit-hit','resources','protocol','oom-kill'}}
    for key, values in enums.items():
        output[key] = result[key] if result[key] in values else 'unrecognized'
    output['cgroup_empty_property'] = result['ControlGroup'] == ''
    output['cgroup_is_expected_unit'] = result['ControlGroup'] == '/system.slice/lmm-api.service'
    output['cgroup_unrecognized'] = result['ControlGroup'] not in ('','/system.slice/lmm-api.service')
    return output


def diagnose(report_path):
    report = Path(report_path)
    if os.geteuid() != 0 or not report.is_file() or report.is_symlink():
        raise ValueError('private_root_audit_required')
    output = {'operation':'inspect-backup-343','diagnostic_version':6,
              'database_changed':False,'service_changed':False,'audit_changed':False}
    try:
        error = private_read(Path('/var/log/lmm-server-ops/35146372002-1/native-error.log'))
        output['native_error_is_post_stop_check'] = error == ('incident343-recovery: '+FAILURE+'\n').encode()
        output['native_error_sha256'] = hashlib.sha256(error).hexdigest()
        current = json.loads(private_read(ROOT/'status.json'))
        output['native_status'] = {'phase':current.get('phase') if current.get('phase') in {'ROLLBACK_REQUIRED','AWAITING_CONFIRMATION','CONFIRMED','OBSERVING'} else 'unrecognized',
            'failure_is_post_stop_check':current.get('failure') == FAILURE,
            'reason_is_forward_recovery_failure':current.get('reason') == 'incident-343-forward-recovery-failure'}
        output['original_audit'] = {name:metadata(OLD/name) for name in ('before-manifest.json','before-status.json','before-schema.database.dump','database.sha256','schema-created.json')}
        names = ('before-manifest.json','before-status.json','before-schema.database.dump','before-schema.database.dump.stderr','database.sha256','schema-created.json','resume.json')
        output['retry_audit'] = {name:metadata(RETRY/name) for name in names}
        output['retry_manifest_matches_current'] = private_read(RETRY/'before-manifest.json') == private_read(ROOT/'deployment.json')
        backup = private_read(RETRY/'before-schema.database.dump',64*1024*1024)
        digest = hashlib.sha256(backup).hexdigest()
        output['retry_backup_sha256'] = digest
        output['retry_backup_checksum_valid'] = private_read(RETRY/'database.sha256',256).strip() == digest.encode()
        output['retry_backup_is_custom_archive'] = backup.startswith(b'PGDMP')
        output['retry_backup_bytes'] = len(backup)
    except (OSError,ValueError,UnicodeError,KeyError) as error:
        output['audit_probe_error_type'] = type(error).__name__
    try:
        proc = subprocess.run(['/usr/bin/systemctl','show','--all','--property=LoadState,ActiveState,SubState,MainPID,ControlPID,ControlGroup,Result,NRestarts','lmm-api.service'],
            stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=12,check=False,
            env={'PATH':'/usr/bin:/bin','LC_ALL':'C'})
        if proc.returncode != 0 or len(proc.stdout) > 65536:
            raise ValueError('unit_state_unavailable')
        output['unit'] = properties(proc.stdout)
        directory = Path('/sys/fs/cgroup/system.slice/lmm-api.service')
        output['kernel_cgroup_exists'] = directory.exists()
        if directory.exists():
            info = directory.lstat()
            if not stat.S_ISDIR(info.st_mode) or info.st_uid != 0:
                raise ValueError('unexpected_kernel_cgroup')
            events = {}
            for line in kernel_read(directory/'cgroup.events').splitlines():
                key, value = line.split()
                if key in events or key not in ('populated','frozen') or value not in ('0','1'):
                    raise ValueError('invalid_cgroup_events')
                events[key] = int(value)
            if 'populated' not in events:
                raise ValueError('missing_population_evidence')
            output['kernel_cgroup_events'] = events
            pids = kernel_read(directory/'cgroup.procs').splitlines()
            if any(not pid.isdecimal() for pid in pids):
                raise ValueError('invalid_cgroup_processes')
            output['kernel_cgroup_process_count'] = len(pids)
    except (OSError,ValueError,UnicodeError,subprocess.SubprocessError) as error:
        output['unit_probe_error_type'] = type(error).__name__
    report.write_text(json.dumps(output,sort_keys=True,indent=2)+'\n')


if __name__ == '__main__':
    diagnose(sys.argv[1])
PY
