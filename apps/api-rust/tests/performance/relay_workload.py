"""Actual authenticated relay workload, including durable accounting checks."""

import hashlib
import http.client
import json
from pathlib import Path
import secrets
import time
from urllib.parse import urlsplit

import compare_backends as compare


REQUEST_PATH = "/v1/chat/completions"
MODEL = "gpt-4o-mini"
QUOTA_PER_REQUEST = 640
INITIAL_QUOTA = 1_000_000_000


def sql_string(value):
    return "'" + value.replace("'", "''") + "'"


def prepare(services, provider_url):
    """Seed equivalent independent principals before either serving process starts."""
    keys = {name: secrets.token_hex(24) for name in ("go", "rust")}
    for index, (name, key) in enumerate(keys.items(), 1001):
        services.sql(f"""
            INSERT INTO users(id,username,password,display_name,role,status,auth_version,
                quota,used_quota,request_count,"group",setting)
                VALUES({index},'relay-{name}','unused-local-fixture','Relay benchmark',1,1,1,
                    {INITIAL_QUOTA},0,0,'default','{{"billing_preference":"wallet_only"}}');
            INSERT INTO tokens(id,user_id,key,name,status,created_time,accessed_time,expired_time,
                remain_quota,used_quota,unlimited_quota,allow_ips,"group",model_limits_enabled)
                VALUES({index},{index},{sql_string(key)},'local benchmark',1,0,0,-1,
                    {INITIAL_QUOTA},0,FALSE,'','default',FALSE);
        """)
    services.sql(f"""
        INSERT INTO channels(id,type,key,status,name,weight,base_url,models,"group",used_quota,
            priority,auto_ban,channel_info,settings)
            VALUES(1001,1,'local-parity-upstream',1,'Local benchmark',100,{sql_string(provider_url)},
                '{MODEL}','default',0,100,0,'{{}}','{{}}');
        INSERT INTO abilities("group",model,channel_id,enabled,priority,weight)
            VALUES('default','{MODEL}',1001,TRUE,100,100);
    """)
    options = {
        "ModelRatio": json.dumps({MODEL: 1}), "CompletionRatio": json.dumps({MODEL: 1}),
        "ModelPrice": "{}", "GroupRatio": '{"default":1}', "PreConsumedQuota": "500",
        "QuotaPerUnit": "500000", "LogConsumeEnabled": "true",
        "ModelRequestRateLimitCount": "0", "ModelRequestRateLimitSuccessCount": "0",
        "developer_access_setting.paid_activation_enabled": "false",
        "fetch_setting.allow_private_ip": "true",
    }
    for key, value in options.items():
        services.sql(f"INSERT INTO options(key,value) VALUES({sql_string(key)},{sql_string(value)}) "
                     "ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value;")
    return keys


def request(base, path, body=None, headers=None):
    url = urlsplit(base)
    if url.scheme != "http" or url.hostname not in ("127.0.0.1", "::1") or url.username:
        raise ValueError("relay benchmark accepts only literal loopback HTTP")
    connection = http.client.HTTPConnection(url.hostname, url.port, timeout=30)
    try:
        connection.request("POST" if body is not None else "GET", path, body, headers or {})
        response = connection.getresponse()
        raw = response.read(4 * 1024 * 1024 + 1)
        if response.status != 200 or len(raw) > 4 * 1024 * 1024:
            raise RuntimeError(f"local relay request failed with status {response.status}")
        return json.loads(raw)
    finally:
        connection.close()


def snapshot(services, user_id):
    # These are actual accounting rows, not the relay HTTP success envelope.
    sql = f"""SELECT json_build_object(
        'quota',u.quota,'used_quota',u.used_quota,'request_count',u.request_count,
        'token_quota',t.remain_quota,'token_used',t.used_quota,
        'log_count',(SELECT COUNT(*) FROM logs WHERE user_id=u.id AND type=2),
        'log_quota',(SELECT COALESCE(SUM(quota),0) FROM logs WHERE user_id=u.id AND type=2),
        'prompt_tokens',(SELECT COALESCE(SUM(prompt_tokens),0) FROM logs WHERE user_id=u.id AND type=2),
        'completion_tokens',(SELECT COALESCE(SUM(completion_tokens),0) FROM logs WHERE user_id=u.id AND type=2),
        'channel_quota',(SELECT used_quota FROM channels WHERE id=1001),
        'pending_settlements',(SELECT COUNT(*) FROM relay_settlement_records
            WHERE user_id=u.id AND status IN ('reserved','settling'))
        ) FROM users u JOIN tokens t ON t.user_id=u.id WHERE u.id={int(user_id)};"""
    return json.loads(services.sql(sql))


def assert_accounting(services, user_id, completed, timeout=15, total_completed=None):
    quota = completed * QUOTA_PER_REQUEST
    expected = {"quota": INITIAL_QUOTA - quota, "used_quota": quota,
                "request_count": completed, "token_quota": INITIAL_QUOTA - quota,
                "token_used": quota, "log_count": completed, "log_quota": quota,
                "prompt_tokens": completed * 512, "completion_tokens": completed * 128,
                "channel_quota": (completed if total_completed is None else total_completed) * QUOTA_PER_REQUEST,
                "pending_settlements": 0}
    # Go persists consume logs asynchronously. Wait for all side effects to
    # become visible, and include this duration beside HTTP timing.
    deadline = time.monotonic() + timeout
    while True:
        actual = snapshot(services, user_id)
        if actual == expected:
            return actual
        if time.monotonic() >= deadline:
            raise RuntimeError(f"relay accounting mismatch for user {user_id}: expected {expected}, observed {actual}")
        time.sleep(.025)


def run(args, services, provider_url, keys, listeners):
    report = {"valid": False, "scope": "authenticated nonstream chat relay with real wallet/token/log commits",
              "model": MODEL, "quota_per_request": QUOTA_PER_REQUEST,
              "limitations": ["deterministic loopback provider; no real model latency or paid API calls",
                              "one owner/key per backend: concurrent requests contend on that owner's balance",
                              "excludes streaming, subscription funding, other model protocols, and deployments"],
              "cold_requests": [], "runs": [], "accounting": {}, "provider": {}}
    report["backends"] = {name: {"binary_sha256": hashlib.sha256(Path(f"/proc/{pid}/exe").read_bytes()).hexdigest()}
                          for name, (_, pid) in listeners.items()}
    output = args.output_dir.resolve() / "relay-comparison.json"

    def persist():
        output.write_text(json.dumps(report, indent=2) + "\n")

    persist()
    payload = json.dumps({"model": MODEL, "max_tokens": 256,
                          "messages": [{"role": "user", "content": "Explain durable database transactions. " * 64}]}).encode()
    request_file = services.runtime / "relay-request.json"
    request_file.write_bytes(payload)
    expected_file = services.runtime / "relay-expected.json"
    report["request_sha256"] = hashlib.sha256(payload).hexdigest()
    completed = {name: 0 for name in listeners}
    identities = {name: compare.process_sample(value[1])["start_ticks"] for name, value in listeners.items()}
    before_provider = request(provider_url, "/stats")
    bodies = {}
    header_files = {}
    for name, (base, pid) in listeners.items():
        headers = {"Authorization": "Bearer sk-" + keys[name], "Content-Type": "application/json"}
        header_file = services.runtime / f"{name}-relay-headers.json"
        header_file.write_text(json.dumps(headers))
        header_file.chmod(0o600)
        header_files[name] = header_file
        before = compare.process_sample(pid, identities[name])
        start = time.monotonic()
        bodies[name] = request(base, REQUEST_PATH, payload, headers)
        elapsed = time.monotonic() - start
        after = compare.process_sample(pid, identities[name])
        completed[name] += 1
        settled_start = time.monotonic()
        account = assert_accounting(services, 1001 if name == "go" else 1002, completed[name],
                                    total_completed=sum(completed.values()))
        report["cold_requests"].append({"backend": name, "http_seconds": elapsed,
            "after_http_settlement_wait_seconds": time.monotonic() - settled_start,
            "before": before, "after": after, "accounting": account})
        persist()
    if bodies["go"] != bodies["rust"] or bodies["go"].get("usage") != {
            "prompt_tokens": 512, "completion_tokens": 128, "total_tokens": 640}:
        report["body_differential"] = bodies
        persist()
        raise RuntimeError("relay bodies differ or usage is incorrect; refusing throughput comparison")
    expected_file.write_text(json.dumps(bodies["go"]))
    for concurrency in args.concurrency:
        for round_index in range(args.rounds + 1):
            warmup = round_index == 0
            count = min(100, args.relay_requests) if warmup else args.relay_requests
            for name in (["go", "rust"] if round_index % 2 == 0 else ["rust", "go"]):
                base, pid = listeners[name]
                result = compare.run_load(args.driver.resolve(), base, pid, identities[name], REQUEST_PATH,
                                          expected_file, count, concurrency, request_file, header_files[name])
                result.update({"backend": name, "round": round_index, "warmup": warmup})
                report["runs"].append(result)
                persist()
                if not result["valid"]:
                    raise RuntimeError(f"{name} relay workload had HTTP failures: {result['errors']}")
                completed[name] += count
                settle_start = time.monotonic()
                report["accounting"][name] = assert_accounting(services, 1001 if name == "go" else 1002, completed[name],
                                                              total_completed=sum(completed.values()))
                result["after_http_settlement_wait_seconds"] = time.monotonic() - settle_start
                result["durable_requests_per_second"] = count / (result["elapsed_seconds"] + result["after_http_settlement_wait_seconds"])
                persist()
                print(f"{name} relay c={concurrency} round={round_index}: "
                      f"{result['successful_requests_per_second']:.0f} req/s, "
                      f"p95={result['latency_ms']['p95']:.3f} ms; accounting verified", flush=True)
    after_provider = request(provider_url, "/stats")
    report["provider"] = {"before": before_provider, "after": after_provider,
                          "expected_completed_delta": sum(completed.values())}
    if after_provider["rejected"] != before_provider["rejected"] or after_provider["completed"] - before_provider["completed"] != sum(completed.values()):
        persist()
        raise RuntimeError("provider calls differ from completed relay requests")
    report["valid"] = True
    persist()
