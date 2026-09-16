#!/usr/bin/env bash
# Reviewed one-incident forward recovery; raw logs remain in the private audit.
set -Eeuo pipefail
set +x
: "${LMM_OPS_REPORT:?Run through the owner-approved incident workflow}"
audit=$(dirname -- "$LMM_OPS_REPORT")
helper="$audit/incident343-recovery"
test -f "$helper" && test ! -L "$helper" && test -x "$helper"
status=0
"$helper" --confirm api.lmm.best >"$audit/native-result.json" 2>"$audit/native-error.log" || status=$?
python3 - "$LMM_OPS_REPORT" "$audit/native-result.json" "$audit/native-error.log" "$status" <<'PY'
import hashlib,json,sys
from pathlib import Path
report={'operation':'recover-red-packet-schema-343','exit_code':int(sys.argv[4]),'auto_confirmed':False}
if report['exit_code']==0:
    result=json.loads(Path(sys.argv[2]).read_text())
    report.update({key:result[key] for key in ('deployment_id','phase','version','previous_version','reason','updated_utc','observation_seconds') if key in result})
    if report.get('phase') != 'AWAITING_CONFIRMATION':
        raise ValueError('unexpected successful native recovery phase')
else:
    error=Path(sys.argv[3]).read_bytes()[:65536]
    report['error_sha256']=hashlib.sha256(error).hexdigest()
    labels={
        'identity':'restricted to the recorded incident',
        'phase':'failed pre-observation Go-only activation',
        'old_writer_receipt':'old-writer shutdown evidence',
        'admission':'billing barrier',
        'archive_identity':'manifest metadata',
        'configuration_snapshot':'configuration rollback snapshot',
        'installed_identity':'installed ',
        'active_writer':'interrupt a running application writer',
        'database_identity':'database connection changed',
        'database_search_path':'database search path',
        'existing_relation':'relations must be absent',
        'prior_attempt':'instead of replaying',
        'backup':'backup',
        'snapshot':'snapshot',
        'stop':'could not be stopped',
        'service_cgroup':'cgroup',
        'schema_transaction':'schema repair failed',
        'migration':'migration ',
        'readiness':'did not start',
        'restarts':'restart',
        'health':'health',
        'memory':'memory',
        'observation':'observation',
        'transaction_lock':'transaction lock',
    }
    text=error.decode('utf-8',errors='replace')
    report['failure_categories']=[k for k,v in labels.items() if v in text]
Path(sys.argv[1]).write_text(json.dumps(report,sort_keys=True,indent=2)+'\n')
PY
exit "$status"
