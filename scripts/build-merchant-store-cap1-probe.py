#!/usr/bin/env python3
"""Build the synthetic PG probe from one explicitly reviewed local cap1 tree.

No reference discovery, network checkout, production config, migration, or server
startup. The resulting model-test executable is not an official release binary.
"""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile

REPOSITORY = Path(__file__).resolve().parents[1]
FIXTURE = REPOSITORY / 'apps/api-go/model/testdata/merchant_store_cap1_variant_probe_test.go.txt'
GATE_SOURCE = 'apps/api-go/model/merchant_store_writer_gate.go'


def git(source, *args):
    return subprocess.check_output(['git', '-C', str(source), *args], stderr=subprocess.PIPE)


def reviewed_source(source, revision):
    if not re.fullmatch(r'[0-9a-f]{40}', revision):
        raise ValueError('an exact reviewed 40-character source revision is required')
    source = source.resolve(strict=True)
    if Path(git(source, 'rev-parse', '--show-toplevel').decode().strip()).resolve() != source:
        raise ValueError('cap1 source must be the root of its own checkout')
    if git(source, 'rev-parse', 'HEAD').decode().strip() != revision:
        raise ValueError('cap1 checkout HEAD differs from the reviewed revision')
    if git(source, 'status', '--porcelain').strip():
        raise ValueError('cap1 checkout must be clean; no unreviewed source')
    archive = git(source, 'archive', '--format=tar', revision, 'apps/api-go')
    hashes = {}
    gate = None
    with tarfile.open(fileobj=io.BytesIO(archive), mode='r:') as tar:
        for member in tar.getmembers():
            if member.isdir():
                continue
            if not member.isfile() or not member.name.startswith('apps/api-go/'):
                raise ValueError('cap1 source archive contains a nonregular or unexpected entry')
            payload = tar.extractfile(member).read()
            local = source / member.name
            if not local.is_file() or local.is_symlink() or local.read_bytes() != payload:
                raise ValueError('cap1 working source differs from reviewed Git content: ' + member.name)
            hashes[member.name] = hashlib.sha256(payload).hexdigest()
            if member.name == GATE_SOURCE:
                gate = payload.decode()
    if gate is None or re.findall(r'^const MerchantStoreWriterCapability = (\d+)\s*$', gate, flags=re.M) != ['1']:
        raise ValueError('reviewed source must declare exact capability 1; never rewrite capability 2')
    if 'apps/api-go/go.mod' not in hashes:
        raise ValueError('reviewed source does not contain the Go module')
    manifest_sha = hashlib.sha256(json.dumps(hashes, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
    return archive, hashes, manifest_sha


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cap1-source-tree', type=Path, required=True)
    parser.add_argument('--cap1-source-revision', required=True)
    parser.add_argument('--output-directory', type=Path)
    parser.add_argument('--validate-only', action='store_true')
    parser.add_argument('--go-jobs', type=int, choices=(1, 2), default=2)
    parser.add_argument('--go-procs', type=int, choices=(1, 2), help='runtime CPU limit; defaults to --go-jobs')
    parser.add_argument('--cpu-affinity', help='optional existing Linux CPU list, e.g. 2,3')
    args = parser.parse_args(argv)
    if not args.validate_only and args.output_directory is None:
        parser.error('--output-directory is required when building the probe')
    try:
        archive, hashes, source_sha = reviewed_source(args.cap1_source_tree, args.cap1_source_revision)
        fixture = FIXTURE.read_bytes()
        receipt = {'source_revision': args.cap1_source_revision, 'capability': 1, 'source_manifest_sha256': source_sha, 'source_files_sha256': hashes, 'probe_fixture_sha256': hashlib.sha256(fixture).hexdigest(), 'kind': 'synthetic-capability-one-model-probe; not official N-1 release proof'}
        if args.validate_only:
            print(json.dumps({'source_revision': args.cap1_source_revision, 'capability': 1, 'source_manifest_sha256': source_sha, 'probe_fixture_sha256': receipt['probe_fixture_sha256'], 'status': 'source-validated-no-build'}))
            return 0
        output = args.output_directory.resolve()
        if output == args.cap1_source_tree.resolve() or args.cap1_source_tree.resolve() in output.parents:
            raise ValueError('output must be outside the reviewed source checkout')
        output.mkdir(mode=0o700, parents=False, exist_ok=False)
        with tarfile.open(fileobj=io.BytesIO(archive), mode='r:') as tar:
            tar.extractall(output, filter='data')
        target = output / 'apps/api-go/model/merchant_store_cap1_variant_probe_test.go'
        if target.exists():
            raise ValueError('reviewed source already contains an unreviewed probe filename')
        target.write_bytes(fixture)
        binary = output / 'capability-one-model.test'
        command = ['go', 'test', '-mod=readonly', '-p', str(args.go_jobs), '-c', '-o', str(binary), './model']
        if args.cpu_affinity:
            if not re.fullmatch(r'\d+(,\d+)*', args.cpu_affinity):
                raise ValueError('CPU affinity must be an explicit comma-separated CPU list')
            command = ['taskset', '-c', args.cpu_affinity, *command]
        go_procs = args.go_procs or args.go_jobs
        receipt.update(go_parallelism=args.go_jobs, go_max_procs=go_procs)
        build_env = dict(os.environ, GOMAXPROCS=str(go_procs), GOPROXY='off', GOSUMDB='off')
        log = output / 'compile.log'
        with log.open('wb') as stream:
            result = subprocess.run(command, cwd=output/'apps/api-go', env=build_env, stdout=stream, stderr=subprocess.STDOUT)
        receipt['build_exit_code'] = result.returncode
        receipt['compile_output_sha256'] = hashlib.sha256(log.read_bytes()).hexdigest()
        if result.returncode == 0:
            receipt['binary'] = str(binary)
            receipt['binary_sha256'] = hashlib.sha256(binary.read_bytes()).hexdigest()
        path = output / 'receipt.json'
        path.write_text(json.dumps(receipt, indent=2) + '\n')
        path.chmod(0o600)
        if result.returncode:
            raise ValueError('offline probe compilation failed; retained output: ' + str(log))
        print(json.dumps({'source_revision': args.cap1_source_revision, 'binary': str(binary), 'binary_sha256': receipt['binary_sha256'], 'source_manifest_sha256': source_sha, 'receipt': str(path)}))
        return 0
    except (ValueError, OSError, subprocess.CalledProcessError, tarfile.TarError) as err:
        parser.exit(1, 'cap1 probe refused: ' + str(err) + '\n')


if __name__ == '__main__':
    raise SystemExit(main())
