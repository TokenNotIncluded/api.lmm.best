#!/usr/bin/env python3
"""Private operator client for the opt-in Core abuse administration API.

This script never opens a database. Session bearer credentials stay local.
User moderation requires a server preview and an explicit artifact hash.
"""
from __future__ import annotations

import argparse
import hashlib
import hmac
import http.client
import ipaddress
import json
import os
import re
import stat
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path
from typing import Any

API = "/core/v1/admin/abuse"
USERS_API = "/core/v2/admin/abuse"
ARTIFACT_VERSION = 2
MAX_I64 = 9223372036854775807
MAX_BODY = 1024 * 1024
MAX_FILE = 1024 * 1024
MAX_TOKEN = 8192
TIMEOUT = 10.0
_OFFLINE_ALIAS_VALIDATION = object()
SHA256 = re.compile(r"^[a-f0-9]{64}$")


class ClientError(Exception):
    """A deliberately non-sensitive error safe to display."""


class ApiError(ClientError):
    def __init__(self, status: int, code: str = "UNRECOGNIZED_SERVER_ERROR"):
        self.status, self.code = status, code
        super().__init__(f"HTTP {status}: {code}")


class OutcomeUnknown(ClientError):
    pass


def require(ok: bool, message: str) -> None:
    if not ok:
        raise ClientError(message)


def integer(value: Any, name: str, minimum: int = 0) -> int:
    require(type(value) is int and value >= minimum, f"Invalid {name}")
    return value


def user_id_string(value: Any, *, wire_version: int = 2) -> str:
    """Compare IDs exactly without rewriting a legacy artifact or its hash."""
    if wire_version == 1:
        require(type(value) is int and 1 <= value <= MAX_I64, "Invalid legacy user ID")
        return str(value)
    require(wire_version == 2 and isinstance(value, str)
            and re.fullmatch(r"[1-9][0-9]{0,18}", value) is not None
            and int(value) <= MAX_I64, "Invalid v2 user ID")
    return value


def cli_user_id(value: Any, *, allow_zero: bool = False) -> str | None:
    require(type(value) is int and (0 if allow_zero else 1) <= value <= MAX_I64,
            "User ID/cursor outside supported i64 range")
    return None if value == 0 else str(value)


def unique_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        require(key not in result, "Duplicate JSON field")
        result[key] = value
    return result


def decode_json(raw: bytes) -> Any:
    try:
        value = json.loads(raw.decode("utf-8"), object_pairs_hook=unique_object,
                          parse_constant=lambda _: (_ for _ in ()).throw(ClientError("Non-finite JSON number")))
        canonical(value)  # Also reject unpaired Unicode surrogates before validation.
        return value
    except (UnicodeDecodeError, ValueError, RecursionError):
        raise ClientError("Invalid JSON response or input") from None


def canonical_bytes_no_newline(value: Any) -> bytes:
    try:
        return json.dumps(value, ensure_ascii=False, allow_nan=False,
                          sort_keys=True, separators=(",", ":")).encode("utf-8")
    except (ValueError, TypeError, RecursionError, UnicodeError):
        raise ClientError("Input cannot be encoded as bounded JSON") from None


def canonical(value: Any) -> bytes:
    return canonical_bytes_no_newline(value) + b"\n"


def policy_config_hash(config: Any) -> str:
    return hashlib.sha256(canonical_bytes_no_newline(config)).hexdigest()


def read_bounded(response: Any, limit: int, timeout: float) -> bytes:
    deadline = time.monotonic() + timeout
    chunks, total = [], 0
    # read1 performs one underlying read, so a drip-fed response cannot reset
    # the whole body deadline indefinitely. One final socket read may time out.
    reader = getattr(response, "read1", response.read)
    while True:
        require(time.monotonic() < deadline, "Response body deadline exceeded")
        chunk = reader(min(65536, limit + 1 - total))
        if not chunk:
            return b"".join(chunks)
        chunks.append(chunk)
        total += len(chunk)
        require(total <= limit, "Response is too large")


def origin_url(value: str, allow_loopback: bool) -> str:
    try:
        parsed = urllib.parse.urlsplit(value)
        port = parsed.port
        host = parsed.hostname
    except ValueError:
        raise ClientError("Invalid server origin") from None
    require(parsed.scheme in ("https", "http") and host is not None, "Server must be an HTTPS origin")
    require(not parsed.username and not parsed.password, "Server origin must not contain credentials")
    require(parsed.path in ("", "/") and not parsed.query and not parsed.fragment,
            "Server must be an origin without path, query or fragment")
    require(not any(ord(ch) < 33 or ord(ch) > 126 for ch in value), "Invalid server origin")
    if parsed.scheme == "http":
        try:
            loopback = ipaddress.ip_address(host).is_loopback
        except ValueError:
            loopback = False
        require(allow_loopback and loopback, "HTTP requires --allow-insecure-loopback and a literal loopback IP")
    require(port is None or 1 <= port <= 65535, "Invalid server port")
    host = host.lower()
    authority = f"[{host}]" if ":" in host else host
    if port is not None and port != (443 if parsed.scheme == "https" else 80):
        authority += f":{port}"
    return f"{parsed.scheme}://{authority}"


def private_read(path: str, limit: int = MAX_FILE) -> bytes:
    try:
        fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
    except OSError:
        raise ClientError("Cannot open private file (symlinks are forbidden)") from None
    try:
        info = os.fstat(fd)
        require(stat.S_ISREG(info.st_mode), "Private input must be a regular file")
        require(info.st_uid == os.getuid() and stat.S_IMODE(info.st_mode) & 0o077 == 0,
                "Private input must be owned by the current user with mode 0600 or stricter")
        require(info.st_size <= limit, "Private input is too large")
        with os.fdopen(fd, "rb", closefd=False) as source:
            data = source.read(limit + 1)
        require(len(data) <= limit, "Private input is too large")
        return data
    finally:
        os.close(fd)


def private_write(path: str, value: Any) -> str:
    data = canonical(value)
    require(len(data) <= MAX_FILE, "Artifact is too large")
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o600)
    except OSError:
        raise ClientError("Cannot create private artifact; existing files are never overwritten") from None
    try:
        with os.fdopen(fd, "wb", closefd=False) as target:
            target.write(data)
            target.flush()
            os.fsync(fd)
    finally:
        os.close(fd)
    return hashlib.sha256(data).hexdigest()


def bounded_stdin(limit: int, name: str) -> str:
    raw = sys.stdin.buffer.read(limit + 1)
    require(len(raw) <= limit, f"{name} is too long")
    try:
        return raw.decode("utf-8").strip()
    except UnicodeDecodeError:
        raise ClientError(f"{name} must be UTF-8") from None


def session_token(args: argparse.Namespace) -> str:
    if args.token_stdin:
        value = bounded_stdin(MAX_TOKEN, "Session token")
    elif args.credential_file:
        try:
            value = private_read(args.credential_file, MAX_TOKEN).decode("ascii").strip()
        except UnicodeDecodeError:
            raise ClientError("Invalid session credential") from None
    else:
        value = os.environ.get("LMM_ADMIN_SESSION", "").strip()
    require(0 < len(value) <= MAX_TOKEN and all(33 <= ord(c) <= 126 for c in value),
            "Set LMM_ADMIN_SESSION or use --credential-file/--token-stdin with a valid superadmin Session bearer")
    return value


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req: Any, fp: Any, code: int, msg: str, headers: Any, newurl: str) -> None:
        return None


def permitted_api_path(method: str, path: str) -> None:
    allowed = {(API + "/policies", "GET"), (API + "/policies", "PUT"),
               (USERS_API + "/users", "GET"), (USERS_API + "/users/preview", "POST"),
               (USERS_API + "/users/commit", "POST"), (USERS_API + "/audit", "GET")}
    if isinstance(path, str) and (path, method) in allowed:
        return
    if method == "GET" and isinstance(path, str):
        for prefix in (API, USERS_API):
            if path.startswith(prefix + "/operations/"):
                valid_operation_id(path.removeprefix(prefix + "/operations/"))
                return
        if path.startswith(USERS_API + "/users/"):
            user_id_string(path.removeprefix(USERS_API + "/users/"))
            return
    raise ClientError("Invalid API path or method")


class Client:
    def __init__(self, server: str, token: str, *, timeout: float = TIMEOUT):
        self.server, self.token, self.timeout = server, token, timeout
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def request(self, method: str, path: str, body: Any = None, query: Any = None) -> Any:
        permitted_api_path(method, path)
        url = self.server + path
        if query:
            url += "?" + urllib.parse.urlencode(query)
        data = canonical(body) if body is not None else None
        require(data is None or len(data) <= MAX_BODY, "Request is too large")
        headers = {"Authorization": "Bearer " + self.token, "Accept": "application/json"}
        if data is not None:
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with self.opener.open(request, timeout=self.timeout) as response:
                require(response.headers.get_content_type() == "application/json", "Expected JSON response")
                raw = read_bounded(response, MAX_BODY, self.timeout)
                return decode_json(raw)
        except urllib.error.HTTPError as exc:
            code = "UNRECOGNIZED_SERVER_ERROR"
            try:
                raw = read_bounded(exc, 8192, self.timeout)
                if len(raw) <= 8192:
                    parsed = decode_json(raw)
                    error = parsed.get("error") if isinstance(parsed, dict) else None
                    candidate = error.get("code") if isinstance(error, dict) else None
                    if isinstance(candidate, str) and candidate in {"ADMIN_ABUSE_INVALID", "ADMIN_ABUSE_UNAUTHORIZED", "ADMIN_ABUSE_FORBIDDEN",
                                     "ADMIN_ABUSE_NOT_FOUND", "ADMIN_ABUSE_OPERATION_NOT_FOUND", "ADMIN_ABUSE_CONFLICT",
                                     "ADMIN_ABUSE_PREVIEW_EXPIRED", "ADMIN_ABUSE_RATE_LIMITED", "ADMIN_ABUSE_UNAVAILABLE"}:
                        code = candidate
            except (ClientError, OSError, http.client.HTTPException):
                pass
            finally:
                exc.close()
            raise ApiError(exc.code, code) from None
        except (urllib.error.URLError, TimeoutError, OSError, ValueError, http.client.HTTPException):
            raise ClientError("Network request failed; server response is unavailable") from None

    def operation(self, operation_id: str, *, kind: str | None = None) -> Any | None:
        valid_operation_id(operation_id)
        require(kind in (None, "users", "policies"), "Invalid operation kind")
        # Manual lookup is the explicit mixed v2 route; known policy callers
        # retain v1. No HTTP status can select a different wire or replay a write.
        prefix = API if kind == "policies" else USERS_API
        try:
            result = self.request("GET", prefix + "/operations/" + operation_id)
        except ApiError as exc:
            if exc.status == 404 and exc.code == "ADMIN_ABUSE_OPERATION_NOT_FOUND":
                return None
            raise
        require(isinstance(result, dict), "Invalid operation response")
        return result


def valid_operation_id(value: Any) -> str:
    require(isinstance(value, str) and re.fullmatch(r"[A-Za-z0-9_.:-]{8,128}", value) is not None,
            "Invalid operation ID")
    return value


def safe_user(value: Any, *, detail: bool = False) -> dict[str, Any]:
    fields = {"user_id", "username", "email", "active", "platform_role", "revision", "marked", "policy_id"}
    require(isinstance(value, dict) and set(value) == (fields | {"version"} if detail else fields),
            "Invalid user response fields")
    if detail:
        require(type(value["version"]) is int and value["version"] == 2, "Invalid user detail version")
    user_id_string(value["user_id"])
    integer(value.get("revision"), "mark revision")
    require(isinstance(value.get("username"), str) and type(value.get("active")) is bool
            and isinstance(value.get("email"), str) and type(value.get("marked")) is bool
            and value.get("platform_role") in ("user", "admin", "superadmin"),
            "Invalid user state")
    require(value["policy_id"] is None or (isinstance(value["policy_id"], str)
            and re.fullmatch(r"[A-Za-z0-9_.-]{1,64}", value["policy_id"]) is not None), "Invalid user policy ID")
    require(value["marked"] == (value["policy_id"] is not None), "Inconsistent user mark and policy")
    return {key: value[key] for key in ("user_id", "username", "active", "platform_role", "revision", "marked", "policy_id")}


def private_user(value: Any, *, detail: bool = False) -> dict[str, Any]:
    result = safe_user(value, detail=detail)
    require(isinstance(value.get("email"), str), "Invalid user email")
    return {**result, "email": value["email"]}


def safe_operation(value: Any, operation_id: str, *, kind: str | None = None) -> dict[str, Any]:
    require(isinstance(value, dict) and value.get("kind") in ("policies", "users")
            and (kind is None or value["kind"] == kind), "Invalid operation kind")
    expected_version = 2 if value["kind"] == "users" else 1
    fields = {"version", "status", "operation_id", "kind", "policy_revision", "users"}
    require(type(value.get("version")) is int and value["version"] == expected_version
            and value.get("status") == "applied" and value.get("operation_id") == operation_id
            and isinstance(value.get("users"), list), "Invalid operation response")
    if value["kind"] == "policies":
        config_hash = value.get("applied_config_hash")
        if not isinstance(config_hash, str) or SHA256.fullmatch(config_hash) is None:
            raise OutcomeUnknown(f"Outcome unknown for policy operation {operation_id}: missing valid applied_config_hash; do not resend")
        require(set(value) == fields | {"applied_config_hash"} and value["users"] == [],
                "Invalid policy operation fields")
    else:
        require(set(value) == fields and len(value["users"]) <= 200, "Invalid users operation fields")
    revision = integer(value.get("policy_revision"), "operation policy revision")
    output = {"operation_id": operation_id, "recorded": True, "status": "applied", "kind": value["kind"],
              "policy_revision": revision, "users": [safe_user(user) for user in value["users"]]}
    if value["kind"] == "policies":
        output["applied_config_hash"] = config_hash
    else:
        ids = [row["user_id"] for row in output["users"]]
        require(len(ids) == len(set(ids)), "Duplicate operation target ID")
    return output


def has_control(value: str) -> bool:
    # Rust char::is_control uses Unicode Cc: C0, DEL and C1 controls.
    return any(ord(char) < 32 or 0x7f <= ord(char) <= 0x9f for char in value)


def valid_reason(value: Any) -> bool:
    return isinstance(value, str) and 1 <= len(value) <= 1000 and bool(value.strip()) and "\0" not in value


_CURRENT_POLICY_STRUCTURE = object()


def current_upstream_url(value: Any) -> None:
    """A before snapshot is not a new route grant or a network destination."""
    require(isinstance(value, str), "Invalid current upstream URL")
    try:
        size = len(value.encode("utf-8"))
    except UnicodeError:
        raise ClientError("Invalid current upstream URL") from None
    require(1 <= size <= 2048
            and not has_control(value) and not any(char in value for char in "?#\\"), "Invalid current upstream URL")
    try:
        parsed = urllib.parse.urlsplit(value)
        host, port = parsed.hostname, parsed.port
    except ValueError:
        raise ClientError("Invalid current upstream URL") from None
    require(parsed.scheme in ("http", "https") and host is not None and parsed.netloc
            and not parsed.username and not parsed.password and not any(char.isspace() for char in host)
            and (port is None or 1 <= port <= 65535), "Invalid current upstream URL")


def upstream_origin(value: Any, local_http: bool) -> str:
    """Parse an ASCII URL only; Core still owns DNS/IP filtering and dispatch."""
    require(isinstance(value, str) and 1 <= len(value) <= 2048
            and not any(char in value for char in "@?#\\")
            and all(33 <= ord(char) <= 126 for char in value), "Invalid upstream URL")
    try:
        parsed = urllib.parse.urlsplit(value)
        host, port = parsed.hostname, parsed.port
    except ValueError:
        raise ClientError("Invalid upstream URL") from None
    require(parsed.scheme in ("http", "https") and host is not None and parsed.netloc
            and not parsed.netloc.endswith(":") and "%" not in host
            and not parsed.username and not parsed.password and not parsed.query and not parsed.fragment
            and (port is None or 1 <= port <= 65535), "Invalid upstream URL")
    try:
        address = ipaddress.ip_address(host)
    except ValueError:
        address = None
        labels = host.split(".")
        require(len(host) <= 253 and all(re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?", label)
                for label in labels)
                and re.fullmatch(r"(?:[0-9]+|0[xX][0-9a-fA-F]+)", labels[-1]) is None,
                "Upstream host must use an ASCII DNS name or standard numeric IP")
    loopback = address is not None and address.is_loopback
    require((parsed.scheme == "https" and (not loopback or local_http))
            or (parsed.scheme == "http" and loopback and local_http),
            "HTTP or loopback upstream requires the server's explicit numeric-loopback capability")
    canonical_host = address.compressed if address is not None else host.lower()
    authority = f"[{canonical_host}]" if address is not None and address.version == 6 else canonical_host
    if port is not None and port != (443 if parsed.scheme == "https" else 80):
        authority += f":{port}"
    return f"{parsed.scheme}://{authority}"


def policy_capabilities(value: Any) -> dict[str, Any]:
    """Accept only the old five-key contract or the complete eight-key contract."""
    message = "INVALID_RESPONSE: incompatible admin policy capabilities"
    old_keys = {"modes", "client_protocols", "upstream_protocols", "numeric_loopback_only", "credential_references"}
    new_keys = old_keys | {"external_https", "local_http", "allowed_origins"}
    require(isinstance(value, dict) and set(value) in (old_keys, new_keys), message)
    supported = {"modes": {"fixed_text", "custom_upstream"},
                 "client_protocols": {"openai_chat", "openai_responses"},
                 "upstream_protocols": {"openai_chat", "openai_responses", "anthropic_messages", "gemini"}}
    for key, expected in supported.items():
        entries = value[key]
        require(isinstance(entries, list) and all(isinstance(item, str) for item in entries)
                and len(entries) == len(expected) and set(entries) == expected, message)
    aliases = value["credential_references"]
    require(isinstance(aliases, list)
            and all(isinstance(alias, str) and re.fullmatch(r"[A-Za-z0-9_.-]{1,64}", alias) is not None for alias in aliases)
            and len(aliases) == len(set(aliases)), message)
    if set(value) == old_keys:
        require(value["numeric_loopback_only"] is True, message)
        return value
    require(type(value["numeric_loopback_only"]) is bool and type(value["external_https"]) is bool
            and type(value["local_http"]) is bool and len(aliases) <= 32 and aliases == sorted(aliases), message)
    if value["numeric_loopback_only"]:
        require(value["external_https"] is False and value["local_http"] is True
                and value["allowed_origins"] is None, message)
        return value
    origins = value["allowed_origins"]
    require(value["external_https"] is True and isinstance(origins, dict) and set(origins) == set(aliases), message)
    for entries in origins.values():
        require(isinstance(entries, list) and 1 <= len(entries) <= 16
                and all(isinstance(item, str) for item in entries)
                and entries == sorted(set(entries)), message)
        for entry in entries:
            try:
                canonical = upstream_origin(entry, value["local_http"])
            except ClientError:
                raise ClientError(message) from None
            require(entry == canonical, message)
    return value


def policy_config(value: Any, credential_references: Any = _OFFLINE_ALIAS_VALIDATION,
                  *, capabilities: Any = None) -> dict[str, Any]:
    structure_only = capabilities is _CURRENT_POLICY_STRUCTURE
    if capabilities is not None and not structure_only:
        capabilities = policy_capabilities(capabilities)
        require(credential_references is _OFFLINE_ALIAS_VALIDATION
                or credential_references == capabilities["credential_references"], "Credential capability mismatch")
        credential_references = capabilities["credential_references"]
    require(credential_references is _OFFLINE_ALIAS_VALIDATION or isinstance(credential_references, list),
            "INVALID_RESPONSE: missing credential alias metadata")
    require(isinstance(value, dict) and set(value) == {"version", "policies"}
            and type(value["version"]) is int and value["version"] == 1 and isinstance(value["policies"], list),
            "Policy config must contain only version:1 and policies:[...]")
    try:
        config_size = len(canonical_bytes_no_newline(value))
    except UnicodeError:
        raise ClientError("Policy config must contain valid UTF-8") from None
    require(config_size <= 65536 and len(value["policies"]) <= 32, "Policy config is too large")
    ids = set()
    for policy in value["policies"]:
        require(isinstance(policy, dict) and set(policy).issubset({"id", "name", "mode", "fixed_text", "clients", "limits", "public_models"})
                and {"id", "name", "mode", "clients", "limits", "public_models"}.issubset(policy), "Invalid policy fields")
        policy_id = policy["id"]
        require(isinstance(policy_id, str) and re.fullmatch(r"[A-Za-z0-9_.-]{1,64}", policy_id) is not None
                and policy_id not in ids, "Policy IDs must be valid and unique")
        ids.add(policy_id)
        require(isinstance(policy["name"], str) and 1 <= len(policy["name"]) <= 128
                and not has_control(policy["name"]), "Invalid policy name")
        models = policy["public_models"]
        require(isinstance(models, list) and 1 <= len(models) <= 200
                and all(isinstance(model, str) and 1 <= len(model.encode("utf-8")) <= 256
                        and not has_control(model) for model in models)
                and len(set(models)) == len(models), "Invalid public model allowlist")
        clients = policy["clients"]
        require(isinstance(clients, dict), "Invalid policy clients")
        if policy["mode"] == "fixed_text":
            require(isinstance(policy.get("fixed_text"), str) and len(policy["fixed_text"].encode("utf-8")) <= 16384
                    and clients == {}, "Fixed text policy requires bounded text and empty clients")
        else:
            require(policy["mode"] == "custom_upstream" and "fixed_text" not in policy
                    and set(clients) == {"openai_chat", "openai_responses"}, "Invalid custom upstream clients")
            for target in clients.values():
                require(isinstance(target, dict) and set(target) == {"base_url", "upstream_protocol", "model", "credential_reference"},
                        "Upstream requires only base_url, upstream_protocol, model, credential_reference; literal secrets are forbidden")
                require(target["upstream_protocol"] in ("openai_chat", "openai_responses", "anthropic_messages", "gemini"),
                        "Unsupported upstream protocol")
                url = target["base_url"]
                external = isinstance(capabilities, dict) and capabilities.get("external_https") is True
                if structure_only:
                    current_upstream_url(url)
                else:
                    require(isinstance(url, str) and 1 <= len(url) <= 2048
                            and not any(char in url for char in "@?#\\") and all(33 <= ord(c) <= 126 for c in url), "Invalid upstream URL")
                    if external:
                        origin = upstream_origin(url, capabilities["local_http"])
                    else:
                        try:
                            parsed = urllib.parse.urlsplit(url)
                            host = parsed.hostname
                            loopback = host is not None and ipaddress.ip_address(host).is_loopback
                            port = parsed.port
                        except ValueError:
                            raise ClientError("Upstream must use literal loopback HTTP") from None
                        require(parsed.scheme == "http" and loopback and not parsed.username and not parsed.password
                                and not parsed.query and not parsed.fragment and (port is None or 1 <= port <= 65535),
                                "Upstream must use literal loopback HTTP without credentials/query/fragment")
                require(isinstance(target["model"], str) and 1 <= len(target["model"].encode("utf-8")) <= 256
                        and not has_control(target["model"]), "Invalid upstream model")
                alias = target["credential_reference"]
                require(isinstance(alias, str) and re.fullmatch(r"[A-Za-z0-9_.-]{1,64}", alias) is not None,
                        "Invalid credential alias; paths and literal secrets are forbidden")
                if not structure_only and credential_references is not _OFFLINE_ALIAS_VALIDATION:
                    require(isinstance(credential_references, list) and alias in credential_references,
                            "Credential alias is not available on this server")
                if external:
                    require(origin in capabilities["allowed_origins"].get(alias, []),
                            "Upstream origin is not allowed for this credential alias")
        limits = policy["limits"]
        bounds = {"requests_per_minute_per_user": (1,100000), "requests_per_minute_global": (1,100000),
                  "max_concurrent_per_user": (1,256), "max_concurrent_global": (1,256),
                  "max_input_bytes": (1,1048576), "max_output_bytes": (1,1048576),
                  "max_output_tokens": (1,65536), "output_bytes_per_second": (0,1048576),
                  "total_timeout_seconds": (1,240)}
        require(isinstance(limits, dict) and set(limits) == set(bounds), "Invalid policy limit fields")
        for key, (minimum, maximum) in bounds.items():
            integer(limits[key], key, minimum)
            require(limits[key] <= maximum, "Policy limit exceeds supported maximum")
        require(limits["requests_per_minute_per_user"] <= limits["requests_per_minute_global"]
                and limits["max_concurrent_per_user"] <= limits["max_concurrent_global"], "Per-user limit exceeds global limit")
        if policy["mode"] == "fixed_text":
            require(len(policy["fixed_text"].encode("utf-8")) <= limits["max_output_bytes"], "Fixed text exceeds output byte limit")
    return value


def policy_response(value: Any) -> dict[str, Any]:
    """Validate the complete online v4 envelope before preparing any action."""
    message = "INVALID_RESPONSE: incompatible admin policy response"
    require(isinstance(value, dict) and {"version", "revision", "config", "capabilities"}.issubset(value)
            and type(value["version"]) is int and value["version"] == 1
            and type(value["revision"]) is int and value["revision"] >= 0, message)
    caps = policy_capabilities(value["capabilities"])
    if "external_https" in caps:
        require(set(value) == {"version", "revision", "config", "capabilities"}, message)
    try:
        # Current routes may predate a tightened host registry. Keep a repair
        # path: validate their structure, never authorize them as a new config.
        policy_config(value["config"], capabilities=_CURRENT_POLICY_STRUCTURE if "external_https" in caps else None)
    except ClientError:
        raise ClientError("INVALID_RESPONSE: invalid current policy config") from None
    return value


def revision_from(value: Any) -> int:
    require(isinstance(value, dict), "Invalid revision response")
    return integer(value.get("revision"), "revision")


def check_preview(value: Any, requested: dict[str, Any], *, allow_expired: bool = False,
                  wire_version: int = 2) -> dict[str, Any]:
    fields = {"version", "preview_id", "preview_hash", "expires_at", "operation_id", "action",
              "policy_id", "policy_revision", "targets"}
    require(isinstance(value, dict) and set(value) == fields and type(value.get("version")) is int
            and value["version"] == wire_version and wire_version in (1, 2),
            "Invalid server preview version")
    require(value.get("operation_id") == requested["operation_id"] and value.get("action") == requested["action"]
            and value.get("policy_id") == requested.get("policy_id")
            and integer(value.get("policy_revision"), "preview policy revision") == requested["expected_policy_revision"],
            "Server preview does not match requested operation")
    require(isinstance(value.get("preview_id"), str) and re.fullmatch(r"[A-Za-z0-9_.:-]{8,128}", value["preview_id"]) is not None,
            "Invalid preview ID")
    require(isinstance(value.get("preview_hash"), str) and SHA256.fullmatch(value["preview_hash"]) is not None,
            "Invalid preview hash")
    require(type(value.get("expires_at")) is int and value["expires_at"] > 0
            and (allow_expired or value["expires_at"] > int(time.time())), "Invalid or expired preview expiry")
    targets = value.get("targets")
    require(isinstance(targets, list) and 1 <= len(targets) <= 200
            and len(targets) == len(requested["targets"]), "Invalid preview target count")
    expected = {user_id_string(row["user_id"], wire_version=wire_version): row["expected_revision"]
                for row in requested["targets"]}
    require(len(expected) == len(requested["targets"]), "Duplicate requested target ID")
    seen = set()
    for row in targets:
        require(isinstance(row, dict) and set(row) == {"user_id", "username", "active", "expected_revision", "before", "after"},
                "Invalid preview target")
        uid = user_id_string(row["user_id"], wire_version=wire_version)
        rev = integer(row.get("expected_revision"), "preview revision")
        require(uid in expected and uid not in seen and expected[uid] == rev, "Preview IDs or revisions changed")
        seen.add(uid)
        require(isinstance(row.get("username"), str) and type(row.get("active")) is bool, "Invalid preview identity")
        before, after = row.get("before"), row.get("after")
        require(isinstance(before, dict) and set(before) == {"marked", "policy_id"}
                and type(before["marked"]) is bool and isinstance(after, dict)
                and set(after) == {"marked", "policy_id", "active"} and type(after["marked"]) is bool
                and type(after["active"]) is bool, "Invalid preview state transition")
        require(all(state["policy_id"] is None or isinstance(state["policy_id"], str) for state in (before, after)),
                "Invalid preview policy ID")
        require(after["marked"] == (requested["action"] != "unmark")
                and after["policy_id"] == requested.get("policy_id")
                and after["active"] == (True if requested["action"] == "convert_disabled" else row["active"]),
                "Unexpected server preview state transition")
        if requested["action"] == "mark":
            require(row["active"], "mark cannot reactivate a disabled user; use convert_disabled explicitly")
        if requested["action"] == "convert_disabled":
            require(not row["active"], "convert_disabled requires a disabled user")
    return value


def changed_policy_ids(before: dict[str, Any], after: dict[str, Any]) -> list[str]:
    old = {row["id"]: row for row in before["policies"]}
    new = {row["id"]: row for row in after["policies"]}
    return sorted(key for key in set(old) | set(new) if old.get(key) != new.get(key))


def artifact(server: str, kind: str, operation_id: str, payload: Any) -> dict[str, Any]:
    return {"artifact_version": ARTIFACT_VERSION if kind == "users" else 1, "server": server, "kind": kind,
            "operation_id": operation_id, "payload": payload}


def apply_artifact(client: Client, args: argparse.Namespace) -> dict[str, Any]:
    raw = private_read(args.preview)
    digest = hashlib.sha256(raw).hexdigest()
    if args.apply:
        require(isinstance(args.confirm, str) and SHA256.fullmatch(args.confirm) is not None
                and hmac.compare_digest(args.confirm, digest), "Explicit --confirm must match the artifact SHA256")
    saved = decode_json(raw)
    require(isinstance(saved, dict) and set(saved) == {"artifact_version", "server", "kind", "operation_id", "payload"}
            and type(saved["artifact_version"]) is int and saved["artifact_version"] in (1, 2) and saved["server"] == client.server,
            "Artifact version or server binding mismatch")
    opid = valid_operation_id(saved["operation_id"])
    payload = saved["payload"]
    require(isinstance(payload, dict), "Invalid artifact payload")
    policy_caps = None
    if saved["kind"] == "users":
        require(set(payload) == {"request", "preview"} and isinstance(payload["request"], dict)
                and isinstance(payload["preview"], dict), "Invalid user artifact")
        requested, preview = payload["request"], payload["preview"]
        require(set(requested) in ({"operation_id", "reason", "action", "expected_policy_revision", "targets"},
                                  {"operation_id", "reason", "action", "expected_policy_revision", "targets", "policy_id"})
                and requested["operation_id"] == opid and requested["action"] in ("mark", "unmark", "convert_disabled")
                and valid_reason(requested["reason"]),
                "Invalid preview request binding")
        integer(requested["expected_policy_revision"], "expected policy revision")
        require(isinstance(requested["targets"], list) and 1 <= len(requested["targets"]) <= 200, "Invalid requested targets")
        ids = set()
        for target in requested["targets"]:
            require(isinstance(target, dict) and set(target) == {"user_id", "expected_revision"}, "Invalid target fields")
            user_id = user_id_string(target["user_id"], wire_version=saved["artifact_version"])
            integer(target["expected_revision"], "expected revision")
            require(user_id not in ids, "Duplicate target ID")
            ids.add(user_id)
        require((requested["action"] == "unmark" and "policy_id" not in requested)
                or (requested["action"] != "unmark" and isinstance(requested.get("policy_id"), str)
                    and re.fullmatch(r"[A-Za-z0-9_.-]{1,64}", requested["policy_id"]) is not None), "Invalid requested policy")
        check_preview(preview, requested, allow_expired=True, wire_version=saved["artifact_version"])
        operation_kind = "users"
        method, path = "POST", USERS_API + "/users/commit"
        body = {key: preview[key] for key in ("operation_id", "preview_id", "preview_hash")}
    elif saved["kind"] == "policy-local-diff":
        require(saved["artifact_version"] == 1, "Policy artifacts require version 1")
        operation_kind = "policies"
        keys = {"operation_id", "reason", "expected_revision", "config", "before_config"}
        require(set(payload) in (keys, keys | {"capabilities"}) and payload["operation_id"] == opid,
                "Invalid policy artifact")
        integer(payload["expected_revision"], "expected policy revision")
        if "capabilities" in payload:
            policy_caps = policy_capabilities(payload["capabilities"])
            require("external_https" in policy_caps, "Invalid bound policy capabilities")
        policy_config(payload["config"], capabilities=policy_caps)
        policy_config(payload["before_config"], capabilities=_CURRENT_POLICY_STRUCTURE if policy_caps is not None else None)
        require(valid_reason(payload["reason"]), "Invalid reason")
        method, path = "PUT", API + "/policies"
        body = {key: payload[key] for key in ("operation_id", "reason", "expected_revision", "config")}
    else:
        raise ClientError("Unknown artifact action")
    if not args.apply:
        return {"dry_run": True, "artifact_sha256": digest, "operation_id": opid, "kind": saved["kind"], "target_writes": 0}
    existing = client.operation(opid, kind=operation_kind)
    if existing is not None:
        return {**checked_operation(existing, saved), "submitted": False, "reused": True, "exact_request_match_verified": False}
    if policy_caps is not None:
        current = policy_response(client.request("GET", API + "/policies"))
        require(canonical_bytes_no_newline(current["capabilities"]) == canonical_bytes_no_newline(policy_caps)
                and current["revision"] == payload["expected_revision"]
                and hmac.compare_digest(policy_config_hash(current["config"]), policy_config_hash(payload["before_config"])),
                "Reviewed policy capabilities or state changed; prepare a fresh preview")
    try:
        result = client.request(method, path, body)
        require(isinstance(result, dict), "Invalid mutation response")
        return {**checked_operation(result, saved), "submitted": True, "reused": False}
    except KeyboardInterrupt:
        raise OutcomeUnknown(f"Outcome unknown for operation {opid}; use operation {opid}, never create a replacement operation") from None
    except ClientError as exc:
        known_rejections = {(400, "ADMIN_ABUSE_INVALID"), (401, "ADMIN_ABUSE_UNAUTHORIZED"),
                            (403, "ADMIN_ABUSE_FORBIDDEN"), (404, "ADMIN_ABUSE_NOT_FOUND"),
                            (409, "ADMIN_ABUSE_CONFLICT"), (410, "ADMIN_ABUSE_PREVIEW_EXPIRED"),
                            (429, "ADMIN_ABUSE_RATE_LIMITED")}
        if isinstance(exc, ApiError) and (exc.status, exc.code) in known_rejections:
            raise
        # No automatic mutation replay: query the original operation exactly once.
        try:
            observed = client.operation(opid, kind=operation_kind)
            if observed is not None:
                return {**checked_operation(observed, saved), "submitted": True, "reconciled": True, "exact_request_match_verified": False}
        except ClientError:
            pass
        raise OutcomeUnknown(f"Outcome unknown for operation {opid}; use operation {opid}, never create a replacement operation") from None


def checked_operation(value: Any, saved: dict[str, Any]) -> dict[str, Any]:
    safe = safe_operation(value, saved["operation_id"], kind="users" if saved["kind"] == "users" else "policies")
    payload = saved["payload"]
    if saved["kind"] == "users":
        preview = payload["preview"]
        expected = {user_id_string(row["user_id"], wire_version=saved["artifact_version"]): row for row in preview["targets"]}
        require(safe["kind"] == "users" and safe["policy_revision"] == preview["policy_revision"]
                and len(safe["users"]) == len(expected), "Recorded operation kind or targets differ from preview")
        seen = set()
        for user in safe["users"]:
            uid = user["user_id"]
            require(uid in expected and uid not in seen, "Recorded operation target IDs differ from preview")
            seen.add(uid)
            target = expected[uid]
            require(user["revision"] == target["expected_revision"] + 1
                    and all(user[key] == target["after"][key] for key in ("marked", "policy_id", "active")),
                    "Recorded operation post-state differs from preview")
    else:
        require(safe["kind"] == "policies" and safe["policy_revision"] == payload["expected_revision"] + 1
                and safe["users"] == [], "Recorded policy operation revision differs from local preview")
        require(hmac.compare_digest(safe["applied_config_hash"], policy_config_hash(payload["config"])),
                "OUTCOME_CONFLICT: recorded policy config differs from the reviewed artifact; do not resend")
        safe["applied_config_match_verified"] = True
    return safe


class ArgumentParser(argparse.ArgumentParser):
    def __init__(self, *args: Any, **kwargs: Any):
        kwargs.setdefault("allow_abbrev", False)
        super().__init__(*args, **kwargs)

    def error(self, message: str) -> None:
        # argparse messages can contain the actual invalid argument, including
        # an accidentally supplied credential. Never echo that message.
        self.exit(2, "Invalid arguments; use --help for supported options.\n")


def parser() -> argparse.ArgumentParser:
    root = ArgumentParser(description=__doc__)
    root.add_argument("--server", required=True, help="HTTPS origin; never include a credential")
    root.add_argument("--allow-insecure-loopback", action="store_true", help="Permit HTTP only on a literal loopback IP")
    credentials = root.add_mutually_exclusive_group()
    credentials.add_argument("--credential-file", help="Private 0600 file containing a superadmin Session bearer")
    credentials.add_argument("--token-stdin", action="store_true", help="Read the Session bearer from stdin")
    commands = root.add_subparsers(dest="command", required=True)
    users = commands.add_parser("users")
    user_commands = users.add_subparsers(dest="user_command", required=True)
    listing = user_commands.add_parser("list")
    listing.add_argument("--after", type=int, default=0)
    listing.add_argument("--limit", type=int, default=50)
    listing.add_argument("--query-stdin", action="store_true")
    listing.add_argument("--out", help="Explicit private export of this one page, including email")
    listing.add_argument("--state", choices=("all", "marked", "normal", "disabled"), default="all")
    inspecting = user_commands.add_parser("inspect")
    inspecting.add_argument("user_id", type=int)
    inspecting.add_argument("--out", help="Explicit private export, including email")
    honey = commands.add_parser("honeypot")
    honey_commands = honey.add_subparsers(dest="honey_command", required=True)
    preview = honey_commands.add_parser("preview")
    preview.add_argument("--user-id", action="append", type=int, required=True)
    preview.add_argument("--action", choices=("mark", "unmark", "convert_disabled"), required=True)
    preview.add_argument("--policy-id")
    preview.add_argument("--out", required=True)
    preview.add_argument("--reason-stdin", action="store_true", required=True)
    policy = commands.add_parser("policy")
    policy_commands = policy.add_subparsers(dest="policy_command", required=True)
    pget = policy_commands.add_parser("get")
    pget.add_argument("--out", help="Export current config privately for review/editing")
    ppreview = policy_commands.add_parser("preview")
    ppreview.add_argument("--file", required=True, help="Private JSON policy config; literal credentials are forbidden")
    ppreview.add_argument("--out", required=True)
    ppreview.add_argument("--reason-stdin", action="store_true", required=True)
    apply = commands.add_parser("apply")
    apply.add_argument("--preview", required=True)
    apply.add_argument("--confirm", help="SHA256 of the exact reviewed artifact")
    apply.add_argument("--apply", action="store_true", help="Submit exactly one reviewed mutation")
    operation = commands.add_parser("operation")
    operation.add_argument("operation_id")
    return root


def run(args: argparse.Namespace) -> dict[str, Any]:
    server = origin_url(args.server, args.allow_insecure_loopback)
    require(not (args.token_stdin and (getattr(args, "query_stdin", False) or getattr(args, "reason_stdin", False))),
            "Session token and query/reason cannot both consume stdin; use a private credential file")
    if args.command == "users":
        if args.user_command == "list":
            cli_user_id(args.after, allow_zero=True)
            require(1 <= args.limit <= 200, "Users cursor/limit outside supported range")
        else:
            cli_user_id(args.user_id)
    if args.command == "honeypot":
        require(len(args.user_id) <= 200 and len(set(args.user_id)) == len(args.user_id), "User IDs must be unique (maximum 200)")
        for user_id in args.user_id:
            cli_user_id(user_id)
        require((args.action == "unmark" and args.policy_id is None) or
                (args.action != "unmark" and isinstance(args.policy_id, str) and 1 <= len(args.policy_id) <= 64),
                "mark/convert_disabled requires --policy-id; unmark must omit it")
    if args.command == "operation":
        valid_operation_id(args.operation_id)
    client = Client(server, "" if args.command == "apply" and not args.apply else session_token(args))
    if args.command == "apply":
        return apply_artifact(client, args)
    if args.command == "operation":
        value = client.operation(args.operation_id)
        return {"operation_id": args.operation_id, "recorded": False} if value is None else safe_operation(value, args.operation_id)
    if args.command == "users":
        if args.user_command == "inspect":
            value = client.request("GET", USERS_API + f"/users/{args.user_id}")
            output = safe_user(value, detail=True)
            require(output["user_id"] == cli_user_id(args.user_id), "Server returned a different user")
            if args.out:
                output["private_export_sha256"] = private_write(args.out, {"version": 2, **private_user(value, detail=True)})
            return output
        query = bounded_stdin(320, "Query") if args.query_stdin else ""
        params = {"limit": args.limit, "query": query, "state": args.state}
        if args.after:
            params["after"] = cli_user_id(args.after)
        result = client.request("GET", USERS_API + "/users", query=params)
        require(isinstance(result, dict) and set(result) == {"version", "users", "next_after"}
                and type(result.get("version")) is int and result["version"] == 2
                and isinstance(result.get("users"), list) and len(result["users"]) <= args.limit, "Invalid users list response")
        output = {"users": [safe_user(row) for row in result["users"]], "next_after": result.get("next_after")}
        ids = [row["user_id"] for row in output["users"]]
        require(ids == sorted(set(ids), key=int) and all(int(uid) > args.after for uid in ids), "Invalid users page order")
        if output["next_after"] is not None:
            user_id_string(output["next_after"])
            require(bool(ids) and output["next_after"] == ids[-1], "Invalid users next cursor")
        if args.out:
            output["private_export_sha256"] = private_write(args.out, {"version": 2, "users": [private_user(row) for row in result["users"]], "next_after": result["next_after"]})
        return output
    if args.command == "policy" and args.policy_command == "get":
        value = policy_response(client.request("GET", API + "/policies"))
        # Config may contain operator-only endpoints; display the revision/count only.
        config = value.get("config", {})
        output = {"revision": revision_from(value), "policies_count": len(config.get("policies", [])) if isinstance(config, dict) else None}
        if args.out:
            output["private_export_sha256"] = private_write(args.out, config)
        return output
    reason = bounded_stdin(4000, "Reason")
    require(valid_reason(reason), "Reason must contain 1 to 1000 nonblank characters without NUL")
    operation_id = str(uuid.uuid4())
    policies = policy_response(client.request("GET", API + "/policies"))
    revision = revision_from(policies)
    if args.command == "policy":
        caps = policies["capabilities"]
        config = policy_config(decode_json(private_read(args.file)), capabilities=caps)
        payload = {"operation_id": operation_id, "reason": reason, "expected_revision": revision, "config": config, "before_config": policies["config"]}
        if "external_https" in caps:
            payload["capabilities"] = caps
        saved = artifact(server, "policy-local-diff", operation_id, payload)
        digest = private_write(args.out, saved)
        return {"preview_kind": "LOCAL_POLICY_DIFF", "artifact_sha256": digest,
                "operation_id": operation_id, "expected_revision": revision,
                "before_config_sha256": policy_config_hash(policies["config"]),
                "after_config_sha256": policy_config_hash(config),
                "changed_policy_ids": changed_policy_ids(policies["config"], config),
                "target_writes": 0}
    targets = []
    for user_id in sorted(args.user_id):
        user = client.request("GET", USERS_API + f"/users/{user_id}")
        require(safe_user(user, detail=True)["user_id"] == cli_user_id(user_id), "Server returned a different user")
        targets.append({"user_id": cli_user_id(user_id), "expected_revision": revision_from(user)})
    requested = {"operation_id": operation_id, "reason": reason, "action": args.action,
                 "expected_policy_revision": revision, "targets": targets}
    if args.policy_id:
        requested["policy_id"] = args.policy_id
    preview = check_preview(client.request("POST", USERS_API + "/users/preview", requested), requested)
    digest = private_write(args.out, artifact(server, "users", operation_id, {"request": requested, "preview": preview}))
    return {"preview_kind": "SERVER_USER_PREVIEW", "artifact_sha256": digest,
            "operation_id": operation_id, "user_ids": [row["user_id"] for row in targets], "target_writes": 0}


def main(argv: list[str] | None = None) -> int:
    try:
        args = parser().parse_args(argv)
        print(canonical(run(args)).decode("utf-8"), end="")
        return 0
    except OutcomeUnknown as exc:
        print(str(exc), file=sys.stderr)
        return 3
    except (ClientError, KeyboardInterrupt) as exc:
        print(str(exc) if isinstance(exc, ClientError) else "Interrupted; query the original operation before retrying", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
