from pathlib import Path
import sys

root = Path(sys.argv[1]).resolve()
workbench = Path(__file__).parent
output = Path(sys.argv[2])
fixture = root / 'apps/web/src/features/debug/console-page-fixtures.ts'
text = fixture.read_text()
old = "  '/api/assistant/support/self': [],"
assert text.count(old) == 1
fixture.write_text(text.replace(old, "  '/api/assistant/support/self': { request: null },\n  '/api/assistant/registration-check': { state: 'ready' },"))
entry = root / 'apps/web/src/debug-main.tsx'
text = entry.read_text()
anchor = "import { api } from '@/lib/http-client'"
assert text.count(anchor) == 1
text = text.replace(anchor, anchor + "\nimport { useAuthStore } from '@/stores/auth-store'\nObject.assign(window, { __l0ReviewApi: { api, useAuthStore } })")
entry.write_text(text)
script = (workbench / 'l0-browser.mjs').read_text()
old = "        const { api } = await import('/src/lib/http-client.ts')\n        const { useAuthStore } = await import('/src/stores/auth-store.ts')"
assert script.count(old) == 1
script = script.replace(old, "        const { api, useAuthStore } = window.__l0ReviewApi")
output.write_text(script)
print('Prepared temporary Rsbuild debug fixture; production entry is unchanged')
