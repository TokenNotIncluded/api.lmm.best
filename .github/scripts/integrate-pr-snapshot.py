# Copyright (C) 2026 LIghtJUNction
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Reproduce the reviewed merge resolutions on an isolated integration branch."""
import json
import pathlib
import re
import subprocess

ROOT = pathlib.Path('.')
OUT = ROOT / 'integration-evidence'
OUT.mkdir(exist_ok=True)
SNAPSHOT = [
    (324, 'f888cc68b52f3a9187bdd4b04f43b7dc61dd25d2'),
    (316, '8fd32ddc7fdf1e6e5a4930f671b516d6449a3a27'),
    (334, 'f52658cf184c256e1a8bbcab50f0525872263d4c'),
    (320, '549dc1dd94b2f07774da3bb2c412e9fd8a999445'),
    (328, '884e1d2b3f88b465544c4947f1390297b9fed17d'),
    (333, '3486e368ccee2b25f9ec1a70d2246b98b55a53f7'),
    (338, '591472c28b613bfc6d8831108316cdd1d7968b28'),
    (335, 'da3aac81c286b74f22271360872f589e89715e55'),
    (329, 'c34bd6d4de0ce614b1049d48da8249e5fc8e4e30'),
    (336, '83feb35bb5debde1583aa74080aa4d3f2e0b5138'),
    (337, '03c3037783ead5f9d84a08ef66974a19ec44c82f'),
]


def run(*args, check=True):
    return subprocess.run(args, text=True, check=check, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)


def replace_once(path, before, after):
    target = ROOT / path
    content = target.read_text()
    if content.count(before) != 1:
        raise RuntimeError(f'Unexpected source while resolving {path}')
    target.write_text(content.replace(before, after))


def incoming(path, sha):
    (ROOT / path).write_text(run('git', 'show', f'{sha}:{path}').stdout)


def drop_restored_loop(path):
    content = (ROOT / path).read_text()
    pattern = re.compile(r'^<<<<<<< .*\n(.*?)^=======\n(.*?)^>>>>>>> .*\n', re.M | re.S)
    matches = list(pattern.finditer(content))
    if len(matches) != 1:
        raise RuntimeError('Expected one moved-agent-loop conflict')
    match = matches[0]
    if match[1].strip() or not match[2].lstrip().startswith('func runAssistantAgent('):
        raise RuntimeError('Refusing to discard an unreviewed agent change')
    (ROOT / path).write_text(content[:match.start()] + content[match.end():])


report = []
run('git', 'config', 'core.hooksPath', '/dev/null')
run('git', 'config', 'user.name', 'github-actions[bot]')
run('git', 'config', 'user.email', '41898282+github-actions[bot]@users.noreply.github.com')
for number, sha in SNAPSHOT:
    ref = f'refs/heads/integration-input-{number}'
    run('git', 'fetch', '--no-tags', 'origin', f'refs/pull/{number}/head:{ref}')
    if run('git', 'rev-parse', ref).stdout.strip() != sha:
        raise RuntimeError(f'PR {number} moved; stop for renewed review')
    result = run('git', 'merge', '--no-ff', '--no-edit', ref, check=False)
    (OUT / f'pr-{number}-merge.log').write_text(result.stdout)
    if result.returncode:
        conflicts = set(filter(None, run('git', 'diff', '--name-only', '--diff-filter=U').stdout.splitlines()))
        if number == 329 and conflicts == {'apps/web/src/features/assistant/assistant-tool-calls.tsx'}:
            path = 'apps/web/src/features/assistant/assistant-tool-calls.tsx'
            content = (ROOT / path).read_text()
            pattern = re.compile(r'^<<<<<<< .*\n(.*?)^=======\n(.*?)^>>>>>>> .*\n', re.M | re.S)
            matches = list(pattern.finditer(content))
            if len(matches) != 2 or 'AssistantSupportReview' not in matches[0][1] or 'canReviewSupport' not in matches[1][1]:
                raise RuntimeError('Unexpected support recovery conflict')
            (ROOT / path).write_text(pattern.sub(lambda match: match[1], content))
            replace_once(path, "trace.input?.action !== 'disable_account'", "(trace.input?.action === undefined || trace.input.action === 'support')")
            path = ROOT / 'apps/web/src/features/assistant/assistant-support-review.tsx'
            header = path.read_text().split('import ', 1)[0]
            path.write_text(header + "import { AssistantSupportReviewDialog } from './assistant-support-review-dialog'\n\n// Share the reviewed form rather than maintaining two confirmation paths.\nexport function AssistantSupportReview() {\n  return (\n    <div className='px-3 pb-3'>\n      <AssistantSupportReviewDialog />\n    </div>\n  )\n}\n")
            replace_once('apps/web/src/features/assistant/assistant-support-review-dialog.tsx', "type='button'\n        size=", "type='button'\n        data-testid='assistant-support-review'\n        size=")
        elif number == 336 and conflicts == {
            'apps/api-go/controller/assistant_agent.go',
            'apps/web/src/features/assistant/assistant-activation-tool.tsx',
            'apps/web/src/features/assistant/assistant-activation-tool.test.tsx',
        }:
            drop_restored_loop('apps/api-go/controller/assistant_agent.go')
            # The registration PR intentionally retires application letters.
            # Keep that new status-only UI, not the obsolete mutable form.
            for name in ['assistant-activation-tool.tsx', 'assistant-activation-tool.test.tsx']:
                incoming('apps/web/src/features/assistant/' + name, sha)
            replace_once('apps/web/src/features/assistant/assistant-activation-tool.tsx', '  onDraftConsumed?: () => void', '  onDraftConsumed?: () => void\n  onApproved?: () => void')
            # Carry the actual new termination behavior into the moved loop.
            replace_once('apps/api-go/controller/assistant_agent_loop.go', 'result = executeAssistantTool(c, call)', 'result = executeAssistantTool(c, call)\n\t\t\t\tif finishAssistantRegistrationTermination(c) {\n\t\t\t\t\treturn\n\t\t\t\t}')
        elif number == 337 and conflicts == {
            'apps/api-go/controller/assistant_agent.go',
            'apps/web/src/features/assistant/assistant-registration-state.ts',
            'apps/web/src/features/assistant/assistant-registration-status.tsx',
            'apps/web/src/features/system-settings/content/assistant-l1-review-settings.tsx',
            'apps/web/src/features/system-settings/content/assistant-settings-section.test.tsx',
        }:
            drop_restored_loop('apps/api-go/controller/assistant_agent.go')
            # These four files differ from #336 only in its reviewed follow-up.
            for path in sorted(conflicts - {'apps/api-go/controller/assistant_agent.go'}):
                incoming(path, sha)
        else:
            (OUT / 'unexpected-conflicts.json').write_text(json.dumps({'pr': number, 'paths': sorted(conflicts)}))
            raise RuntimeError(f'Unreviewed merge conflict in PR {number}: {sorted(conflicts)}')
        run('git', 'add', '--', 'apps')
        run('git', 'commit', '--no-edit')
    report.append({'pr': number, 'head': sha, 'integrated': True})
    print(json.dumps(report[-1]))
run('git', 'diff', '--check')
(OUT / 'merge-report.json').write_text(json.dumps(report, indent=2))
(OUT / 'integrated-head.txt').write_text(run('git', 'rev-parse', 'HEAD').stdout)
