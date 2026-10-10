import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { test } from 'node:test';

const root = new URL('../', import.meta.url);
const read = (path) => readFileSync(new URL(path, root), 'utf8');
const workflow = (name) => read(`.github/workflows/${name}.yml`);

// Source contracts complement actionlint and runtime tests. They do not assert
// that unimplemented business modules are ready or that release is authorized.
test('backend and release workflows do not have server access', () => {
  const files = readdirSync(new URL('.github/workflows/', root))
    .filter((name) => /\.ya?ml$/.test(name));
  for (const name of ['ci.yml', 'core-protocol.yml', 'release-web.yml', 'server-release-qualification.yml']) {
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
  assert.throws(() => workflow('release-go'), /ENOENT/);
  assert.match(workflow('server-release-qualification'), /uses: \.\/\.github\/workflows\/core-protocol\.yml/);
  assert.throws(() => read('scripts/ci/qualify-go-migration-startup.sh'), /ENOENT/);
});


test('OpenCode fixture runs the pinned child and real host without privileged events', () => {
  const source = workflow('opencode-lmm-auth');
  assert.match(source, /permissions:\n  contents: read/);
  assert.doesNotMatch(source, /pull_request_target|secrets\.|contents: write/);
  assert.match(source, /persist-credentials: false/);
  assert.match(source, /git submodule update --init --depth 1 -- packages\/opencode-lmm-auth/);
  assert.match(source, /host-version: \['1\.18\.34', 'latest'\]/);
  assert.match(source, /opencode-ai@\$HOST_VERSION/);
  assert.match(source, /npm run test:host && npm run test:integration/);
});


test('manual frontend deployment is restricted to a signed web release on both origins', () => {
  const deploy = workflow('deploy-web-frontend');
  const triggers = deploy.split('\non:\n')[1]?.split('\npermissions:\n')[0];
  assert.ok(triggers, 'frontend deploy workflow has an event block');
  assert.deepEqual([...triggers.matchAll(/^  ([\w-]+):/gm)].map((match) => match[1]), ['workflow_dispatch']);
  assert.doesNotMatch(deploy, /github\.event\.workflow_run/);
  assert.match(deploy, /environment: production/);
  assert.match(deploy, /LMM_WEB_DEPLOY_SSH_KEY/);
  assert.match(deploy, /LMM_WEB_DEPLOY_KNOWN_HOSTS/);
  assert.ok(deploy.includes('INPUT_TAG: ${{ inputs.release_tag }}'));
  assert.ok(deploy.includes('.target_commitish'));
  assert.ok(!deploy.includes('gh release list --repo'));
  const release = workflow('release-web');
  assert.match(release, /Preserve signed web package for recovery/);
  assert.match(release, /gh release upload/);
  assert.match(release, /stable_checks == 2/);
  assert.match(deploy, /curl --fail [^\n]*\\\n\s+--retry 3 /);
  assert.ok(deploy.includes('/releases/download/${RELEASE_TAG}'));
  assert.match(deploy, /cosign verify-blob/);
  assert.match(deploy, /certificate-oidc-issuer/);
  assert.match(deploy, /revision=\$\(tar -xzOf/);
  assert.match(deploy, /\[\[ "\$target" == "\$revision" \]\]/);
  assert.doesNotMatch(deploy, /gh release download/);
  assert.match(deploy, /sha256sum --check/);
  assert.match(deploy, /publish "API origin" "\$API_HOST" "\$API_PORT"\n\s+publish "Public ingress" "\$INGRESS_HOST" "\$INGRESS_PORT"/);
  assert.match(deploy, /StrictHostKeyChecking=yes/);
  assert.doesNotMatch(deploy, /release-go\.yml|production-release-transaction\.py|lmm-api-deploy\s|operator\s+plan/);
});


test('only the isolated microkernel checks may run automatically', () => {
  for (const file of readdirSync(new URL('.github/workflows/', root)).filter((name) => /\.ya?ml$/.test(name))) {
    const source = read(`.github/workflows/${file}`);
    const block = source.match(/^on:\n((?:[ \t].*\n|\n)*)/m);
    assert.ok(block, `${file}: event block is required`);
    const events = [...block[1].matchAll(/^  ([\w-]+):/gm)].map((match) => match[1]);
    if (file === 'core-protocol.yml') {
      assert.deepEqual(events, ['workflow_dispatch', 'workflow_call', 'push']);
      assert.match(block[1], /branches: \[wip\/rust-core-go-extensions\]/);
      assert.match(source, /permissions:\n  contents: read/);
      assert.doesNotMatch(source, /secrets\.|contents: write|pull_request_target|environment: production/);
    } else if (file === 'mk-01-identity.yml') {
      // MK-01 runs read-only checks on its isolated branch and PRs.
      assert.deepEqual(events, ['push', 'pull_request']);
      assert.match(block[1], /branches: \[wip\/mk-01-identity-teams\]/);
      assert.match(block[1], /pull_request:\n    branches: \[wip\/rust-core-go-extensions\]/);
      assert.match(source, /permissions:\n  contents: read/);
      assert.doesNotMatch(source, /secrets\.|contents: write|pull_request_target|environment: production/);
    } else {
      assert.deepEqual(events, ['workflow_dispatch'], `${file}: no new automatic publication or deployment`);
    }
  }
});

test('manual checks reuse the exact core and extension verification pipeline', () => {
  for (const name of ['ci', 'server-release-qualification']) {
    const source = workflow(name);
    assert.match(source, /uses: \.\/\.github\/workflows\/core-protocol\.yml/);
    assert.doesNotMatch(source, /qualify-go-migration-startup|credit-balance-rebase|\.\/model|\.\/relaykit/);
  }
  const source = workflow('core-protocol');
  for (const command of ['cargo clippy --locked --all-targets -- -D warnings', 'cargo test --locked --all-targets',
    'go mod verify', 'go vet ./...', 'go test -race', 'scripts/test-core-rpc-docker.py', 'scripts/test-core-boundaries.py',
    'scripts/test-local-release-tests.py', 'scripts/generate-core-protocol.sh --check',
    'apps/lmm-extensions/internal/modules/store/pgtest', 'go test -mod=readonly -race',
    'contracts/proto/tests/process_recovery.py', 'LMM_RELAY_EXTENSION_BIN']) {
    assert.ok(source.includes(command), command);
  }
  assert.match(source, /services:\n      postgres:/);
  assert.match(source, /DATABASE_URL: postgres:\/\/postgres:postgres@127\.0\.0\.1:5432\/postgres/);
  assert.match(source, /working-directory: apps\/lmm-extensions/);
  assert.doesNotMatch(source, /continue-on-error:|apps\/extensions-go/);
});

test('the full manual gate cannot pass when an implemented component fails or is skipped', () => {
  const source = workflow('ci');
  assert.match(source, /needs: \[microkernel, web, translations\]/);
  assert.match(source, /if: \$\{\{ always\(\) \}\}/);
  for (const name of ['KERNEL_RESULT', 'WEB_RESULT', 'TRANSLATION_RESULT']) {
    assert.ok(source.includes(`test "$${name}" = success`));
  }
  assert.match(source, /name: Translation regression check/);
  assert.match(source, /bun install --frozen-lockfile/);
  assert.doesNotMatch(source, /continue-on-error:/);
});

test('obsolete database upgrade tooling is not an alternate installation path', () => {
  for (const path of ['scripts/deploy-systemd.py', 'scripts/deploy-shared-postgres.py',
    'scripts/ci/qualify-go-migration-startup.sh', 'packaging/common/lmm-api/migration-compatibility.env']) {
    assert.throws(() => read(path), /ENOENT/);
  }
  const sample = read('.env.example');
  assert.match(sample, /LMM_EXTENSION_TOKEN_FILE=/);
  assert.doesNotMatch(sample, /SQL_DSN=|SESSION_SECRET=|SKIP_64BIT_QUOTA_SCHEMA_CHECK/);
  for (const path of ['README.md', 'README_EN.md', 'docs/development.md']) {
    const text = read(path);
    assert.doesNotMatch(text, /Startup applies schema migrations by default|服务启动时默认执行数据库迁移/);
    assert.match(text, /init-db/);
  }
});
