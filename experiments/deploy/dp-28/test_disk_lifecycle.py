"""Run only via run-lab.sh: actual local ENOSPC/inode tests, no production data."""
from __future__ import annotations
import contextlib
import errno
import gzip
import io
import json
import multiprocessing
import os
from pathlib import Path
import shutil
import signal
import subprocess
import tarfile
import tempfile
import time
import unittest

from disk_lifecycle import Policy, Refused, Store, digest, encoded
from event_retirement import REQUIRED_PROOFS, assess
from lab_support import (
    DEFAULT_POLICY, assert_sentinels, clean, df_du, fill_blocks, install,
    make_archive, new_store, protect_sentinels,
)

EVIDENCE: list[dict] = []


def record(case: str, **values: object) -> None:
    EVIDENCE.append({"case": case, **values})
    Path(os.environ["DP28_OUT"], "fault-evidence.json").write_bytes(encoded(EVIDENCE))


def install_worker(root: str, artifact: tuple, ready: object, proceed: object) -> None:
    def hook(event: str) -> None:
        if event == "after-preflight":
            ready.set()
            if not proceed.wait(5):
                raise RuntimeError("test controller did not release the worker")
    with Store(Path(root), hook) as store:
        store.install_fixture(*artifact)


def crash_gc_worker(root: str, plan_hash: str) -> None:
    def hook(event: str) -> None:
        if event == "after-unlink":
            os.kill(os.getpid(), signal.SIGKILL)
    with Store(Path(root), hook) as store:
        store.gc(plan_hash)


def crash_install_worker(root: str, artifact: tuple) -> None:
    def hook(event: str) -> None:
        if event == "archive-durable":
            os.kill(os.getpid(), signal.SIGKILL)
    with Store(Path(root), hook) as store:
        store.install_fixture(*artifact)


class DiskLifecycleTests(unittest.TestCase):
    def setUp(self) -> None:
        # No fallback to /tmp or a user's data when the isolation proof is absent.
        self.lab = Path(os.environ["DP28_LAB"])
        self.workspace = Path(tempfile.mkdtemp(prefix="test-", dir=self.lab))
        self.store = new_store(self.workspace)
        self.artifacts = [
            (Path(os.environ["DP28_OUT"]) / "packages" / f"r{i:03d}.tar.gz",
             Path(os.environ["DP28_OUT"]) / "packages" / f"r{i:03d}.manifest.json",
             digest((Path(os.environ["DP28_OUT"]) / "packages" / f"r{i:03d}.manifest.json").read_bytes()))
            for i in range(4)
        ]
        self.sentinels = protect_sentinels(self.store)

    def tearDown(self) -> None:
        self.store.close()
        shutil.rmtree(self.workspace)

    def populated(self, count: int = 3) -> None:
        for artifact in self.artifacts[:count]:
            install(self.store, artifact)

    def pending_gc(self) -> dict:
        self.populated(2)
        self.store.install_fixture(*self.artifacts[2])
        return self.store.gc()["plan"]

    def test_preview_is_default_and_makes_no_content_changes(self) -> None:
        plan = self.pending_gc()
        def snapshot() -> dict:
            return {str(path.relative_to(self.store.root)): digest(path.read_bytes())
                    for path in self.store.root.rglob("*") if path.is_file()}
        before = snapshot()
        preview = self.store.gc()
        self.assertEqual(preview["status"], "preview")
        self.assertEqual(plan["plan_sha256"], preview["plan"]["plan_sha256"])
        self.assertEqual(before, snapshot())
        self.assertGreater(len(plan["delete"]), 0)
        assert_sentinels(self.store, self.sentinels)
        record("default-preview", candidate_entries=len(plan["delete"]), files_unchanged=True)

    def test_stale_preview_is_rejected_before_deletion(self) -> None:
        plan = self.pending_gc()
        self.store.pin("r000", "incident")
        with self.assertRaisesRegex(Refused, "stale"):
            self.store.gc(plan["plan_sha256"])
        self.assertTrue((self.store.root / "releases/r000/probe").is_file())
        self.assertEqual(self.store.read_json("control/gc.json")["status"], "done")
        record("stale-preview", refused=True, pinned_release_intact=True)

    def test_real_concurrent_pull_excludes_cleanup(self) -> None:
        self.populated(1)
        context = multiprocessing.get_context("spawn")
        ready, proceed = context.Event(), context.Event()
        worker = context.Process(target=install_worker, args=(str(self.store.root), self.artifacts[1], ready, proceed))
        worker.start()
        try:
            self.assertTrue(ready.wait(5))
            with self.assertRaisesRegex(Refused, "busy"):
                self.store.gc()
            with self.assertRaisesRegex(Refused, "busy"):
                self.store.preflight(*self.artifacts[2])
        finally:
            proceed.set()
            worker.join(10)
            if worker.is_alive():
                worker.kill()
                worker.join()
        self.assertEqual(worker.exitcode, 0)
        clean(self.store)
        record("concurrent-pull-and-cleanup", cleanup_refused_busy=True, updater_exit=worker.exitcode)

    def test_live_lease_retains_old_release_until_process_exits(self) -> None:
        self.populated(2)
        with self.store.lease("r000") as fd:
            child = subprocess.Popen([str(self.store.root / "releases/r000/probe"), "--hold"],
                                     cwd=self.store.root / "releases/r000", pass_fds=(fd,),
                                     stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                self.assertIn(b'"r000"', child.stdout.readline())
                self.store.install_fixture(*self.artifacts[2])
                preview = self.store.gc()
                self.assertIn("live-lease", preview["plan"]["retain"]["r000"])
                self.store.gc(preview["plan"]["plan_sha256"])
                self.assertIsNone(child.poll())
                self.assertTrue((self.store.root / "releases/r000/probe").exists())
            finally:
                child.stdin.close()
                child.wait(timeout=5)
                child.stdout.close()
                child.stderr.close()
        receipt = clean(self.store)
        self.assertFalse((self.store.root / "releases/r000").exists())
        record("live-version-retention", retained_while_running=True, deleted_after_exit=receipt["deleted_this_attempt"])

    def test_corrupt_fallback_refuses_update_and_rollback(self) -> None:
        self.populated(2)
        (self.store.root / "releases/r000/version.txt").chmod(0o644)
        (self.store.root / "releases/r000/version.txt").write_bytes(b"broken\n")
        before = self.store.state()
        with self.assertRaisesRegex(Refused, "checksum"):
            self.store.preflight(*self.artifacts[2])
        with self.assertRaisesRegex(Refused, "checksum"):
            self.store.rollback_probe()
        self.assertEqual(before, self.store.state())
        self.assertTrue((self.store.root / "releases/r001/probe").exists())
        record("corrupt-fallback", update_refused=True, rollback_not_claimed=True)

    def test_root_marker_permissions_and_symlink_rejected(self) -> None:
        alias = self.workspace / "alias"
        alias.symlink_to(self.store.root, target_is_directory=True)
        with self.assertRaises((Refused, OSError)):
            Store(alias)
        self.store.root.chmod(0o777)
        with self.assertRaises(Refused):
            Store(self.store.root)
        self.store.root.chmod(0o700)
        marker = self.store.root / ".lmm-dp28.json"
        original = marker.read_bytes()
        marker.write_bytes(encoded({"project": "some-other-project", "scope": "dp28-local-only"}))
        with self.assertRaises(Refused):
            Store(self.store.root)
        marker.write_bytes(original)
        record("root-ownership", wrong_project_refused=True, root_symlink_refused=True, writable_root_refused=True)

    def test_path_traversal_and_unmarked_files_are_not_cleaned(self) -> None:
        self.populated(2)
        outside = self.workspace / "outside.txt"
        outside.write_bytes(b"outside-sentinel")
        with self.assertRaises(Refused):
            self.store.read("releases/../../outside.txt")
        (self.store.root / "cache/customer-file").write_bytes(b"not-lifecycle-owned")
        (self.store.root / "releases/foreign").mkdir()
        (self.store.root / "releases/foreign/unmarked.txt").write_bytes(b"foreign")
        preview = self.store.gc()
        self.assertIn("foreign", preview["plan"]["retain"])
        self.assertFalse(any("foreign" in x["path"] for x in preview["plan"]["delete"]))
        self.store.gc(preview["plan"]["plan_sha256"])
        self.assertEqual(outside.read_bytes(), b"outside-sentinel")
        self.assertEqual((self.store.root / "cache/customer-file").read_bytes(), b"not-lifecycle-owned")
        record("unmarked-and-outside", unmarked_retained=True, traversal_refused=True, outside_unchanged=True)

    def test_symlink_substitution_cannot_escape_cleanup(self) -> None:
        plan = self.pending_gc()
        target = self.store.root / "releases/r000"
        saved = self.workspace / "saved-release"
        target.rename(saved)
        target.symlink_to(saved, target_is_directory=True)
        before = (saved / "payload.bin").read_bytes()
        with self.assertRaises(Refused):
            self.store.gc(plan["plan_sha256"])
        self.assertEqual(before, (saved / "payload.bin").read_bytes())
        target.unlink()
        saved.rename(target)
        record("symlink-replacement", refused=True, linked_target_unchanged=True)

    def test_nested_bind_mount_cannot_be_traversed(self) -> None:
        self.populated(2)
        outside = self.workspace / "mounted-outside"
        outside.mkdir()
        (outside / "keep").write_text("outside")
        mountpoint = self.store.root / "releases/foreign"
        mountpoint.mkdir()
        subprocess.run(["mount", "--bind", str(outside), str(mountpoint)], check=True)
        try:
            preview = self.store.gc()
            self.assertIn("foreign", preview["plan"]["retain"])
            self.assertFalse(any(e["path"].startswith("releases/foreign/") for e in preview["plan"]["delete"]))
            self.store.gc(preview["plan"]["plan_sha256"])
            self.assertEqual((outside / "keep").read_text(), "outside")
        finally:
            subprocess.run(["umount", str(mountpoint)], check=True)
        record("nested-bind-mount", same_device_different_mount_rejected=True, outside_unchanged=True)

    def test_external_hardlink_is_not_claimed_as_reclaimed(self) -> None:
        plan = self.pending_gc()
        outside = self.workspace / "external-payload"
        os.link(self.store.root / "releases/r000/payload.bin", outside)
        fresh = self.store.gc()["plan"]
        self.assertIn("external-hardlink", fresh["retain"]["r000"])
        self.store.gc(fresh["plan_sha256"])
        self.assertEqual(digest(outside.read_bytes()), self.store.file_digest("releases/r000/payload.bin"))
        record("external-hardlink", retained=True, bytes_not_claimed_free=True)

    def test_shared_objects_count_once_not_once_per_release(self) -> None:
        self.populated(2)
        first = (self.store.root / "releases/r000/probe").stat()
        second = (self.store.root / "releases/r001/probe").stat()
        self.assertEqual((first.st_dev, first.st_ino), (second.st_dev, second.st_ino))
        bill = self.store.bill()
        # Three paths (two release links + object) refer to one physical inode.
        all_files = [p for p in self.store.root.rglob("*") if p.is_file()]
        naive = sum(p.stat().st_blocks * 512 for p in all_files)
        self.assertGreater(naive, bill["reachable_allocated_bytes"])
        record("shared-inode-accounting", unique_bytes=bill["reachable_allocated_bytes"], naive_path_sum=naive,
               shared_binary_inode=first.st_ino)

    def test_open_deleted_file_is_counted_until_descriptor_closes(self) -> None:
        self.populated(2)
        target = self.store.root / "cache/held.bin"
        target.write_bytes(b"h" * (1024 * 1024))
        fd = os.open(target, os.O_RDONLY)
        held_size = target.stat().st_blocks * 512
        target.unlink()
        try:
            held = self.store.bill()
            self.assertGreaterEqual(held["open_deleted_allocated_lower_bound"], held_size)
            tools = df_du(self.store.root)
        finally:
            os.close(fd)
        released = self.store.bill()
        self.assertEqual(released["open_deleted_allocated_lower_bound"], 0)
        self.assertGreaterEqual(released["filesystem"]["available_bytes"] - held["filesystem"]["available_bytes"], held_size)
        record("open-deleted", held_allocated_bytes=held_size, bill_while_open=held,
               bill_after_close=released, gnu_tools=tools)

    def test_real_disk_full_rejects_publication_and_default_cleanup(self) -> None:
        self.populated(2)
        state = self.store.state()
        pressure = fill_blocks(self.workspace)
        try:
            free = os.statvfs(self.store.root).f_bavail
            self.assertEqual(free, 0)
            with self.assertRaisesRegex(Refused, "insufficient space"):
                self.store.preflight(*self.artifacts[2])
            with self.assertRaisesRegex(Refused, "insufficient space"):
                self.store.install_fixture(*self.artifacts[2])
            self.assertEqual(state, self.store.state())
            self.assertFalse(self.store.diagnostic("release-refused"))
            assert_sentinels(self.store, self.sentinels)
            record("block-enospc", free_blocks=free, state_unchanged=True, diagnostics_reported_failure=True,
                   gnu_tools=df_du(self.store.root))
        finally:
            pressure.unlink()
        self.assertTrue(self.store.diagnostic("request-complete"))
        install(self.store, self.artifacts[2])

    def test_real_inode_full_with_free_bytes_rejects_publication(self) -> None:
        self.populated(2)
        directory = self.workspace / "inode-pressure"
        directory.mkdir()
        created = []
        caught = None
        for index in range(2000):
            try:
                path = directory / f"inode-{index}"
                fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                os.close(fd)
                created.append(path)
            except OSError as exc:
                caught = exc
                break
        self.assertIsNotNone(caught)
        self.assertEqual(caught.errno, errno.ENOSPC)
        stats = os.statvfs(self.store.root)
        state = self.store.state()
        try:
            self.assertEqual(stats.f_favail, 0)
            self.assertGreater(stats.f_bavail * stats.f_frsize, 1024 * 1024)
            with self.assertRaisesRegex(Refused, "inodes"):
                self.store.install_fixture(*self.artifacts[2])
            self.assertFalse(self.store.diagnostic("release-refused"))
            self.assertEqual(state, self.store.state())
            assert_sentinels(self.store, self.sentinels)
            record("inode-enospc", created_files=len(created), free_inodes=stats.f_favail,
                   free_bytes=stats.f_bavail * stats.f_frsize, state_unchanged=True,
                   diagnostics_reported_failure=True, gnu_tools=df_du(self.store.root))
        finally:
            for path in created:
                path.unlink()
        install(self.store, self.artifacts[2])

    def test_space_disappears_after_preflight_copy_fails_honestly(self) -> None:
        self.populated(2)
        before = self.store.state()
        pressure = []
        def hook(event: str) -> None:
            if event == "before-copy":
                pressure.append(fill_blocks(self.workspace))
        self.store.hook = hook
        with self.assertRaises(OSError) as error:
            self.store.install_fixture(*self.artifacts[2])
        self.assertEqual(error.exception.errno, errno.ENOSPC)
        self.store.hook = lambda _: None
        self.assertEqual(before, self.store.state())
        preview = self.store.gc()["plan"]
        before_entries = {e["path"] for e in self.store.walk()}
        with self.assertRaises(OSError) as error:
            self.store.gc(preview["plan_sha256"])
        self.assertEqual(error.exception.errno, errno.ENOSPC)
        # The failed intent may leave a bounded .next control file. No approved
        # artifact/staging deletion was performed without its durable intent.
        self.assertTrue(all(self.store.exists(e["path"]) for e in preview["delete"]))
        fault_bill = self.store.bill()
        for path in pressure:
            path.unlink()
        recover_samples = [self.store.bill()["filesystem"]["used_bytes"]]
        def sample(_: str) -> None:
            fs = os.fstatvfs(self.store.fd)
            recover_samples.append((fs.f_blocks - fs.f_bfree) * fs.f_frsize)
        self.store.hook = sample
        receipt = self.store.gc(preview["plan_sha256"])
        self.assertEqual(receipt["status"], "done")
        cleanup_recovery_peak = max(recover_samples)
        recover_samples.clear()
        install(self.store, self.artifacts[2])
        retry_release_peak = max(recover_samples)
        assert_sentinels(self.store, self.sentinels)
        record("mid-copy-enospc", errno=errno.ENOSPC, previous_state_preserved=True,
               cleanup_refused_without_intent_space=True, recovery_deleted=receipt["deleted_this_attempt"],
               fault_filesystem_used_bytes=fault_bill["filesystem"]["used_bytes"],
               cleanup_recovery_peak_bytes=cleanup_recovery_peak, retry_release_peak_bytes=retry_release_peak)

    def test_required_state_write_failure_is_not_reported_as_success(self) -> None:
        self.populated(2)
        state = self.store.state()
        pressure = []
        def hook(event: str) -> None:
            if event == "before-state":
                pressure.append(fill_blocks(self.workspace))
        self.store.hook = hook
        with self.assertRaises(OSError) as error:
            self.store.install_fixture(*self.artifacts[2])
        self.assertEqual(error.exception.errno, errno.ENOSPC)
        self.assertEqual(state, self.store.state())
        self.assertEqual(self.store.rollback_probe()["release"], "r000")
        self.store.hook = lambda _: None
        for path in pressure:
            path.unlink()
        receipt = clean(self.store)
        self.assertFalse((self.store.root / "releases/r002").exists())
        assert_sentinels(self.store, self.sentinels)
        record("state-write-enospc", success_not_reported=True, old_state_valid=True,
               uncommitted_release_deleted=receipt["deleted_this_attempt"])

    def test_pre_stage_marker_failure_can_be_recovered_from_install_intent(self) -> None:
        self.populated(2)
        state = self.store.state()
        original = self.store.atomic_json
        pressure = []
        def atomic(path: str, value: object) -> None:
            if path == "staging/pending/owner.json":
                pressure.append(fill_blocks(self.workspace))
            original(path, value)
        self.store.atomic_json = atomic
        with self.assertRaises(OSError) as error:
            self.store.install_fixture(*self.artifacts[2])
        self.assertEqual(error.exception.errno, errno.ENOSPC)
        self.store.atomic_json = original
        self.assertEqual(state, self.store.state())
        for path in pressure:
            path.unlink()
        receipt = clean(self.store)
        self.assertFalse(self.store.exists("staging/pending"))
        install(self.store, self.artifacts[2])
        record("stage-marker-write-failure", recovered_via_durable_install_intent=True,
               deleted=receipt["deleted_this_attempt"])

    def test_interrupted_cleanup_resumes_the_same_exact_intent(self) -> None:
        plan = self.pending_gc()
        count = 0
        def hook(event: str) -> None:
            nonlocal count
            if event == "after-unlink":
                count += 1
                if count == 2:
                    raise OSError(errno.EIO, "injected I/O failure after two actual deletions")
        self.store.hook = hook
        with self.assertRaises(OSError):
            self.store.gc(plan["plan_sha256"])
        self.store.hook = lambda _: None
        self.assertEqual(self.store.gc()["status"], "resume-required")
        with self.assertRaisesRegex(Refused, "interrupted cleanup"):
            self.store.preflight(*self.artifacts[3])
        receipt = self.store.gc(plan["plan_sha256"])
        self.assertEqual(len(receipt["already_missing_on_resume"]), 2)
        self.assertEqual(self.store.state()["current"], "r002")
        self.assertEqual(self.store.rollback_probe()["release"], "r001")
        assert_sentinels(self.store, self.sentinels)
        record("retry-after-partial-deletion", injected_errno=errno.EIO, receipt=receipt)

    def test_real_sigkill_during_cleanup_is_recoverable(self) -> None:
        plan = self.pending_gc()
        worker = multiprocessing.get_context("spawn").Process(target=crash_gc_worker, args=(str(self.store.root), plan["plan_sha256"]))
        worker.start()
        worker.join(5)
        if worker.is_alive():
            worker.kill()
            worker.join()
        self.assertEqual(worker.exitcode, -signal.SIGKILL)
        self.assertEqual(self.store.gc()["status"], "resume-required")
        receipt = self.store.gc(plan["plan_sha256"])
        self.assertEqual(len(receipt["already_missing_on_resume"]), 1)
        assert_sentinels(self.store, self.sentinels)
        record("sigkill-cleanup", exitcode=worker.exitcode, resumed=True, receipt=receipt)

    def test_real_sigkill_after_download_preserves_current_and_fallback(self) -> None:
        self.populated(2)
        state = self.store.state()
        worker = multiprocessing.get_context("spawn").Process(target=crash_install_worker, args=(str(self.store.root), self.artifacts[2]))
        worker.start()
        worker.join(5)
        if worker.is_alive():
            worker.kill()
            worker.join()
        self.assertEqual(worker.exitcode, -signal.SIGKILL)
        self.assertEqual(state, self.store.state())
        receipt = clean(self.store)
        self.assertEqual(self.store.rollback_probe()["release"], "r000")
        install(self.store, self.artifacts[2])
        record("sigkill-download", exitcode=worker.exitcode, old_state_preserved=True,
               deleted=receipt["deleted_this_attempt"])

    def test_gc_receipt_failure_returns_error_and_retry_finishes(self) -> None:
        plan = self.pending_gc()
        def hook(event: str) -> None:
            if event == "before-gc-receipt":
                raise OSError(errno.EIO, "injected receipt failure")
        self.store.hook = hook
        with self.assertRaises(OSError):
            self.store.gc(plan["plan_sha256"])
        self.store.hook = lambda _: None
        receipt = self.store.gc(plan["plan_sha256"])
        self.assertEqual(receipt["deleted_this_attempt"], [])
        self.assertEqual(len(receipt["already_missing_on_resume"]), len(plan["delete"]))
        record("receipt-failure", false_success_avoided=True, retry_receipt=receipt)

    def test_replaced_entry_on_resume_is_not_deleted(self) -> None:
        plan = self.pending_gc()
        def hook(event: str) -> None:
            if event == "before-unlink":
                raise OSError(errno.EIO, "pause before first unlink")
        self.store.hook = hook
        with self.assertRaises(OSError):
            self.store.gc(plan["plan_sha256"])
        self.store.hook = lambda _: None
        target = next(e for e in plan["delete"] if not e["directory"] and e["bytes"] > 0)
        path = self.store.root / target["path"]
        old = self.workspace / "old-inode"
        path.rename(old)
        path.write_bytes(b"new-unapproved-file")
        with self.assertRaisesRegex(Refused, "replaced|changed"):
            self.store.gc(plan["plan_sha256"])
        self.assertEqual(path.read_bytes(), b"new-unapproved-file")
        record("resume-entry-replaced", new_file_not_deleted=True)

    def test_completed_cleanup_retry_returns_the_existing_receipt(self) -> None:
        plan = self.pending_gc()
        original = self.store.gc(plan["plan_sha256"])
        replay = self.store.gc(plan["plan_sha256"])
        self.assertEqual(replay["status"], "already-done")
        self.assertEqual(replay["receipt"], original)
        record("completed-cleanup-retry", no_second_deletion=True, existing_receipt_returned=True)

    def test_new_open_reference_blocks_interrupted_cleanup_resume(self) -> None:
        plan = self.pending_gc()
        def hook(event: str) -> None:
            if event == "before-unlink":
                raise OSError(errno.EIO, "stop with durable intent before deletion")
        self.store.hook = hook
        with self.assertRaises(OSError):
            self.store.gc(plan["plan_sha256"])
        self.store.hook = lambda _: None
        target = self.store.root / "releases/r000/payload.bin"
        with target.open("rb"):
            with self.assertRaisesRegex(Refused, "visible process reference"):
                self.store.gc(plan["plan_sha256"])
            self.assertTrue(target.exists())
        self.store.gc(plan["plan_sha256"])
        record("resume-new-open-reference", deletion_blocked_until_close=True)

    def test_generic_precompiled_package_can_be_measured_but_not_executed(self) -> None:
        source, manifest_path, _ = self.artifacts[0]
        manifest = json.loads(manifest_path.read_bytes())
        manifest["kind"] = "lmm-release-package-v1"
        path = self.workspace / "generic-manifest.json"
        path.write_bytes(encoded(manifest))
        pin = digest(path.read_bytes())
        budget = self.store.preflight(source, path, pin)
        self.assertGreaterEqual(budget["terms"]["archive_allocated_upper"], source.stat().st_size)
        self.assertGreaterEqual(budget["terms"]["expanded_allocated_upper"], sum(item["bytes"] for item in manifest["files"]))
        with self.assertRaisesRegex(Refused, "must not execute"):
            self.store.install_fixture(source, path, pin)
        self.assertIsNone(self.store.state()["current"])
        record("generic-package-preflight", actual_archive_bytes=source.stat().st_size,
               computed_budget=budget, nonfixture_execution_refused=True)

    def test_log_rotation_is_bounded_and_does_not_accept_secrets(self) -> None:
        for _ in range(1000):
            self.assertTrue(self.store.diagnostic("request-complete"))
        logs = list((self.store.root / "logs").iterdir())
        self.assertLessEqual(len(logs), DEFAULT_POLICY.diagnostic_files)
        self.assertTrue(all(p.stat().st_size <= DEFAULT_POLICY.diagnostic_file_bytes for p in logs))
        with self.assertRaises(Refused):
            self.store.diagnostic("Authorization: Bearer synthetic-secret")
        with self.assertRaises(Refused):
            self.store.diagnostic("request-complete", "synthetic-secret")
        for path in logs:
            for line in path.read_text().splitlines():
                self.assertEqual(set(json.loads(line)), {"event", "count", "time"})
        assert_sentinels(self.store, self.sentinels)
        record("diagnostic-log-bounds", file_count=len(logs), sizes={p.name: p.stat().st_size for p in logs},
               raw_content_rejected=True, audit_and_dedup_intact=True)

    def test_diagnostic_age_retention_and_symlink_are_scoped(self) -> None:
        self.store.diagnostic("request-complete")
        rotated = self.store.root / "logs/diag-1.jsonl"
        rotated.write_bytes(b"old diagnostic\n")
        os.utime(rotated, (time.time() - 200000, time.time() - 200000))
        self.store.diagnostic("request-complete")
        self.assertFalse(rotated.exists())
        outside = self.workspace / "outside-log"
        outside.write_bytes(b"untouched")
        current = self.store.root / "logs/diag-0.jsonl"
        current.unlink()
        current.symlink_to(outside)
        with self.assertRaises(Refused):
            self.store.diagnostic("request-complete")
        self.assertEqual(outside.read_bytes(), b"untouched")
        record("log-age-and-symlink", expired_diagnostics_only=True, symlink_refused=True)

    def test_archive_integrity_and_unsafe_members_are_rejected(self) -> None:
        source, manifest_path, manifest_hash = self.artifacts[0]
        before = self.store.state()
        with self.assertRaisesRegex(Refused, "manifest checksum"):
            self.store.install_fixture(source, manifest_path, "0" * 64)
        for label, member_name, member_type in (
            ("traversal", "../outside", tarfile.REGTYPE),
            ("symlink", "probe", tarfile.SYMTYPE),
            ("hardlink", "probe", tarfile.LNKTYPE),
        ):
            archive = self.workspace / f"{label}.tar.gz"
            with tarfile.open(archive, "w:gz") as tar:
                item = tarfile.TarInfo(member_name)
                item.type, item.linkname, item.size = member_type, "../../outside", 0
                tar.addfile(item, io.BytesIO(b""))
            manifest = json.loads(manifest_path.read_bytes())
            manifest["archive_bytes"] = archive.stat().st_size
            manifest["archive_sha256"] = digest(archive.read_bytes())
            rewritten = self.workspace / f"{label}.json"
            rewritten.write_bytes(encoded(manifest))
            with self.assertRaises(Refused):
                self.store.install_fixture(archive, rewritten, digest(rewritten.read_bytes()))
        self.assertEqual(before, self.store.state())
        self.assertFalse(self.store.exists("staging/pending"))
        record("artifact-validation", wrong_pin_refused=True, traversal_refused=True,
               archive_symlink_refused=True, archive_hardlink_refused=True, no_release_activated=True)

    def test_event_payload_eligibility_never_removes_dedup_evidence(self) -> None:
        complete = {key: True for key in REQUIRED_PROOFS}
        self.assertTrue(assess(complete)["payload_candidate_only"])
        for key in REQUIRED_PROOFS:
            missing = dict(complete)
            missing[key] = False
            value = assess(missing)
            self.assertFalse(value["payload_candidate_only"])
            self.assertIn(key, value["blocking_proofs"])
        self.assertFalse(assess({})["payload_candidate_only"])
        self.assertIn("dedup-uniqueness", assess(complete)["never_remove"])
        assert_sentinels(self.store, self.sentinels)
        record("event-retirement-contract", missing_proof_refuses=True, database_delete_implemented=False,
               dedup_and_audit_unchanged=True)

    def test_first_install_has_no_invented_rollback(self) -> None:
        install(self.store, self.artifacts[0])
        with self.assertRaisesRegex(Refused, "no verified fallback"):
            self.store.rollback_probe()
        record("first-install", no_invented_rollback=True)

    def test_pending_versions_have_a_declared_limit_not_a_ttl(self) -> None:
        # One draining slot. An older active version plus existing fallback
        # cannot be evicted merely to fit the next update.
        limited = new_store(self.workspace, "limited", Policy(**{
            **DEFAULT_POLICY.__dict__, "max_draining_versions": 1,
        }))
        try:
            install(limited, self.artifacts[0])
            limited.pin("r000", "controller-unknown")
            install(limited, self.artifacts[1])
            install(limited, self.artifacts[2])
            limited.pin("r001", "inflight")
            with self.assertRaisesRegex(Refused, "draining-version budget"):
                limited.preflight(*self.artifacts[3])
            self.assertTrue(limited.exists("releases/r000/probe"))
            self.assertIn("r000", limited.state()["pins"])
        finally:
            limited.close()
        record("draining-limit", further_update_refused=True, unknown_controller_pin_not_expired=True)


if __name__ == "__main__":
    unittest.main(verbosity=2)
