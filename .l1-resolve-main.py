from pathlib import Path
import json,re,subprocess

def git(*args):return subprocess.check_output(['git',*args],text=True)
conflicts=git('diff','--name-only','--diff-filter=U').splitlines()
assert len(conflicts)==21,conflicts
incoming=git('show','MERGE_HEAD:apps/web/src/features/onboarding/getting-started.test.tsx')
for name in conflicts:
 p=Path(name)
 if '/i18n/locales/' in name:
  base,ours,theirs=[json.loads(git('show',f':{stage}:{name}')) for stage in (1,2,3)]
  missing=object(); merged=theirs['translation']; a=base['translation'];o=ours['translation']
  for key in a.keys()|o.keys():
   if a.get(key,missing)==o.get(key,missing):continue
   assert merged.get(key,missing) in (a.get(key,missing),o.get(key,missing)),(name,key)
   if key in o:merged[key]=o[key]
   else:merged.pop(key,None)
  p.write_text(json.dumps(theirs,ensure_ascii=False,indent=2)+'\n')
 elif name.endswith('/assistant-settings-section.tsx'):
  s=p.read_text();s,n=re.subn(r'^<<<<<<< [^\n]*\n(.*?)^=======\n.*?^>>>>>>> [^\n]*\n',r'\1',s,flags=re.M|re.S);assert n==1
  css="import './assistant-settings-workspace.css'"
  assert s.count(css)==2;s=s.replace(css+'\n','',1);p.write_text(s)
 elif name.endswith(('/access-request-details.tsx','/access-request-details.test.tsx','/onboarding/api.ts')):
  subprocess.run(['git','rm','--',name],check=True)
 else:
  assert '/features/onboarding/' in name or name.endswith('/webmcp/areas/auth-onboarding.test.ts'),name
  p.write_text(git('show','HEAD:'+name))

def change(path,old,new):
 p=Path(path);s=p.read_text();assert s.count(old)==1,(path,old,s.count(old));p.write_text(s.replace(old,new))
page='apps/web/src/features/onboarding/getting-started.tsx'
change(page,"import { useCallback } from 'react'","import { useCallback, useEffect, useRef } from 'react'")
needle='  const onboarding = getOnboardingState(user)\n'
change(page,needle,needle+'''  const wasActivated = useRef(onboarding.activationComplete)
  useEffect(() => {
    const justActivated = !wasActivated.current && onboarding.activationComplete
    wasActivated.current = onboarding.activationComplete
    if (justActivated) {
      void navigate({ to: getAuthenticatedLandingRoute(user) })
    }
  }, [navigate, onboarding.activationComplete, user])
''')
test='apps/web/src/features/onboarding/getting-started.test.tsx'
change(test,'data: { success: true, data: currentUser }','data: { success: true, data: { ...currentUser } }')
helper=incoming[incoming.index('async function askInline('):incoming.index('afterEach(() => {')]
change(test,'afterEach(() => {',helper+'afterEach(() => {')
start=incoming.index('  for (const history of [')
end=incoming.index("  test('keeps the model square",start)
newtests=incoming[start:end]
old='      const page = await renderPage(false, undefined, history)'
assert newtests.count(old)==1
newtests=newtests.replace(old,"""      const page = await renderPage()
      page.queryClient.setQueryData(
        ['assistant-developer-access-request', user.id], history
      )""")
# A completed reply always refreshes authoritative account state. A trace alone
# never grants access, even when the old/new request presents success text.
old="page.gets.filter((url) => url === '/api/user/self').length,\n          1"
assert newtests.count(old)==1;newtests=newtests.replace(old,old[:-1]+'2')
newtests+='''  test('focus activation uses the authoritative account and ignores retired history', async () => {
    const page = await renderPage()
    page.queryClient.setQueryData(
      ['assistant-developer-access-request', user.id], { status: 'rejected' }
    )
    try {
      page.currentUser.developer_access_granted = true
      await act(async () => {
        window.dispatchEvent(new Event('focus'))
        await flushEffects()
      })
      const deadline = Date.now() + 2000
      while (Date.now() < deadline && page.router.state.location.pathname !== '/dashboard') {
        await act(flushEffects)
      }
      assert.equal(useAuthStore.getState().auth.user?.developer_access_granted, true)
      assert.equal(page.router.state.location.pathname, '/dashboard')
      assert.equal(page.gets.some((url) => url.includes('developer-access/request')), false)
    } finally {
      await unmountPage(page)
    }
  })
'''
needle="describe('getting started access boundaries', () => {\n"
change(test,needle,needle+newtests)
subprocess.run(['git','add','--',*[name for name in conflicts if Path(name).exists()]],check=True)
assert not git('diff','--name-only','--diff-filter=U').strip()
for name in git('diff','--cached','--name-only').splitlines():
 p=Path(name)
 if p.is_file() and p.suffix in ('.go','.ts','.tsx','.json'):
  assert not re.search(r'^(<<<<<<< |=======|>>>>>>> )',p.read_text(),re.M),name
print('Resolved all 21 conflicts; preserved main policy, account-write epoch and dashboard changes.')
