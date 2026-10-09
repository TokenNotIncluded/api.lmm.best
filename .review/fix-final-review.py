import os
from pathlib import Path
root=Path(os.environ['L0_REPO'])
folder=root/'apps/web/src/features/onboarding'
p=folder/'getting-started.test.tsx';s=p.read_text()
a="page.container.querySelector('.l0-rail-meta')?.textContent ?? ''"
b='''page.container.querySelector('[data-testid="l0-paid-progress"]')?.textContent ?? '' '''
assert a in s;s=s.replace(a,b)
anchor="describe('getting started access boundaries', () => {\n"
extra='''  test('the unlock pane has a direct path back to the visible composer', async () => {
    const page = await renderPage(false, undefined, { state: 'ready' })
    try {
      await act(async () => { button(page, 'Unlock').click(); await flushEffects() })
      const action = page.container.querySelector<HTMLButtonElement>('[data-testid="l0-access-continue"]')
      assert.ok(action)
      assert.equal(page.container.querySelector<HTMLElement>('#l0-panel-access')?.hidden, false)
      await act(async () => { action.click(); await flushEffects() })
      assert.equal(page.container.querySelector<HTMLElement>('#l0-panel-chat')?.hidden, false)
      assert.equal(document.activeElement?.id, 'l0-question')
    } finally { await unmountPage(page) }
  })
'''
assert anchor in s;s=s.replace(anchor,anchor+extra,1)
needle="    assert.equal(\n      page.container.querySelector('textarea#access-request-reason'),\n      null\n    )"
held=s.index("  test('shows a current hold")
i=s.index(needle,held)
s=s[:i]+"    assert.equal(page.container.querySelector('[data-testid=\"l0-access-continue\"]'), null)\n"+s[i:]
p.write_text(s)
p=folder/'l0-welcome.tsx';s=p.read_text()
a="      document.getElementById('l0-question')?.focus({ preventScroll: true })"
b="""      const input = document.getElementById('l0-question')
      input?.scrollIntoView({ block: 'center', behavior: 'auto' })
      input?.focus({ preventScroll: true })"""
assert s.count(a)==1;s=s.replace(a,b)
a="                  {children}\n                  <details className='l0-oauth'"
b="""                  {children}
                  {!registration.isError &&
                    (registration.data === 'context_needed' || registration.data === 'ready') && (
                      <button
                        type='button'
                        className='l0-rail-action mt-3'
                        data-testid='l0-access-continue'
                        onClick={continueConversation}
                      >
                        {t('Chat to enable L1')}
                        <Arrow />
                      </button>
                    )}
                  <details className='l0-oauth'"""
assert s.count(a)==1;s=s.replace(a,b);p.write_text(s)
