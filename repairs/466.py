from pathlib import Path
import subprocess,json,re
root=Path.cwd()
def resolve_ours(text):
    return re.sub(r'^<<<<<<< HEAD\n(.*?)^=======\n.*?^>>>>>>> review-main\n',lambda m:m[1],text,flags=re.M|re.S)
p=root/'apps/web/src/features/system-settings/content/assistant-settings-section.tsx'
s=resolve_ours(p.read_text())
s=s.replace('<SettingsForm onSubmit={form.handleSubmit(onSubmit, revealError)}>',"<SettingsForm className='settings-stack' onSubmit={form.handleSubmit(onSubmit, revealError)}>")
start=s.index("            <div className='assistant-settings-savebar'>") if "            <div className='assistant-settings-savebar'>" in s else -1
if start>=0:
    end=s.index('</div>',start)+len('</div>')
    s=s[:start]+s[end:]
for group,title in [('model','Model and response'),('conversation','Assistant behavior'),('tools','Search and skills'),('runtime','Agent runtime'),('review','Access and review'),('retention','History retention')]:
    marker=f"id='assistant-panel-{group}'"
    start=s.index(marker)
    opening=s.index('>',start)+1
    closing=s.index('</section>',opening)
    inner=s[opening:closing]
    s=s[:opening]+f"\n<SettingsDisclosure title={{t('{title}')}} defaultOpen>\n<div className='space-y-6'>"+inner+"</div>\n</SettingsDisclosure>\n"+s[closing:]
s=s.replace("import { FormDirtyIndicator } from '../components/form-dirty-indicator'\n",'')
p.write_text(s)
p=root/'apps/web/src/features/onboarding/l0-cloud-conversation.tsx'
s=resolve_ours(p.read_text())
if 'L0_ARRIVAL_DURATION' in s and not re.search(r'import.*L0_ARRIVAL_DURATION',s):
    s="import { L0_ARRIVAL_DURATION } from './l0-flight-path'\n"+s
p.write_text(s)
for p in (root/'apps/web/src/i18n/locales').glob('*.json'):
    if '<<<<<<<' not in p.read_text(): continue
    rel=str(p.relative_to(root))
    ours=json.loads(subprocess.check_output(['git','show','HEAD:'+rel],cwd=root))
    theirs=json.loads(subprocess.check_output(['git','show','review-main:'+rel],cwd=root))
    base_ref=subprocess.check_output(['git','merge-base','HEAD','review-main'],cwd=root,text=True).strip()
    base=json.loads(subprocess.check_output(['git','show',base_ref+':'+rel],cwd=root))
    def merge(b,a,c):
        if isinstance(a,dict) and isinstance(c,dict):
            out=dict(c)
            for k,v in a.items():
                if k not in c: out[k]=v
                elif v==b.get(k): continue
                elif c[k]==b.get(k):out[k]=v
                elif isinstance(v,dict) and isinstance(c[k],dict):out[k]=merge(b.get(k,{}),v,c[k])
                elif v!=c[k]: raise ValueError((k,v,c[k]))
            return out
        raise ValueError('non-dict')
    merged=merge(base,ours,theirs)
    p.write_text(json.dumps(merged,ensure_ascii=False,indent=2)+'\n')
subprocess.run(['bun','run','--filter','@lmm/web','format'],cwd=root,check=True)
p=root/'apps/web/src/features/system-settings/content/assistant-settings-section.tsx'
s=p.read_text()
for a,b in [('Model and response','Model & response'),('Search and skills','Search & skills'),('Access and review','Access & safety'),('History retention','Conversation retention')]:
    s=s.replace("t('"+a+"')","t('"+b+"')")
marker="name='AssistantPreConversationPresets'"
pos=s.index(marker)
start=s.rfind('<FormField',0,pos)
line_start=s.rfind('\n',0,start)+1
indent=s[line_start:start]
end=s.index('\n'+indent+'/>',pos)+len('\n'+indent+'/>')
s=s[:line_start]+indent+"<SettingsDisclosure title={t('Conversation starter prompts')}>\n"+s[line_start:end]+"\n"+indent+"</SettingsDisclosure>"+s[end:]
p.write_text(s)
p=root/'apps/web/src/features/onboarding/l0-cloud-conversation.tsx'
s=p.read_text().replace("import { L0_ARRIVAL_DURATION } from './l0-flight-path'\n",'')
s=s.replace("import { getL0AccessCopy }", "import { L0_ARRIVAL_DURATION } from './l0-flight-path'\nimport { getL0AccessCopy }")
p.write_text(s)
p=root/'apps/web/src/features/debug/console-page-fixtures.ts'
s=p.read_text().replace("  '/api/assistant/weekly-discount': null,","  '/api/assistant/models': modelNames,\n  '/api/assistant/weekly-discount': null,")
p.write_text(s)
p=root/'apps/web/src/features/debug/console-page-fixtures.test.ts'
s=p.read_text()+'''

test('assistant model reads are explicit, cloned fixtures and never authorize writes', () => {
  const first = consolePageFixture(config('/api/assistant/models?group=default')) as { data: string[] }
  assert.ok(Array.isArray(first.data))
  assert.ok(first.data.length > 0)
  assert.ok(first.data.every((model) => typeof model === 'string'))
  first.data.push('mutated-preview')
  const second = consolePageFixture(config('/api/assistant/models?group=default')) as { data: string[] }
  assert.ok(!second.data.includes('mutated-preview'))
  assert.equal(consolePageFixture(config('/api/assistant/models', 'post')), undefined)
})
'''
p.write_text(s)
p=root/'apps/web/scripts/visual-review/settings_review.py'
s=p.read_text()
s=s.replace("            assert ['GET', '/api/assistant/models'] not in fixture.requests, 'Eager model request'\n            await click(page.get_by_test_id('assistant-get-model-list'))\n            assert ['GET', '/api/assistant/models'] in fixture.requests\n            model_group = page.locator('summary').filter(has_text='模型与响应').first\n            await click(model_group)", "            assert ['GET', '/api/assistant/models'] in fixture.requests, 'Automatic model list missing'\n            before_refresh = fixture.requests.count(['GET', '/api/assistant/models'])\n            await click(page.get_by_test_id('assistant-get-model-list'))\n            await page.wait_for_timeout(600)\n            assert fixture.requests.count(['GET', '/api/assistant/models']) > before_refresh\n            await click(page.locator('[data-settings-tab=conversation]'))\n            assert await page.locator('#assistant-panel-model').is_hidden()\n            assert await page.locator('#assistant-panel-conversation').is_visible()")
s=s.replace("            await field.fill('带我完成第一次调用')\n", "            await field.fill('带我完成第一次调用')\n            await click(page.locator('[data-settings-tab=model]'))\n            await click(page.locator('[data-settings-tab=conversation]'))\n            assert await field.input_value() == '带我完成第一次调用'\n")
p.write_text(s)
subprocess.run(['bun','run','--filter','@lmm/web','format'],cwd=root,check=True)
