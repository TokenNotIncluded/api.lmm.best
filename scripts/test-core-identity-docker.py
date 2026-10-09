#!/usr/bin/env python3
"""Run checked-in identity Compose files using disposable projects and secrets."""
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


def run(*args, data=None, env=None):
    result = subprocess.run(args, cwd=ROOT, input=data, text=True,
                            stdout=subprocess.PIPE, check=True, env=env)
    return result.stdout.strip()


def request(url, token=None, method="GET", body=None):
    headers = {"Authorization": "Bearer " + token} if token else {}
    payload = None
    if body is not None:
        payload = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=payload, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=10) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def wait(url, token=None):
    last = "no response"
    for _ in range(120):
        try:
            status, _ = request(url, token)
            if status == 200:
                return
            last = f"HTTP {status}"
        except (OSError, TimeoutError) as error:
            last = type(error).__name__
        time.sleep(0.5)
    raise RuntimeError(f"test service did not become available: {last}")


def main():
    suffix = secrets.token_hex(6)
    projects = {"core": "lmm-identity-test-" + suffix,
                "extensions": "lmm-identity-ext-test-" + suffix}
    with tempfile.TemporaryDirectory(prefix="lmm-identity-test-") as directory:
        temp = Path(directory)
        password = secrets.token_hex(32)
        for name, value in {
            "database-password": password,
            "database-url": f"postgres://lmm_core:{password}@core-db:5432/lmm_core",
            "extension-token": secrets.token_hex(32),
        }.items():
            path = temp / name
            path.write_text(value)
            # Compose file secrets are bind mounts. The enclosing directory is 0700.
            path.chmod(0o444)
        env = dict(os.environ,
                   LMM_CORE_DATABASE_URL_SECRET_FILE=str(temp / "database-url"),
                   LMM_CORE_DATABASE_PASSWORD_SECRET_FILE=str(temp / "database-password"))
        core = json.loads(run("docker", "compose", "-p", projects["core"],
                              "-f", str(DOCKER / "compose.core.yml"),
                              "-f", str(DOCKER / "compose.identity.yml"),
                              "--profile", "tools", "config", "--format", "json", env=env))
        ext = json.loads(run("docker", "compose", "-p", projects["extensions"],
                             "-f", str(DOCKER / "compose.extensions.yml"),
                             "config", "--format", "json"))
        assert set(core["services"]) == {"core", "core-db", "core-admin"}
        assert set(ext["services"]) == {"extensions"}
        assert core["networks"]["identity"]["internal"]
        assert not core["services"]["core-db"].get("ports")
        assert set(ext["services"]["extensions"]["networks"]) == {"core"}
        for data, group in ((core, "core"), (ext, "extensions")):
            data["name"] = projects[group]
            data["networks"]["core"]["name"] = projects["core"] + "-runtime"
            for service in data["services"].values():
                service["restart"] = "no"
                service["stop_grace_period"] = "10s"
            spec = data["services"][group]
            spec["image"] = projects[group] + ":test"
            spec["ports"] = [{"target": 8080 if group == "core" else 8081,
                              "published": "0", "host_ip": "127.0.0.1", "protocol": "tcp"}]
        core["services"]["core-admin"]["image"] = core["services"]["core"]["image"]
        core["networks"]["identity"]["name"] = projects["core"] + "-database"
        core["volumes"]["core-identity-data"]["name"] = projects["core"] + "-data"
        ext["secrets"]["extension_token"]["file"] = str(temp / "extension-token")
        for group, data in (("core", core), ("extensions", ext)):
            (temp / (group + ".json")).write_text(json.dumps(data))

        def compose(group, *args, data=None):
            return run("docker", "compose", "-f", str(temp / (group + ".json")), *args, data=data)

        def endpoint(group):
            port = 8080 if group == "core" else 8081
            return "http://" + compose(group, "port", group, str(port))

        def inspect(service):
            return json.loads(run("docker", "inspect", compose("core", "ps", "-q", service)))[0]

        def unchanged(before, after):
            assert before["Id"] == after["Id"]
            assert before["State"]["StartedAt"] == after["State"]["StartedAt"]
            assert before["RestartCount"] == after["RestartCount"]

        try:
            compose("core", "build", "core")
            compose("core", "up", "-d", "--wait", "core-db")
            compose("core", "run", "--rm", "-T", "core-admin", "migrate")
            compose("core", "run", "--rm", "-T", "core-admin", "migrate")
            session = json.loads(compose("core", "run", "--rm", "-T", "core-admin", "bootstrap-user",
                                         data=json.dumps({"user_id": 1, "platform_level": 1})))
            compose("core", "up", "-d", "core")
            url = endpoint("core")
            wait(url + "/health/live")
            status, payload = request(url + "/core/v1/keys", session["secret"], "POST",
                                      {"owner": {"kind": "personal", "id": 1}, "ttl_seconds": 3600})
            assert status == 201, status
            key = json.loads(payload)
            assert request(url + "/core/v1/identity", key["secret"])[0] == 200
            # Docker may assign a new host port after restart when published=0.
            old_url = url
            compose("core", "restart", "core")
            url = endpoint("core")
            print(f"Core restart endpoint: {old_url} -> {url}", flush=True)
            wait(url + "/core/v1/identity", key["secret"])
            print("PASS: identity and keys survived the core process restart", flush=True)
            before = inspect("core")
            database_before = inspect("core-db")
            compose("extensions", "up", "-d", "--build", "extensions")
            wait(endpoint("extensions") + "/health/live")
            ext_id = compose("extensions", "ps", "-q", "extensions")
            run("docker", "kill", ext_id)
            assert request(url + "/core/v1/identity", key["secret"])[0] == 200
            compose("extensions", "start", "extensions")
            wait(endpoint("extensions") + "/health/live")
            compose("extensions", "up", "-d", "--no-deps", "--force-recreate", "extensions")
            wait(endpoint("extensions") + "/health/live")
            assert compose("extensions", "ps", "-q", "extensions") != ext_id
            unchanged(before, inspect("core"))
            unchanged(database_before, inspect("core-db"))
            assert request(url + "/core/v1/identity", key["secret"])[0] == 200
            for path in ("/health/ready", "/v1/models"):
                assert request(url + path, key["secret"])[0] == 503
            compose("core", "stop", "core-db")
            assert request(url + "/core/v1/identity", key["secret"])[0] == 503
            assert request(url + "/health/live")[0] == 200
            compose("core", "start", "core-db")
            wait(url + "/core/v1/identity", key["secret"])
            assert request(url + f'/core/v1/credentials/{key["id"]}', session["secret"], "DELETE")[0] == 204
            assert request(url + "/core/v1/identity", key["secret"])[0] == 401
            print("PASS: explicit migrations, persistent keys, core/database isolation, database failure and revocation", flush=True)
        except Exception:
            # No inspect environment or request headers: they can contain secrets.
            subprocess.run(["docker", "compose", "-f", str(temp / "core.json"),
                            "logs", "--tail", "30", "core"], cwd=ROOT, check=False)
            raise
        finally:
            for group in ("extensions", "core"):
                subprocess.run(["docker", "compose", "-f", str(temp / (group + ".json")),
                                "down", "--remove-orphans", "--volumes"], cwd=ROOT, check=False)


if __name__ == "__main__":
    main()
