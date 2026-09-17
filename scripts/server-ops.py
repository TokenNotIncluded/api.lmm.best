#!/usr/bin/env python3
"""Manual SSH transport, not a replacement for the native production deploy CLI."""
import base64
import hashlib
import html
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import tempfile
import urllib.request

REPOSITORY = "TokenNotIncluded/api.lmm.best"
OWNER = "LIghtJUNction"
HOST = "root@45.59.187.63"
PUBLIC_STATUS = "https://api.lmm.best/api/status"
TIMEOUTS = {"60", "180", "300", "600"}
SCRIPT_PATH = re.compile(r"scripts/server-repairs/[A-Za-z0-9][A-Za-z0-9_.-]{0,100}\.sh")


def validate(env):
    """Fail closed, including dispatch API inputs and the identity of rerun callers."""
    if (env.get("GITHUB_EVENT_NAME") != "workflow_dispatch"
            or env.get("GITHUB_REPOSITORY") != REPOSITORY
            or env.get("GITHUB_REF") != "refs/heads/main"):
        raise ValueError("Only a manual dispatch on this repository's main branch is allowed")
    owner = OWNER
    allowed = {owner.casefold()} | {
        actor.strip().casefold()
        for actor in env.get("OPS_ALLOWED_ACTORS", "").split(",") if actor.strip()
    }
    for field in ("GITHUB_ACTOR", "GITHUB_TRIGGERING_ACTOR"):
        if env.get(field, "").casefold() not in allowed:
            raise ValueError("The original actor and rerun actor must both be authorized")
    if not re.fullmatch(r"[0-9a-f]{40}", env.get("GITHUB_SHA", "")):
        raise ValueError("An immutable commit SHA is required")
    for field in ("GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT"):
        if not re.fullmatch(r"[1-9][0-9]{0,19}", env.get(field, "")):
            raise ValueError("Invalid run identity")
    operation = env.get("OPS_OPERATION", "")
    if operation not in ("diagnose", "repair"):
        raise ValueError("Unsupported operation")
    reason = env.get("OPS_REASON", "")
    if not reason.strip() or len(reason) > 240 or any(ord(c) < 32 or ord(c) == 127 for c in reason):
        raise ValueError("A single-line reason of 1-240 characters is required; do not include secrets")
    if env.get("OPS_TIMEOUT") not in TIMEOUTS:
        raise ValueError("Unsupported timeout")
    path = env.get("OPS_REPAIR_SCRIPT", "")
    if operation == "repair":
        if env.get("OPS_CONFIRM") != "api.lmm.best":
            raise ValueError("Production changes require confirm=api.lmm.best")
        if env["GITHUB_RUN_ATTEMPT"] != "1":
            raise ValueError("Never rerun a mutation: inspect its outcome and start a new dispatch")
        if not SCRIPT_PATH.fullmatch(path):
            raise ValueError("Repair must name one .sh file under scripts/server-repairs/")
    elif path or env.get("OPS_CONFIRM"):
        raise ValueError("Diagnosis must not include a repair script or mutation confirmation")


def load_repair(env):
    if env["OPS_OPERATION"] == "diagnose":
        return b""
    sha, path = env["GITHUB_SHA"], env["OPS_REPAIR_SCRIPT"]
    entry = subprocess.check_output(["git", "ls-tree", sha, "--", path], text=True).strip()
    if not entry or entry.split()[0] not in ("100644", "100755"):
        raise ValueError("Repair script is missing, a symlink, or not a regular committed file")
    spec = f"{sha}:{path}"
    size = int(subprocess.check_output(["git", "cat-file", "-s", spec], text=True))
    if not 1 <= size <= 65536:
        raise ValueError("Repair script must be 1-65536 bytes")
    payload = subprocess.check_output(["git", "show", spec])
    payload.decode("utf-8")
    if b"\0" in payload or b"\r" in payload:
        raise ValueError("Repair scripts must use UTF-8 text and LF line endings")
    # Never print source or shell diagnostics: committed scripts must not contain secrets.
    result = subprocess.run(["bash", "--noprofile", "--norc", "-n"], input=payload,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if result.returncode:
        raise ValueError("Repair script failed bash syntax validation")
    return payload


DIAGNOSE = r'''
set -Eeuo pipefail
umask 077
export PATH=/usr/local/sbin:/usr/local/bin:/usr/bin:/bin
printf 'utc='; date -u +%FT%TZ
printf '\nDisk usage\n'; df -h / /var /srv || true
printf '\nMemory\n'; free -m || true
printf '\nService metadata (no environment or command lines)\n'
systemctl show lmm-api.service nginx.service --no-pager \
  -p Id -p LoadState -p ActiveState -p SubState -p MainPID -p MemoryCurrent -p NRestarts || true
printf '\nInstalled packages\n'
for package in lmm-api-go-bin lmm-api-web-bin; do
  pacman -Q "$package" || true
done
printf '\nBackend selector\n'
readlink /usr/bin/lmm-api || true
local_health() {
  systemctl is-active --quiet lmm-api.service || return 1
  systemctl is-active --quiet nginx.service || return 1
  curl --fail --silent --output /dev/null --connect-timeout 3 --max-time 8 \
    http://127.0.0.1:3000/api/status
}
'''


def remote_script(env, payload):
    script = DIAGNOSE
    if env["OPS_OPERATION"] == "diagnose":
        return (script + '\nlocal_health\nprintf "local_http_health=ok\\n"\n').encode()
    digest = hashlib.sha256(payload).hexdigest()
    audit_id = env["GITHUB_RUN_ID"] + "-" + env["GITHUB_RUN_ATTEMPT"]
    metadata = {key: env[key] for key in ("GITHUB_ACTOR", "GITHUB_TRIGGERING_ACTOR", "GITHUB_SHA",
                "GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT", "OPS_REASON", "OPS_REPAIR_SCRIPT")}
    metadata["script_sha256"] = digest
    # All input is validated and shell-quoted. The payload is data, not runner-side code.
    script += '\n' + "\n".join([
        "command -v flock >/dev/null",
        "exec 9>/run/lock/lmm-server-ops.lock",
        "flock --nonblock 9 || { printf 'another manual operation holds the lock\\n'; exit 75; }",
        "test ! -L /var/log/lmm-server-ops",
        "install -d -m 700 /var/log/lmm-server-ops",
        f"audit=/var/log/lmm-server-ops/{audit_id}",
        'mkdir -m 700 "$audit"',  # Existing run ID is never reused or silently overwritten.
        f"printf '%s\\n' {shlex.quote(json.dumps(metadata))} >\"$audit/context.json\"",
        f"printf '%s' '{base64.b64encode(payload).decode()}' | base64 --decode >\"$audit/repair.sh\"",
        f"printf '%s  %s\\n' '{digest}' \"$audit/repair.sh\" | sha256sum --check --status -",
        'export LMM_OPS_REPORT="$audit/public-report.txt"',
        ': >"$LMM_OPS_REPORT"',
        'printf "repair_started=true\\n"',
        'if bash --noprofile --norc -euo pipefail "$audit/repair.sh" >"$audit/repair.log" 2>&1; then',
        '  result=0',
        'else',
        '  result=$?',
        'fi',
        'printf "%s\\n" "$result" >"$audit/exit-code"',
        'printf "repair_exit_code=%s\\nprivate_log=%s/repair.log\\n" "$result" "$audit"',
        '# Publish only a deliberately prepared public report, never the raw repair output.',
        'if [[ -f "$LMM_OPS_REPORT" && ! -L "$LMM_OPS_REPORT" ]]; then',
        '  head -c 16384 "$LMM_OPS_REPORT"',
        '  printf "\\n"',
        'fi',
        'healthy=false',
        'for attempt in 1 2 3 4 5 6; do',
        '  if local_health; then healthy=true; break; fi',
        '  sleep 2',
        'done',
        'printf "local_http_health=%s\\n" "$healthy"',
        'if [[ "$result" != 0 ]]; then exit "$result"; fi',
        '[[ "$healthy" == true ]]',
    ]) + "\n"
    return script.encode()


def safe_output(raw):
    """Neutralize workflow commands; public reports are intentionally not secret dumps."""
    text = raw[:65536].decode("utf-8", errors="replace")
    text = re.sub(r"[\x00-\x08\x0b-\x1f\x7f]", "", text)
    return "\n".join("remote | " + line for line in text.splitlines())


def public_health():
    try:
        # Do not inherit proxy credentials from the controller environment.
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with opener.open(PUBLIC_STATUS, timeout=12) as response:
            data = response.read(262145)
            return (response.status == 200 and len(data) <= 262144
                    and json.loads(data).get("success") is True)
    except (OSError, ValueError, AttributeError):
        return False


def execute(env, payload):
    for key in ("PRODUCTION_SSH_PRIVATE_KEY", "PRODUCTION_SSH_KNOWN_HOSTS"):
        if not env.get(key, "").strip():
            raise ValueError(f"Missing production secret: {key}")
    timeout = int(env["OPS_TIMEOUT"])
    # The private key never enters the repository, command arguments, or remote environment.
    with tempfile.TemporaryDirectory(prefix="lmm-server-ops-") as directory:
        key = Path(directory, "identity")
        known = Path(directory, "known_hosts")
        for path, content in ((key, env["PRODUCTION_SSH_PRIVATE_KEY"]),
                              (known, env["PRODUCTION_SSH_KNOWN_HOSTS"])):
            path.write_text(content.strip() + "\n", encoding="utf-8")
            path.chmod(0o600)
        command = ["ssh", "-F", "/dev/null", "-T", "-p", "222", "-i", str(key)]
        for option in ("BatchMode=yes", "IdentitiesOnly=yes", "IdentityAgent=none",
                       "StrictHostKeyChecking=yes", f"UserKnownHostsFile={known}",
                       "GlobalKnownHostsFile=/dev/null", "ConnectTimeout=15", "ConnectionAttempts=1",
                       "ServerAliveInterval=15", "ServerAliveCountMax=2", "LogLevel=ERROR"):
            command.extend(["-o", option])
        command.extend([HOST, "env -i PATH=/usr/local/sbin:/usr/local/bin:/usr/bin:/bin "
                        f"HOME=/root LC_ALL=C timeout --signal=TERM --kill-after=10s {timeout}s bash -se"])
        clean_env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "LC_ALL": "C"}
        result = subprocess.run(command, input=remote_script(env, payload),
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                env=clean_env, timeout=timeout + 45)
        print(safe_output(result.stdout))
        return result.returncode


def main():
    env = dict(os.environ)
    result, public_ok, digest = 1, None, "not applicable"
    try:
        validate(env)
        payload = load_repair(env)
        if payload:
            digest = hashlib.sha256(payload).hexdigest()
        if sys.argv[1:] == ["--validate-only"]:
            print(f"Validated {env['OPS_OPERATION']}; script_sha256={digest}")
            return 0
        if sys.argv[1:]:
            raise ValueError("Unknown controller argument")
        result = execute(env, payload)
        public_ok = public_health()
        print(f"public_status_success={str(public_ok).lower()}")
        if not public_ok and result == 0:
            result = 1
    except subprocess.TimeoutExpired:
        print("Operation timed out; server changes may already have happened. Do not rerun a repair.")
        result = 124
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        # Never print subprocess source, environment, or credential contents.
        print(f"Controller stopped: {error}" if isinstance(error, ValueError)
              else f"Controller stopped: {type(error).__name__}; no automatic retry")
    finally:
        summary = env.get("GITHUB_STEP_SUMMARY")
        if summary and sys.argv[1:] != ["--validate-only"]:
            record = {key: env.get(key, "") for key in
                      ("OPS_OPERATION", "OPS_REASON", "GITHUB_ACTOR", "GITHUB_SHA", "OPS_REPAIR_SCRIPT")}
            record.update(script_sha256=digest, exit_code=result, public_health=public_ok)
            with open(summary, "a", encoding="utf-8") as handle:
                handle.write("## Manual server operation\n\n<pre>" +
                             html.escape(json.dumps(record, indent=2, ensure_ascii=False)) + "</pre>\n")
    return result if 0 <= result <= 255 else 1


if __name__ == "__main__":
    sys.exit(main())
