from pathlib import Path
import subprocess,re,os,json
root=Path(os.environ['L0_REPO'])
def show(ref,path):return subprocess.check_output(['git','show',f'{ref}:{path}'],cwd=root,text=True)
def write(path,data): (root/path).write_text(data)
def edit(p,a,b):
    f=root/p;s=f.read_text();assert a in s,p;f.write_text(s.replace(a,b,1))
folder='apps/web/src/features/onboarding/'
for name in ['access-request-details.test.tsx','access-request-details.tsx','api.ts']:
    subprocess.run(['git','rm',folder+name],cwd=root,check=True)
for name in ['l0-access-copy.ts','l0-cloud-conversation.tsx','l0-welcome.tsx','l0-paid-welcome.test.tsx','next-step.ts','next-step.test.ts','use-account-next-step.ts']:
    write(folder+name,show('HEAD',folder+name))
p=folder+'l0-cloud-conversation.tsx'
edit(p,'''          // Tool receipts do not replace authorization. Refresh the account from
          // the server after the reply; a failed refresh must not lose the reply.
          void refreshCurrentAccount().catch(() => undefined)
''','''          // A receipt only triggers a fresh account read; it cannot grant access.
          // Keep this independent of the completed answer and any older read.
          if (
            reply.tools?.some(
              (trace) =>
                trace.name === 'grant_l1_access' &&
                trace.status === 'output-available'
            )
          ) {
            void refreshCurrentAccount()
          }
''')
p=folder+'l0-welcome.tsx'
edit(p,'  const navigateTabs = (','''  const continueConversation = () => {
    selectScene('chat', true)
    requestAnimationFrame(() => {
      document.getElementById('l0-question')?.focus({ preventScroll: true })
    })
  }
  const navigateTabs = (''')
edit(p,'''                  selectScene('chat', true)
                  requestAnimationFrame(() => {
                    document
                      .querySelector<HTMLInputElement>('.l0-input-row input')
                      ?.focus()
                  })''','                  continueConversation()')
p=folder+'getting-started.tsx';s=show('origin/review-main',p)
s=s.replace("import { useEffect, useRef } from 'react'", "import { useCallback, useEffect, useRef } from 'react'")
s=s.replace("import { AccessRequestDetails } from './access-request-details'\n", '')
s=s.replace("import { Button } from '@/components/ui/button'", "import { Button } from '@/components/ui/button'\nimport { AssistantRegistrationStatus } from '@/features/assistant/assistant-registration-status'")
s=s.replace("import { useAuthUserRefresh } from './use-auth-user-refresh'", "import { refreshCurrentAccount, useAuthUserRefresh } from './use-auth-user-refresh'")
s=s.replace('  const wasActivated = useRef(onboarding.activationComplete)', '''  const wasActivated = useRef(onboarding.activationComplete)
  const refreshAfterActivation = useCallback(() => {
    void refreshCurrentAccount()
  }, [])''')
s=s.replace('            <AccessRequestDetails inline />', '''            <AssistantRegistrationStatus
              onApproved={refreshAfterActivation}
              onContinueSetup={refreshAfterActivation}
            />''')
write(p,s)
p=folder+'getting-started.test.tsx';s=show('HEAD',p);main=show('origin/review-main',p)
s=s.replace('data: currentUser }', 'data: { ...currentUser } }')
helper=main[main.index('async function askInline('):main.index('afterEach(() =>')]
s=s.replace('afterEach(() =>',helper+'afterEach(() =>',1)
start="describe('getting started access boundaries', () => {\n"
added=main[main.index(start)+len(start):main.index("  test('keeps the model square discoverable")]
added=added.replace('const page = await renderPage(false, undefined, history)', '''const page = await renderPage()
      if (history) {
        page.queryClient.setQueryData(
          ['assistant-developer-access-request', user.id],
          history
        )
      }''')
s=s.replace(start,start+added,1)
s=s.replace('      useAuthStore.getState().auth.user?.developer_access_granted !== true\n', "      page.router.state.location.pathname !== '/dashboard'\n")
extra='''  for (const status of ['pending', 'rejected', 'approved']) {
    test(`retired ${status} letters never become an upgrade gate or grant`, async () => {
      const page = await renderPage()
      page.queryClient.setQueryData(
        ['assistant-developer-access-request', user.id],
        { status, reason: 'retired reason', ai_recommendation: 'retired letter' }
      )
      try {
        await act(async () => {
          button(page, 'Unlock').click()
          await flushEffects()
        })
        assert.doesNotMatch(page.container.textContent ?? '', /retired reason|retired letter|Pending review|Access request rejected/)
        assert.ok(!page.gets.some((url) => url.includes('developer-access/request')))
        assert.equal(useAuthStore.getState().auth.user?.developer_access_granted, false)
        assert.equal(page.router.state.location.pathname, '/getting-started')
      } finally {
        await unmountPage(page)
      }
    })
  }
  test('a focus refresh leaves L0 after server-confirmed activation', async () => {
    const page = await renderPage()
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
      assert.equal(page.router.state.location.pathname, '/dashboard')
    } finally {
      await unmountPage(page)
    }
  })
'''
s=s.replace(start,start+extra,1)
write(p,s)
for lang in ['en','zh']:
    p=f'apps/web/src/i18n/locales/{lang}.json';s=(root/p).read_text()
    s,n=re.subn(r'^<<<<<<< HEAD\n(.*?)^=======\n.*?^>>>>>>> origin/review-main\n',r'\1',s,flags=re.M|re.S)
    assert n==1
    json.loads(s)
    write(p,s)
subprocess.run(['git','add',folder,'apps/web/src/i18n/locales/en.json','apps/web/src/i18n/locales/zh.json'],cwd=root,check=True)
base='apps/web/src/features/'
edit(base+'onboarding/l0-welcome.tsx', "            <p className='l0-rail-headline'>{t('Enable L1 access')}</p>", """            <p className='l0-rail-headline'>{t('Enable L1 access')}</p>
            <p className='l0-rail-meta' data-testid='l0-free-access'>
              {t('Describe what you need. The assistant can enable L1 without an application letter.')}
            </p>""")
edit(base+'onboarding/l0-welcome.tsx', """                    ? t(
                        'Describe what you need. The assistant can enable L1 without an application letter.'
                      )""", '                    ? copy.reviewNote')
edit(base+'onboarding/l0-paid-welcome.test.tsx', "container.querySelector('.l0-rail-meta')?.textContent ?? ''", '''container.querySelector('[data-testid="l0-paid-progress"]')?.textContent ?? '' ''')
edit(base+'onboarding/l0-paid-welcome.test.tsx', "    assert.ok(direct.classList.contains('l0-rail-action--ghost'))", """    assert.match(
      container.querySelector('[data-testid="l0-free-access"]')?.textContent ?? '',
      /The assistant can enable L1 without an application letter/
    )
    assert.ok(direct.classList.contains('l0-rail-action--ghost'))""")
edit(base+'todos/api.ts','''    if (!retired) return page
    const { developer_access: _retiredUnread, ...unread } =
      page.unread_by_category''','''    const { developer_access: retiredUnread, ...unread } =
      page.unread_by_category
    const retiredUnreadCount = Math.max(0, retired?.unread ?? retiredUnread ?? 0)''')
p=root/(base+'todos/api.ts');s=p.read_text();s=s.replace('(retired?.unread ?? 0)','retiredUnreadCount');p.write_text(s)
edit(base+'todos/todo-list-model.ts','  visible.add(selected)','  if (Object.hasOwn(TODO_CATEGORY_LABELS, selected)) visible.add(selected)')
p=root/(base+'todos/todo-list-model.test.ts');p.write_text(p.read_text()+'''

test('a stale selected application filter cannot restore the retired tab', () => {
  assert.deepEqual(visibleTodoCategories([], 'developer_access', true), ['all'])
  assert.deepEqual(visibleTodoCategories(
    [{ key: 'developer_access', total: 3, unread: 2 }],
    'developer_access', false
  ), ['all'])
})
''')
p=root/(base+'todos/api.test.ts');p.write_text(p.read_text()+'''

test('retired items are filtered even without a legacy category summary', async () => {
  const page: TodoPage = {
    items: [{ id: 'developer_access:1', source_id: 1,
      category: 'developer_access', type: 'legacy', title: 'legacy',
      summary: '', read: false, created_at: 1, updated_at: 1 }],
    page: 1, page_size: 50, total: 51, category: 'all',
    unread_count: 2, total_unread_count: 2,
    unread_by_category: { developer_access: 1, moderation: 1 },
    categories: [{ key: 'moderation', total: 1, unread: 1 }],
  }
  api.get = (async () => ({data: { success: true, data: page }})) as typeof api.get
  const result = await getTodos('all')
  assert.deepEqual(result.items, [])
  assert.deepEqual(result.unread_by_category, { moderation: 1 })
  assert.equal(result.unread_count, 1)
  assert.equal(result.total_unread_count, 1)
  assert.equal(result.total, 51, 'Do not hide later pages during rolling upgrades')
  assert.equal(page.items.length, 1, 'Do not mutate shared API response data')
})
''')
edit('docs/security/assistant-registration-guard.md','`grant_developer_access`','`grant_l1_access`')
edit('docs/security/assistant-registration-guard.md','letter status is never an access decision.', '''letter status is never an access decision. A completed grant triggers a separate,
owner/session-scoped account read that cannot reuse a pre-grant request. Activation
confirmed by inline chat, payment or a window-focus refresh leaves the L0 page.
The free assistant path remains visible beside any optional paid activation.
Old todo category URLs, selected filters and cached letters cannot restore the
retired application workflow; audit records remain available to administrators.''')
subprocess.run(['git','add','apps/web/src/features','docs/security/assistant-registration-guard.md'],cwd=root,check=True)
