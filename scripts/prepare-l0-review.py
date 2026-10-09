from pathlib import Path
import gzip, hashlib, json, re, subprocess, sys

root = Path.cwd()
base = root / 'apps/web'
if (base / 'src/features/onboarding/l0-upgrade-copy.test.ts').exists():
    print('Review source already prepared; running verification only.')
    sys.exit(0)

def replace(path, old, new, count=1):
    text = path.read_text()
    if text.count(old) != count:
        raise RuntimeError(f'{path}: expected {count} matches, got {text.count(old)}: {old!r}')
    path.write_text(text.replace(old, new))

patch = gzip.decompress((root / 'scripts/l0-review.patch.gz').read_bytes())
assert hashlib.sha256(patch).hexdigest() == 'd154ae223f0e209d29420b51a38176cdb495688237fc61d1753a982de9c8f537'
subprocess.run(['git', 'apply', '--check', '-'], input=patch, check=True)
subprocess.run(['git', 'apply', '-'], input=patch, check=True)

keys = [
    'Upgrade to a full account',
    'Chat for free or top up to upgrade to a full account.',
    'Chat for free to upgrade to a full account.',
    'Chat (free)',
    'Top up to upgrade',
    'Paid upgrade details',
]
translations = {
    'en': keys,
    'zh': ['升级为正式用户', '通过对话（免费）或充值，升级为正式用户。', '通过免费对话，升级为正式用户。', '对话（免费）', '充值升级', '充值升级说明'],
    'zh-TW': ['升級為正式使用者', '透過對話（免費）或儲值，升級為正式使用者。', '透過免費對話，升級為正式使用者。', '對話（免費）', '儲值升級', '儲值升級說明'],
    'fr': ['Activer votre compte', 'Discutez gratuitement ou rechargez pour activer votre compte.', 'Discutez gratuitement pour activer votre compte.', 'Discuter (gratuit)', 'Recharger et activer', 'Conditions de recharge'],
    'ru': ['Активируйте аккаунт', 'Бесплатно пообщайтесь с помощником или пополните баланс, чтобы активировать аккаунт.', 'Бесплатно пообщайтесь с помощником, чтобы активировать аккаунт.', 'Чат (бесплатно)', 'Пополнить и активировать', 'Условия платной активации'],
    'ja': ['正式ユーザーにアップグレード', '無料の対話またはチャージで、正式ユーザーにアップグレードできます。', '無料の対話で、正式ユーザーにアップグレードできます。', '対話（無料）', 'チャージでアップグレード', 'チャージによるアップグレードの条件'],
    'vi': ['Nâng cấp thành tài khoản chính thức', 'Trò chuyện miễn phí hoặc nạp tiền để nâng cấp thành tài khoản chính thức.', 'Trò chuyện miễn phí để nâng cấp thành tài khoản chính thức.', 'Trò chuyện (miễn phí)', 'Nạp tiền để nâng cấp', 'Điều kiện nâng cấp bằng nạp tiền'],
}
for locale, values in translations.items():
    assert len(values) == len(keys)
    path = base / f'src/i18n/locales/{locale}.json'
    data = json.loads(path.read_text())
    for key, value in zip(keys, values):
        data['translation'][key] = value
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + '\n')

p = base / 'src/features/onboarding/l0-paid-welcome.test.tsx'
replace(p, "'Enable L1 access'", "'Upgrade to a full account'", 2)
replace(p, '    // Both the persistent topbar button and the first-screen rail action exist.\n    const topbar', '    // The two upgrade choices stay visible; the duplicate recharge bar is gone.\n    const freeChat')
replace(p, "      '[data-testid=\"l0-topbar-topup\"]'", "      '[data-testid=\"l0-chat-free\"]'")
replace(p, '    assert.ok(topbar)', "    assert.ok(freeChat)\n    assert.match(freeChat.textContent ?? '', /Chat \\(free\\)/)\n    assert.equal(container.querySelector('[data-testid=\"l0-topbar-topup\"]'), null)")
replace(p, '    assert.equal(topbar.disabled, false)', '    assert.equal(freeChat.disabled, false)')
replace(p, "      container.querySelector('.l0-rail-meta')?.textContent ?? '',\n      /Top up 21\\.01 CNY for instant approval/", "      container.querySelector('[data-testid=\"l0-paid-progress\"]')?.textContent ?? '',\n      /Top up 21\\.01 CNY for instant approval/")
replace(p, "    assert.ok(direct.classList.contains('l0-rail-action--ghost'))", "    assert.equal(\n      container.querySelector('[data-testid=\"l0-upgrade-description\"]')?.textContent,\n      'Chat for free or top up to upgrade to a full account.'\n    )\n    assert.equal(access.contains(container.querySelector('[data-testid=\"l0-paid-progress\"]')), true)\n    assert.ok(direct.classList.contains('l0-rail-action--ghost'))")

p = base / 'src/features/onboarding/getting-started.test.tsx'
replace(p, "button(page, 'Chat to enable L1')", "button(page, 'Chat (free)')")
replace(p, "button(page, 'Unlock')", "button(page, 'Access details')")
replace(p, "page.container.querySelector('.l0-rail-meta')?.textContent ?? ''", "page.container.querySelector('[data-testid=\"l0-paid-progress\"]')?.textContent ?? ''")
new_tests = '''
for (const [name, enabled] of [
  ['disabled', false],
  ['unknown', undefined],
] as const) {
  test(`free dialogue remains explicit when paid activation is ${name}`, async () => {
    const page = await renderPage(false, undefined, null, {
      onboarding: {
        activation_complete: false,
        credential_complete: false,
        first_request_complete: false,
        stage: 'activate',
        paid_activation_enabled: enabled,
      },
    })
    try {
      assert.equal(
        page.container.querySelector('[data-testid="l0-upgrade-description"]')?.textContent,
        'Chat for free to upgrade to a full account.'
      )
      assert.equal(page.container.querySelector('[data-testid="l0-topup-direct"]'), null)
      assert.equal(page.container.querySelector('[data-testid="l0-paid-progress"]'), null)
      assert.ok(page.container.querySelector('[data-testid="l0-wallet-fallback"]'))
      await act(async () => {
        button(page, 'Explore').click()
        await flushEffects()
      })
      await act(async () => {
        button(page, 'Chat (free)').click()
        await flushEffects()
      })
      assert.equal(document.activeElement, page.container.querySelector('#l0-question'))
      assert.equal(consumeQueuedAssistantRequest(), undefined)
      assert.equal(useAuthStore.getState().auth.user?.developer_access_granted, false)
    } finally {
      await unmountPage(page)
    }
  })
}

test('payment confirmation does not ask a paid user to recharge again', async () => {
  const page = await renderPage(false, undefined, null, {
    onboarding: {
      activation_complete: false,
      credential_complete: false,
      first_request_complete: false,
      stage: 'activate',
      paid_activation_enabled: true,
      paid_activation_complete: true,
      paid_activation_min_credits: '500000',
    },
    trust_level_info: {
      level: 0,
      automatic_level: 0,
      override_level: null,
      paid_amount: 0,
      paid_credits: '500000',
      discount_ratio: 1,
      discount_percent: 0,
      inactivity_decay_steps: 0,
      decay_period_days: 0,
      overridden: false,
    },
  })
  try {
    assert.match(
      page.container.querySelector('[data-testid="l0-upgrade-description"]')?.textContent ?? '',
      /Credit requirement met/
    )
    const refresh = page.container.querySelector<HTMLButtonElement>('[data-testid="l0-check-payment"]')
    assert.ok(refresh)
    assert.equal(refresh.disabled, false)
    assert.equal(page.container.querySelector('[data-testid="l0-topup-direct"]'), null)
    assert.equal(page.container.querySelector('[data-testid="l0-chat-free"]'), null)
    assert.equal(useAuthStore.getState().auth.user?.developer_access_granted, false)
  } finally {
    await unmountPage(page)
  }
})
'''
p.write_text(p.read_text() + '\n' + new_tests)

copy_test = base / 'src/features/onboarding/l0-upgrade-copy.test.ts'
copy_test.write_text('''/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const keys = [
  'Upgrade to a full account',
  'Chat for free or top up to upgrade to a full account.',
  'Chat for free to upgrade to a full account.',
  'Chat (free)',
  'Top up to upgrade',
  'Paid upgrade details',
]

for (const locale of ['en', 'zh', 'zh-TW', 'fr', 'ru', 'ja', 'vi']) {
  test(`${locale} provides both upgrade paths without an English fallback`, () => {
    const { translation } = JSON.parse(
      readFileSync(new URL(`../../i18n/locales/${locale}.json`, import.meta.url), 'utf8')
    ) as { translation: Record<string, string> }
    for (const key of keys) {
      assert.ok(translation[key]?.trim(), key)
      if (locale !== 'en') assert.notEqual(translation[key], key)
    }
    if (locale === 'zh') {
      assert.equal(translation[keys[1]], '通过对话（免费）或充值，升级为正式用户。')
      assert.equal(translation[keys[3]], '对话（免费）')
      assert.equal(translation[keys[4]], '充值升级')
    }
  })
}
''')

p = base / 'DESIGN.md'
text = p.read_text()
start = text.index('### L0 welcome surface exception')
end = text.index('### Cut logo rollout', start)
text = text[:start] + '''### L0 welcome surface exception

- The unactivated `/getting-started` branch uses the existing console theme, a compact route header, and a single centered composition. The activated setup branch is unchanged.
- A borderless introduction explains the two upgrade paths: free conversation or a qualifying top-up. Two equal-width controls lead into the existing inline conversation and wallet. There is no duplicate LMM/top-up bar, payment progress ring, or application-letter form.
- The token cloud, view tabs, and mounted conversation remain in order. Smaller cloud heights and tighter mobile spacing keep the question form near the first screen. Input text stays at 16px, controls are at least 44px high, and reduced motion is preserved.
- Account status and contact support are secondary actions below the workspace. The access-details tab contains the exact backend-derived payment amount, eligibility conditions, confirmation action, account status, Pi guide, and source questionnaire. The amount never comes from wallet balance or a historical policy amount.
- Disabled or unknown paid activation never promises a paid upgrade. A completed payment asks for account refresh instead of another top-up. Only the authoritative server access decision advances the account; neither answer text nor a browser click grants access.
- The existing focused-onboarding shell, compact service disclosure, scrolling chrome, and hidden sidebar assistant are preserved. No production payment, deployment, or access-policy change is part of this presentation update.
- Coverage lives in `l0-paid-welcome.test.tsx`, `getting-started.test.tsx`, and `l0-upgrade-copy.test.ts`. Repeat real-browser review for layout changes; DOM tests alone do not establish visual quality.

''' + text[end:]
p.write_text(text)

paths = subprocess.check_output(['git', 'diff', '--name-only'], text=True).splitlines()
paths.append(str(copy_test.relative_to(root)))
Path('/tmp/l0-review-paths.json').write_text(json.dumps(paths))
headers = {}
pattern = re.compile(r'^/\*\nCopyright \(C\)[\s\S]*?QuantumNous[\s\S]*?\*/\n+')
try:
    for name in paths:
        path = root / name
        if path.suffix not in ('.ts', '.tsx'):
            continue
        text = path.read_text()
        match = pattern.match(text)
        if match:
            headers[name] = match[0]
            path.write_text(text[match.end():])
    formatter = next(p for p in (base / 'node_modules/.bin/oxfmt', root / 'node_modules/.bin/oxfmt') if p.exists())
    subprocess.run([str(formatter), '-c', '.oxfmtrc.json', '--write', *[str((root / p).relative_to(base)) for p in paths]], cwd=base, check=True)
finally:
    for name, header in headers.items():
        path = root / name
        path.write_text(header + path.read_text().lstrip('\n'))
subprocess.run(['git', 'diff', '--check'], check=True)
subprocess.run(['git', 'add', '--', *paths], check=True)
print('Prepared source paths:', *paths, sep='\n')
