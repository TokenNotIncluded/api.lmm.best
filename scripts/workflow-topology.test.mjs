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

test('migration preserves qualification and has no separate legacy deployment workflow', () => {
  const files = readdirSync(new URL('.github/workflows/', root))
    .filter((name) => /\.ya?ml$/.test(name)).sort();
  assert.deepEqual(files, ['ci.yml', 'pr-check.yml', 'release-go.yml', 'release-web.yml', 'server-ops.yml',
    'server-release-qualification.yml']);
  assert.match(workflow('server-release-qualification'), /qualify-go-migration-startup.sh/);
  for (const component of ['go', 'web']) {
    assert.match(workflow(`release-${component}`), /^  workflow_dispatch:/m);
    assert.doesNotMatch(workflow(`release-${component}`), /^  (?:push|pull_request|schedule|workflow_run):/m);
  }
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
  assert.match(request, /recover-red-packet-schema-343/);
  assert.match(request, /go test -race -count=1/);
  assert.match(request, /prepare-ci-apt.py/);
  assert.match(request, /POSTGRES_DB: lmm_test_release/);
  assert.match(request, /server-ops-helper-payload.py/);
  assert.match(request, /HEAD:scripts\/server-ops.py/);
  assert.doesNotMatch(request, /server-ops-transport.py/);
  assert.match(controller, /github\.repository == 'TokenNotIncluded\/api\.lmm\.best'/);
  assert.match(controller, /github\.event_name == 'workflow_dispatch'/);
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

test('release publication and deployment require explicit manual dispatch', () => {
  for (const component of ['go', 'web']) {
    const source = workflow(`release-${component}`);
    assert.match(source, /^  workflow_dispatch:/m);
    assert.doesNotMatch(source, /^  (?:push|pull_request|schedule|workflow_run):/m);
    assert.match(source, /default: false/);
    assert.match(job(source, 'deploy'), /inputs.deploy && inputs.confirm == 'api.lmm.best'/);
    assert.match(source, /verify-release-work-items.py/);
    assert.match(source, /verify-release-commit-checks.sh/);
  }
  assert.match(action, /using: composite/);
  assert.match(action, /run: bash scripts\/auto-deploy-production-release\.sh/);
});

test('shared deployment keeps the signed-package script and pinned verification tool', () => {
  assert.match(action, /using: composite/);
  assert.match(action, /sigstore\/cosign-installer@[0-9a-f]{40}/);
  assert.match(action, /run: bash scripts\/auto-deploy-production-release\.sh/);
  assert.match(action, /sudo --preserve-env=GITHUB_ACTIONS python3 scripts\/prepare-ci-apt\.py/);
  assert.match(action, /test -n "\$PRODUCTION_SSH_PRIVATE_KEY"/);
  assert.match(action, /test -n "\$PRODUCTION_SSH_KNOWN_HOSTS"/);
  const script = read('scripts/auto-deploy-production-release.sh');
  assert.match(script, /production-release-transaction.py/);
  assert.match(script, /--acceptance-script .*verify-public-production.py/);
  assert.match(script, /--expected-backend-version/);
  assert.doesNotMatch(action, /run:.*verify-public-production.py/);
  assert.match(action, /test-production-release-transaction.py/);
  assert.match(action, /actions\/upload-artifact@[0-9a-f]{40}/);
  assert.match(action, /PRODUCTION_RESULT_FILE: \$\{\{ runner.temp \}\}/);
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
    assert.match(source, /paths-ignore: \[\.github\/server-ops-343-request.json\]/);
    assert.doesNotMatch(source, /production-auto-deploy/);
    assert.match(source, /github.event_name == 'pull_request'/);
    assert.match(source, /github.event_name == 'push'/);
    assert.match(source, /github.run_id/);
  }
  assert.doesNotMatch(workflow('server-release-qualification'), /fix\/incident343-additive-recovery/);
});
