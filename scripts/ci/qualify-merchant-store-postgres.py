#!/usr/bin/env python3
"""Actions-only merchant PG proof with an independently compiled, signed cap1.

The existing builder retains clean/archive/constant guards and compiles offline.
The probe is synthetic model evidence, never an official N-1 or rollout proof.
"""
import argparse
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

REPOSITORY = Path(__file__).resolve().parents[2]
PACKAGE = 'github.com/LIghtJUNction/api.lmm.best/model'
CAP1_REVISION = '7ad0469eeaa83bddf45cdd45d225c92bf6078421'
SIGNING_FINGERPRINT = '9F6AE0BCDB7FF917BEC6E234F304DD651D11929C'
PRIMARY_FINGERPRINT = 'EB21B83AB1E982DF66F08387A67178405F7736FD'
PUBLIC_KEY = b'''-----BEGIN PGP PUBLIC KEY BLOCK-----

mDMEZ/6uihYJKwYBBAHaRw8BAQdAGM5JPSEZCHEAma0d8JoMDtfy+JJwmPlf4Lo9
5RJVMDq0KkxJZ2h0SlVOY3Rpb24gPExJZ2h0SlVOY3Rpb24ubWVAZ21haWwuY29t
PoiTBBMWCgA7AhsDBQsJCAcCAiICBhUKCQgLAgQWAgMBAh4HAheAFiEE6yG4OrHp
gt9m8IOHpnF4QF93Nv0FAmf+smUACgkQpnF4QF93Nv17uAD/QcyMTrc98nfAf88i
mCZOAgwTfqT4ZE/I9pFj3xxxJwQA/Rlq0SC5/vWuPhr6J7S22u/PUOFJP2fj+nKp
EX6EQ18I0cLowuYBEAABAQAAAAAAAAAAAAAAAP/Y/+AAEEpGSUYAAQEAAAEAAQAA
/9sAQwD/////////////////////////////////////////////////////////
/////////////////////////////9sAQwH/////////////////////////////
/////////////////////////////////////////////////////////8AAEQgB
IADwAwEiAAIRAQMRAf/EABcAAQEBAQAAAAAAAAAAAAAAAAABAgP/xAAkEAEBAQAB
AwMEAwAAAAAAAAAAARExIVHwQWGxcYHB4ZGh8f/EABUBAQEAAAAAAAAAAAAAAAAA
AAAB/8QAFREBAQAAAAAAAAAAAAAAAAAAABH/2gAMAwEAAhEDEQA/AMgAAAAAAAAA
AAAAAANMqAAgCgIKAM1pmqAKCgIMgKAAAAAAAAAAAAAAACAAAAAAAAoKigoCCYYq
AYYAGGAAmKAmCgIKAgoogoCCogAAAAAAAAKigoAAAAACKgKCigACKAmI0AyLiCCK
gAAAKCAAAApABQAAAAQBUUUVFAAQAAAFAAE/tltMBlTAAAAABFQRQICgAAAJ/gAK
iiqIoACAAAAAAoAAAAmJjQDIqZ5+gEVBFIAKAAACACiooCooACAAoCaaCjO0Bdhs
QBdN9kUDfY32T1AXfqbEAXlA3v1EA8/QCgAFEoAAqgAKgCiagLqAAAAAAAAqAIoA
AAAAATz77vwIoACAKAAoGgIAAAAKCC4oIYoCGKAiNJYCAAAAAAHnyEBQBEAFFQA1
FAAAAUBQBUAABAVBQABkLyAAAAAECCKACACooAAAAoCggJbis1Qlv4aYjYAADN5a
Zs6gs4VJwoJUWoAAACCKACgAgAoAACgKAAoCAACgAJbgKjG3uv3BoPyoIzW2aCAA
AgigAoAIAKAAoAKqKAAAAAAAlmqA5nLYIefwoCjN7NM3kEAABBFABQAQAUAAVAFV
AGhFAAAAABNBROvsAoAJWVt1AAAEVBFIEBQAQDqAAAgqgrKoKqAqqyoKIoAAAJbg
KzamgAAAACKgKQBFAAAASqgAiqAAAqAoCAAKAoIlaT3EQAAAUAARUBQBFABAABUU
AAMFARUMAVAGugzptQaGdppBefPkQUVAvcABAAFEVBFIEBQAQAABQILAVlagNRKq
AheysgAABQBeKi+n08+QaCICC3nzzsiAAKIoAAIoAP/ZiJAEExYKADgWIQTrIbg6
semC32bwg4emcXhAX3c2/QUCafZzwgIbAwULCQgHAgYVCgkICwIEFgIDAQIeAQIX
gAAKCRCmcXhAX3c2/ZzIAQCK1sSAt+Ew9otTCviO/kRNt0MqsThW+O0GzLdwsGEF
zAD+NY63z+9fkF25a73xc5eowrdcYg9awMn1b4iPkMFEMgG0MExJZ2h0SlVOY3Rp
b24gKOecnykgPGxpZ2h0anVuY3Rpb24ubWVAZ21haWwuY29tPoiQBBMWCgA4FiEE
6yG4OrHpgt9m8IOHpnF4QF93Nv0FAmqkAwECGwMFCwkIBwIGFQoJCAsCBBYCAwEC
HgECF4AACgkQpnF4QF93Nv2lRQD/fAFh7MLo32TtWBr5St/xllXVtOclEB7dQI94
MH+F6AQA/RaZw0G5jI3c5icpvCSKJAz+lTlWp2S+Nx8C+AHCp7sEtD1MSWdodEpV
TmN0aW9uIChUb2tlbk5vdEluY2x1ZGVkKSA8bGlnaHRqdW5jdGlvbi5tZUBnbWFp
bC5jb20+iJAEExYKADgWIQTrIbg6semC32bwg4emcXhAX3c2/QUCaqQDMgIbAwUL
CQgHAgYVCgkICwIEFgIDAQIeAQIXgAAKCRCmcXhAX3c2/SisAP0fzZLZFmR8Nv/S
o7TnkJBjafwEn7C0DxnmFcpnBlOPIAEAl1Qw2wpR39HdCAx0BB5SOMpQT07m8B/5
esYNkhF/qAm4OARn/q6KEgorBgEEAZdVAQUBAQdAOHg580F2K4igtSMsnvnzX+Ur
dmCCIucjpljILCPfKAUDAQgHiHgEGBYKACACGwwWIQTrIbg6semC32bwg4emcXhA
X3c2/QUCZ/6ydwAKCRCmcXhAX3c2/SKFAP90gadUEULx41wDdbbSxDtRXmfayPf0
vOl/o2xlLtHlGwD9Fw045u1HMGZihqvUfnFNxXXqLiBimjc5yc0P2DkR7wu4MwRp
XO/1FgkrBgEEAdpHDwEBB0CCH5/Tuh8G14uAlgF6edmYSIaP+6V54mOL9735vaLG
PYh4BBgWCgAgFiEE6yG4OrHpgt9m8IOHpnF4QF93Nv0FAmlc7/UCGyAACgkQpnF4
QF93Nv0RxwD7BAniuAYcS61IQfsgFVtyDZRr9s7mQYkvV2xekdus304A/2VcPzJR
GOGXfFCHHodUrwrM7e7tt0jFU0jC6P5+qaECuDMEaR7WuBYJKwYBBAHaRw8BAQdA
R9zK+LrBceFgji2byVJsrVHjP6kxW6WlRpdzM+pduOSI7wQYFgoAIBYhBOshuDqx
6YLfZvCDh6ZxeEBfdzb9BQJpHta4AhsiAIEJEKZxeEBfdzb9diAEGRYKAB0WIQSf
auC823/5F77G4jTzBN1lHRGSnAUCaR7WuAAKCRDzBN1lHRGSnBinAQDl3tflPWtK
bsBj4cMtMSUfPB/9ZdjPySzOnvR7Pj5sjAEA2nHOOKX0bUaXD0JIQq5aKvcnz5Y1
CBvnwi1NQocxhAWmZAEA0udRgfcrvi0KM9ilVkL71vJ7QAJ4KQuv/mh80MNn8gUB
APPb79MuP26uIua4KZP6UxcgvxbYQdBk5X6YdiWblxIG
=jCO7
-----END PGP PUBLIC KEY BLOCK-----
'''
GETTER_ENV = 'MERCHANT_STORE_CAP1_COMPILED_CAPABILITY_ONLY'
PARENTS = (
    'TestMerchantStorePostgresDSNGuard',
    'TestMerchantStorePostgresConcurrency',
    'TestMerchantStorePostgresVariants',
    'TestMerchantStorePostgresSalesLimit',
    'TestMerchantStorePostgresRemainingQuota',
    'TestMerchantStorePostgresDeletionSerializesWithCheckout',
    'TestMerchantStoreTradeNoPostgresCollisionRetries',
    'TestMerchantStorePurchaseLimitsPostgresConcurrentVariantsCannotBypassBuyerCap',
    'TestMerchantStoreRefundPostgresLocksPartialRequestsAndApproval',
    'TestMerchantStoreRefundActivationPostgresQualifiesActualSchemaAndTerminalCallbacks',
    'TestMerchantStoreDeploymentFencePostgresSharedSessionAndDurableCrashOwner',
    'TestMerchantStoreSchemaPreparationPostgresPreservesFactsAndRollsBackDDL',
    'TestMerchantStoreGuestEmailCheckoutBindingAndConcurrentSingleUse',
)
LEAF_GROUPS = {
    PARENTS[1]: (
        'same-request-settles-once', 'distinct-checkouts-never-oversell',
        'cross-product-buyer-pending-limit', 'verified-callback-cancel-and-expiry',
        'receipt-belongs-to-one-order', 'wallet-boundary-full-rollback',
    ),
    PARENTS[2]: (
        'two-skus-share-one-cumulative-cap',
        'eight-checkouts-never-borrow-another-skus-stock',
        'activation-waits-for-share-then-capability-one-freezes',
    ),
    PARENTS[3]: ('physical-stock-exceeds-cumulative-limit', 'late-payment-proof-prevents-fresh-checkout'),
    PARENTS[4]: (
        'quota-locks-before-settlement', 'settlement-locks-before-quota',
        'quota-zero-before-checkout', 'checkout-before-quota-zero',
    ),
    PARENTS[5]: ('deletion-wins', 'checkout-wins'),
    PARENTS[11]: (
        'installation_preserves_paid_facts_and_separates_activation',
        'backfill_failure_rolls_back_already_executed_PostgreSQL_DDL',
    ),
    PARENTS[12]: ('postgres_guest_row_single_use_and_send_budget',),
}
LEAVES = tuple(parent + '/' + leaf for parent, leaves in LEAF_GROUPS.items() for leaf in leaves)
SELECTOR = '^(' + '|'.join(PARENTS) + ')$'


def strict_json(payload):
    def unique_fields(pairs):
        value = {}
        for key, item in pairs:
            if key in value:
                raise ValueError('duplicate JSON field: ' + key)
            value[key] = item
        return value
    return json.loads(payload, object_pairs_hook=unique_fields)


def read_events(path):
    events = [strict_json(line) for line in path.read_text().splitlines() if line.strip()]
    if not events or any(not isinstance(event, dict) for event in events):
        raise ValueError('missing or invalid Go JSON evidence')
    return events


def require_passes(events, required):
    if any(event.get('Action') in ('skip', 'fail') for event in events):
        raise ValueError('merchant qualification rejects every skipped or failed test/package')
    if any(event.get('Package') != PACKAGE for event in events):
        raise ValueError('evidence must originate from the merchant model package')
    passed = Counter(event.get('Test') for event in events if event.get('Action') == 'pass')
    if passed[None] != 1 or any(passed[name] != 1 for name in required):
        missing = [name for name in required if passed[name] != 1]
        raise ValueError('required tests must each pass exactly once, with package PASS: ' + ', '.join(missing))


def validate_getter(payload):
    data = strict_json(payload)
    if not isinstance(data, dict) or set(data) != {'merchant_store_writer_capability'} or type(data['merchant_store_writer_capability']) is not int or data['merchant_store_writer_capability'] != 1:
        raise ValueError('the actual independent compiled getter must report exact integer capability 1')


def validate_pg(events):
    require_passes(events, (*PARENTS, *LEAVES))
    return {'parents_passed': len(PARENTS), 'pg_leaves_passed': len(LEAVES), 'failed': 0, 'skipped': 0}


def git(repository, *args, env=None):
    return subprocess.check_output(['git', '--no-replace-objects', '-C', str(repository), *args], env=env, stderr=subprocess.PIPE)


def verify_ancestor(repository, env, evidence=None):
    git(repository, 'merge-base', '--is-ancestor', CAP1_REVISION, 'HEAD', env=env)
    with tempfile.TemporaryDirectory(prefix='merchant-cap1-signature-') as directory:
        Path(directory).chmod(0o700)
        signature_env = dict(env, GNUPGHOME=directory)
        imported = subprocess.run(['gpg', '--no-options', '--batch', '--import'], input=PUBLIC_KEY,
                                  env=signature_env, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        result = subprocess.run(['git', '--no-replace-objects', '-C', str(repository),
                                 '-c', 'gpg.format=openpgp', '-c', 'gpg.program=gpg',
                                 'verify-commit', '--raw', CAP1_REVISION],
                                env=signature_env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True)
        if evidence is not None:
            evidence.write_bytes(imported.stderr + result.stderr)
        statuses = [line.split()[1:] for line in result.stderr.decode().splitlines() if line.startswith('[GNUPG:] ')]
        valid = [status for status in statuses if status[0] == 'VALIDSIG']
        bad = {'BADSIG', 'ERRSIG', 'EXPSIG', 'EXPKEYSIG', 'REVKEYSIG'}
        if len(valid) != 1 or valid[0][1] != SIGNING_FINGERPRINT or valid[0][-1] != PRIMARY_FINGERPRINT or any(status[0] in bad for status in statuses):
            raise ValueError('pinned cap1 commit must have the reviewed valid signing and primary fingerprints')
    # Current setup-go warms the same module graph before the offline old build.
    for name in ('go.mod', 'go.sum'):
        if git(repository, 'show', CAP1_REVISION + ':apps/api-go/' + name, env=env) != (repository / 'apps/api-go' / name).read_bytes():
            raise ValueError('cap1 module graph differs; review dependency warming before changing the pin')


def record_command(command, cwd, env, path):
    with path.open('wb') as stream:
        result = subprocess.run(command, cwd=cwd, env=env, stdout=stream, stderr=subprocess.STDOUT)
    if result.returncode:
        raise ValueError('command failed; retained evidence: ' + str(path))


def qualify(artifact_directory, runner_temp):
    if os.environ.get('GITHUB_ACTIONS') != 'true':
        raise ValueError('this runner is restricted to Actions; offline contract tests do not run Go or PG')
    if not os.environ.get('MERCHANT_STORE_POSTGRES_TEST_DSN'):
        raise ValueError('explicit disposable merchant PostgreSQL DSN is required')
    artifact_directory.mkdir(parents=True, exist_ok=False)
    env = dict(os.environ, GOMAXPROCS='2', GOTOOLCHAIN='local', GIT_NO_REPLACE_OBJECTS='1')
    env.pop(GETTER_ENV, None)
    receipt = {
        'status': 'started', 'source_revision': git(REPOSITORY, 'rev-parse', 'HEAD', env=env).decode().strip(),
        'cap1_revision': CAP1_REVISION, 'cap1_kind': 'synthetic-model-probe; not official N-1 release or production proof',
        'required_parents': PARENTS, 'required_pg_leaves': LEAVES,
        'go_parallelism': 1, 'go_max_procs': 2,
    }
    runtime = Path(tempfile.mkdtemp(prefix='merchant-pg-ci-', dir=runner_temp))
    cap1_tree, output = runtime / 'cap1-source', runtime / 'cap1-probe'
    worktree_added = False
    try:
        receipt['stage'] = 'verify-pinned-cap1-source'
        verify_ancestor(REPOSITORY, env, artifact_directory / 'signature-check.log')
        receipt['signature_verified'] = True
        receipt['signing_fingerprint'] = SIGNING_FINGERPRINT
        receipt['stage'] = 'checkout-pinned-cap1-source'
        git(REPOSITORY, 'worktree', 'add', '--detach', str(cap1_tree), CAP1_REVISION, env=env)
        worktree_added = True
        receipt['stage'] = 'warm-current-dependency-cache'
        record_command(['go', 'mod', 'download'], REPOSITORY / 'apps/api-go', env, artifact_directory / 'dependency-warming.log')
        receipt['stage'] = 'compile-independent-cap1-offline'
        record_command(['python3', '-B', str(REPOSITORY / 'scripts/build-merchant-store-cap1-probe.py'),
                        '--cap1-source-tree', str(cap1_tree), '--cap1-source-revision', CAP1_REVISION,
                        '--output-directory', str(output), '--go-jobs', '1', '--go-procs', '2'],
                       REPOSITORY, env, artifact_directory / 'cap1-builder.log')
        binary = output / 'capability-one-model.test'
        probe_receipt = strict_json((output / 'receipt.json').read_text())
        fixture_sha = hashlib.sha256((REPOSITORY / 'apps/api-go/model/testdata/merchant_store_cap1_variant_probe_test.go.txt').read_bytes()).hexdigest()
        binary_sha = hashlib.sha256(binary.read_bytes()).hexdigest()
        if probe_receipt['source_revision'] != CAP1_REVISION or probe_receipt['capability'] != 1 or probe_receipt['probe_fixture_sha256'] != fixture_sha or probe_receipt['binary_sha256'] != binary_sha:
            raise ValueError('independent cap1 receipt must bind the reviewed source, exact fixture, and actual executable')
        receipt.update(cap1_fixture_sha256=fixture_sha, cap1_binary_sha256=binary_sha, cap1_source_manifest_sha256=probe_receipt['source_manifest_sha256'])
        env['MERCHANT_STORE_CAP1_TEST_BINARY'] = str(binary)
        getter_log = artifact_directory / 'cap1-compiled-getter.json'
        # The test-only init branch reads the real compiled constant and exits
        # before the old model TestMain creates its in-memory SQLite fixture.
        receipt['stage'] = 'read-independent-compiled-capability'
        record_command([str(binary)], REPOSITORY, dict(env, **{GETTER_ENV: '1'}), getter_log)
        validate_getter(getter_log.read_text())
        receipt['cap1_actual_compiled_capability'] = 1
        pg_log = artifact_directory / 'postgres.jsonl'
        receipt['stage'] = 'execute-all-required-merchant-postgres-cases'
        record_command(['go', 'test', '-race', '-p', '1', '-count=1', '-json', './model',
                        '-run', SELECTOR, '-timeout', '300s'], REPOSITORY / 'apps/api-go', env, pg_log)
        receipt.update(validate_pg(read_events(pg_log)), status='passed', stage='complete')
    except Exception as error:
        receipt.update(status='failed', failure_stage=receipt.get('stage'), failure_message=str(error))
        if isinstance(error, subprocess.CalledProcessError):
            (artifact_directory / 'stage-error.log').write_bytes((error.stdout or b'') + (error.stderr or b''))
        raise
    finally:
        # Retain failed compilation receipts/logs too; never erase the CI failure.
        for name in ('receipt.json', 'compile.log'):
            if (output / name).is_file():
                shutil.copyfile(output / name, artifact_directory / ('cap1-' + name))
        cleanup_ok = True
        if worktree_added:
            try:
                git(REPOSITORY, 'worktree', 'remove', str(cap1_tree), env=env)
            except subprocess.CalledProcessError:
                cleanup_ok = False
                receipt['status'] = 'failed'
        receipt['owned_cap1_worktree_removed'] = cleanup_ok
        receipt['evidence_sha256'] = {path.name: hashlib.sha256(path.read_bytes()).hexdigest()
                                     for path in sorted(artifact_directory.iterdir()) if path.is_file()}
        (artifact_directory / 'result.json').write_text(json.dumps(receipt, indent=2) + '\n')
        if not cleanup_ok:
            raise ValueError('failed to remove the owned cap1 checkout; qualification refused')
    return receipt


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--artifact-directory', type=Path, required=True)
    parser.add_argument('--runner-temp', type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        receipt = qualify(args.artifact_directory.resolve(), args.runner_temp.resolve(strict=True))
        print(json.dumps({key: receipt[key] for key in ('status', 'parents_passed', 'pg_leaves_passed', 'failed', 'skipped')}))
        return 0
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, 'merchant PG qualification refused: ' + str(error) + '\n')


if __name__ == '__main__':
    raise SystemExit(main())
