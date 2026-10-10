#!/usr/bin/env python3
"""Verify a local OCI layout and account for missing whole blobs. No extraction.

Byte counts exclude registry HTTP/TLS overhead. Tar bytes and a logical block
estimate are NOT observed Docker snapshotter use or a measured disk peak.
"""
from __future__ import annotations
import argparse
import gzip
import hashlib
import io
import json
import math
import re
import tarfile
from pathlib import Path

MAX_BLOB = 512 * 1024 * 1024
MAX_EXPANDED = 1024 * 1024 * 1024


def blob(root: Path, desc: dict) -> bytes:
    value = desc.get('digest', '')
    size = desc.get('size')
    if not re.fullmatch(r'sha256:[0-9a-f]{64}', value) or type(size) is not int or not 0 < size <= MAX_BLOB:
        raise ValueError('bad OCI descriptor')
    path = root / 'blobs' / 'sha256' / value.split(':')[1]
    if path.is_symlink() or path.stat().st_size != size:
        raise ValueError('bad OCI blob length/path')
    data = path.read_bytes()
    if hashlib.sha256(data).hexdigest() != value.split(':')[1]:
        raise ValueError('OCI digest mismatch')
    return data


def inspect(root: Path, name: str) -> dict:
    if json.loads((root / 'oci-layout').read_text()).get('imageLayoutVersion') != '1.0.0':
        raise ValueError('unsupported OCI layout')
    index = json.loads((root / 'index.json').read_text())
    selected = [d for d in index['manifests'] if d.get('annotations', {}).get('org.opencontainers.image.ref.name') == name]
    if len(selected) != 1:
        raise ValueError('select exactly one tagged single-platform image')
    desc = selected[0]
    manifest = json.loads(blob(root, desc))
    if manifest.get('schemaVersion') != 2 or manifest.get('mediaType') != 'application/vnd.oci.image.manifest.v1+json':
        raise ValueError('expected an OCI single-platform image manifest')
    configdesc = manifest['config']
    config = json.loads(blob(root, configdesc))
    if config.get('os') != 'linux' or config.get('architecture') not in {'amd64', 'arm64'}:
        raise ValueError('unsupported image target')
    diffs = config.get('rootfs', {}).get('diff_ids', [])
    if len(diffs) != len(manifest['layers']):
        raise ValueError('layer/DiffID length mismatch')
    layers, chain = [], []
    for layer, diffid in zip(manifest['layers'], diffs):
        data = blob(root, layer)
        if layer['mediaType'] == 'application/vnd.oci.image.layer.v1.tar+gzip':
            with gzip.GzipFile(fileobj=io.BytesIO(data)) as stream:
                raw = stream.read(MAX_EXPANDED + 1)
        elif layer['mediaType'] == 'application/vnd.oci.image.layer.v1.tar':
            raw = data
        else:
            # Fail rather than silently treating zstd or foreign layers as zero.
            raise ValueError('unsupported layer encoding (raw/gzip only in this lab)')
        if len(raw) > MAX_EXPANDED:
            raise ValueError('layer expansion limit')
        if 'sha256:' + hashlib.sha256(raw).hexdigest() != diffid:
            raise ValueError('uncompressed DiffID mismatch')
        logical = allocated = entries = 0
        with tarfile.open(fileobj=io.BytesIO(raw), mode='r:') as archive:
            for item in archive:
                # Inspection only: never extract attacker-controlled paths.
                entries += 1
                if item.isfile():
                    logical += item.size
                    allocated += math.ceil(item.size / 4096) * 4096
                else:
                    allocated += 4096
                if entries > 100000 or logical > MAX_EXPANDED:
                    raise ValueError('layer entry/data limit')
        chain.append(diffid)
        layers.append({'digest': layer['digest'], 'compressed_bytes': len(data),
                       'diff_id': diffid, 'snapshot_chain': chain.copy(),
                       'tar_bytes': len(raw), 'file_logical_bytes': logical,
                       'file_block_estimate_4096': allocated, 'entries': entries})
    metadata = {desc['digest']: desc['size'], configdesc['digest']: configdesc['size']}
    return {'name': name, 'platform': config['os'] + '/' + config['architecture'],
            'manifest_digest': desc['digest'], 'metadata': metadata, 'layers': layers}


def cost(old: list[dict], new: list[dict]) -> dict:
    cached = {digest for image in old for digest in image['metadata']}
    cached |= {layer['digest'] for image in old for layer in image['layers']}
    old_chains = {tuple(layer['snapshot_chain']) for image in old for layer in image['layers']}
    missing_meta, missing_layers, snapshots = {}, {}, {}
    for image in new:
        for digest, size in image['metadata'].items():
            if digest not in cached:
                missing_meta[digest] = size
        for layer in image['layers']:
            if layer['digest'] not in cached:
                missing_layers[layer['digest']] = layer
            chain = tuple(layer['snapshot_chain'])
            if chain not in old_chains:
                snapshots[chain] = layer
    compressed = sum(x['compressed_bytes'] for x in missing_layers.values())
    meta = sum(missing_meta.values())
    blocks = sum(x['file_block_estimate_4096'] for x in snapshots.values())
    largest = max((x['compressed_bytes'] for x in missing_layers.values()), default=0)
    return {'download_blob_bytes': compressed + meta, 'image_metadata_bytes': meta,
            'new_unique_layer_blobs': len(missing_layers),
            'new_snapshot_chains': len(snapshots),
            'new_uncompressed_tar_bytes': sum(x['tar_bytes'] for x in snapshots.values()),
            'new_file_logical_bytes': sum(x['file_logical_bytes'] for x in snapshots.values()),
            'unpacked_block_estimate_4096': blocks,
            'sequential_stage_budget_estimate_bytes': compressed + meta + blocks + largest,
            'largest_partial_blob_bytes': largest,
            'measured_docker_peak_bytes': None,
            'note': 'Budget estimate excludes daemon metadata, journals, parallel pulls and filesystem effects; old versions remain resident.'}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('layout', type=Path)
    parser.add_argument('--old', action='append', default=[])
    parser.add_argument('--new', action='append', required=True)
    args = parser.parse_args()
    print(json.dumps(cost([inspect(args.layout, n) for n in args.old],
                          [inspect(args.layout, n) for n in args.new]), indent=2))

if __name__ == '__main__':
    main()
