#!/usr/bin/env python3
"""Offline image cleanup preview. Never connects to or modifies Docker.

Input contains full docker image/container inspect JSON and DP-29 image roots.
It is not an authorization to delete: references must be refreshed under the
release lock immediately before any future Engine operation.
"""
from __future__ import annotations
import argparse
import json
from pathlib import Path
import re
from typing import Any
from disk_lifecycle import PROJECT, Refused, digest, encoded, read_json_file

IMAGE_ID = re.compile(r"sha256:[0-9a-f]{64}\Z")
ROLES = {"current", "previous_verified", "inflight", "pulling", "pinned"}


def preview(snapshot: dict[str, Any]) -> dict[str, Any]:
    if snapshot.get("project") != PROJECT or snapshot.get("all_containers_included") is not True:
        raise Refused("full local inventory, including other projects and stopped containers, is required")
    roles = snapshot.get("release_images", {})
    if set(roles) != ROLES or not all(isinstance(v, list) for v in roles.values()):
        raise Refused("all release reference roles must be supplied")
    images = {}
    for image in snapshot.get("images", []):
        image_id = image.get("Id", "")
        if not IMAGE_ID.fullmatch(image_id) or image_id in images:
            raise Refused("invalid or duplicate immutable image ID")
        images[image_id] = image
    keep: dict[str, list[str]] = {}
    for role, image_ids in roles.items():
        for image_id in image_ids:
            if not IMAGE_ID.fullmatch(image_id) or image_id not in images:
                raise Refused("a retained or pulling image is missing; do not clean from an incomplete snapshot")
            keep.setdefault(image_id, []).append(role)
    for container in snapshot.get("containers", []):
        image_id = container.get("Image", "")
        if not IMAGE_ID.fullmatch(image_id) or image_id not in images:
            raise Refused("container image reference is missing from the snapshot")
        # Do NOT filter this list to project labels. A different project's
        # running/stopped container can still reference a candidate image.
        keep.setdefault(image_id, []).append("container:" + str(container.get("Id", "unknown")))
    candidates = []
    for image_id, image in images.items():
        labels = (image.get("Config") or {}).get("Labels") or {}
        if labels.get("io.lmm.project") != PROJECT or labels.get("io.lmm.managed-by") != "dp28":
            keep.setdefault(image_id, []).append("unmanaged-image")
        if image_id not in keep:
            candidates.append(image_id)
    result = {
        "status": "preview-only", "image_candidates": sorted(candidates),
        "retained": {image: sorted(set(reasons)) for image, reasons in sorted(keep.items())},
        "containers_to_delete": [], "volumes_to_delete": [],
        "reclaimable_physical_bytes": None,
        "accounting_note": "Image Size/SHARED SIZE cannot be summed as physical reclaimed bytes. Re-measure Engine and filesystem after approved image removal.",
        "apply_implemented": False,
    }
    return {**result, "preview_sha256": digest(encoded(result))}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("snapshot", type=Path)
    args = parser.parse_args()
    try:
        print(json.dumps(preview(read_json_file(args.snapshot)), indent=2, sort_keys=True))
    except (Refused, ValueError, TypeError, KeyError, OSError) as exc:
        parser.exit(2, str(exc) + "\n")
