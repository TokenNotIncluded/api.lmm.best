#!/usr/bin/env bash
# Read-only PostgreSQL backup diagnosis; SQL, DSNs and stderr stay on the host.
set -euo pipefail
set +x
: "${LMM_OPS_REPORT:?Use the reviewed owner incident workflow}"
python3 - "$LMM_OPS_REPORT" <<'PY'
import json
import hashlib
import os
from pathlib import Path
import re
import resource
import stat
import subprocess
import sys
import urllib.parse

DEPLOYMENT = 'release-go-v0.2.51-35116594330-attempt-1'
CONFIG = Path('/etc/lmm-api-go/lmm-api-go.env')
AUDIT = Path('/var/lib/lmm-api-go-deploy/work') / DEPLOYMENT / 'state/incident-343-schema-recovery'


def private_bytes(path, limit=1048576):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as f:
        s = os.fstat(f.fileno())
        if not stat.S_ISREG(s.st_mode) or s.st_uid != 0 or s.st_nlink != 1 or s.st_mode & 0o077 or s.st_size > limit:
            raise ValueError('unsafe_private_input')
        return f.read(limit + 1)


def parse_environment(text):
    values = {}
    for line in text.replace('\r\n', '\n').split('\n'):
        line = line.strip()
        if not line or line[0] in '#;':
            continue
        key, sep, value = line.partition('=')
        key, value = key.strip(), value.strip()
        if not sep or not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*', key) or key in values:
            raise ValueError('invalid_environment')
        if any(c in value for c in '\0\r\n'):
            raise ValueError('invalid_environment')
        if value and value[0] in "'\"":
            quote = value[0]
            if len(value) < 2 or value[-1] != quote:
                raise ValueError('invalid_environment')
            value = value[1:-1]
            if quote in value or '`' in value or '$(' in value or (quote == '"' and any(c in value for c in '\\$')):
                raise ValueError('invalid_environment')
        elif any(c in value for c in ' \t`;') or '$(' in value:
            raise ValueError('invalid_environment')
        values[key] = value
    return values


def connection(values):
    found = [values[k] for k in ('SQL_DSN', 'DATABASE_URL') if values.get(k)]
    if len(found) != 1:
        raise ValueError('invalid_database_configuration')
    u = urllib.parse.urlsplit(found[0])
    if u.scheme not in ('postgres', 'postgresql') or not u.hostname:
        raise ValueError('invalid_database_configuration')
    env = {'PATH': '/usr/bin:/bin', 'HOME': '/root', 'LC_ALL': 'C'}
    env.update({k: v for k, v in values.items() if k.startswith('PG')})
    if u.password is not None:
        env['PGPASSWORD'] = urllib.parse.unquote(u.password)
        u = u._replace(netloc=(u.username or '') + '@' + u.netloc.rsplit('@', 1)[1])
    env['PGCONNECT_TIMEOUT'] = '8'
    return urllib.parse.urlunsplit(u), env


def categories(data):
    text = data[:65536].decode('utf-8', errors='replace').lower()
    patterns = {'version_mismatch': 'server version mismatch',
                'permission_denied': 'permission denied',
                'table_privilege': 'permission denied for table',
                'schema_privilege': 'permission denied for schema',
                'sequence_privilege': 'permission denied for sequence',
                'function_privilege': 'permission denied for function',
                'large_object_privilege': 'permission denied for large object',
                'filesystem_output': 'could not open output file',
                'authentication': 'authentication failed',
                'connection_refused': 'connection refused', 'connection_timeout': 'timeout expired',
                'lock_timeout': 'lock timeout', 'disk_full': 'no space left',
                'unsupported_option': 'unrecognized option', 'ssl_error': 'ssl error'}
    return [name for name, pattern in patterns.items() if pattern in text]


def bounded_child():
    resource.setrlimit(resource.RLIMIT_FSIZE, (64 * 1024 * 1024, 64 * 1024 * 1024))


def run_private(audit, label, args, env, seconds):
    stderr_path = audit / (label + '.stderr')
    fd = os.open(stderr_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'wb') as stderr:
        try:
            p = subprocess.run(args, env=env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                               stderr=stderr, timeout=seconds, check=False, preexec_fn=bounded_child)
            output = {'exit_code': p.returncode, 'categories': categories(private_bytes(stderr_path))}
            return output, p.stdout[:4096]
        except (OSError, subprocess.TimeoutExpired) as e:
            return {'error_type': type(e).__name__}, b''


# Labels derived from the public application model registry. Unknown identifiers
# are represented only by a digest, never copied to public workflow output.
TABLE_LABELS = set("""channels tokens users user_sessions auth_flows external_identity_claims
passkey_credentials options redemptions discount_codes discount_code_reservations
red_packets red_packet_items red_packet_claims abilities logs midjourneys top_ups quota_data
 tasks models vendors prefill_groups setups two_fas two_fa_backup_codes checkins gifts gift_claims
user_ranking_revisions open_source_bounty_projects open_source_bounty_challenges
 developer_access_requests developer_access_recommendation_archives account_action_requests
open_source_bounty_ledgers open_source_bounty_disputes open_source_bounty_mcp_tokens
open_source_bounty_mcp_confirmations open_source_bounty_mcp_operations open_source_bounty_rest_operations
subscription_orders subscription_payment_events subscription_payment_refunds subscription_plans
waffo_pancake_subscription_payments waffo_pancake_subscription_periods user_subscriptions
subscription_reset_vouchers subscription_reset_events subscription_reset_previews subscription_reset_operations
waffo_pancake_webhook_receipts company_billing_profiles finance_ledger_entries finance_payment_methods
hero_sms_email_orders hero_sms_email_activations hero_sms_email_quota_ledgers hero_sms_sms_orders
hero_sms_sms_quota_ledgers hero_sms_provider_purchase_leases subscription_pre_consume_records
custom_oauth_providers user_oauth_bindings perf_metrics system_instances system_tasks system_task_locks
casbin_rules authz_roles assistant_leads assistant_profile_buckets assistant_user_profiles
assistant_user_profile_audits assistant_memories assistant_first_question_stats prompt_presets
prompt_preset_rows prompt_preset_stats prompt_conversion_refs prompt_conversation_refs
assistant_conversations assistant_support_requests assistant_history_messages assistant_secure_cards
assistant_security_incidents assistant_security_review_notices assistant_request_reviews
assistant_review_resets assistant_new_user_gifts assistant_weekly_discounts assistant_gift_risk_keys
assistant_gift_risk_memories assistant_registration_profiles assistant_registration_fingerprints
assistant_registration_cases assistant_registration_events advanced_security_events violation_fee_states
violation_fee_records violation_fee_appeals release_notes release_note_reads unified_todo_reads
l1_onboarding_todos public_relay_contributions public_relay_reports public_relay_tips
public_relay_reviews public_relay_preferences ratio_notifications ratio_deliveries
lmm_adoption_ledger""".split())


def summarize_denied(row):
    bools = ('owner_is_current', 'owner_is_session', 'owner_can_select', 'session_can_select',
             'owner_set_allowed', 'owner_usage', 'select_grant_option', 'current_equals_session')
    if not isinstance(row, dict) or any(type(row.get(k)) is not bool for k in bools):
        raise ValueError('invalid_relation_privilege_metadata')
    if any(not isinstance(row.get(k), str) or not row[k] or len(row[k]) > 63 for k in ('schema','table','owner')):
        raise ValueError('invalid_relation_identifier')
    out = {k:row[k] for k in bools}
    out['table_label'] = row['table'] if row['table'] in TABLE_LABELS else 'non_allowlisted_relation'
    out['relation_sha256'] = hashlib.sha256((row['schema']+'\0'+row['table']).encode()).hexdigest()
    out['table_name_sha256'] = hashlib.sha256(row['table'].encode()).hexdigest()
    return out


def diagnose(report_path):
    report = Path(report_path)
    if os.geteuid() != 0 or not report.is_file() or report.is_symlink():
        raise ValueError('private_root_audit_required')
    answer = {'operation':'inspect-backup-343','database_changed':False,'service_changed':False,
              'diagnostic_version':3,'previous_recovery':{}}
    for name in ('before-manifest.json','before-status.json','before-schema.database.dump','database.sha256','schema-created.json'):
        try:
            s = (AUDIT/name).lstat()
            answer['previous_recovery'][name] = {'exists':True,'regular':stat.S_ISREG(s.st_mode),'bytes':s.st_size}
        except FileNotFoundError:
            answer['previous_recovery'][name] = {'exists':False}
    try:
        database, env = connection(parse_environment(private_bytes(CONFIG).decode('utf-8')))
        u = urllib.parse.urlsplit(database)
        answer['database_host_is_loopback'] = u.hostname in ('localhost','127.0.0.1','::1')
        query = """SELECT coalesce(json_agg(x),'[]'::json) FROM (
          SELECT n.nspname AS schema,c.relname AS table,r.rolname AS owner,
            r.rolname=current_user AS owner_is_current,
            r.rolname=session_user AS owner_is_session,
            has_table_privilege(r.oid,c.oid,'SELECT') AS owner_can_select,
            has_table_privilege(session_user,c.oid,'SELECT') AS session_can_select,
            pg_has_role(r.oid,'SET') AS owner_set_allowed,
            pg_has_role(r.oid,'USAGE') AS owner_usage,
            has_table_privilege(c.oid,'SELECT WITH GRANT OPTION') AS select_grant_option,
            current_user=session_user AS current_equals_session
          FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
          JOIN pg_catalog.pg_roles r ON r.oid=c.relowner
          WHERE n.nspname=current_schema() AND c.relkind IN ('r','p','m')
            AND NOT has_table_privilege(c.oid,'SELECT') ORDER BY c.oid LIMIT 2) x"""
        outcome, stdout = run_private(report.parent,'denied-relation-metadata',
            ['/usr/bin/psql','-X','--no-password','-At','-v','ON_ERROR_STOP=1','-c',query,database],env,15)
        answer['denied_relation_probe'] = outcome
        if outcome.get('exit_code') != 0:
            raise ValueError('relation_probe_failed')
        rows = json.loads(stdout)
        if not isinstance(rows,list) or len(rows) != 1:
            raise ValueError('expected_exactly_one_denied_relation')
        row = rows[0]
        answer['denied_relation'] = summarize_denied(row)
        # The exact catalog result is private evidence for a later reviewed repair.
        fd = os.open(report.parent/'denied-relation.private.json',os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
        with os.fdopen(fd,'wb') as f:
            f.write(json.dumps(row,sort_keys=True).encode()+b'\n')
        if row['owner_set_allowed'] and row['owner_can_select'] and re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]{0,62}',row['owner']):
            target = report.parent/'owner-schema-only-probe.dump'
            if target.exists() or target.is_symlink():
                raise ValueError('diagnostic_target_exists')
            outcome, _ = run_private(report.parent,'owner-schema-only-probe',
                ['/usr/bin/pg_dump','--no-password','--schema-only','--format=custom',
                 '--role='+row['owner'],'--lock-wait-timeout=5s','--file='+str(target),database],env,30)
            answer['owner_role_schema_probe'] = outcome
        else:
            answer['owner_role_schema_probe'] = {'not_run':'no_available_owner_read_role'}
    except (ValueError,OSError,UnicodeError) as e:
        answer['diagnostic_error_type'] = type(e).__name__
    report.write_text(json.dumps(answer,sort_keys=True,indent=2)+'\n')


if __name__ == '__main__':
    diagnose(sys.argv[1])
PY
