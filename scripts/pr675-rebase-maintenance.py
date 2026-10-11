#!/usr/bin/env python3
"""Replay the reviewed PR 675 rebase; never publish or change remote refs."""
import hashlib
import os
from pathlib import Path
import subprocess

OLD = '1a2db5f244468932fbda8c013891e72ad91ef566'
MAIN = 'de2c439f26446792d4a605b2b91fa2b84777e284'
REPLAY_TREE = '3c05fc1a3fad4dea32898d74cc66d97bb215494b'
FINAL_TREE = '30ac78002d96bf5e7339ecaacb7de97e0e3af424'
FINGERPRINTS = {
    'bec3d0e8': '9adabb0f7e823e58bcca2b99bd5a24d5d28edc00a92e974f121414c30cc37059',
    'b5efd656': '6471edf6058a045887ed3792a1bc4e3f2821b5fcf314cfd9ca7c4296eacb03bb',
    'e716a611': '1c2312a5def2c7779f18e656e01a1cf162fe93f87421854721a5f2fea2a1e0f0',
    'a4f7c098': '8a5d3a75481bab0540de809d2270801b8a8ca4c9c562e9c334103ad3c86d11cd',
    '4179ea1a': 'eea5b1633c58a46885eacc6d690b2bd6c353a8db235e3ebb69d16a78854edeb8',
    'fdfd9022': '678e4f93bb1baed6e5103c0720025b8fb14755db3c6b66ba55ad731d388f6b4e',
}
DOCS_CI = '''# 微服务分支的 GitHub Actions 入口

[文档目录](README.md) · [贡献指南](../CONTRIBUTING.md) · [架构与状态](core-migration.md)

本文对应 PR #675 的微服务分支，不是 main 旧单体的发布说明。
触发方式、输入和权限以当前提交的工作流为准。

## 检查入口

| 入口 | 作用和边界 |
| --- | --- |
| [ci.yml](../.github/workflows/ci.yml) | 手动诊断；调用微服务检查，并检查前端和翻译。 |
| [core-protocol.yml](../.github/workflows/core-protocol.yml) | 手动、可复用及指定 WIP 路径触发；检查真实 Rust/Go 实现、协议与一次性数据库。 |
| [microservice-distribution.yml](../.github/workflows/microservice-distribution.yml) | 手动或复用，生成独立预览包；不发布 Release，不部署。 |
| [store-claim-refund-review.yml](../.github/workflows/store-claim-refund-review.yml) | 手动；复用当前微服务检查，保留商店前端及浏览器回归。旧单体用例仍在 main。 |

检查成功、失败、取消和未运行项必须分别记录。指定路径的触发规则不代表
整个仓库每次提交都会运行完整检查。测试入口不能修改成空检查来消除失败。
旧 `ci_plan.py` 与单体 Go 发布流程已移除，不适用于本分支。

## 发布与生产边界

[Web 发布](../.github/workflows/release-web.yml)与
[Web 部署](../.github/workflows/deploy-web-frontend.yml)仍是独立手动入口。
它们不是变基或测试的后续步骤；本 WIP 不能据此替换线上服务。
微服务包的支持范围、校验与限制见[打包与分发](release-architecture.md)。

真实测试记录必须绑定实际源码、命令、退出码与日志。保留
[本地证据工具](../scripts/local-release-tests.py)和
[校验入口](../scripts/verify-release-commit-checks.sh)的限制，
不伪造记录，不套用不同源码的结果。

模块测试通过不代表前端、权限、订单与资金的完整链路已接通。
跨服务业务缺口仍按[业务验收范围](microkernel-business-acceptance.md)记录。
任何生产操作都需要单独授权；手动触发请求被接受也不等于部署完成。

## 修改后验证

```sh
python3 -B scripts/check-docs-brand.py
python3 -B -m unittest discover -s scripts -p test_check_docs_brand.py
python3 -B scripts/test-core-boundaries.py
python3 -B scripts/test-docs-microservices.py
node --test scripts/workflow-topology.test.mjs
```

这些检查验证文档与工作流边界，不替代应用、数据库及生产验收。
'''


def git(*args, check=True):
    return subprocess.run(['git', *args], check=check, stdout=subprocess.PIPE, stderr=subprocess.PIPE)


def text(*args):
    return git(*args).stdout.decode().strip()


def conflicts():
    result = {}
    for row in git('ls-files', '-u', '-z').stdout.split(b'\0'):
        if row:
            meta, path = row.split(b'\t', 1)
            mode, sha, stage = meta.decode().split()
            result.setdefault(os.fsdecode(path), {})[stage] = (mode, sha)
    return result


def resolve():
    original = Path(text('rev-parse', '--git-path', 'rebase-merge/stopped-sha')).read_text().strip()
    key = original[:8]
    entries = conflicts()
    digest = hashlib.sha256(('\n'.join(sorted(entries)) + '\n').encode()).hexdigest()
    if FINGERPRINTS.get(key) != digest:
        raise RuntimeError(f'Unreviewed conflict set at {original}: {sorted(entries)}')
    for name, stages in entries.items():
        path = Path(name)
        if key in {'b5efd656', 'e716a611', 'a4f7c098'} and set(stages) == {'1', '2'}:
            if git('cat-file', '-e', original + ':' + name, check=False).returncode == 0:
                raise RuntimeError('Refuse deletion not present in the original WIP commit')
            path.unlink()
        elif key == 'bec3d0e8' and name.startswith('apps/api-go/internal/deploycli/'):
            git('checkout', '--ours', '--', name)
            value = path.read_text()
            for old in ('\n\t\tRust:      filepath.Join(filepath.Dir(runtime.paths.InstalledBinary), backendRustName),',
                        '\n\t\tRust: filepath.Join(filepath.Dir(runtime.paths.InstalledBinary), backendRustName),'):
                value = value.replace(old, '')
            path.write_text(value.replace('target != backendGoName && target != backendRustName', 'target != backendGoName'))
        elif key == 'fdfd9022' and name == 'scripts/check-docs-brand.py':
            value = path.read_text()
            start = value.index('<<<<<<< HEAD')
            end = value.index('\n', value.index('\n>>>>>>>', start) + 1)
            merged = '''    "CONTRIBUTING.md", "DESIGN.md", ".github/assets/README.md", "AGENTS.md",
    ".agents/skills/README.md", "docs/agent-workflows.md", "docs/ci-workflow-layout.md",
    "docs/core-migration.md", "docs/release-architecture.md", "docs/legal/README.md",
    "docs/archive.md", "deployment/docker/README.md",'''
            path.write_text(value[:start] + merged + value[end:])
        else:
            git('checkout', '--theirs', '--', name)
    git('add', '-A', '--', *entries)
    print(f'Resolved reviewed conflict {original}: {len(entries)} paths', flush=True)


def cleanup():
    old_paths = git('ls-files', '-z', 'apps/api-go').stdout.split(b'\0')
    old_paths = [p.decode() for p in old_paths if p]
    if len(old_paths) != 64 or git('ls-tree', '-r', '--name-only', OLD, '--', 'apps/api-go').stdout:
        raise RuntimeError('Unexpected retired application additions')
    git('rm', '-q', '--', *old_paths, 'scripts/check-deploy-isolation.sh', 'scripts/test-deploy-package-payload.py')
    path = Path('.github/workflows/store-claim-refund-review.yml')
    value = path.read_text()
    start, end = value.index('  backend:\n'), value.index('  frontend:\n')
    path.write_text(value[:start] + '''  # The legacy monolith tests remain on main. This branch validates the actual
  # Rust core, Go extensions, and store/support PostgreSQL implementation.
  # Module checks do not establish complete migrated payment/refund acceptance.
  backend:
    uses: ./.github/workflows/core-protocol.yml
''' + value[end:])
    Path('docs/ci-workflow-layout.md').write_text(DOCS_CI)
    value = git('show', MAIN + ':docs/waffo-pancake.md').stdout.decode()
    heading, rest = value.split('\n', 1)
    Path('docs/waffo-pancake.md').write_text(heading + '（旧单体参考）\n\n> 本文保留 main 的支付协议与回归说明。`apps/api-go`、旧数据库迁移与部署命令不适用于当前微服务分支。当前实现以 [架构与状态](core-migration.md)及 [Go 扩展](../apps/lmm-extensions/README.md)为准，不能据此认定付款或退款链路已迁移完成。\n' + rest)
    path = Path('docs/standalone-deployment-tool.md')
    heading, rest = path.read_text().split('\n', 1)
    path.write_text(heading + '（旧单体参考）\n\n> 本文的旧 Go 部署引擎、打包脚本与隔离测试不适用于当前 WIP。旧实现保留在 main；新分支使用 [独立分发包](release-architecture.md)，不恢复旧单体工具。\n' + rest)
    path = Path('docs/agent-workflows.md')
    path.write_text(path.read_text().replace('[Actions 说明](ci-workflow-layout.md)、[部署流程](deployment-workflow.md)', '[Actions 说明](ci-workflow-layout.md)、[Docker 开发栈](../deployment/docker/README.md)、[独立分发](release-architecture.md)'))
    retired = Path('apps/api-go')
    if retired.exists():
        for root, dirs, files in os.walk(retired, topdown=False):
            if files:
                raise RuntimeError(f'Unexpected untracked retired files: {files}')
            Path(root).rmdir()


def main():
    if text('rev-parse', 'refs/remotes/origin/main') != MAIN:
        raise RuntimeError('main advanced; a new review is required')
    if text('rev-parse', 'refs/remotes/origin/wip/rust-core-go-extensions') != OLD:
        raise RuntimeError('PR source advanced; do not overwrite it')
    git('switch', '-c', 'pr675-replayed', OLD)
    git('config', 'user.name', 'github-actions[bot]')
    git('config', 'user.email', '41898282+github-actions[bot]@users.noreply.github.com')
    os.environ['GIT_EDITOR'] = 'true'
    result = git('-c', 'rerere.enabled=false', '-c', 'merge.conflictStyle=diff3', 'rebase', '--rebase-merges', MAIN, check=False)
    for attempt in range(7):
        if result.returncode == 0:
            break
        resolve()
        result = git('-c', 'rerere.enabled=false', '-c', 'merge.conflictStyle=diff3', 'rebase', '--continue', check=False)
    if result.returncode or text('rev-parse', 'HEAD^{tree}') != REPLAY_TREE:
        raise RuntimeError('Replayed tree differs from independently reviewed local result')
    if text('rev-list', '--count', MAIN + '..HEAD') != '64':
        raise RuntimeError('Original commit count changed')
    if text('rev-list', '--count', '--merges', MAIN + '..HEAD') != '11':
        raise RuntimeError('Merge history changed')
    cleanup()
    git('diff', '--check')
    git('add', '-A')
    if text('write-tree') != FINAL_TREE:
        raise RuntimeError('Final tree differs from independently tested local result')
    git('commit', '-m', 'fix(rebase): reconcile main additions with microservice boundaries')
    git('merge-base', '--is-ancestor', MAIN, 'HEAD')
    git('diff', '--exit-code', OLD, 'HEAD', '--', 'apps/lmm-core', 'apps/lmm-extensions')
    if text('status', '--porcelain'):
        raise RuntimeError('Working tree is not clean')
    head = text('rev-parse', 'HEAD')
    print(f'REBASED_HEAD={head}\nMAIN={MAIN}\nTREE={FINAL_TREE}', flush=True)
    if 'GITHUB_OUTPUT' in os.environ:
        with open(os.environ['GITHUB_OUTPUT'], 'a') as output:
            output.write(f'sha={head}\n')


if __name__ == '__main__':
    main()
