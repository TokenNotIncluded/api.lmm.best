#!/usr/bin/env node
// Explicit maintainer operation: never called by npm installation or the plugin.
import { execFileSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, copyFileSync, existsSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const repo = 'TokenNotIncluded/codewhale-lmm-provider';
const parentRepo = 'TokenNotIncluded/api.lmm.best';
const subpath = 'packages/codewhale-lmm-provider';
const source = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const run = (cmd, args, cwd, capture = false) => execFileSync(cmd, args, {
  cwd, encoding: 'utf8', stdio: capture ? ['ignore', 'pipe', 'pipe'] : 'inherit', shell: false,
});
let temporary, worktree, parent, created = false, complete = false;
try {
  parent = run('git', ['rev-parse', '--show-toplevel'], source, true).trim();
  if (resolve(parent, subpath) !== source) throw new Error('Run from the staged package in the parent repository.');
  if (run('git', ['status', '--porcelain'], parent, true).trim()) throw new Error('Parent checkout must be clean.');
  const remote = run('git', ['remote', 'get-url', 'origin'], parent, true).trim();
  if (!/^(https:\/\/github\.com\/|git@github\.com:)TokenNotIncluded\/api\.lmm\.best(?:\.git)?$/.test(remote)) throw new Error('Origin is not the expected parent repository.');
  run('gh', ['auth', 'status'], parent);
  const defaultBranch = run('gh', ['repo', 'view', parentRepo, '--json', 'defaultBranchRef', '--jq', '.defaultBranchRef.name'], parent, true).trim();
  const names = JSON.parse(run('gh', ['api', '--paginate', '--slurp', 'orgs/TokenNotIncluded/repos?per_page=100&type=all'], parent, true)).flat();
  if (names.some(item => item.name.toLowerCase() === 'codewhale-lmm-provider')) throw new Error('Destination already exists; no remote or source will be overwritten.');
  temporary = mkdtempSync(join(tmpdir(), 'lmm-codewhale-publish-'));
  const independent = join(temporary, 'repository');
  mkdirSync(independent);
  const files = run('git', ['ls-files', '-z', '--', subpath], parent, true).split('\0').filter(Boolean);
  if (!files.some(file => file === `${subpath}/src/cli.mjs`)) throw new Error('Package source is not committed as ordinary files.');
  for (const file of files) {
    const destination = join(independent, file.slice(subpath.length + 1));
    mkdirSync(dirname(destination), { recursive: true });
    copyFileSync(join(parent, file), destination);
  }
  run('git', ['init', '-b', 'main'], independent);
  run('git', ['add', '.'], independent);
  run('git', ['commit', '-m', 'feat: add Codewhale LMM OAuth companion adapter'], independent);
  // gh creation fails rather than replacing an existing repository; the prior list is diagnostic.
  run('gh', ['repo', 'create', repo, '--public', '--description', 'LMM OAuth companion adapter for Codewhale'], independent);
  created = true;
  run('git', ['remote', 'add', 'origin', `https://github.com/${repo}.git`], independent);
  run('git', ['push', '-u', 'origin', 'main'], independent);
  const sha = run('git', ['rev-parse', 'HEAD'], independent, true).trim();
  run('git', ['fetch', 'origin', defaultBranch], parent);
  const branch = `chore/codewhale-submodule-${Date.now()}`;
  worktree = join(temporary, 'parent');
  run('git', ['worktree', 'add', '-b', branch, worktree, `origin/${defaultBranch}`], parent);
  if (!existsSync(join(worktree, subpath, 'src', 'cli.mjs'))) throw new Error('Merge the adapter source PR before converting it into a submodule.');
  run('git', ['rm', '-r', '--', subpath], worktree);
  run('git', ['submodule', 'add', `https://github.com/${repo}.git`, subpath], worktree);
  run('git', ['checkout', '--detach', sha], join(worktree, subpath));
  run('git', ['add', '.gitmodules', subpath], worktree);
  run('git', ['commit', '-m', 'chore: extract Codewhale provider to independent submodule'], worktree);
  run('git', ['push', '-u', 'origin', branch], worktree);
  run('gh', ['pr', 'create', '--repo', parentRepo, '--base', defaultBranch, '--head', branch,
    '--title', 'chore: add Codewhale provider as an independent submodule',
    '--body', `Extracts the committed adapter to ${repo} and pins submodule ${subpath} to ${sha}. No Pi/DSH submodule changes. Review before merging.`], worktree);
  complete = true;
  console.log(`Published ${repo}; submodule conversion is in the new parent PR.`);
} catch (error) {
  console.error(error.message);
  if (created) console.error(`The remote ${repo} was created. It was NOT deleted or rolled back.`);
  if (temporary) console.error(`Recovery files remain at ${temporary}`);
  process.exitCode = 1;
} finally {
  if (complete) {
    if (worktree) run('git', ['worktree', 'remove', '--force', worktree], parent);
    if (temporary) rmSync(temporary, { recursive: true, force: true });
  }
}
