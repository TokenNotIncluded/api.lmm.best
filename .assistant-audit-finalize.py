from pathlib import Path

# Preserve the existing notice verbatim and put it before imports.
p = Path('apps/web/src/features/store/new-variants-editor.tsx')
text = p.read_text()
old = "import { Plus, Trash2 } from 'lucide-react'\n/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */\n"
new = "/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */\nimport { Plus, Trash2 } from 'lucide-react'\n"
assert text.startswith(old)
p.write_text(text.replace(old, new, 1))

# Restore the repository's existing explicit-manual-CI contract. Keep every
# browser/test job, permission, concurrency rule and deployment boundary intact.
p = Path('.github/workflows/sitewide-ui-review.yml')
text = p.read_text()
block = "  pull_request:\n    paths:\n      - 'apps/web/**'\n      - 'package.json'\n      - 'bun.lock'\n      - '.github/workflows/sitewide-ui-review.yml'\n"
assert block in text
p.write_text(text.replace(block, '', 1))

# The efficiency report describes measurements before the three policy tools.
# Keep those measurements rather than pretending to have remeasured them.
p = Path('docs/assistant-efficiency-audit.md')
text = p.read_text()
anchor = '原目录有 67 个工具；本次增加按需发现和普通结束工具，合计 69 个，仍按 16 组管理。'
assert anchor in text
p.write_text(text.replace(anchor, '本报告记录按需发现和普通结束功能合入时的检查结果：原目录有 67 个工具，新增后为 69 个、16 组。后续政策工具已将当前目录扩为 72 个、17 组，最新目录见 [工具配置说明](assistant-tool-policy.md)。本报告下列数字仍保留当时的测量口径。', 1))
