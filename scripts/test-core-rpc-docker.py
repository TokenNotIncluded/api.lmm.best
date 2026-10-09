#!/usr/bin/env python3
"""Real Go -> protobuf/gRPC -> Rust -> PostgreSQL in disposable stacks."""
import importlib.util
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
DOCKER = ROOT / "deployment/docker"
spec = importlib.util.spec_from_file_location("identity_test", ROOT / "scripts/test-core-identity-docker.py")
base = importlib.util.module_from_spec(spec)
spec.loader.exec_module(base)
run = base.run


def main():
    suffix = secrets.token_hex(5)
    projects = {"core": "lmm-rpc-core-" + suffix, "extensions": "lmm-rpc-ext-" + suffix}
    with tempfile.TemporaryDirectory(prefix="lmm-rpc-test-") as directory:
        temp = Path(directory)
        password, rpc_token, ext_token = (secrets.token_hex(32) for _ in range(3))
        for name, value in {
            "database-password": password,
            "database-url": f"postgres://lmm_core:{password}@core-db:5432/lmm_core",
            "rpc-token": rpc_token, "extension-token": ext_token,
        }.items():
            path = temp / name
            path.write_text(value)
            path.chmod(0o444)
        env = dict(os.environ,
                   LMM_CORE_DATABASE_URL_SECRET_FILE=str(temp / "database-url"),
                   LMM_CORE_DATABASE_PASSWORD_SECRET_FILE=str(temp / "database-password"),
                   LMM_CORE_RPC_TOKEN_SECRET_FILE=str(temp / "rpc-token"),
                   LMM_CORE_RPC_VOLUME=projects["core"] + "-rpc")
        core = json.loads(run("docker", "compose", "-p", projects["core"],
                              "-f", str(DOCKER / "compose.core.yml"),
                              "-f", str(DOCKER / "compose.identity.yml"),
                              "-f", str(DOCKER / "compose.rpc-core.yml"),
                              "--profile", "tools", "config", "--format", "json", env=env))
        ext = json.loads(run("docker", "compose", "-p", projects["extensions"],
                             "-f", str(DOCKER / "compose.extensions.yml"),
                             "-f", str(DOCKER / "compose.rpc-extensions.yml"),
                             "config", "--format", "json", env=env))
        assert set(core["services"]) == {"core", "core-db", "core-admin"}
        assert set(ext["services"]) == {"extensions"}
        assert ext["volumes"]["core-rpc"]["external"]
        mount = next(v for v in ext["services"]["extensions"]["volumes"] if v["target"] == "/run/lmm-core-rpc")
        assert mount["read_only"]
        assert "identity" not in ext["networks"]
        for data, group in ((core, "core"), (ext, "extensions")):
            data["name"] = projects[group]
            data["networks"]["core"]["name"] = projects["core"] + "-runtime"
            for service in data["services"].values():
                service["restart"] = "no"
                service["stop_grace_period"] = "8s"
            item = data["services"][group]
            item["image"] = projects[group] + ":test"
            item["ports"] = [{"target": 8080 if group == "core" else 8081, "published": "0", "host_ip": "127.0.0.1", "protocol": "tcp"}]
        core["services"]["core-admin"]["image"] = core["services"]["core"]["image"]
        core["networks"]["identity"]["name"] = projects["core"] + "-database"
        core["volumes"]["core-identity-data"]["name"] = projects["core"] + "-data"
        ext["secrets"]["extension_token"]["file"] = str(temp / "extension-token")
        for group, data in (("core", core), ("extensions", ext)):
            (temp / (group + ".json")).write_text(json.dumps(data))

        def compose(group, *args, data=None):
            return run("docker", "compose", "-f", str(temp / (group + ".json")), *args, data=data)

        def endpoint(group):
            port = "8080" if group == "core" else "8081"
            return "http://" + compose(group, "port", group, port)

        def snapshot(service):
            data = json.loads(run("docker", "inspect", compose("core", "ps", "-q", service)))[0]
            return data["Id"], data["State"]["StartedAt"], data["RestartCount"]

        def rpc(path, user=None, service=ext_token):
            headers = {"Authorization": "Bearer " + service}
            if user is not None:
                headers["X-LMM-User-Credential"] = user
            req = urllib.request.Request(endpoint("extensions") + "/extensions/v1/identity/" + path, headers=headers)
            try:
                with urllib.request.urlopen(req, timeout=5) as response:
                    return response.status, json.loads(response.read())
            except urllib.error.HTTPError as error:
                return error.code, json.loads(error.read())

        def wait_rpc(path, user=None):
            for _ in range(80):
                try:
                    status, payload = rpc(path, user)
                    if status == 200:
                        return payload
                except (OSError, TimeoutError):
                    pass
                time.sleep(0.25)
            raise AssertionError("protobuf round trip did not recover")

        def rewrite_token(value):
            path = temp / "rpc-token"
            path.chmod(0o600)
            path.write_text(value)
            path.chmod(0o444)

        try:
            compose("core", "build", "core")
            compose("core", "up", "-d", "--wait", "core-db")
            compose("core", "run", "--rm", "-T", "core-admin", "migrate")
            user_id = 9007199254740993
            session = json.loads(compose("core", "run", "--rm", "-T", "core-admin", "bootstrap-user",
                                         data=json.dumps({"user_id": user_id, "platform_level": 1})))
            compose("core", "up", "-d", "core")
            base.wait(endpoint("core") + "/health/live")
            status, body = base.request(endpoint("core") + "/core/v1/teams", session["secret"], "POST")
            assert status == 201
            team_id = json.loads(body)["id"]
            status, body = base.request(endpoint("core") + "/core/v1/keys", session["secret"], "POST",
                                        {"owner": {"kind": "team", "id": team_id}, "ttl_seconds": 3600})
            assert status == 201
            key = json.loads(body)
            compose("extensions", "up", "-d", "--build", "extensions")
            base.wait(endpoint("extensions") + "/health/live")
            capabilities = wait_rpc("capabilities")
            assert capabilities["protocol_major"] == 1 and capabilities["identity_available"]
            actor = wait_rpc("self", key["secret"])
            assert actor["user_id"] == str(user_id)
            assert actor["owner"] == {"kind": "ACCOUNT_KIND_TEAM", "id": str(team_id)}
            assert actor["funding_accounts"] == [actor["owner"]]
            teams = wait_rpc("teams", session["secret"])["teams"]
            assert teams[0]["id"] == str(team_id) and teams[0]["role"] == "TEAM_ROLE_OWNER"
            assert rpc("teams", key["secret"])[0] == 403
            assert rpc("self")[0] == 401
            assert rpc("self", key["secret"], service="wrong")[0] == 401
            compose("extensions", "exec", "-T", "extensions", "sh", "-c",
                    "if touch /run/lmm-core-rpc/forbidden 2>/dev/null; then exit 1; fi")
            print("PASS: real protobuf round trips, large IDs, native team grants, separate credentials, read-only socket", flush=True)
            before, database_before = snapshot("core"), snapshot("core-db")
            ext_id = compose("extensions", "ps", "-q", "extensions")
            run("docker", "kill", ext_id)
            assert base.request(endpoint("core") + "/core/v1/identity", key["secret"])[0] == 200
            compose("extensions", "start", "extensions")
            wait_rpc("self", key["secret"])
            compose("extensions", "up", "-d", "--no-deps", "--force-recreate", "extensions")
            wait_rpc("self", key["secret"])
            assert snapshot("core") == before and snapshot("core-db") == database_before
            rewrite_token(secrets.token_hex(32))
            compose("extensions", "up", "-d", "--no-deps", "--force-recreate", "extensions")
            base.wait(endpoint("extensions") + "/health/live")
            assert rpc("capabilities")[0] == 401
            assert base.request(endpoint("core") + "/core/v1/identity", key["secret"])[0] == 200
            rewrite_token(rpc_token)
            compose("extensions", "up", "-d", "--no-deps", "--force-recreate", "extensions")
            wait_rpc("self", key["secret"])
            assert snapshot("core") == before and snapshot("core-db") == database_before
            print("PASS: extension kill/recreate and wrong service credential do not restart core/database", flush=True)
            ext_id = compose("extensions", "ps", "-q", "extensions")
            compose("core", "restart", "core")
            base.wait(endpoint("core") + "/health/live")
            wait_rpc("self", key["secret"])
            assert compose("extensions", "ps", "-q", "extensions") == ext_id
            compose("core", "stop", "core-db")
            started = time.monotonic()
            assert rpc("self", key["secret"])[0] in (503, 504)
            assert time.monotonic() - started < 4
            assert base.request(endpoint("core") + "/health/live")[0] == 200
            assert base.request(endpoint("extensions") + "/health/live")[0] == 200
            compose("core", "start", "core-db")
            wait_rpc("self", key["secret"])
            assert base.request(endpoint("core") + f'/core/v1/credentials/{key["id"]}', session["secret"], "DELETE")[0] == 204
            assert rpc("self", key["secret"])[0] == 401
            assert base.request(endpoint("core") + "/health/ready")[0] == 503
            assert base.request(endpoint("core") + "/v1/models", key["secret"])[0] == 503
            print("PASS: reconnect after core restart, bounded DB failure, recovery, immediate revocation, model traffic disabled", flush=True)
        finally:
            for group in ("extensions", "core"):
                subprocess.run(["docker", "compose", "-f", str(temp / (group + ".json")),
                                "down", "--remove-orphans", "--volumes"], cwd=ROOT, check=False)


if __name__ == "__main__":
    main()
