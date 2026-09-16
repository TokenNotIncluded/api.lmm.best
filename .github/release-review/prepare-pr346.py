from pathlib import Path
import subprocess,re
r=Path.cwd()
for p in ['.github/server-ops-343-request.json','scripts/server-ops-commit-request.py','scripts/server-repairs/inspect-startup-343.sh','scripts/test-server-repair-startup.py']:
    (r/p).write_bytes(subprocess.check_output(['git','show','HEAD:'+p],cwd=r))
# Both old required checks and fork additions remain; keep merge queue events.
p=r/'.github/workflows/ci.yml';s=p.read_text()
blocks=list(re.finditer(r'^<<<<<<< HEAD\n(.*?)^=======\n(.*?)^>>>>>>>[^\n]*\n',s,re.M|re.S))
assert len(blocks)==3
pieces=[];last=0
for i,m in enumerate(blocks):
    pieces+=[s[last:m.start()]]
    a,b=m.group(1,2)
    pieces += [b+a if i==0 else a+'\n'+b]
    last=m.end()
s=''.join(pieces)+s[last:]
s=s.replace('      - aur-package-matrix\n    runs-on:', '      - aur-package-matrix\n      - translations\n    runs-on:')
s=s.replace("    # Tag pushes have no meaningful previous branch revision to compare.\n    if: github.event_name != 'push' || github.ref_type != 'tag'\n",'')
s=s.replace('      - name: Check added keys and removed translations\n        env:',"      - name: Check added keys and removed translations\n        if: github.ref_type != 'tag'\n        env:")
s=s.replace("github.event.pull_request.base.sha || github.event.before", "github.event.pull_request.base.sha || github.event.merge_group.base_sha || github.event.before")
s=s.replace('        run: node --test scripts/workflow-topology.test.mjs','        run: |\n          node --test scripts/workflow-topology.test.mjs\n          python3 scripts/test-server-ops.py\n          python3 scripts/test-server-ops-commit-request.py\n          python3 scripts/test-server-repair-startup.py')
p.write_text(s)
p=r/'scripts/ci_quality_gate.py';s=p.read_text().replace('    "aur-package-matrix",','    "aur-package-matrix",\n    "translations",');p.write_text(s)
# Move the diagnostic entry out of the subscriber. Do not leave duplicate deploy lanes.
subprocess.run(['git','rm','-f','.github/workflows/deploy-production.yml'],cwd=r,check=True)
p=r/'.github/workflows/server-ops.yml';s=p.read_text().replace("github.repository == 'LIghtJUNction/api.lmm.best'", "github.repository == 'TokenNotIncluded/api.lmm.best'")
s=s.replace('        run: python3 scripts/server-ops-commit-request.py --validate-only','        run: |\n          test "$(git rev-parse HEAD:scripts/server-ops-transport.py)" = 291b68df38ec40005872060aac98a4a4631a79af\n          python3 scripts/server-ops-commit-request.py --validate-only')
p.write_text(s)
p=r/'scripts/server-ops.py';s=p.read_text().replace('REPOSITORY = "LIghtJUNction/api.lmm.best"','REPOSITORY = "TokenNotIncluded/api.lmm.best"\nOWNER = "LIghtJUNction"').replace('owner = REPOSITORY.split("/")[0]', 'owner = OWNER');p.write_text(s)
# Fork consolidation had accidentally lost public post-deploy acceptance.
p=r/'.github/actions/deploy-production/action.yml';s=p.read_text();s += '''

    - name: Verify public backend version and frontend entry assets
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
''';p.write_text(s)
# Keep all four upstream-only qualification/security workflows; obsolete count=5 loses them.
p=r/'scripts/workflow-topology.test.mjs';s=p.read_text().replace('only five maintained workflow entry points remain','consolidation retains upstream server, assistant and security qualification')
s=s.replace("['ci.yml', 'pr-check.yml', 'release-go.yml', 'release-web.yml', 'server-ops.yml']", "['assistant-support-regressions.yml', 'ci.yml', 'pr-check.yml', 'release-go.yml', 'release-web.yml', 'rust-root-route-acceptance.yml', 'rust-security-audit.yml', 'server-ops.yml', 'server-release-qualification.yml']")
s=s.replace('server operations stay manual, main-only, and share the production lock','manual operations and owner-only diagnosis are main-only and share the production lock')
s=s.replace("assert.match(ops, /on:\\n  workflow_dispatch:/);", "assert.match(ops, /  workflow_dispatch:/);")
s=s.replace("(?:push|pull_request|pull_request_target|schedule|workflow_run)", "(?:pull_request|pull_request_target|schedule|workflow_run)")
s=s.replace("  assert.match(ops, /default: diagnose/);", "  assert.match(ops, /default: diagnose/);\n  assert.match(ops, /paths: \\[\\.github\\/server-ops-343-request\\.json\\]/);\n  const owner = job(ops, 'owner-request');\n  assert.match(owner, /github\\.actor == 'LIghtJUNction'/);\n  assert.match(owner, /github\\.triggering_actor == 'LIghtJUNction'/);\n  assert.match(owner, /server-ops-commit-request\\.py --validate-only/);\n  assert.match(controller, /github\\.repository == 'TokenNotIncluded\\/api\\.lmm\\.best'/);\n  assert.match(controller, /github\\.event_name == 'workflow_dispatch'/);")
s=s.replace("'rust-real-integration', 'aur-package-matrix'])", "'rust-real-integration', 'aur-package-matrix', 'quality-gate'])")
s=s.replace("assert.match(translations, /if: github.event_name != 'push' \\|\\| github.ref_type != 'tag'/);", "assert.match(translations, /if: github.ref_type != 'tag'/);\n  assert.match(job(ci, 'quality-gate'), /- translations/);")
s=s.replace("assert.match(action, /run: bash scripts\\/auto-deploy-production-release\\.sh/);", "assert.match(action, /run: bash scripts\\/auto-deploy-production-release\\.sh/);\n  assert.match(action, /scripts\\/verify-public-production\\.py/);\n  assert.match(action, /--expected-backend-version/);")
p.write_text(s)
# Documentation describes actual upstream topology, not the obsolete fork.
p=r/'.github/WORKFLOWS.md';s=p.read_text().replace('Keep five workflow entry points.', 'Keep nine upstream workflow entry points, including the four existing qualification workflows.').replace('See `docs/server-ops.md` for the existing operator contract.','See `docs/server-ops.md` for the operator contract. The fixed owner-request diagnostic\ntrigger lives only in `server-ops.yml`; ordinary code pushes cannot execute repairs.')
s=s.replace('| `server-ops.yml` | Manual runs on `main` only |', '| `server-ops.yml` | Manual runs on `main`; explicit owner diagnostic request |')
s=s.replace('## CI and translations','The independent `server-release-qualification.yml`, `rust-root-route-acceptance.yml`,\n`rust-security-audit.yml`, and `assistant-support-regressions.yml` remain intact.\nThe standalone deploy subscriber and i18n workflow are folded into their callers;\ntheir protections are not removed.\n\n## CI and translations')
s=s.replace('Tag pushes skip only the translation\ncomparison; all original CI release gates remain.', 'Tag pushes skip only the translation\ncomparison, not the checker tests. Translations are a required CI Quality Gate\ndependency; merge-queue comparisons use `merge_group.base_sha`. All original CI\nrelease gates remain.')
s=s.replace('The common action checks the event, checkout and tag identity', 'The common action checks checkout and tag identity')
s=s.replace('script. No production rollout is needed', 'script and the same public version/page/asset acceptance. No production rollout is needed')
p.write_text(s)
p=r/'docs/server-ops.md';s=p.read_text().replace('`.github/workflows/server-ops.yml` is a **manual-only** SSH transport for an\noperator or an authorized assistant. It has no schedule, push, PR, comment, or\nrelease trigger.', '`.github/workflows/server-ops.yml` in **TokenNotIncluded/api.lmm.best** is the\nproduction operator entry. Manual `workflow_dispatch` remains separate from the\nexisting, fixed read-only owner-commit diagnosis described in\n`docs/incident-343-owner-ops.md`. Neither PRs nor releases trigger arbitrary repairs.')
s=s.replace('Exactly one additional workflow is introduced.', 'The standalone entry replaces the diagnostic job previously embedded in deployment.')
s=s.replace("Only the repository owner's login is allowed by default.", 'Only the maintainer `LIghtJUNction` is allowed by default. The organization name\n`TokenNotIncluded` is not a user login and must not be inferred as an operator.')
s=s.replace('--repo LIghtJUNction/api.lmm.best','--repo TokenNotIncluded/api.lmm.best')
s=s.replace('Repairs share the `production-auto-deploy` concurrency group and also take a', 'Within this production repository, repairs share the `production-auto-deploy`\nconcurrency group and also take a')
s=s.replace('Run offline controller tests with', 'The fork does not inherit upstream secrets or share its Actions concurrency.\nDo not copy credentials into the fork to work around this repository boundary.\n\nRun offline controller tests with');p.write_text(s)
p=r/'docs/incident-343-owner-ops.md';s=p.read_text().replace('Its existing deployment lane\nnow accepts', 'The `LMM assistant server ops` workflow in this production repository\nshares the deployment lane and accepts').replace('The original release-triggered deploy job and all native safety gates\nremain unchanged. No additional workflow entry file was added.', 'Component releases deploy through their final job; all native safety gates\nremain unchanged. The diagnostic trigger was moved out of the old deployment\nsubscriber, so a request is handled exactly once.');p.write_text(s)
subprocess.run(['git','add','-A'],cwd=r,check=True)

p=r/'scripts/workflow-topology.test.mjs';s=p.read_text()
old="  assert.match(translations, /if: github\\.event_name != 'push' \\|\\| github\\.ref_type != 'tag'/);"
assert old in s
s=s.replace(old,"  assert.match(translations, /if: github\\.ref_type != 'tag'/);\n  assert.match(job(ci, 'quality-gate'), /- translations/);\n  assert.match(translations, /github\\.event\\.merge_group\\.base_sha/);")
p.write_text(s)
p=r/'.gitignore';p.write_text(p.read_text()+'\n# Python test/import caches are not source artifacts.\n__pycache__/\n*.py[cod]\n')
subprocess.run(['git','add','-A'],cwd=r,check=True)
assert subprocess.check_output(['git','write-tree'],cwd=r,text=True).strip() == '84c7e50527564428ad31f16909db2ca3140b6cde'
