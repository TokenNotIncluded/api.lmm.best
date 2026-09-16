import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, test } from 'node:test';

const root = new URL('../', import.meta.url);
const read = (path) => readFileSync(new URL(path, root), 'utf8');
const workflow = (name) => read(`.github/workflows/${name}.yml`);
const action = read('.github/actions/deploy-production/action.yml');

// These assertions complement actionlint: they protect the repository's intended
// entry points and trust boundaries, rather than trying to parse arbitrary YAML.
function job(source, id) {
  const jobs = source.split('\njobs:\n')[1];
  assert.ok(jobs, 'workflow must contain jobs');
  const found = jobs.split(/\n(?=  [\w-]+:\n)/).find((part) => part.startsWith(`  ${id}:\n`));
  assert.ok(found, `missing job: ${id}`);
  return found;
}

test('migration preserves upstream qualification and isolates the legacy deployment adapter', () => {
  const files = readdirSync(new URL('.github/workflows/', root))
    .filter((name) => /\.ya?ml$/.test(name)).sort();
  assert.deepEqual(files, ['assistant-support-regressions.yml', 'ci.yml', 'deploy-production.yml',
    'pr-check.yml', 'release-go.yml', 'release-web.yml', 'rust-root-route-acceptance.yml',
    'rust-security-audit.yml', 'server-ops.yml', 'server-release-qualification.yml']);
  const legacy = workflow('deploy-production');
  assert.match(legacy, /^  workflow_run:/m);
  assert.doesNotMatch(legacy, /^  push:/m);
  assert.match(job(legacy, 'deploy'), /needs: legacy-route/);
  assert.match(job(legacy, 'deploy'), /needs.legacy-route.outputs.required == 'true'/);
  assert.match(job(legacy, 'legacy-route'), /-f \.github\/actions\/deploy-production\/action.yml/);
  assert.doesNotMatch(job(legacy, 'legacy-route'), /secrets\.|environment: production/);
  assert.match(workflow('server-release-qualification'), /qualify-go-migration-startup.sh/);
});

test('server operations stay manual, main-only, and share the production lock', () => {
  const ops = workflow('server-ops');
  const controller = job(ops, 'server-ops');
  assert.match(ops, /^  workflow_dispatch:/m);
  assert.doesNotMatch(ops, /^  (?:pull_request|pull_request_target|schedule|workflow_run):/m);
  assert.match(ops, /paths: \[\.github\/server-ops-343-request.json\]/);
  const request = job(ops, 'owner-request');
  assert.match(request, /github.actor == 'LIghtJUNction'/);
  assert.match(request, /github.triggering_actor == 'LIghtJUNction'/);
  assert.match(request, /server-ops-commit-request.py --validate-only/);
  assert.match(ops, /default: diagnose/);
  assert.match(ops, /group: production-auto-deploy\n  cancel-in-progress: false/);
  assert.match(ops, /permissions:\n  contents: read/);
  assert.match(controller, /github\.ref == 'refs\/heads\/main'/);
  assert.match(controller, /environment: production/);
  assert.match(controller, /ref: \$\{\{ github\.sha \}\}/);
  assert.match(controller, /persist-credentials: false/);
  assert.match(controller, /OPS_ALLOWED_ACTORS: \$\{\{ vars\.PRODUCTION_OPS_ALLOWED_ACTORS \}\}/);
  assert.match(controller, /python3 scripts\/test-server-ops\.py/);
  const validation = controller.indexOf('run: python3 scripts/server-ops.py --validate-only');
  const credentials = controller.indexOf('secrets.PRODUCTION_SSH_PRIVATE_KEY');
  assert.ok(validation >= 0 && credentials > validation,
    'authorization must be validated before the credential-bearing step');
  assert.doesNotMatch(controller, /continue-on-error|: write/);
});

test('CI keeps every original quality gate and the translation check name', () => {
  const ci = workflow('ci');
  for (const id of ['repository-contracts', 'release-artifact-contract', 'pi-lmm-provider',
    'web', 'go', 'rust-preview', 'route-coverage-contract', 'rust-real-integration', 'aur-package-matrix']) {
    job(ci, id);
  }
  assert.match(job(ci, 'repository-contracts'), /node --test scripts\/workflow-topology\.test\.mjs/);
  const translations = job(ci, 'translations');
  assert.match(translations, /name: Translation regression check/);
  assert.match(translations, /if: github\.event_name != 'push' \|\| github\.ref_type != 'tag'/);
  assert.match(job(ci, 'quality-gate'), /- translations/);
  assert.match(ci, /merge_group:/);
  assert.match(translations, /fetch-depth: 0/);
  assert.match(translations, /persist-credentials: false/);
  assert.match(translations, /node --test scripts\/check-i18n\.test\.mjs/);
  assert.match(translations, /node scripts\/check-i18n\.mjs --base "\$BASE_REF" --merge-base/);
  assert.match(translations, /node scripts\/check-i18n\.mjs --base "\$BASE_REF"\n/);
  assert.match(ci, /types: \[opened, reopened, synchronize, ready_for_review\]/);
  assert.match(ci, /workflow_dispatch:\n    inputs:\n      base-ref:/);
  assert.doesNotMatch(ci, /pull_request_target|secrets\./);
});

test('PR metadata policy stays isolated, read-only, and on trusted base code', () => {
  const pr = workflow('pr-check');
  assert.match(pr, /pull_request_target:/);
  assert.match(pr, /types: \[opened, reopened, edited, synchronize, ready_for_review\]/);
  assert.match(pr, /ref: \$\{\{ github\.event\.pull_request\.base\.sha \}\}/);
  assert.match(pr, /contents: read/);
  assert.match(pr, /pull-requests: read/);
  assert.match(pr, /persist-credentials: false/);
  assert.doesNotMatch(pr, /: write|secrets\.|pull_request\.head\./);
});

for (const [component, needs, revision] of [
  ['go', '[prepare, publish]', 'needs.prepare.outputs.revision'],
  ['web', 'web', 'github.sha'],
]) {
  test(`${component} deploy depends on publication, keeps the production lock, and cannot run on a branch`, () => {
    const source = workflow(`release-${component}`);
    const deploy = job(source, 'deploy');
    assert.ok(deploy.includes(`    needs: ${needs}\n`));
    assert.ok(deploy.includes(`if: success() && github.repository == 'TokenNotIncluded/api.lmm.best' && startsWith(github.ref, 'refs/tags/${component}-v')`));
    assert.match(deploy, /environment: production/);
    assert.match(deploy, /group: production-auto-deploy\n      cancel-in-progress: false/);
    assert.match(deploy, /timeout-minutes: 50/);
    assert.match(deploy, /permissions:\n      contents: read\n      actions: read/);
    assert.ok(deploy.includes(`ref: \${{ ${revision} }}`));
    assert.ok(deploy.includes(`release-sha: \${{ ${revision} }}`));
    assert.match(deploy, /fetch-depth: 0/);
    assert.match(deploy, /persist-credentials: false/);
    assert.match(deploy, /uses: \.\/\.github\/actions\/deploy-production/);
    assert.match(deploy, /release-tag: \$\{\{ github\.ref_name \}\}/);
    assert.match(deploy, /github-token: \$\{\{ github\.token \}\}/);
    for (const [input, secret] of [['ssh-private-key', 'PRODUCTION_SSH_PRIVATE_KEY'], ['ssh-known-hosts', 'PRODUCTION_SSH_KNOWN_HOSTS']]) {
      assert.ok(deploy.includes(`${input}: \${{ secrets.${secret} }}`));
    }
    assert.doesNotMatch(deploy, /always\(\)|continue-on-error|: write/);
    assert.ok(source.includes(`/.github/workflows/release-${component}.yml@refs/tags/`),
      'existing Sigstore signing identity must not be renamed');
    assert.match(source, /bash scripts\/verify-release-commit-checks\.sh/);
  });
}

test('shared deployment keeps the signed-package script and pinned verification tool', () => {
  assert.match(action, /using: composite/);
  assert.match(action, /sigstore\/cosign-installer@[0-9a-f]{40}/);
  assert.match(action, /run: bash scripts\/auto-deploy-production-release\.sh/);
  assert.match(action, /sudo --preserve-env=GITHUB_ACTIONS python3 scripts\/prepare-ci-apt\.py/);
  assert.match(action, /test -n "\$PRODUCTION_SSH_PRIVATE_KEY"/);
  assert.match(action, /test -n "\$PRODUCTION_SSH_KNOWN_HOSTS"/);
  assert.match(action, /verify-public-production.py/);
  assert.match(action, /--expected-backend-version/);
  assert.doesNotMatch(action, /apt-get install[^\n]*docker\.io|secrets\./);
});

// Execute the actual composite action's context guard, without credentials,
// package installs, network requests, or a production deployment.
const block = action.match(/^      run: \|\n((?: {8}[^\n]*\n|\n)+)/m);
assert.ok(block, 'the first action step must contain the release context guard');
const guard = block[1].replace(/^ {8}/gm, '');
const fixture = mkdtempSync(join(tmpdir(), 'lmm-workflow-context-'));
after(() => rmSync(fixture, { recursive: true, force: true }));
const git = (...args) => execFileSync('git', [
  '-c', 'user.name=Workflow Test', '-c', 'user.email=workflow@example.invalid', ...args,
], { cwd: fixture, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
git('init', '--quiet');
git('commit', '--quiet', '--allow-empty', '-m', 'old release');
const oldRevision = git('rev-parse', 'HEAD');
git('tag', 'go-v9.0.0');
git('commit', '--quiet', '--allow-empty', '-m', 'candidate release');
const revision = git('rev-parse', 'HEAD');
git('tag', 'go-v1.2.3');
git('tag', 'web-v1.2.3');
git('tag', '-a', 'web-v1.2.4', '-m', 'annotated tag');

function checkContext(overrides = {}) {
  const result = spawnSync('bash', ['-euo', 'pipefail', '-c', guard], {
    cwd: fixture,
    encoding: 'utf8',
    timeout: 5000,
    env: {
      ...process.env,
      RELEASE_TAG: 'go-v1.2.3', RELEASE_SHA: revision,
      GITHUB_SHA: revision, GITHUB_REF: 'refs/tags/go-v1.2.3',
      GITHUB_REPOSITORY: 'TokenNotIncluded/api.lmm.best',
      ...overrides,
    },
  });
  assert.ifError(result.error);
  return result.status;
}

for (const tag of ['go-v1.2.3', 'web-v1.2.3', 'web-v1.2.4']) {
  test(`deployment guard accepts the exact released revision: ${tag}`, () => {
    assert.equal(checkContext({ RELEASE_TAG: tag, GITHUB_REF: `refs/tags/${tag}` }), 0);
  });
}

for (const [name, env] of [
  ['personal fork', { GITHUB_REPOSITORY: 'LIghtJUNction/api.lmm.best' }],
  ['branch ref', { GITHUB_REF: 'refs/heads/main' }],
  ['wrong component ref', { GITHUB_REF: 'refs/tags/web-v1.2.3' }],
  ['non-release tag', { RELEASE_TAG: 'v1.2.3', GITHUB_REF: 'refs/tags/v1.2.3' }],
  ['leading-zero version', { RELEASE_TAG: 'go-v01.2.3', GITHUB_REF: 'refs/tags/go-v01.2.3' }],
  ['missing revision', { RELEASE_SHA: '' }],
  ['different event revision', { GITHUB_SHA: oldRevision }],
  ['different checkout revision', { RELEASE_SHA: oldRevision, GITHUB_SHA: oldRevision }],
  ['tag bound to another commit', { RELEASE_TAG: 'go-v9.0.0', GITHUB_REF: 'refs/tags/go-v9.0.0' }],
  ['missing tag', { RELEASE_TAG: 'go-v8.8.8', GITHUB_REF: 'refs/tags/go-v8.8.8' }],
  ['shell metacharacters', { RELEASE_TAG: 'go-v1.2.3; exit 0' }],
  ['prerelease', { RELEASE_TAG: 'go-v1.2.3-rc.1', GITHUB_REF: 'refs/tags/go-v1.2.3-rc.1' }],
]) {
  test(`deployment guard rejects ${name}`, () => {
    assert.notEqual(checkContext(env), 0);
  });
}
