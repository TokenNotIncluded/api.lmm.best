import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { test } from 'node:test';

const root = new URL('../', import.meta.url);
const read = (path) => readFileSync(new URL(path, root), 'utf8');
const workflow = (name) => read(`.github/workflows/${name}.yml`);

// These assertions complement actionlint: they protect the repository's intended
// entry points and trust boundaries, rather than trying to parse arbitrary YAML.
function job(source, id) {
  const jobs = source.split('\njobs:\n')[1];
  assert.ok(jobs, 'workflow must contain jobs');
  const found = jobs.split(/\n(?=  [\w-]+:\n)/).find((part) => part.startsWith(`  ${id}:\n`));
  assert.ok(found, `missing job: ${id}`);
  return found;
}

test('backend and release workflows do not have server access', () => {
  const files = readdirSync(new URL('.github/workflows/', root))
    .filter((name) => /\.ya?ml$/.test(name));
  for (const name of ['ci.yml', 'release-go.yml', 'release-web.yml', 'server-release-qualification.yml']) {
    assert.ok(files.includes(name), `missing workflow: ${name}`);
  }
  assert.ok(!files.includes('server-ops.yml'));
  for (const file of files.filter((name) => name !== 'deploy-web-frontend.yml')) {
    const source = read(`.github/workflows/${file}`);
    assert.doesNotMatch(source, /PRODUCTION_SSH|production-auto-deploy|deploy-production|server-ops|inputs\.deploy/);
    assert.doesNotMatch(source, /environment:\s*production/);
    assert.doesNotMatch(source, /^\s*(?:run:\s*)?(?:ssh|scp|rsync)\s/m);
  }
  for (const path of ['.github/actions/deploy-production/action.yml', '.github/server-ops-343-request.json', 'scripts/auto-deploy-production-release.sh', 'scripts/server-ops.py']) {
    assert.throws(() => read(path), /ENOENT/);
  }
  assert.match(workflow('server-release-qualification'), /qualify-go-migration-startup.sh/);
});

test('frontend deployment is restricted to a signed web release on both origins', () => {
  const deploy = workflow('deploy-web-frontend');
  assert.match(deploy, /workflows: \[\"LMM web release\"\]/);
  assert.match(deploy, /types: \[completed\]/);
  assert.match(deploy, /github\.event\.workflow_run\.conclusion == 'success'/);
  assert.match(deploy, /environment: production/);
  assert.match(deploy, /LMM_WEB_DEPLOY_SSH_KEY/);
  assert.match(deploy, /LMM_WEB_DEPLOY_KNOWN_HOSTS/);
  assert.ok(deploy.includes('gh api "repos/${GITHUB_REPOSITORY}/releases?per_page=100"'));
  assert.ok(deploy.includes('.target_commitish'));
  assert.ok(!deploy.includes('gh release list --repo'));
  const release = workflow('release-web');
  assert.match(release, /Preserve signed web package for recovery/);
  assert.match(release, /gh release upload/);
  assert.match(release, /stable_checks == 2/);
  assert.match(deploy, /for attempt in 1 2 3 4 5 6/);
  assert.ok(deploy.includes('/releases/download/${RELEASE_TAG}'));
  assert.match(deploy, /cosign verify-blob/);
  assert.match(deploy, /certificate-oidc-issuer/);
  assert.match(deploy, /revision=\$\(tar -xzOf/);
  assert.match(deploy, /\[\[ "\$target" == "\$revision" \]\]/);
  assert.doesNotMatch(deploy, /gh release download/);
  assert.match(deploy, /sha256sum --check/);
  assert.match(deploy, /publish \"ArchDmit/);
  assert.match(deploy, /publish \"DmitUbuntu/);
  assert.doesNotMatch(deploy, /release-go\.yml|production-release-transaction\.py|lmm-api-deploy\s|operator\s+plan/);
});

test('CI keeps every original quality gate and the translation check name', () => {
  const ci = workflow('ci');
  for (const id of ['repository-contracts', 'release-artifact-contract', 'pi-lmm-provider',
    'web', 'go', 'rust-preview', 'route-coverage-contract', 'rust-real-integration', 'aur-package-matrix', 'quality-gate']) {
    job(ci, id);
  }
  assert.match(job(ci, 'repository-contracts'), /node --test scripts\/workflow-topology\.test\.mjs/);
  const translations = job(ci, 'translations');
  assert.match(translations, /name: Translation regression check/);
  assert.match(translations, /if: github\.ref_type != 'tag'/);
  assert.match(job(ci, 'quality-gate'), /- translations/);
  assert.match(ci, /merge_group:/);
  assert.match(translations, /github\.event\.merge_group\.base_sha/);
  assert.match(translations, /fetch-depth: 0/);
  assert.match(translations, /persist-credentials: false/);
  assert.match(translations, /node --test scripts\/check-i18n\.test\.mjs/);
  assert.match(translations, /node scripts\/check-i18n\.mjs --base "\$BASE_REF" --merge-base/);
  assert.match(translations, /node scripts\/check-i18n\.mjs --base "\$BASE_REF"\n/);
  assert.match(ci, /types: \[opened, reopened, synchronize, ready_for_review\]/);
  assert.match(ci, /workflow_dispatch:\n    inputs:\n      base-ref:/);
  assert.doesNotMatch(ci, /pull_request_target|secrets\./);
});

test('PR formatting checks stay removed while code checks remain', () => {
  assert.throws(() => workflow('pr-check'), /ENOENT/);
  assert.throws(() => read('scripts/pr-quality.mjs'), /ENOENT/);
  assert.throws(() => read('scripts/pr-quality.test.mjs'), /ENOENT/);
  assert.doesNotMatch(workflow('ci'), /pr-quality|PR description policy/);
  assert.match(workflow('ci'), /^  pull_request:/m);
});

test('signed publication is manual and never deploys', () => {
  for (const component of ['go', 'web']) {
    const source = workflow(`release-${component}`);
    assert.match(source, /^  workflow_dispatch:/m);
    assert.doesNotMatch(source, /^  (?:push|pull_request|schedule|workflow_run|deploy):/m);
    assert.doesNotMatch(source, /ssh-private-key|ssh-known-hosts|deploy-production|inputs\.confirm/);
    assert.doesNotMatch(source, /verify-release-work-items.py|issues:\s*read/);
    assert.match(source, /verify-release-commit-checks.sh/);
    assert.match(source, /cosign sign-blob/);
    assert.match(source, /gh release create/);
  }
});

test('consolidation retains specialist evidence without independent trigger storms', () => {
  const ci = workflow('ci');
  assert.match(job(ci, 'web'), /assistant-handoff-confirmation.test.ts/);
  assert.match(job(ci, 'web'), /assistant-handoff-tool.test.tsx/);
  assert.match(job(ci, 'web'), /assistant-handoff-review.test.tsx/);
  assert.match(job(ci, 'web'), /bun test --preload .* --timeout 15000/);
  assert.match(job(ci, 'root-route-acceptance-lockfile'), /cargo fetch --locked/);
  assert.match(job(ci, 'root-route-acceptance-lockfile'), /test-root-route-acceptance.sh/);
  assert.match(job(ci, 'rustsec'), /rustsec\/audit-check@858dc40f52ca2b8570b7a997c1c4e35c6fc9a432/);
  assert.match(ci, /cron: "23 3 \* \* \*"/);
  assert.match(job(ci, 'quality-gate'), /- root-route-acceptance-lockfile/);
  assert.match(job(ci, 'quality-gate'), /- rustsec/);
  assert.match(read('.github/required-release-checks.txt'),
    /Rust root-route acceptance lockfile\|\.github\/workflows\/ci.yml\|push\|main/);
  assert.doesNotMatch(read('.github/required-release-checks.txt'), /rust-root-route-acceptance.yml/);
});

test('test-only concurrency and request filtering cannot cancel production work', () => {
  for (const name of ['ci', 'server-release-qualification']) {
    const source = workflow(name);
    assert.doesNotMatch(source, /server-ops-343-request/);
    assert.doesNotMatch(source, /production-auto-deploy/);
    assert.match(source, /github.event_name == 'pull_request'/);
    assert.match(source, /github.event_name == 'push'/);
    assert.match(source, /github.run_id/);
  }
  assert.doesNotMatch(workflow('server-release-qualification'), /fix\/incident343-additive-recovery/);
});
