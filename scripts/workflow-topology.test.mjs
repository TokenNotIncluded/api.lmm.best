import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
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

function jobNeeds(source) {
  const block = source.match(/^    needs:\n((?:      - [\w-]+\n)+)/m);
  assert.ok(block, 'job must declare its dependencies');
  return [...block[1].matchAll(/      - ([\w-]+)/g)].map((match) => match[1]);
}

test('every Ubuntu native test consumer prepares fixed verified tools before tests', () => {
  for (const [name, id] of [
    ['ci', 'release-artifact-contract'], ['ci', 'go'],
    ['release-go', 'test'], ['server-release-qualification', 'go-server'],
  ]) {
    const source = workflow(name);
    const block = job(source, id);
    const action = block.indexOf('uses: sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6');
    const prepare = block.indexOf('run: bash scripts/install-ci-native-cosign.sh');
    const tests = block.indexOf('go test ');
    assert.ok(action >= 0 && prepare > action && tests > prepare, `${name}/${id}: verified canonical cosign before tests`);
    const install = block.match(/sudo apt-get install[^\n]+/);
    assert.ok(install, `${name}/${id}: system dependency installation`);
    for (const tool of ['libarchive-tools', 'nginx', 'curl', 'util-linux', 'systemd']) {
      assert.ok(install[0].split(/\s+/).includes(tool), `${name}/${id}: explicit ${tool} dependency`);
    }
    assert.ok(block.indexOf(install[0]) < tests, `${name}/${id}: system tools before tests`);
  }
  const helper = read('scripts/install-ci-native-cosign.sh');
  assert.match(helper, /GITHUB_ACTIONS:-.*== true/);
  assert.match(helper, /cosign_stage_sha.*sha256sum/);
  assert.match(helper, /cosign_installed_sha.*sha256sum/);
  assert.match(helper, /--owner=root --group=root --mode=0755/);
  assert.match(helper, /cosign_parent in \/ \/usr \/usr\/bin/);
  assert.match(helper, /--mode=0755[^\n]+\/usr\/bin\/cosign/);
  assert.doesNotMatch(helper, /sudo.*(?:chmod|chown)/);
  assert.match(helper, /== 0:0:755/);
  assert.doesNotMatch(helper, /curl|wget|insecure|ignore/);
  const syntax = spawnSync('bash', ['-n', new URL('scripts/install-ci-native-cosign.sh', root).pathname], { encoding: 'utf8' });
  assert.equal(syntax.status, 0, syntax.stderr);
  const refused = spawnSync('bash', [new URL('scripts/install-ci-native-cosign.sh', root).pathname], { encoding: 'utf8', env: { PATH: process.env.PATH, GITHUB_ACTIONS: 'false' } });
  assert.equal(refused.status, 2, 'helper refuses ordinary host execution before any install');
});

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

test('Go/Web publication aggregates every non-Rust CI dependency only on main pushes', () => {
  const ci = workflow('ci');
  const gate = job(ci, 'go-web-release-gate');
  const required = [
    'changes', 'repository-contracts', 'release-artifact-contract',
    'pi-lmm-provider', 'web', 'go', 'route-coverage-contract',
    'aur-package-matrix', 'translations',
  ];
  const rustOnly = new Set([
    'rust-preview', 'rust-real-integration', 'root-route-acceptance-lockfile', 'rustsec',
  ]);
  const allJobs = [...ci.split('\njobs:\n')[1].matchAll(/^  ([\w-]+):\n/gm)]
    .map((match) => match[1]);
  assert.deepEqual(allJobs.filter((id) =>
    !rustOnly.has(id) && !['quality-gate', 'go-web-release-gate'].includes(id)).sort(),
  [...required].sort(), 'new non-Rust jobs must join the publication gate');
  assert.deepEqual(jobNeeds(gate).sort(), [...required].sort());
  assert.match(gate, /name: Go\/Web release qualification gate\n/);
  assert.ok(gate.includes("    if: ${{ always() && github.event_name == 'push' && github.ref == 'refs/heads/main' }}\n"));
  assert.ok(gate.includes('CI_GO_WEB_NEEDS: ${{ toJSON(needs) }}'));
  assert.doesNotMatch(gate, /continue-on-error:|CI_SELECTED|secrets\./);
  assert.deepEqual(jobNeeds(job(ci, 'quality-gate')).sort(),
    [...required, ...rustOnly].sort(), 'full CI must still require every Rust job');

  const script = gate.match(/          python3 -B - <<'PYTHON'\n([\s\S]*?)          PYTHON\n/);
  assert.ok(script, 'publication gate must explicitly validate every dependency result');
  const python = script[1].replace(/^          /gm, '');
  const success = () => Object.fromEntries(required.map((id) => [id, { result: 'success' }]));
  const run = (needs) => spawnSync('python3', ['-B', '-c', python], {
    env: { ...process.env, CI_GO_WEB_NEEDS: JSON.stringify(needs) }, encoding: 'utf8',
  });
  const passed = run(success());
  assert.equal(passed.status, 0, passed.stderr);
  for (const id of required) {
    for (const result of ['failure', 'cancelled', 'skipped', 'neutral', '', null]) {
      const needs = success();
      needs[id] = { result, outputs: { result: 'success' } };
      assert.notEqual(run(needs).status, 0, `${id}: ${result} must block publication`);
    }
    const needs = success();
    delete needs[id];
    assert.notEqual(run(needs).status, 0, `${id}: missing evidence must block publication`);
  }
  for (const needs of [null, [], {}, { ...success(), 'unexpected-job': { result: 'success' } }]) {
    assert.notEqual(run(needs).status, 0, 'invalid dependency inventory must block publication');
  }
});

test('Go/Web publication explicitly selects its component without changing server qualification', () => {
  for (const component of ['go', 'web']) {
    assert.ok(workflow(`release-${component}`).includes(
      `bash scripts/verify-release-commit-checks.sh "\${GITHUB_SHA}" --component ${component}`),
    `${component} publication must request the Go/Web evidence inventory explicitly`);
  }
  const gate = job(workflow('server-release-qualification'), 'release-gate');
  assert.match(gate, /name: Server release qualification gate\n/);
  assert.deepEqual(jobNeeds(gate).sort(), ['go-server', 'harness-contracts']);
});

test('standalone STAGED archival runs in both offline owner and required release contracts', () => {
  for (const [name, id] of [
    ['standalone-deployment-tests', 'standalone-deployment-tests'],
    ['server-release-qualification', 'harness-contracts'],
  ]) {
    const contracts = job(workflow(name), id);
    assert.match(contracts, /python3 -B scripts\/test-deploy-systemd-archive-staged\.py(?: -v)?\n/);
    assert.doesNotMatch(contracts, /continue-on-error:/);
  }
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

test('signed Go archives derive merchant capability from the compiled source for both architectures', () => {
  const source = workflow('release-go');
  const prepare = job(source, 'prepare');
  const build = job(source, 'build');
  assert.ok(prepare.includes('merchant_writer_capability: ${{ steps.identity.outputs.merchant_writer_capability }}'));
  assert.ok(prepare.includes('merchant_writer_capability=$("$contract_cli_dir/lmm-api" merchant-store-writer-gate capability)'));
  assert.ok(prepare.includes('echo "merchant_writer_capability=$merchant_writer_capability"'));
  assert.match(build, /arch: \[amd64, arm64\]/);
  assert.ok(build.includes('MERCHANT_WRITER_CAPABILITY: ${{ needs.prepare.outputs.merchant_writer_capability }}'));
  assert.ok(build.includes('printf \'%s\\n\' "$MERCHANT_WRITER_CAPABILITY" > \\\n            "$bundle/MERCHANT_STORE_WRITER_CAPABILITY"'));
  assert.doesNotMatch(build, /merchant-store-writer-gate|MERCHANT_WRITER_CAPABILITY:\s*[1-9]\b/);
  for (const contracts of [job(source, 'package-contract'), job(workflow('ci'), 'aur-package-matrix')]) {
    assert.match(contracts, /bash packaging\/aur\/test-merchant-writer-marker\.sh\n/);
    assert.doesNotMatch(contracts, /continue-on-error:/);
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

test('paid tool-market PostgreSQL qualification cannot silently use SQLite or skip isolation', () => {
  const go = job(workflow('server-release-qualification'), 'go-server');
  assert.match(go, /image: postgres:18-alpine/);
  const step = go.split(/\n(?=      - name: )/).find((part) =>
    part.startsWith('      - name: Protect paid tool-market authorization and ledger under PostgreSQL concurrency\n'));
  assert.ok(step, 'paid market qualification must have its own PostgreSQL step');
  assert.match(step, /working-directory: apps\/api-go\n/);
  assert.match(step, /TEST_POSTGRES_DSN: postgres:\/\/[^\n]+@127\.0\.0\.1:5432\/lmm_test_release\?sslmode=disable\n/);
  assert.match(step, /TEST_POSTGRES_ISOLATED_SCHEMA: '1'\n/);
  assert.doesNotMatch(step, /continue-on-error:|\n        if:/);
  const tests = [
    ['TestToolMarketPaidHTTPMCPUploadAndSettlementPostgres', 'tool_market_paid_e2e_test.go'],
    ['TestToolMarketPaidPostgresBuyerIsolationAndConcurrentLedger', 'tool_market_paid_postgres_e2e_test.go'],
  ];
  const names = tests.map(([name]) => name).join('|');
  assert.ok(step.includes(`run: go test -race -count=1 -v ./service -run '^(${names})$' -timeout 180s`));
  for (const [name, file] of tests) {
    assert.ok(read(`apps/api-go/service/${file}`).includes(`func ${name}(t *testing.T)`),
      `qualification filter must still select the real ${name} test`);
  }
});

test('drawing expiry qualification uses real MySQL with repeatable-read session defaults', () => {
  const go = job(workflow('server-release-qualification'), 'go-server');
  assert.match(go, /image: mysql:8\.4/);
  const step = go.split(/\n(?=      - name: )/).find((part) =>
    part.startsWith('      - name: Protect drawing expiry against MySQL repeatable-read snapshots\n'));
  assert.ok(step, 'drawing expiry must have its own real MySQL qualification step');
  assert.match(step, /working-directory: apps\/api-go\n/);
  assert.match(step, /TEST_MYSQL_DSN: root:[^\n]+@tcp\(127\.0\.0\.1:3306\)\/lmm_test_release\?parseTime=true\n/);
  assert.match(step, /TEST_MYSQL_ISOLATED_DATABASE: '1'\n/);
  assert.doesNotMatch(step, /continue-on-error:|\n        if:/);
  const name = 'TestToolMarketDrawingExpiryReadsCommittedBillingMySQL';
  assert.ok(step.includes(`run: go test -race -count=1 -v ./model -run '^${name}$' -timeout 180s`));
  assert.ok(read('apps/api-go/model/tool_market_drawing_mysql_test.go').includes(`func ${name}(t *testing.T)`),
    'qualification filter must select the actual MySQL regression');
});

test('tool-market client identity qualification exercises real MySQL collation aliases', () => {
  const go = job(workflow('server-release-qualification'), 'go-server');
  assert.match(go, /image: mysql:8\.4/);
  const step = go.split(/\n(?=      - name: )/).find((part) =>
    part.startsWith('      - name: Protect tool-market client boundaries against MySQL collation aliases\n'));
  assert.ok(step, 'client boundaries need a dedicated real MySQL qualification step');
  assert.match(step, /working-directory: apps\/api-go\n/);
  assert.match(step, /TEST_MYSQL_DSN: root:[^\n]+@tcp\(127\.0\.0\.1:3306\)\/lmm_test_release\?parseTime=true\n/);
  assert.match(step, /TEST_MYSQL_ISOLATED_DATABASE: '1'\n/);
  assert.doesNotMatch(step, /continue-on-error:|\n        if:/);
  const name = 'TestToolMarketClientIdentityMySQL';
  assert.ok(step.includes(`run: go test -race -count=1 -v ./model -run '^${name}$' -timeout 180s`));
  assert.ok(read('apps/api-go/model/tool_market_clients_mysql_test.go').includes(`func ${name}(t *testing.T)`),
    'qualification filter must select the actual client-identity regression');
});

test('tool-market accounting qualification selects both MySQL lock-contention regressions', () => {
  const go = job(workflow('server-release-qualification'), 'go-server');
  assert.match(go, /image: mysql:8\.4/);
  const step = go.split(/\n(?=      - name: )/).find((part) =>
    part.startsWith('      - name: Protect tool-market limits and settlement counters under MySQL lock contention\n'));
  assert.ok(step, 'reservation and settlement counters need real MySQL qualification');
  assert.match(step, /working-directory: apps\/api-go\n/);
  assert.match(step, /TEST_MYSQL_DSN: root:[^\n]+@tcp\(127\.0\.0\.1:3306\)\/lmm_test_release\?parseTime=true\n/);
  assert.match(step, /TEST_MYSQL_ISOLATED_DATABASE: '1'\n/);
  assert.doesNotMatch(step, /continue-on-error:|\n        if:/);
  const tests = ['TestToolMarketConcurrentFinishCountersMySQL', 'TestToolMarketConcurrentReserveLimitsMySQL'];
  assert.ok(step.includes(`run: go test -race -count=1 -v ./model -run '^(${tests.join('|')})$' -timeout 180s`));
  for (const name of tests) {
    assert.ok(read('apps/api-go/model/tool_market_finish_mysql_test.go').includes(`func ${name}(t *testing.T)`),
      `qualification filter must select the actual ${name} regression`);
  }
});
