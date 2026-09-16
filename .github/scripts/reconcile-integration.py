# Copyright (C) 2026 LIghtJUNction
# SPDX-License-Identifier: AGPL-3.0-or-later
import subprocess
import hashlib
from pathlib import Path
import re
root=Path.cwd()
def edit(path,old,new,count=1):
 p=root/path;s=p.read_text(); assert s.count(old)==count,(path,s.count(old),old[:80]);p.write_text(s.replace(old,new))
# Plain literals are required by the router generator. Commit its regenerated
# output after building instead of suppressing type errors in source routes.
for path in ['apps/web/src/routes/red-packet/$slug.tsx','apps/web/src/routes/_authenticated/red-packets/index.tsx']:
 p=root/path;s=p.read_text();s=re.sub(r'// (?:routeTree.gen.ts|The TanStack Vite plugin)[\s\S]*?\nexport const Route', 'export const Route',s,count=1);s=s.replace("' as never)","')");p.write_text(s)
# Separate non-component exports to retain React refresh and pure examples.
p=root/'apps/web/src/features/home/home-code-preview.tsx';s=p.read_text();a=s.index('export const CODE_TABS');b=s.index('type CodePreviewProps')
header=s[:a];examples=s[a:b]
examples=examples.replace("if (tab === 'Claude')\n    return", "if (tab === 'Claude') {\n    return").replace("  if (tab === 'Gemini')", "  }\n  if (tab === 'Gemini')").replace("if (tab === 'Gemini')\n    return", "if (tab === 'Gemini') {\n    return").replace("  if (tab === 'API')", "  }\n  if (tab === 'API')").replace("if (tab === 'API')\n    return", "if (tab === 'API') {\n    return").replace('  return `curl -X POST', '  }\n  return `curl -X POST')
(root/'apps/web/src/features/home/home-code-examples.ts').write_text(header+examples)
p.write_text(header+"import { CODE_TABS, codeForTab, type CodeTab } from './home-code-examples'\n\n"+s[b:])
edit('apps/web/src/features/forge/forge-home.tsx',"import {\n  CodePreview,\n  type CodeTab,\n  codeForTab,\n} from '@/features/home/home-code-preview'", "import { codeForTab, type CodeTab } from '@/features/home/home-code-examples'\nimport { CodePreview } from '@/features/home/home-code-preview'")
edit('apps/web/src/features/forge/forge-home.tsx',"      !assistantEnabled\n    )\n      return", "      !assistantEnabled\n    ) {\n      return\n    }")
edit('apps/web/src/features/home/home-motion.ts',"    if (!disposed && frame === null && !document.hidden)\n      frame = requestAnimationFrame(render)","    if (!disposed && frame === null && !document.hidden) {\n      frame = requestAnimationFrame(render)\n    }")
edit('apps/web/src/features/home/home-motion.ts',"      paused\n    )\n      return", "      paused\n    ) {\n      return\n    }")
edit('apps/web/src/features/home/home-motion.ts',"    ])\n      inner.style.removeProperty(key)", "    ]) {\n      inner.style.removeProperty(key)\n    }")
edit('apps/web/src/features/assistant/assistant-panel.tsx',"            if (isCurrentRequest() && !abortController.signal.aborted)\n              setAgentStep(step)","            if (isCurrentRequest() && !abortController.signal.aborted) {\n              setAgentStep(step)\n            }")
edit('apps/web/src/features/assistant/assistant-panel.tsx',"      } else if (reply.action?.type === 'l1_recommendation') {\n        setRecommendationDraft(reply.action)","      } else if (reply.action?.type === 'l1_recommendation') {\n        // Old cached replies may contain a letter/token. Access is now decided\n        // from server-recorded evidence; do not restore that retired form.\n        setRecommendationDraft(null)")
edit('apps/web/src/features/assistant/assistant-panel.tsx',"          label: t('Review AI recommendation'),", "          label: t('Registration verification'),")
p='apps/web/src/features/assistant/assistant-ai-stream.ts'
edit(p,"          if (!payload || typeof payload !== 'object' || Array.isArray(payload))\n            throw new Error()", "          if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {\n            throw new Error()\n          }")
edit(p,"        if (eventSize > ASSISTANT_STREAM_EVENT_MAX_CHARS)\n          throw protocolError('Assistant stream event exceeds its size limit')", "        if (eventSize > ASSISTANT_STREAM_EVENT_MAX_CHARS) {\n          throw protocolError('Assistant stream event exceeds its size limit')\n        }")
edit(p,"        else if (line.startsWith('data:'))\n          eventData.push(line.slice(5).replace(/^ /, ''))", "        else if (line.startsWith('data:')) {\n          eventData.push(line.slice(5).replace(/^ /, ''))\n        }")
edit(p,"            if (!result)\n              throw protocolError('Assistant stream ended before completion')", "            if (!result) {\n              throw protocolError('Assistant stream ended before completion')\n            }")
edit(p,"        return result!", "        if (!result) {\n          throw protocolError('Assistant stream ended before completion')\n        }\n        return result")
edit(p,"          (error instanceof Error && error.name === 'AbortError')\n        )\n          throw error", "          (error instanceof Error && error.name === 'AbortError')\n        ) {\n          throw error\n        }")
edit('apps/web/src/features/assistant/assistant-ai-stream.test.ts',"eventStream('data: ' + 'x'.repeat(512 * 1024), false)","eventStream(`data: ${'x'.repeat(512 * 1024)}`, false)")
# Update interaction contracts for the intentional retirement of letters.
path=root/'apps/web/src/features/assistant/assistant-panel.test.tsx';s=path.read_text()
def transform_test(name,transform):
 global s
 start=s.index("  test('"+name+"'");end=s.find("\n  test('",start+10)
 assert end>start
 s=s[:start]+transform(s[start:end])+s[end:]
auth="{ id: 71, username: 'l0-user', role: 1, developer_access_granted: false }"
def setup(block):
 block=block.replace("      if (url === '/api/user/developer-access/request') {\n        return { data: { success: true, data: null } }\n      }", "      if (url === '/api/assistant/registration-check') {\n        return { data: { success: true, data: { state: 'ready' } } }\n      }")
 block=block.replace("await renderPanel('onboarding')", "await renderPanel('onboarding', 'mobile', "+auth+")")
 block=block.replace('await renderPanel()', "await renderPanel(undefined, 'mobile', "+auth+")")
 return block
name='keeps L0 guidance useful without exposing account or payment actions'
def first(block):
 block=setup(block).replace("() => document.querySelector('textarea') !== null,\n          'L0 access request did not render'", "() => document.body.textContent?.includes('Continue with the assistant') === true,\n          'L0 registration status did not render'")
 return block.replace("assert.match(document.body.textContent ?? '', /Unlock L1 with AI/)", "assert.match(document.body.textContent ?? '', /No recommendation letter is required/)\n      assert.equal(document.querySelector('[data-testid=\"assistant-registration-status\"]') === null, false)\n      assert.throws(() => findButton('Write request myself'))")
transform_test(name,first)
name='opens an explicit confirmation for an AI L1 recommendation'
def second(block):
 block=setup(block).replace(name,'discards legacy L1 recommendation tokens and shows server verification status')
 oldstart=block.index('      await act(async () =>\n        waitForCondition(',block.index('submit.click()'))
 oldend=block.index('\n    } finally {',oldstart)
 block=block[:oldstart]+'''      await act(async () =>
        waitForCondition(
          () => document.body.textContent?.includes('No recommendation letter is required') === true,
          'Server verification status did not render'
        )
      )
      assert.equal(submittedRecommendation, undefined)
      assert.doesNotMatch(document.body.textContent ?? '', /assistant-confirmation-token/)
      assert.throws(() => findButton('Confirm and submit for review'))
      assert.throws(() => findButton('Confirm AI recommendation'))
      assert.ok(findButton('Registration verification'))
'''+block[oldend:]
 return block
transform_test(name,second)
name='keeps the direct L1 request path available when the AI request fails'
def third(block):
 block=setup(block).replace(name,'keeps verification refresh available without a letter bypass when AI fails')
 start=block.index("      await act(async () => {\n        findButton('Write request myself').click()")
 end=block.index('\n    } finally {',start)
 block=block[:start]+'''      assert.throws(() => findButton('Write request myself'))
      assert.throws(() => findButton('Submit for review'))
      const refresh = document.querySelector<HTMLButtonElement>(
        'button[aria-label="Refresh registration status"]'
      )
      assert.ok(refresh)
      await act(async () => {
        refresh.click()
        await flushEffects()
      })
      await act(async () => waitForCondition(
        () => document.body.textContent?.includes('No recommendation letter is required') === true,
        'Verification refresh did not recover independently of the AI request'
      ))
      assert.doesNotMatch(document.body.textContent ?? '', /L1 access is active/)
'''+block[end:]
 return block
transform_test(name,third)
path.write_text(s)
# Remove an unused test-only constructor; retain all production behavior.
edit('apps/api-rust/src/channel_balance_store.rs', "    #[cfg(test)]\n    pub(crate) fn with_client(pg: PgPool, client: DeepSeekBalanceClient) -> Self {\n        Self { pg, client }\n    }\n\n", '')
edit('apps/api-rust/src/channel_balance.rs', '    #[derive(Clone)]\n    struct MockState {\n        requests: Arc<Mutex<Vec<(String, Option<String>)>>>,', '    type RecordedRequests = Arc<Mutex<Vec<(String, Option<String>)>>>;\n\n    #[derive(Clone)]\n    struct MockState {\n        requests: RecordedRequests,')
edit('apps/api-rust/src/channel_balance.rs', '        Arc<Mutex<Vec<(String, Option<String>)>>>,', '        RecordedRequests,')
p=root/'apps/web/src/features/home/home-code-examples.ts'
p.write_text(p.read_text().rstrip()+'\n')
subprocess.run(['node','scripts/add-copyright.mjs'],cwd=root/'apps/web',check=True)
subprocess.run(['git','add','-N','apps/web/src/features/home/home-code-examples.ts'],check=True)
patch=subprocess.check_output(['git','diff','--','apps'])
assert hashlib.sha256(patch).hexdigest()=='faf5e5679fc45c0c937cf60813e3782bca42d9de0b4824dfd7bfacb0bc910893', 'Reproduced patch differs from the reviewed source changes'
Path('integration-evidence').mkdir(exist_ok=True)
Path('integration-evidence/source-fixes.patch').write_bytes(patch)
