from pathlib import Path

root = Path('apps/web')
def replace(path, old, new):
    p = root / path
    text = p.read_text()
    assert old in text, (path, old)
    p.write_text(text.replace(old, new, 1))

# Keep the pure command builder outside the React component module. Its input
# validation and shell quoting remain unchanged and retain their existing tests.
p = root / 'src/features/tool-market/oauth-connection.tsx'
text = p.read_text()
start = text.index('export function marketOAuthCommand(')
end = text.index('export function MarketOAuthConnection(', start)
helper = text[start:end]
helper = helper.replace("  )\n    throw new Error('Invalid MCP endpoint')", "  ) {\n    throw new Error('Invalid MCP endpoint')\n  }")
(root / 'src/features/tool-market/oauth-command.ts').write_text(text.splitlines()[0] + "\nimport { marketEndpoint } from './connection-utils'\n\n" + helper)
text = text[:start] + text[end:]
text = text.replace("import { marketEndpoint } from './connection-utils'", "import { marketOAuthCommand } from './oauth-command'")
p.write_text(text)
replace('src/features/tool-market/oauth-connection.test.ts', "from './oauth-connection'", "from './oauth-command'")
replace('src/features/tool-market/oauth-connection.test.ts', "  ])\n    assert.throws(() => marketOAuthCommand(endpoint))", "  ]) {\n    assert.throws(() => marketOAuthCommand(endpoint))\n  }")
replace('src/features/tool-market/meta-tool-card.tsx', "  if (!marketSupports(config, 'metamcp') || tool?.name !== 'metamcp')\n    return null", "  if (!marketSupports(config, 'metamcp') || tool?.name !== 'metamcp') {\n    return null\n  }")
replace('src/features/tool-market/service-editor.tsx', "      if (input.tools.some((tool) => tool.provider_pricing) && !validMultiplier)\n        throw new Error('Invalid multiplier')", "      if (input.tools.some((tool) => tool.provider_pricing) && !validMultiplier) {\n        throw new Error('Invalid multiplier')\n      }")
replace('scripts/tool-market-provider-review.mjs', "        if (url.pathname === '/api/status')\n          return route.fulfill({", "        if (url.pathname === '/api/status') {\n          return route.fulfill({")
replace('scripts/tool-market-provider-review.mjs', "          })\n        if (url.pathname.startsWith('/api/'))", "          })\n        }\n        if (url.pathname.startsWith('/api/'))")
# Bind the memoized translator to the actual language it depends on, retaining
# a stable identity within a language and refreshing it after language changes.
replace('src/features/tool-market/provider-i18n.ts', "  registerProviderTranslations(translation.i18n)\n  const t = useMemo(\n    () => translation.i18n.getFixedT(null, [providerNamespace, 'translation']),\n    [translation.i18n, translation.t]\n  )", "  const { i18n } = translation\n  registerProviderTranslations(i18n)\n  const language = i18n.resolvedLanguage || i18n.language\n  const t = useMemo(\n    () => i18n.getFixedT(language, [providerNamespace, 'translation']),\n    [i18n, language]\n  )")
