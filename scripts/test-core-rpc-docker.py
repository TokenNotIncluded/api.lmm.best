#!/usr/bin/env python3
"""Disposable fresh-install test: Go -> Protobuf -> Rust -> PostgreSQL."""
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
SECRET_NAMES = ("database-password", "database-url", "rpc-token", "extension-token")
PREPARE_SECRETS = """
for path in "$@"; do
    test -f "$path"
    test ! -L "$path"
    chown 65532:65532 "$path"
    chmod 0400 "$path"
done
"""
VERIFY_SECRETS = """
test "$(id -u):$(id -g)" = 65532:65532
for path in "$@"; do
    test -f "$path"
    test ! -L "$path"
    test "$(stat -c '%u:%g:%a' "$path")" = 65532:65532:400
    test -r "$path"
    cat "$path" > /dev/null
done
"""
ROTATE_SECRET = VERIFY_SECRETS + """
test "$#" = 1
fixture_secret_path=$1
trap 'chmod 0400 "$fixture_secret_path"' EXIT
trap 'exit 1' HUP INT TERM
chmod 0600 "$fixture_secret_path"
cat > "$fixture_secret_path"
chmod 0400 "$fixture_secret_path"
""" + VERIFY_SECRETS


def run(*args, data=None, env=None):
    return subprocess.run(args, cwd=ROOT, input=data, text=True,
                          stdout=subprocess.PIPE, check=True, env=env).stdout.strip()


def write_fixture_secrets(temp, values):
    for name in SECRET_NAMES:
        # Private from creation, including while the synthetic value is written.
        fd = os.open(temp / name, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as file:
            file.write(values[name])


def fixture_secrets(image, temp, names, script, *, data=None, prepare=False):
    # Mount only these disposable files, never their parent or any host secrets.
    # Compose file secrets retain bind-source ownership/mode, so uid/mode fields
    # in Compose alone cannot make host-owned files private and container-readable.
    args = ["docker", "run", "--rm", "-i", "--network", "none", "--read-only",
            "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true"]
    if prepare:
        args.extend(["--user", "0:0", "--cap-add", "CHOWN", "--cap-add", "FOWNER"])
    paths = []
    for name in names:
        assert name in SECRET_NAMES
        path = "/fixture-" + name
        mount = f"type=bind,source={temp / name},target={path}"
        if not prepare and data is None:
            mount += ",readonly"
        args.extend(["--mount", mount])
        paths.append(path)
    return run(*args, "--entrypoint", "/bin/sh", image, "-ec", script,
               "fixture-secrets", *paths, data=data)


def request(url, token=None, method="GET", body=None, user=None):
    headers = {"Authorization": "Bearer " + token} if token else {}
    if user is not None:
        headers["X-LMM-User-Credential"] = user
    payload = None
    if body is not None:
        payload = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=payload, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=5) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def main():
    suffix = secrets.token_hex(6)
    projects = {"core": "lmm-fresh-core-" + suffix, "extensions": "lmm-fresh-ext-" + suffix}
    with tempfile.TemporaryDirectory(prefix="lmm-fresh-test-") as directory:
        temp = Path(directory)
        password, rpc_token, ext_token = (secrets.token_hex(32) for _ in range(3))
        values = {"database-password": password,
                  "database-url": f"postgres://lmm_core:{password}@core-db:5432/lmm_core",
                  "rpc-token": rpc_token, "extension-token": ext_token}
        write_fixture_secrets(temp, values)
        env = dict(os.environ,
                   LMM_CORE_DATABASE_URL_SECRET_FILE=str(temp / "database-url"),
                   LMM_CORE_DATABASE_PASSWORD_SECRET_FILE=str(temp / "database-password"),
                   LMM_CORE_RPC_TOKEN_SECRET_FILE=str(temp / "rpc-token"),
                   LMM_CORE_RPC_VOLUME=projects["core"] + "-rpc")
        configs = {}
        for group, files in (("core", ["core", "identity", "rpc-core"]),
                             ("extensions", ["extensions", "rpc-extensions"])):
            args = ["docker", "compose", "-p", projects[group]]
            for name in files:
                args.extend(["-f", str(DOCKER / ("compose." + name + ".yml"))])
            data = json.loads(run(*args, "--profile", "tools", "config", "--format", "json", env=env))
            data["name"] = projects[group]
            data["networks"]["core"]["name"] = projects["core"] + "-runtime"
            for service in data["services"].values():
                service["restart"] = "no"
                service["stop_grace_period"] = "8s"
            service = data["services"][group]
            service["image"] = projects[group] + ":test"
            service["ports"] = [{"target": 8080 if group == "core" else 8081,
                                 "published": "0", "host_ip": "127.0.0.1", "protocol": "tcp"}]
            configs[group] = data
        core, ext = configs["core"], configs["extensions"]
        assert set(core["services"]) == {"core", "core-db", "core-init"}
        assert set(ext["services"]) == {"extensions"}
        assert not core["services"]["core-db"].get("ports")
        assert core["networks"]["identity"]["internal"]
        assert "identity" not in ext["networks"] and ext["volumes"]["core-rpc"]["external"]
        mount = next(v for v in ext["services"]["extensions"]["volumes"] if v["target"] == "/run/lmm-core-rpc")
        assert mount["read_only"]
        core["services"]["core-init"]["image"] = core["services"]["core"]["image"]
        core["networks"]["identity"]["name"] = projects["core"] + "-database"
        core["volumes"]["core-identity-data"]["name"] = projects["core"] + "-data"
        ext["secrets"]["extension_token"]["file"] = str(temp / "extension-token")
        for group, data in configs.items():
            (temp / (group + ".json")).write_text(json.dumps(data))

        def compose(group, *args, data=None):
            return run("docker", "compose", "-f", str(temp / (group + ".json")), *args, data=data)

        def endpoint(group):
            return "http://" + compose(group, "port", group, "8080" if group == "core" else "8081")

        def core_call(path, token=None, method="GET", body=None):
            return request(endpoint("core") + path, token, method, body)

        def rpc(path, user=None, token=ext_token):
            return request(endpoint("extensions") + "/extensions/v1/identity/" + path, token, user=user)

        def wait(call):
            for _ in range(100):
                try:
                    status, body = call()
                    if status == 200:
                        return json.loads(body)
                except (OSError, TimeoutError):
                    pass
                time.sleep(0.3)
            raise AssertionError("test endpoint did not become available")

        def snapshot(service):
            data = json.loads(run("docker", "inspect", compose("core", "ps", "-q", service)))[0]
            return data["Id"], data["State"]["StartedAt"], data["RestartCount"]

        def rewrite_rpc_token(value):
            # Rewrite the same inode: running Compose secret bind mounts must
            # observe the value, and it must stay private during both rotations.
            fixture_secrets(core["services"]["core"]["image"], temp, ("rpc-token",),
                            ROTATE_SECRET, data=value)

        try:
            compose("core", "build", "core")
            image = core["services"]["core"]["image"]
            fixture_secrets(image, temp, SECRET_NAMES, PREPARE_SECRETS, prepare=True)
            fixture_secrets(image, temp, SECRET_NAMES, VERIFY_SECRETS)
            compose("core", "up", "-d", "--wait", "core-db")
            compose("core", "run", "--rm", "-T", "--no-deps", "--entrypoint", "/bin/sh",
                    "core-init", "-ec", VERIFY_SECRETS, "fixture-secrets",
                    "/run/secrets/core_database_url")
            compose("core", "run", "--rm", "-T", "core-init", "init-db")
            try:
                compose("core", "run", "--rm", "-T", "core-init", "init-db")
            except subprocess.CalledProcessError:
                pass
            else:
                raise AssertionError("second initialization must reject the existing database")
            user_id = 9007199254740993
            session = json.loads(compose("core", "run", "--rm", "-T", "core-init", "bootstrap-user",
                                         data=json.dumps({"user_id": user_id, "platform_level": 1})))
            compose("core", "up", "-d", "core")
            wait(lambda: core_call("/health/live"))
            status, body = core_call("/core/v1/teams", session["secret"], "POST")
            assert status == 201
            team_id = json.loads(body)["id"]
            status, body = core_call("/core/v1/keys", session["secret"], "POST",
                                     {"owner": {"kind": "team", "id": team_id}, "ttl_seconds": 3600})
            assert status == 201
            key = json.loads(body)
            compose("extensions", "up", "-d", "--build", "extensions")
            capabilities = wait(lambda: rpc("capabilities"))
            assert capabilities["protocol_major"] == 1 and capabilities["identity_available"]
            actor = wait(lambda: rpc("self", key["secret"]))
            assert actor["user_id"] == str(user_id)
            assert actor["owner"] == {"kind": "ACCOUNT_KIND_TEAM", "id": str(team_id)}
            assert actor["funding_accounts"] == [actor["owner"]]
            teams = wait(lambda: rpc("teams", session["secret"]))["teams"]
            assert teams[0]["id"] == str(team_id) and teams[0]["role"] == "TEAM_ROLE_OWNER"
            assert rpc("teams", key["secret"])[0] == 403
            assert rpc("self")[0] == 401
            assert rpc("self", key["secret"], token="wrong")[0] == 401
            compose("extensions", "exec", "-T", "extensions", "sh", "-c",
                    "if touch /run/lmm-core-rpc/forbidden 2>/dev/null; then exit 1; fi")
            print("PASS: fresh installation, repeat-init rejection, native teams, protobuf and separate credentials", flush=True)
            before = {service: snapshot(service) for service in ("core", "core-db")}
            run("docker", "kill", compose("extensions", "ps", "-q", "extensions"))
            assert core_call("/core/v1/identity", key["secret"])[0] == 200
            compose("extensions", "start", "extensions")
            wait(lambda: rpc("self", key["secret"]))
            compose("extensions", "up", "-d", "--no-deps", "--force-recreate", "extensions")
            wait(lambda: rpc("self", key["secret"]))
            rewrite_rpc_token(secrets.token_hex(32))
            compose("extensions", "up", "-d", "--no-deps", "--force-recreate", "extensions")
            wait(lambda: request(endpoint("extensions") + "/health/live"))
            assert rpc("capabilities")[0] == 401
            assert core_call("/core/v1/identity", key["secret"])[0] == 200
            rewrite_rpc_token(rpc_token)
            compose("extensions", "up", "-d", "--no-deps", "--force-recreate", "extensions")
            wait(lambda: rpc("self", key["secret"]))
            assert all(snapshot(service) == value for service, value in before.items())
            print("PASS: extension restart, replacement and wrong service token do not restart core or database", flush=True)
            ext_id = compose("extensions", "ps", "-q", "extensions")
            compose("core", "restart", "core")
            wait(lambda: rpc("self", key["secret"]))
            assert compose("extensions", "ps", "-q", "extensions") == ext_id
            compose("core", "stop", "core-db")
            started = time.monotonic()
            assert rpc("self", key["secret"])[0] in (503, 504)
            assert time.monotonic() - started < 4
            assert core_call("/health/live")[0] == 200
            assert request(endpoint("extensions") + "/health/live")[0] == 200
            compose("core", "start", "core-db")
            wait(lambda: rpc("self", key["secret"]))
            assert core_call(f'/core/v1/credentials/{key["id"]}', session["secret"], "DELETE")[0] == 204
            assert rpc("self", key["secret"])[0] == 401
            assert core_call("/health/ready")[0] == 503
            assert core_call("/v1/models", key["secret"])[0] == 503
            print("PASS: reconnect, bounded database failure, persistent keys, immediate revocation and closed model routes", flush=True)
        finally:
            for group in ("extensions", "core"):
                subprocess.run(["docker", "compose", "-f", str(temp / (group + ".json")),
                                "down", "--remove-orphans", "--volumes"], cwd=ROOT, check=False)


if __name__ == "__main__":
    main()
