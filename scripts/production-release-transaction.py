#!/usr/bin/env python3
"""Finish one immutable native release transaction; never infer success from HTTP.

This controller does not implement physical A/B switching. The native controller
owns service changes, the single-writer gate, schema compatibility and rollback.
An ambiguous native operation is reconciled by read-only status, never replayed.
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time
from typing import Callable, Sequence

STABLE = frozenset({"AWAITING_CONFIRMATION", "CONFIRMED", "ROLLED_BACK",
                    "FAILED_PREARM", "ROLLBACK_REQUIRED"})
MOVING = frozenset({"WORKSPACE_CREATED", "STAGED", "BACKUPS_READY",
                    "ACTIVATION_DISPATCHED", "PREPARING", "MUTATION_PENDING",
                    "MIGRATING", "DEPLOYING", "DEPLOYING_GO", "DEPLOYING_WEB",
                    "OBSERVING", "CONFIRMING", "ROLLING_BACK"})


class UnverifiedStatus(RuntimeError):
    """A response must not be used as evidence for a production mutation."""


class StatusUnavailable(RuntimeError):
    """The native command did not yield a usable response."""


def unique_object(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            raise UnverifiedStatus("duplicate status JSON key")
        result[key] = value
    return result


def decode_status(raw: bytes, deployment: str, digest: str, version: str) -> str:
    if len(raw) > 65536:
        raise UnverifiedStatus("oversized native status")
    try:
        value = json.loads(raw.decode("utf-8"), object_pairs_hook=unique_object)
    except (ValueError, UnicodeError) as error:
        raise UnverifiedStatus("invalid native status JSON") from error
    if not isinstance(value, dict):
        raise UnverifiedStatus("native status must be an object")
    if value.get("deployment_id") != deployment or value.get("plan_sha256") != digest:
        raise UnverifiedStatus("native status does not match the immutable plan")
    phase = value.get("status")
    if not isinstance(phase, str) or phase not in STABLE | MOVING:
        raise UnverifiedStatus("unknown native transaction phase")
    if phase in {"AWAITING_CONFIRMATION", "CONFIRMED"} and value.get("version") != version:
        raise UnverifiedStatus("native candidate version differs from the planned backend")
    return phase


class ReleaseTransaction:
    def __init__(self, native: Callable[[str], bytes], acceptance: Callable[[], None],
                 deployment: str, digest: str, version: str, *,
                 deadline_seconds: float = 1200, poll_seconds: float = 5,
                 clock: Callable[[], float] = time.monotonic,
                 sleep: Callable[[float], None] = time.sleep):
        self.native = native
        self.acceptance = acceptance
        self.deployment = deployment
        self.digest = digest
        self.version = version
        self.deadline_seconds = deadline_seconds
        self.poll_seconds = poll_seconds
        self.clock = clock
        self.sleep = sleep
        self.phase = "UNKNOWN"
        self.outcome = "recovery-required"
        self.reason = "transaction-not-completed"

    def settled(self) -> str:
        deadline = self.clock() + self.deadline_seconds
        while True:
            # Never return a cached phase after losing the status connection.
            self.phase = "UNKNOWN"
            try:
                self.phase = decode_status(self.native("status"), self.deployment,
                                           self.digest, self.version)
            except StatusUnavailable:
                pass
            if self.phase in STABLE:
                return self.phase
            remaining = deadline - self.clock()
            if remaining <= 0:
                raise StatusUnavailable("native transaction has not reached a verified stable state")
            self.sleep(min(self.poll_seconds, remaining))

    def rollback(self, reason: str) -> bool:
        # Recheck immediately before mutation. A concurrent confirmation, unknown
        # response or a still-running activation must never select a rollback.
        phase = self.settled()
        if phase == "ROLLED_BACK":
            self.outcome, self.reason = "failed-rolled-back", reason
            return False
        if phase not in {"AWAITING_CONFIRMATION", "ROLLBACK_REQUIRED"}:
            self.reason = "rollback-not-authorized-by-native-state"
            return False
        try:
            self.native("rollback")
        except StatusUnavailable:
            # A transport error is not evidence that rollback did not execute.
            pass
        phase = self.settled()
        if phase == "ROLLED_BACK":
            self.outcome, self.reason = "failed-rolled-back", reason
        else:
            self.reason = "native-rollback-not-completed"
        return False

    def execute(self) -> bool:
        try:
            # Exactly one dispatch. The native CLI owns dispatch reconciliation.
            try:
                self.native("promote")
            except StatusUnavailable:
                pass
            phase = self.settled()
            if phase == "ROLLED_BACK":
                self.outcome, self.reason = "failed-rolled-back", "native-activation-rolled-back"
                return False
            if phase == "FAILED_PREARM":
                self.outcome, self.reason = "failed-before-mutation", "native-preparation-failed"
                return False
            if phase == "ROLLBACK_REQUIRED":
                return self.rollback("native-activation-failed")
            try:
                self.acceptance()
            except StatusUnavailable:
                # CONFIRMED is terminal. A workflow must not silently turn a
                # confirmed release into a new rollback transaction.
                if phase == "CONFIRMED":
                    self.outcome, self.reason = "confirmed-health-failed", "public-acceptance-failed"
                    return False
                return self.rollback("public-acceptance-failed")
            if phase != "CONFIRMED":
                # A fresh status prevents confirming a different/stale phase.
                phase = self.settled()
                if phase == "ROLLBACK_REQUIRED":
                    return self.rollback("native-health-changed")
                if phase != "AWAITING_CONFIRMATION":
                    self.reason = "native-state-changed-before-confirmation"
                    return False
                try:
                    self.native("confirm")
                except StatusUnavailable:
                    # A failed SSH response may hide a still-running confirm.
                    # Only reconcile; do not race it with an automatic rollback.
                    pass
                phase = self.settled()
            if phase != "CONFIRMED":
                self.reason = "native-confirmation-not-completed"
                return False
            self.outcome, self.reason = "confirmed", "native-and-public-gates-passed"
            return True
        except UnverifiedStatus:
            self.phase = "UNVERIFIED"
            self.reason = "invalid-or-mismatched-native-evidence"
        except StatusUnavailable:
            self.reason = "native-status-unavailable-or-still-running"
        return False

    def report(self) -> dict[str, str]:
        return {"deployment_id": self.deployment, "plan_sha256": self.digest,
                "expected_backend_version": self.version,
                "native_status": self.phase, "outcome": self.outcome, "reason": self.reason}


def write_report(path: Path, report: dict[str, str]) -> None:
    """Only allowlisted audit metadata; never persist stdout, environment or keys."""
    fd, temporary = tempfile.mkstemp(prefix=".release-result-", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(report, stream, sort_keys=True)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def run_command(command: Sequence[str], timeout: float) -> bytes:
    try:
        result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                timeout=timeout, check=False)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise StatusUnavailable("native command transport failed") from error
    if result.returncode:
        # Do not print native stderr: it may include a DSN or other private data.
        raise StatusUnavailable(f"command exited with status {result.returncode}")
    return result.stdout


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--deployment-id", required=True)
    parser.add_argument("--plan", type=Path, required=True)
    parser.add_argument("--plan-sha256", required=True)
    parser.add_argument("--expected-backend-version", required=True)
    parser.add_argument("--acceptance-script", type=Path, required=True)
    parser.add_argument("--result-file", type=Path, required=True)
    parser.add_argument("native_command", nargs=argparse.REMAINDER)
    args = parser.parse_args(argv)
    command = args.native_command
    if command[:1] == ["--"]:
        command = command[1:]
    if not command or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._:-]{0,127}", args.deployment_id):
        parser.error("a native command and valid deployment ID are required")
    if not re.fullmatch(r"[0-9a-f]{64}", args.plan_sha256):
        parser.error("invalid plan SHA256")
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", args.expected_backend_version):
        parser.error("invalid backend version")
    if not args.plan.is_absolute() or not args.plan.is_file() or not args.acceptance_script.is_file():
        parser.error("the immutable plan and public acceptance script must exist")
    if not args.result_file.is_absolute() or not args.result_file.parent.is_dir():
        parser.error("result file must have an existing absolute parent directory")

    def native(action: str) -> bytes:
        tail = ["deploy", "production", action, "--plan", str(args.plan),
                "--plan-sha256", args.plan_sha256, "--confirm", "api.lmm.best"]
        if action == "rollback":
            tail += ["--reason", "workflow-release-acceptance-failed"]
        return run_command([*command, *tail], 45 if action == "status" else 1200)

    def acceptance() -> None:
        run_command([sys.executable, "-B", str(args.acceptance_script),
                     "--expected-backend-version", args.expected_backend_version], 180)

    transaction = ReleaseTransaction(native, acceptance, args.deployment_id,
                                     args.plan_sha256, args.expected_backend_version)
    # Leave a useful marker even if the runner is forcibly cancelled later.
    write_report(args.result_file, transaction.report())
    try:
        success = transaction.execute()
    finally:
        write_report(args.result_file, transaction.report())
        print(json.dumps(transaction.report(), sort_keys=True))
    return 0 if success else 1


if __name__ == "__main__":
    raise SystemExit(main())
