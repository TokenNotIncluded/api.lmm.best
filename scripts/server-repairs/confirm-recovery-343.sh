#!/usr/bin/env bash
# Finalize only the existing incident transaction through the installed native CLI.
# Never changes its status, observation evidence or transaction lock directly.
set -Eeuo pipefail
set +x
: "${LMM_OPS_REPORT:?Use the reviewed owner incident workflow}"
python3 - "$LMM_OPS_REPORT" <<'PY'
import json
import os
from pathlib import Path
import subprocess
import sys

DEPLOYMENT = 'release-go-v0.2.51-35116594330-attempt-1'
WORKSPACE = '/var/lib/lmm-api-go-deploy/work/' + DEPLOYMENT
CLI = '/usr/bin/lmm-api'
LIMIT = 65536


def invoke(audit, operation, label=None):
    if operation not in {"status", "confirm"} or label not in {None, "status-after-confirm"}:
        raise ValueError("invalid_native_operation")
    paths = [audit / ((label or operation) + suffix) for suffix in ('.json', '.stderr')]
    files = []
    try:
        for path in paths:
            fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
            files.append(os.fdopen(fd, 'wb'))
        result = subprocess.run([CLI, 'deploy', 'production', operation, '--workspace', WORKSPACE],
                                stdin=subprocess.DEVNULL, stdout=files[0], stderr=files[1],
                                timeout=120, check=False)
    finally:
        for source in files:
            source.close()
    if result.returncode:
        raise ValueError('native_' + operation + '_refused')
    with paths[0].open('rb') as source:
        raw = source.read(LIMIT + 1)
    if len(raw) > LIMIT:
        raise ValueError('native_result_oversized')
    value = json.loads(raw)
    if not isinstance(value, dict):
        raise ValueError('native_result_not_object')
    return value


def require_identity(value, phases):
    if value.get('deployment_id') != DEPLOYMENT or value.get('version') != '0.2.51':
        raise ValueError('native_identity_mismatch')
    if value.get('phase') not in phases:
        raise ValueError('native_phase_not_confirmable')


def main(path):
    report = Path(path)
    audit = report.parent
    answer = {'operation': 'confirm-recovery-343', 'deployment_id': DEPLOYMENT,
              'native_confirmation_requested': False, 'success': False}
    try:
        if os.geteuid() != 0 or not report.is_file() or report.is_symlink():
            raise ValueError('private_root_audit_required')
        before = invoke(audit, 'status')
        require_identity(before, {'AWAITING_CONFIRMATION'})
        # The native command alone checks observation, signatures, package identity,
        # live service, public release probes and restarts before finalization.
        answer['native_confirmation_requested'] = True
        result = invoke(audit, 'confirm')
        if result.get('phase') != 'CONFIRMED' or result.get('version') != '0.2.51':
            raise ValueError('native_confirmation_result_mismatch')
        if result.get('deployment_id') not in (None, '', DEPLOYMENT):
            raise ValueError('native_identity_mismatch')
        # The released CLI returns an in-memory confirmation status whose ID
        # can be empty. Read persisted status again rather than falsely reporting
        # failure or treating an incomplete response as final identity evidence.
        after = invoke(audit, 'status', 'status-after-confirm')
        require_identity(after, {'CONFIRMED'})
        answer.update(success=True, phase='CONFIRMED', version='0.2.51')
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        # Raw CLI output remains in the existing private audit directory.
        labels = {'private_root_audit_required', 'native_status_refused', 'native_confirm_refused',
                  'native_identity_mismatch', 'native_phase_not_confirmable',
                  'native_result_oversized', 'native_result_not_object',
                  'native_confirmation_result_mismatch', 'invalid_native_operation'}
        answer['failure_category'] = str(error) if str(error) in labels else type(error).__name__
    report.write_text(json.dumps(answer, sort_keys=True, indent=2) + '\n')
    return 0 if answer['success'] else 1


if __name__ == '__main__':
    raise SystemExit(main(sys.argv[1]))
PY
