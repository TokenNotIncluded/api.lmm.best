from pathlib import Path
import subprocess, re


def git(*args):
    return subprocess.check_output(['git', *args], text=True)


def write(path, text):
    p = Path(path)
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(text)


ours = ['.github/server-ops-343-request.json', '.github/workflows/deploy-production.yml',
        'scripts/server-ops-commit-request.py', 'scripts/server-repairs/inspect-startup-343.sh',
        'scripts/test-server-repair-startup.py']
for path in ours:
    write(path, git('show', '3530a142:' + path))
p = Path('.github/workflows/ci.yml')
s = p.read_text()
pattern = r'^<<<<<<< HEAD\n(.*?)^=======\n(.*?)^>>>>>>> 69f86afe\n'
assert len(list(re.finditer(pattern, s, re.M | re.S))) == 3
counter = [0]


def both(match):
    index = counter[0]
    counter[0] += 1
    return match[2] + match[1] if index == 0 else match[1] + '\n' + match[2]


s = re.sub(pattern, both, s, flags=re.M | re.S)
s = s.replace('      - aur-package-matrix\n', '      - aur-package-matrix\n      - translations\n')
s = s.replace("    # Tag pushes have no meaningful previous branch revision to compare.\n    if: github.event_name != 'push' || github.ref_type != 'tag'\n", '')
s = s.replace('      - name: Check added keys and removed translations\n', "      - name: Check added keys and removed translations\n        # The checker still runs on tags; only the branch comparison is inapplicable.\n        if: github.event_name != 'push' || github.ref_type != 'tag'\n")
s = s.replace('github.event.pull_request.base.sha || github.event.before || inputs.base-ref', 'github.event.pull_request.base.sha || github.event.merge_group.base_sha || github.event.before || inputs.base-ref')
s = s.replace('      - name: Test workflow topology and deployment guards\n        run: node --test scripts/workflow-topology.test.mjs\n', '''      - name: Test workflow topology and deployment guards
        run: |
          node --test scripts/workflow-topology.test.mjs
          python3 scripts/test-server-ops.py
          python3 scripts/test-server-repair-startup.py
          python3 scripts/test-server-ops-commit-request.py
''')
p.write_text(s)
p = Path('scripts/ci_quality_gate.py')
p.write_text(p.read_text().replace('    "aur-package-matrix",\n', '    "aur-package-matrix",\n    "translations",\n'))
p = Path('scripts/server-ops.py')
s = p.read_text().replace('REPOSITORY = "LIghtJUNction/api.lmm.best"', 'REPOSITORY = "TokenNotIncluded/api.lmm.best"\nOWNER = "LIghtJUNction"').replace('owner = REPOSITORY.split("/")[0]', 'owner = OWNER')
p.write_text(s)
p = Path('scripts/server-ops-commit-request.py')
p.write_text(p.read_text().replace("with_name('server-ops-transport.py')", "with_name('server-ops.py')"))
Path('scripts/server-ops-transport.py').unlink()
p = Path('.github/workflows/server-ops.yml')
s = p.read_text().replace("github.repository == 'LIghtJUNction/api.lmm.best'", "github.repository == 'TokenNotIncluded/api.lmm.best'")
transport_blob = git('hash-object', 'scripts/server-ops.py').strip()
s = s.replace('          python3 scripts/test-server-ops.py', '          test "$(git rev-parse HEAD:scripts/server-ops.py)" = ' + transport_blob + '\n          python3 scripts/test-server-ops.py')
p.write_text(s)
p = Path('.github/workflows/deploy-production.yml')
s = p.read_text()
s = s[:s.index('\n  owner-incident-diagnosis:')]
s = s.replace('  push:\n    branches: [main]\n    paths: [.github/server-ops-343-request.json]\n', '')
s = s.replace('  deploy:\n', '''  legacy-route:
    name: Select legacy release deployment
    if: >-
      github.event_name == 'workflow_run' &&
      github.event.workflow_run.conclusion == 'success' &&
      (startsWith(github.event.workflow_run.head_branch, 'go-v') ||
       startsWith(github.event.workflow_run.head_branch, 'web-v'))
    runs-on: ubuntu-latest
    timeout-minutes: 5
    outputs:
      required: ${{ steps.route.outputs.required }}
    steps:
      - name: Inspect immutable release source without production credentials
        uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0
        with:
          ref: ${{ github.event.workflow_run.head_sha }}
          persist-credentials: false
      - name: Skip releases that already own their deployment job
        id: route
        run: |
          if [[ -f .github/actions/deploy-production/action.yml ]]; then
            echo 'required=false' >> "$GITHUB_OUTPUT"
          else
            echo 'required=true' >> "$GITHUB_OUTPUT"
          fi

  deploy:
    needs: legacy-route
''', 1)
position = s.index('  deploy:\n')
before, deploy = s[:position], s[position:]
deploy = deploy.replace("      github.event_name == 'workflow_run' &&", "      needs.legacy-route.outputs.required == 'true' &&\n      github.event_name == 'workflow_run' &&", 1)
p.write_text(before + deploy)
p = Path('.github/actions/deploy-production/action.yml')
s = p.read_text().replace('        set -euo pipefail\n', '        set -euo pipefail\n        [[ "$GITHUB_REPOSITORY" == TokenNotIncluded/api.lmm.best ]]\n', 1)
s += '''
    - name: Verify public backend version, pages and referenced assets
      shell: bash
      env:
        RELEASE_TAG: ${{ inputs.release-tag }}
      run: |
        set -euo pipefail
        arguments=()
        if [[ "$RELEASE_TAG" == go-v* ]]; then
          arguments+=(--expected-backend-version "${RELEASE_TAG#go-v}")
        fi
        python3 -B scripts/verify-public-production.py "${arguments[@]}"
'''
p.write_text(s)
for component in ('go', 'web'):
    p = Path('.github/workflows/release-' + component + '.yml')
    p.write_text(p.read_text().replace("if: success() && startsWith(github.ref, 'refs/tags/", "if: success() && github.repository == 'TokenNotIncluded/api.lmm.best' && startsWith(github.ref, 'refs/tags/"))
p = Path('scripts/test-server-ops.py')
s = p.read_text().replace('("GITHUB_REPOSITORY", "attacker/api.lmm.best"),', '("GITHUB_REPOSITORY", "attacker/api.lmm.best"),\n                           ("GITHUB_REPOSITORY", "LIghtJUNction/api.lmm.best"),\n                           ("GITHUB_ACTOR", "TokenNotIncluded"),')
p.write_text(s)
p = Path('scripts/workflow-topology.test.mjs')
s = p.read_text()
start = s.index("test('only five maintained workflow entry points remain'")
end = s.index("test('server operations stay manual", start)
s = s[:start] + '''test('migration preserves upstream qualification and isolates the legacy deployment adapter', () => {
  const files = readdirSync(new URL('.github/workflows/', root))
    .filter((name) => /\\.ya?ml$/.test(name)).sort();
  assert.deepEqual(files, ['assistant-support-regressions.yml', 'ci.yml', 'deploy-production.yml',
    'pr-check.yml', 'release-go.yml', 'release-web.yml', 'rust-root-route-acceptance.yml',
    'rust-security-audit.yml', 'server-ops.yml', 'server-release-qualification.yml']);
  const legacy = workflow('deploy-production');
  assert.match(legacy, /^  workflow_run:/m);
  assert.doesNotMatch(legacy, /^  push:/m);
  assert.match(job(legacy, 'deploy'), /needs: legacy-route/);
  assert.match(job(legacy, 'deploy'), /needs.legacy-route.outputs.required == 'true'/);
  assert.match(job(legacy, 'legacy-route'), /-f \\.github\\/actions\\/deploy-production\\/action.yml/);
  assert.doesNotMatch(job(legacy, 'legacy-route'), /secrets\\.|environment: production/);
  assert.match(workflow('server-release-qualification'), /qualify-go-migration-startup.sh/);
});

''' + s[end:]
s = s.replace('assert.match(ops, /on:\\n  workflow_dispatch:/);', 'assert.match(ops, /^  workflow_dispatch:/m);')
s = s.replace('assert.doesNotMatch(ops, /^  (?:push|pull_request|pull_request_target|schedule|workflow_run):/m);', "assert.doesNotMatch(ops, /^  (?:pull_request|pull_request_target|schedule|workflow_run):/m);\n  assert.match(ops, /paths: \\[\\.github\\/server-ops-343-request.json\\]/);\n  const request = job(ops, 'owner-request');\n  assert.match(request, /github.actor == 'LIghtJUNction'/);\n  assert.match(request, /github.triggering_actor == 'LIghtJUNction'/);\n  assert.match(request, /server-ops-commit-request.py --validate-only/);")
s = s.replace("  assert.match(translations, /if: github\\.event_name != 'push' \\|\\| github\\.ref_type != 'tag'/);", "  assert.match(translations, /if: github\\.event_name != 'push' \\|\\| github\\.ref_type != 'tag'/);\n  assert.match(job(ci, 'quality-gate'), /- translations/);\n  assert.match(ci, /merge_group:/);")
s = s.replace("if: success() && startsWith(github.ref, 'refs/tags/${component}-v')", "if: success() && github.repository == 'TokenNotIncluded/api.lmm.best' && startsWith(github.ref, 'refs/tags/${component}-v')")
s = s.replace("GITHUB_SHA: revision, GITHUB_REF: 'refs/tags/go-v1.2.3',", "GITHUB_SHA: revision, GITHUB_REF: 'refs/tags/go-v1.2.3',\n      GITHUB_REPOSITORY: 'TokenNotIncluded/api.lmm.best',")
s = s.replace("  ['branch ref', { GITHUB_REF: 'refs/heads/main' }],", "  ['personal fork', { GITHUB_REPOSITORY: 'LIghtJUNction/api.lmm.best' }],\n  ['branch ref', { GITHUB_REF: 'refs/heads/main' }],")
s = s.replace('  assert.doesNotMatch(action, /apt-get install', '  assert.match(action, /verify-public-production.py/);\n  assert.match(action, /--expected-backend-version/);\n  assert.doesNotMatch(action, /apt-get install')
p.write_text(s)
p = Path('docs/server-ops.md')
s = p.read_text().replace('LIghtJUNction/api.lmm.best', 'TokenNotIncluded/api.lmm.best')
s = s.replace('`.github/workflows/server-ops.yml` is a **manual-only** SSH transport for an\noperator or an authorized assistant. It has no schedule, push, PR, comment, or\nrelease trigger. The safe default is `diagnose`; it does not modify services or\napplication configuration. Exactly one additional workflow is introduced.', '`.github/workflows/server-ops.yml` is the upstream SSH operations entry. Manual\nrequests default to read-only diagnosis. Its separate push entry accepts only the\nexisting explicit owner request for fixed read-only incident diagnosis; ordinary\npushes, PRs, comments and releases do not run repairs. The original upstream\nproduction environment is used, not the personal fork.')
s = s.replace("Only the repository owner's login is allowed by default.", 'Only the maintainer LIghtJUNction is allowed by default; the organization name is not an operator login.')
p.write_text(s)
write('.github/WORKFLOWS.md', '''# GitHub Actions in TokenNotIncluded/api.lmm.best

The production repository is **TokenNotIncluded/api.lmm.best**. The personal fork
is not a production operations entry point and receives no production credentials.

## Migrated workflows

- `server-ops.yml`: manual main-only diagnose/repair and the existing explicit
  owner-only, request-only incident diagnosis. Both use the original protected
  production environment and the same `production-auto-deploy` concurrency group.
- `ci.yml`: all upstream Go/Rust/Web/integration/package gates, merge-queue support,
  and the migrated translation checker. CI Quality Gate requires translations too.
  A tag still tests the checker; only its branch-to-branch comparison is skipped.
- `release-go.yml` and `release-web.yml`: retain exact-source release checks,
  unresolved-work barriers, and signing identities. New releases own their final
  serialized deployment job through `.github/actions/deploy-production/`.
- `deploy-production.yml`: compatibility adapter for historical tags only. It
  inspects the immutable source and skips releases with the inline deployment
  action, preventing duplicate deployments. It no longer handles owner requests.

Do not delete upstream qualification workflows to match the former fork's count
of five files. Server release qualification, root-route acceptance, security audit
and assistant regressions remain. Their checks and native migration/observation
contracts are not replaced by topology tests or a successful package publication.

## Trust and rollout

Operations require main, the actual authorized dispatcher and triggering actor,
an exact committed script, confirmation, and a new run for mutations. Fixed
owner requests retain their separate non-forced single-parent request-only commit,
age and digest checks. No caller identity is synthesized. One canonical
`scripts/server-ops.py` transport serves both paths and is hash-checked before
credentials. Raw repair logs stay on the host. See `docs/server-ops.md`.

GitHub concurrency is repository-scoped. All maintained production jobs must stay
in this upstream repository; the server's native transaction lock remains required.
The tag-based deployment environment must permit the intended Go/Web release tags;
this migration does not change its reviewers, secrets or deployment rules.

Old immutable tags keep their historical workflow definitions. Retaining the
legacy adapter lets those tags work without rewriting them. New and legacy
operations never auto-confirm a transaction or bypass a pending recovery.

Run `node --test scripts/workflow-topology.test.mjs`, all server-ops Python tests,
`python3 -m unittest discover -s scripts -p test_ci_quality_gate.py`, and actionlint.
These local tests do not establish production health. No release or server change
is requested merely by merging this migration.
''')
write('docs/incident-343-owner-ops.md', '''# Incident 343 owner operations

The production repository is TokenNotIncluded/api.lmm.best. The earlier personal
fork did not hold SSH credentials; those credentials were not copied or exported.

The `server-ops.yml` workflow now owns both the manual main-only operator path and
the existing explicit-owner read-only request path. `deploy-production.yml` is only
a compatibility adapter for releases whose immutable source predates inline deploy.
All paths share the upstream production environment and deployment concurrency.

The fixed `.github/server-ops-343-request.json` request still requires an actual
LIghtJUNction actor/sender, a fresh unforced single-parent commit changing only that
request, matching parent SHA and diagnostic digest, and explicit confirmation.
It permits no arbitrary repair and no replay. Manual repairs have separate script
selection and operator checks. Both call the one reviewed `scripts/server-ops.py`
transport; there is no fork-specific transport or impersonated dispatch.

The corrected diagnostic accepts the real FatalLog bracket suffix. It only reads
selected service metadata and bounded logs and publishes fixed labels. It does not
migrate, restart, roll back, open admission, or confirm the native transaction.
A failing local/public check remains failure, not successful recovery.
''')
subprocess.run(['git', 'add', '-A'], check=True)
subprocess.run(['git', 'diff', '--cached', '--check'], check=True)
assert not git('diff', '--name-only', '--diff-filter=U').strip()
assert git('write-tree').strip() == 'b04e9d52997a11f2c3e1a428f5fb17da048fcec3'
print('Reproduced reviewed migration tree b04e9d52997a11f2c3e1a428f5fb17da048fcec3')
