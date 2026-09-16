from pathlib import Path
import subprocess
R=Path.cwd()
def git(*args): return subprocess.check_output(['git',*args],text=True)
def put(p,s): (R/p).parent.mkdir(parents=True,exist_ok=True); (R/p).write_text(s)
assert git('rev-parse','HEAD').strip() == 'f34fbe6a8b71e1312372814cbab60bb9f9a1ffcb'
result=subprocess.run(['git','merge','--no-commit','--no-ff','ffabeb1c02d292b00f1b4ef66dcf1b88e6c3bfb1'],capture_output=True,text=True)
assert result.returncode == 1
conflicted=git('diff','--name-only','--diff-filter=U').splitlines()
assert set(conflicted)=={'.github/WORKFLOWS.md','.github/actions/deploy-production/action.yml','.github/workflows/ci.yml','.github/workflows/deploy-production.yml','docs/incident-343-owner-ops.md','docs/server-ops.md','scripts/server-ops-commit-request.py','scripts/workflow-topology.test.mjs'}
for p in conflicted+['.github/workflows/server-ops.yml']:
 put(p,git('show','ffabeb1c:'+p))
subprocess.run(['git','add','-A'],check=True)
subprocess.run(['git','-c','user.name=LIghtJUNction','-c','user.email=lightjunction.me@gmail.com','commit','-qm','local: reconcile migration reviews'],check=True)
result=subprocess.run(['git','merge','--no-commit','--no-ff','c54d399d08f2c1681cd1ea620eb367bff8711fdd'],capture_output=True,text=True)
assert result.returncode == 1
assert git('diff','--name-only','--diff-filter=U').splitlines()==['.github/workflows/deploy-production.yml']
put('.github/server-ops-343-request.json',git('show','c54d399d:.github/server-ops-343-request.json'))
p='scripts/server-ops-commit-request.py'
put(p,git('show','c54d399d:'+p).replace("with_name('server-ops-transport.py')","with_name('server-ops.py')"))
p='.github/workflows/server-ops.yml'
s=git('show','ffabeb1c:'+p)
latest=git('show','c54d399d:.github/workflows/deploy-production.yml')
job=latest[latest.index('  owner-incident-diagnosis:'):]
job=job.replace('  owner-incident-diagnosis:','  owner-request:',1)
job=job.replace('HEAD:scripts/server-ops-transport.py','HEAD:scripts/server-ops.py').replace('291b68df38ec40005872060aac98a4a4631a79af','7e43973c497773870e99008b1affbf43d9fda9d0')
job=job.replace('          python3 scripts/test-server-repair-startup.py','          python3 scripts/test-server-ops.py\n          python3 scripts/test-server-repair-startup.py',1)
s=s[:s.index('  owner-request:')]+job
put(p,s)
put('.github/workflows/deploy-production.yml',git('show','ffabeb1c:.github/workflows/deploy-production.yml'))
p='.github/workflows/ci.yml'
s=(R/p).read_text().replace('          python3 scripts/test-server-ops-commit-request.py\n','          python3 scripts/test-server-ops-commit-request.py\n          python3 scripts/test-server-ops-helper-payload.py\n')
s=s.replace("        if: github.event_name != 'push' || github.ref_type != 'tag'", "        if: github.ref_type != 'tag'")
put(p,s)
p='scripts/workflow-topology.test.mjs'
s=(R/p).read_text().replace("/if: github\\.event_name != 'push' \\|\\| github\\.ref_type != 'tag'/", "/if: github\\.ref_type != 'tag'/")
s=s.replace("  assert.match(request, /server-ops-commit-request.py --validate-only/);", """  assert.match(request, /server-ops-commit-request.py --validate-only/);
  assert.match(request, /recover-red-packet-schema-343/);
  assert.match(request, /go test -race -count=1/);
  assert.match(request, /prepare-ci-apt.py/);
  assert.match(request, /POSTGRES_DB: lmm_test_release/);
  assert.match(request, /server-ops-helper-payload.py/);
  assert.match(request, /HEAD:scripts\\/server-ops.py/);
  assert.doesNotMatch(request, /server-ops-transport.py/);
  assert.match(controller, /github\\.repository == 'TokenNotIncluded\\/api\\.lmm\\.best'/);
  assert.match(controller, /github\\.event_name == 'workflow_dispatch'/);""")
s=s.replace("'rust-real-integration', 'aur-package-matrix'])", "'rust-real-integration', 'aur-package-matrix', 'quality-gate'])")
s=s.replace("  assert.match(ci, /merge_group:/);", "  assert.match(ci, /merge_group:/);\n  assert.match(translations, /github\\.event\\.merge_group\\.base_sha/);")
put(p,s)
p='docs/incident-343-owner-ops.md'
s=git('show','c54d399d:'+p)
s=s.replace('deploy-production.yml','server-ops.yml').replace('server-ops-transport.py','server-ops.py')
s+='\nAll production operations are maintained only in TokenNotIncluded/api.lmm.best. The migration preserves the fixed recovery handler, its pre-credential PostgreSQL qualification and helper digest. The historical deploy-production.yml adapter handles only old signed release tags, never owner requests.\n'
put(p,s)
p='.github/WORKFLOWS.md'; s=(R/p).read_text().replace('owner-only, request-only incident diagnosis.','owner-only, request-only incident diagnosis or fixed schema recovery.').replace('One canonical\n`scripts/server-ops.py` transport serves both paths', 'Recovery retains its freshly tested, digest-bound helper and original native\nconfirmation boundary. One canonical `scripts/server-ops.py` transport serves both paths')
put(p,s)
p='docs/server-ops.md'; s=(R/p).read_text().replace('existing explicit owner request for fixed read-only incident diagnosis', 'existing explicit owner request for fixed incident diagnosis or schema recovery')
put(p,s)
subprocess.run(['git','add','-A'],check=True)
subprocess.run(['git','diff','--cached','--check'],check=True)
assert not git('diff','--name-only','--diff-filter=U').strip()
assert git('write-tree').strip()=='a6a741c0fb49858a300d514549ed2c963a10728f'
assert not git('diff','--cached','c54d399d','--','apps/api-go','apps/api-rust','apps/web','.github/server-ops-343-request.json').strip()
print('Reproduced consolidated migration tree a6a741c0fb49858a300d514549ed2c963a10728f; application source and current request preserved.')
