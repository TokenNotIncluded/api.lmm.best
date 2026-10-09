from pathlib import Path
import json
import re
import subprocess

root = Path.cwd()
workspace = root / '.review/assistant-workspace'

def replace_once(path, before, after):
    file = root / path
    text = file.read_text()
    if text.count(before) != 1:
        raise ValueError('Expected one edit target: '+path)
    file.write_text(text.replace(before, after, 1))

replace_once('apps/api-go/model/assistant_workspace_test.go',
    'func TestAssistantWorkspaceMarketConnectionIsOptInAndDoesNotSpend(t *testing.T) {\n\tf := newMarketFixture(t, 101)',
    'func TestAssistantWorkspaceMarketConnectionIsOptInAndDoesNotSpend(t *testing.T) {\n\tinstallPaidPolicyCurrencyFixture(t, common.QuotaPerUnit)\n\tf := newMarketFixture(t, 101)\n\trequire.NoError(t, f.db.AutoMigrate(&TopUp{}))\n\trequire.NoError(t, f.db.Model(&User{}).Where("id = ?", f.buyer.Id).Update("trust_level_override", 1).Error)')

rows = []
for file in sorted(workspace.glob('copy-*.jsonl')):
    rows.extend(json.loads(line) for line in file.read_text().splitlines() if line.strip())
if len(rows) != 116 or len({row[0] for row in rows}) != 116:
    raise ValueError('Incomplete translation catalogue')
for row in rows:
    if len(row) != 7 or any(not isinstance(text, str) or not text for text in row):
        raise ValueError('Incomplete locale row')
    fields = lambda text: sorted(re.findall(r'\{\{.*?\}\}|\$[a-z]+|\$\$', text))
    if any(fields(text) != fields(row[0]) for text in row[1:]):
        raise ValueError('Translation variable mismatch: '+row[0])
rows.append(['Tool conditions changed in another session. Review the merged limits and save again.', '工具条件已在其他会话修改。请检查合并后的限制，再次保存。', '工具條件已在其他工作階段修改。請檢查合併後的限制，再次儲存。', 'Les conditions ont changé dans une autre session. Vérifiez les limites fusionnées puis enregistrez.', '別のセッションで条件が変更されました。統合後の上限を確認して保存してください。', 'Условия изменены в другом сеансе. Проверьте объединённые ограничения и сохраните снова.', 'Điều kiện đã đổi ở phiên khác. Kiểm tra giới hạn đã gộp rồi lưu lại.'])
module = "/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */\nconst rows = " + json.dumps(rows, ensure_ascii=False, indent=2) + "\n\nexport const assistantWorkspaceCopy = Object.fromEntries(\n  ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi'].map((locale, index) => [\n    locale, Object.fromEntries(rows.map((row) => [row[0], row[index]])),\n  ])\n)\n"
(root/'apps/web/scripts/assistant-workspace-copy.mjs').write_text(module)
replace_once('apps/web/scripts/add-missing-keys.mjs', "import { assistantToolCopy } from './assistant-tool-copy.mjs'", "import { assistantToolCopy } from './assistant-tool-copy.mjs'\nimport { assistantWorkspaceCopy } from './assistant-workspace-copy.mjs'")
replace_once('apps/web/scripts/add-missing-keys.mjs', 'async function main() {', '''async function main() {
  if (process.argv.includes('--assistant-workspace-only')) {
    for (const [locale, translations] of Object.entries(assistantWorkspaceCopy)) {
      Object.assign(newKeys[locale], translations)
      const filePath = path.join(LOCALES_DIR, `${locale}.json`)
      const json = JSON.parse(await fs.readFile(filePath, 'utf8'))
      Object.assign(json.translation, translations)
      json.translation = Object.fromEntries(
        Object.entries(json.translation).sort(([a], [b]) => a.localeCompare(b))
      )
      await fs.writeFile(filePath, stableStringify(json), 'utf8')
      console.log(`${locale}: assistant workspace translations applied`)
    }
    return
  }''')
subprocess.run(['git','add','apps/api-go/model/assistant_workspace_test.go','apps/web/scripts/assistant-workspace-copy.mjs','apps/web/scripts/add-missing-keys.mjs'], check=True)
