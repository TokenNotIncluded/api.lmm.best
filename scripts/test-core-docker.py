#!/usr/bin/env python3
"""Disposable real-container isolation test. Never uses the production deployment."""
import json
from pathlib import Path
import secrets
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]

def run(*args):
    result = subprocess.run(args, cwd=ROOT, text=True, stdout=subprocess.PIPE, check=True)
    return result.stdout.strip()

def response(url, credential=None):
    headers = {"Authorization": "Bearer " + credential} if credential else {}
    try:
        with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=3) as res:
            return res.status, res.read().decode()
    except urllib.error.HTTPError as error:
        return error.code, error.read().decode()

def wait(url):
    for _ in range(120):
        try:
            if response(url)[0] == 200:
                return
        except (OSError, TimeoutError):
            pass
        time.sleep(0.5)
    raise RuntimeError("container did not become live")

def main():
    suffix = secrets.token_hex(6)
    core_project, ext_project = "lmm-test-core-" + suffix, "lmm-test-ext-" + suffix
    docker = ROOT / "deployment/docker"
    def config(file, project):
        return json.loads(run("docker", "compose", "-p", project, "-f", str(docker/file), "config", "--format", "json"))
    # Validate the actual checked-in YAML before adapting only test identity/ports/secrets.
    core, ext = config("compose.core.yml", core_project), config("compose.extensions.yml", ext_project)
    assert set(core["services"]) == {"core"}
    assert set(ext["services"]) == {"extensions"}
    network = "lmm-test-network-" + suffix
    with tempfile.TemporaryDirectory(prefix="lmm-core-isolation-") as directory:
        temp = Path(directory)
        token = secrets.token_hex(32)
        secret = temp / "token"
        secret.write_text(token)
        secret.chmod(0o444)
        for data, project, service, port in ((core, core_project, "core", 8080), (ext, ext_project, "extensions", 8081)):
            data["name"] = project
            spec = data["services"][service]
            spec["image"] = project + ":test"
            spec["ports"] = [{"target": port, "published": "0", "host_ip": "127.0.0.1", "protocol": "tcp"}]
            spec["restart"] = "no"  # Test a crash that stays down until explicitly restarted.
            spec["stop_grace_period"] = "10s"
            for value in data["networks"].values():
                value["name"] = network
            if service == "extensions":
                for value in data["secrets"].values():
                    value["file"] = str(secret)
            (temp / (service + ".json")).write_text(json.dumps(data))
        def compose(service, *args):
            return run("docker", "compose", "-f", str(temp / (service + ".json")), *args)
        def endpoint(service, port):
            return "http://" + compose(service, "port", service, str(port))
        try:
            compose("core", "up", "-d", "--build", "core")
            core_id = compose("core", "ps", "-q", "core")
            before = json.loads(run("docker", "inspect", core_id))[0]
            url = endpoint("core", 8080)
            wait(url + "/health/live")
            assert response(url + "/health/ready")[0] == 503
            assert response(url + "/v1/models")[0] == 503
            compose("extensions", "up", "-d", "--build", "extensions")
            ext_url = endpoint("extensions", 8081)
            wait(ext_url + "/health/live")
            assert response(ext_url + "/extensions/v1/modules")[0] == 401
            status, body = response(ext_url + "/extensions/v1/modules", token)
            assert status == 200, (status, body)
            assert json.loads(body)["modules"] == []
            ext_id = compose("extensions", "ps", "-q", "extensions")
            run("docker", "kill", ext_id)
            assert response(url + "/health/live")[0] == 200
            compose("extensions", "start", "extensions")
            wait(endpoint("extensions", 8081) + "/health/live")
            compose("extensions", "up", "-d", "--no-deps", "--force-recreate", "extensions")
            wait(endpoint("extensions", 8081) + "/health/live")
            assert compose("extensions", "ps", "-q", "extensions") != ext_id
            assert compose("core", "ps", "-q", "core") == core_id
            after = json.loads(run("docker", "inspect", core_id))[0]
            assert after["State"]["Running"]
            assert after["State"]["StartedAt"] == before["State"]["StartedAt"]
            assert after["RestartCount"] == before["RestartCount"]
            assert response(url + "/health/live")[0] == 200
            print("PASS: real Docker builds, fail-closed core, authenticated host, extension crash/restart/recreate leaves core unchanged")
        finally:
            # Remove only random test projects; no default project or production volumes.
            for service in ("extensions", "core"):
                subprocess.run(["docker", "compose", "-f", str(temp / (service + ".json")), "down", "--remove-orphans"], cwd=ROOT, check=False)

if __name__ == "__main__":
    main()
