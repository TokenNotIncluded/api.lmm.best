from pathlib import Path
root = Path.cwd()
workspace = root / '.review/assistant-workspace'
file = root / 'apps/web/src/features/system-settings/content/assistant-settings-section.test.tsx'
text = file.read_text()
for provider in ['generic_http', 'mcp_streamable_http', 'exa']:
    before = "const { container, cleanup } = await renderSettings('"+provider+"')"
    after = "const rendered = await renderSettings('"+provider+"')\n    const container = await openToolConfiguration(rendered, 'search_web')\n    const cleanup = rendered.cleanup"
    if text.count(before) != 1: raise ValueError('Search fixture not unique: '+provider)
    text = text.replace(before, after)
before = '''const input = rendered.container.querySelector<HTMLInputElement>(
        'input[name="AssistantNewUserGiftMaxCredits"]'
      )'''
after = '''const configuration = await openToolConfiguration(rendered, 'prepare_new_user_gift')
      const input = configuration.querySelector<HTMLInputElement>(
        'input[name="AssistantNewUserGiftMaxCredits"]'
      )'''
if text.count(before) != 1: raise ValueError('Gift fixture not unique')
text = text.replace(before, after)
text += '''

async function openToolConfiguration(rendered: Awaited<ReturnType<typeof renderSettings>>, name: string) {
  const originalGet = api.get
  api.get = (async (url: string, ...args: unknown[]) => {
    if (url === '/api/assistant/admin/tool-catalog') return { data: { success: true, data: { groups: [{ id: 'review_tools', label: 'Review tools', tools: [
      { name: 'search_web', label: 'Search the web', description: 'Search with the chosen provider.', effect: 'read_only', access: 'user' },
      { name: 'prepare_new_user_gift', label: 'Welcome gift', description: 'Evaluate a welcome gift.', effect: 'server_guarded', access: 'user' },
    ] }] } } }
    return Reflect.apply(originalGet, api, [url, ...args])
  }) as typeof api.get
  try {
    const tab = Array.from(rendered.container.querySelectorAll<HTMLButtonElement>('[role="tab"]')).find((button) => button.textContent === 'Assistant tools')
    assert.ok(tab)
    await act(async () => { tab.click(); await flushEffects() })
    await act(flushEffects)
    const row = rendered.container.querySelector(`[data-tool="${name}"]`) ?? Array.from(rendered.container.querySelectorAll('[data-testid]')).find((item) => item.getAttribute('data-testid') === `assistant-tool-${name}`)
    const label = name === 'search_web' ? 'Configure Search the web' : 'Configure Welcome gift'
    const button = rendered.container.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`)
    assert.ok(button, `Missing Configure button for ${name}; row=${Boolean(row)}`)
    await act(async () => { button.click(); await flushEffects() })
    const dialog = document.querySelector('[data-testid="assistant-tool-configuration"]')
    assert.ok(dialog)
    assert.equal(rendered.container.querySelector('input[name="AssistantSearchURL"]'), null, 'Search settings must not remain in their former location')
    return dialog
  } finally {
    api.get = originalGet
  }
}
'''
file.write_text(text)
file = root / 'apps/web/src/features/system-settings/content/assistant-tool-policy-editor.test.tsx'
text = file.read_text()
if text.count("setInput(search, 'Super administrators')") != 1: raise ValueError('Old role search target not unique')
text = text.replace("setInput(search, 'Super administrators')", "setInput(search, 'L6 (Super administrator)')")
text += (workspace / 'dialog-tests.txt').read_text()
file.write_text(text)
