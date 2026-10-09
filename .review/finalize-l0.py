import json, os
from pathlib import Path
root=Path(os.environ['L0_REPO'])
old='Top up {{amount}} for instant approval.'
new='Top up {{amount}} to enable L1 immediately.'
values={
'en': ('Automatic paid activation is unavailable for this account.', new),
'zh': ('此账户暂不支持充值自动开通。','充值 {{amount}} 可立即开通 L1。'),
'zh-TW': ('此帳戶暫不支援儲值自動開通。','儲值 {{amount}} 可立即開通 L1。'),
'fr': ('L’activation automatique par paiement n’est pas disponible pour ce compte.', 'Rechargez {{amount}} pour activer immédiatement l’accès L1.'),
'ja': ('このアカウントでは、入金による自動有効化は利用できません。','{{amount}} の入金で L1 をすぐに有効化できます。'),
'vi': ('Tài khoản này không hỗ trợ tự động kích hoạt bằng thanh toán.', 'Nạp {{amount}} để kích hoạt L1 ngay.'),
'ru': ('Автоматическая активация через оплату недоступна для этого аккаунта.', 'Пополните на {{amount}}, чтобы сразу открыть доступ L1.'),
}
for lang,(review,paid) in values.items():
 p=root/f'apps/web/src/i18n/locales/{lang}.json';s=p.read_text();d=json.loads(s)['translation']
 for key,value,newkey in [('Automatic paid activation is unavailable for this account.',review,'Automatic paid activation is unavailable for this account.'),(old,paid,new)]:
  a=json.dumps(key,ensure_ascii=False)+': '+json.dumps(d[key],ensure_ascii=False)
  b=json.dumps(newkey,ensure_ascii=False)+': '+json.dumps(value,ensure_ascii=False)
  assert s.count(a)==1,(lang,key);s=s.replace(a,b)
 json.loads(s);p.write_text(s)
for name in ['l0-welcome.tsx','l0-paid-welcome.test.tsx','getting-started.test.tsx']:
 p=root/'apps/web/src/features/onboarding'/name;s=p.read_text();assert 'for instant approval' in s;s=s.replace('for instant approval','to enable L1 immediately');p.write_text(s)
header=(root/'apps/web/src/features/onboarding/l0-access-copy.ts').read_text().split('*/',1)[0]+'*/\n'
(root/'apps/web/src/features/onboarding/l0-access-translations.test.ts').write_text(header+'''
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'

const staleReview = {
  zh: '此账户需通过审核开通。',
  'zh-TW': '此帳戶須通過審核開通。',
  fr: 'Ce compte nécessite une validation.',
  ja: 'このアカウントの有効化には審査が必要です。',
  vi: 'Tài khoản này cần được xét duyệt.',
  ru: 'Для этого аккаунта требуется проверка.',
}
for (const lang of ['en', ...Object.keys(staleReview)]) {
  test(`${lang} explains activation without restoring an application requirement`, async () => {
    const { translation } = JSON.parse(await readFile(
      new URL(`../../i18n/locales/${lang}.json`, import.meta.url), 'utf8'
    ))
    const paid = translation['Top up {{amount}} to enable L1 immediately.']
    assert.ok(paid.includes('{{amount}}'))
    assert.ok(paid.includes('L1'))
    assert.ok(translation['Describe what you need. The assistant can enable L1 without an application letter.'])
    if (Object.hasOwn(staleReview, lang)) {
      assert.notEqual(
        translation['Automatic paid activation is unavailable for this account.'],
        staleReview[lang as keyof typeof staleReview]
      )
    }
    assert.equal(translation['Top up {{amount}} for instant approval.'], undefined)
  })
}
''')
