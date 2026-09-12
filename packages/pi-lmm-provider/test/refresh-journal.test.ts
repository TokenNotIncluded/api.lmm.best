import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { RefreshJournal, refreshDigest } from '../src/refresh-journal.ts';

test('fences one refresh digest across independent journal instances', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'lmm-refresh-'));
  try {
    const first = new RefreshJournal(directory);
    const second = new RefreshJournal(directory);
    await first.begin('https://api.lmm.best', 'refresh-secret');
    await assert.rejects(second.begin('https://api.lmm.best', 'refresh-secret'), { code: 'refresh_already_attempted' });
    const files = await import('node:fs/promises').then((fs) => fs.readdir(directory));
    assert.equal(files.length, 1);
    const marker = await readFile(join(directory, files[0]!), 'utf8');
    assert.match(marker, /"issuer":"https:\/\/api\.lmm\.best"/);
    assert.match(marker, new RegExp(refreshDigest('refresh-secret')));
    assert.doesNotMatch(marker, /refresh-secret/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('allows a new refresh digest while preserving the old fence', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'lmm-refresh-'));
  try {
    const journal = new RefreshJournal(directory);
    await journal.begin('https://api.lmm.best', 'old-refresh');
    await journal.begin('https://api.lmm.best', 'new-refresh');
    assert.equal((await import('node:fs/promises').then((fs) => fs.readdir(directory))).length, 2);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test('persistence failure is reported before any refresh request', async () => {
  const journal = new RefreshJournal(join('/dev/null', 'lmm-refresh-file-' + Date.now()));
  await assert.rejects(journal.begin('https://api.lmm.best', 'refresh-secret'), { code: 'refresh_storage_unavailable' });
});
