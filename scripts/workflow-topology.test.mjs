import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { test } from 'node:test';

const root = new URL('../', import.meta.url);
const read = (path) => readFileSync(new URL(path, root), 'utf8');
const workflow = (name) => read(`.github/workflows/${name}.yml`);

// Source contracts complement actionlint and runtime tests. They do not assert
// that unimplemented business modules are ready or that release is authorized.
test('current workflows preserve preview artifacts without publishing or server access', () => {
  const files = readdirSync(new URL('.github/workflows/', root))
    .filter((name) => /\.ya?ml$/.test(name));
  for (const name of ['ci.yml', 'core-protocol.yml', 'microservice-distribution.yml', 'server-release-qualification.yml']) {
    assert.ok(files.includes(name), `missing workflow: ${name}`);
  }
  assert.ok(!files.includes('server-ops.yml'));
  for (const file of files) {
    const source = read(`.github/workflows/${file}`);
    assert.doesNotMatch(source, /PRODUCTION_SSH|production-auto-deploy|deploy-production|server-ops|inputs\.deploy/);
    assert.doesNotMatch(source, /environment:\s*production/);
    assert.doesNotMatch(source, /^\s*(?:run:\s*)?(?:ssh|scp|rsync)\s/m);
    assert.doesNotMatch(source, /contents: write|id-token: write|gh release\s/);
  }
  for (const path of ['.github/actions/deploy-production/action.yml', '.github/server-ops-343-request.json', 'scripts/auto-deploy-production-release.sh', 'scripts/server-ops.py']) {
    assert.throws(() => read(path), /ENOENT/);
  }
  assert.throws(() => workflow('release-go'), /ENOENT/);
  assert.throws(() => workflow('release-web'), /ENOENT/);
  assert.throws(() => workflow('deploy-web-frontend'), /ENOENT/);
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

test('CLI consumers initialize only the pinned CLI submodule', () => {
  for (const name of ['lmm', 'microservice-distribution', 'linux-packages', 'core-protocol']) {
    const source = workflow(name);
    const commands = [...source.matchAll(/git submodule update[^\n]*/g)].map((match) => match[0]);
    assert.equal(commands.length, name === 'microservice-distribution' ? 2 : 1, name);
    for (const command of commands) {
      assert.equal(command, 'git submodule update --init --depth 1 -- apps/lmm', name);
    }
    assert.doesNotMatch(source, /submodules:\s*(?:true|recursive)/);
  }
  assert.match(workflow('lmm'), /name: Initialize the pinned CLI only\n        working-directory: \.\n/);
});


test('retired single-server deployment tools cannot bypass the preview boundary', () => {
  for (const path of ['scripts/web-ship.py', 'scripts/fetch-deploy-artifact.sh',
    'scripts/build-release-package.sh', 'scripts/reconcile-frontend-package.py',
    'scripts/server-repairs/install-cluster-nginx.sh', 'scripts/render-frontend-proxy-overlay.py',
    'scripts/lmm-api-deploy.sh', '.github/required-release-checks.txt',
    '.github/required-go-web-release-checks.txt']) {
    assert.throws(() => read(path), /ENOENT/, path);
  }
  const preview = workflow('microservice-distribution');
  assert.match(preview, /component: \[core, extensions, cli, web\]/);
  assert.match(preview, /scripts\/build-distribution\.py verify/);
  assert.match(preview, /Preserve preview archives only/);
  assert.doesNotMatch(preview, /LMM_WEB_DEPLOY|PRODUCTION_SSH|gh release|pacman|systemctl/);
  assert.doesNotMatch(workflow('ci'), /service-unavailable\.test\.mjs/);
});


test('only the isolated microkernel checks may run automatically', () => {
  for (const file of readdirSync(new URL('.github/workflows/', root)).filter((name) => /\.ya?ml$/.test(name))) {
    const source = read(`.github/workflows/${file}`);
    const block = source.match(/^on:\n((?:[ \t].*\n|\n)*)/m);
    assert.ok(block, `${file}: event block is required`);
    const events = [...block[1].matchAll(/^  ([\w-]+):/gm)].map((match) => match[1]);
    if (file === 'core-protocol.yml') {
      assert.deepEqual(events, ['workflow_dispatch', 'workflow_call', 'pull_request', 'push']);
      assert.match(block[1], /pull_request:\n    branches: \[wip\/rust-core-go-extensions\]/);
      assert.match(block[1], /branches: \[wip\/rust-core-go-extensions\]/);
      assert.match(source, /permissions:\n  contents: read/);
      assert.doesNotMatch(source, /secrets\.|contents: write|pull_request_target|environment: production/);
    } else if (file === 'linux-packages.yml') {
      assert.deepEqual(events, ['workflow_dispatch', 'workflow_call', 'pull_request']);
      assert.match(block[1], /pull_request:\n    branches: \[wip\/rust-core-go-extensions\]/);
      assert.match(source, /permissions:\n  contents: read/);
      assert.doesNotMatch(source, /secrets\.|contents: write|id-token: write|pull_request_target|gh release|environment: production/);
      assert.match(source, /packaging\/linux\/test_native\.py/);
    } else if (file === 'microservice-distribution.yml') {
      assert.deepEqual(events, ['workflow_dispatch', 'workflow_call']);
      assert.match(source, /permissions:\n  contents: read/);
      assert.doesNotMatch(source, /secrets\.|contents: write|id-token: write|pull_request_target|gh release|environment: production/);
      assert.match(source, /scripts\/build-distribution\.py verify/);
      assert.match(source, /scripts\/test-legal-process\.py/);
    } else if (file === 'mk10-payments.yml') {
      // Payment tests use disposable PostgreSQL, never a channel credential.
      assert.deepEqual(events, ['pull_request', 'push']);
      assert.match(block[1], /branches: \['wip\/mk-10-payments'\]/);
      assert.match(block[1], /apps\/lmm-extensions\/internal\/modules\/payments\/\*\*/);
      assert.match(source, /permissions:\n  contents: read/);
      assert.match(source, /services:\n      postgres:/);
      assert.doesNotMatch(source, /secrets\.|contents: write|pull_request_target|environment: production/);
    } else if (file === 'mk-09-assistant-promotions.yml') {
      // The merged MK-09 module retains isolated, read-only regression checks.
      assert.deepEqual(events, ['push', 'pull_request', 'workflow_dispatch']);
      assert.match(block[1], /branches: \[wip\/mk-09-assistant-promotions\]/);
      assert.match(block[1], /pull_request:\n    branches: \[wip\/rust-core-go-extensions\]/);
      assert.match(block[1], /apps\/lmm-extensions\/internal\/modules\/assistant\/\*\*/);
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
