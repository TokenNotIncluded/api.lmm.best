import { createHash } from 'node:crypto';
import { mkdir, open } from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import { LmmError } from './protocol.ts';

const FIXED_FAILURE = 'LMM refresh could not be safely recorded. Use /login.';
const ALREADY_ATTEMPTED = 'LMM refresh was already attempted. Use /login.';

export function refreshDigest(refresh: string): string {
  return createHash('sha256').update(refresh, 'utf8').digest('hex');
}

/** Durable, credential-free fencing for one-shot refresh tokens. */
export class RefreshJournal {
  readonly directory: string;

  constructor(directory: string) {
    if (!directory) throw new LmmError('refresh_storage_unverified', FIXED_FAILURE);
    this.directory = directory;
  }

  async begin(issuer: string, refresh: string): Promise<void> {
    const digest = refreshDigest(refresh);
    const marker = join(this.directory, `${refreshDigest(`${issuer}\n${digest}`)}.refresh`);
    let handle: Awaited<ReturnType<typeof open>> | undefined;
    try {
      const firstCreated = await mkdir(this.directory, { recursive: true, mode: 0o700 });
      handle = await open(marker, 'wx', 0o600);
      await handle.writeFile(JSON.stringify({ issuer, refresh_sha256: digest }) + '\n', 'utf8');
      await handle.sync();
      await handle.close();
      handle = undefined;
      const directoryHandle = await open(this.directory, 'r');
      try { await directoryHandle.sync(); } finally { await directoryHandle.close(); }
      if (firstCreated) {
        const firstCreatedParent = dirname(resolve(firstCreated));
        let parent = dirname(resolve(this.directory));
        while (true) {
          const parentHandle = await open(parent, 'r');
          try { await parentHandle.sync(); } finally { await parentHandle.close(); }
          if (parent === firstCreatedParent || parent === dirname(parent)) break;
          parent = dirname(parent);
        }
      }
    } catch (error) {
      await handle?.close().catch(() => {});
      if ((error as NodeJS.ErrnoException).code === 'EEXIST') {
        throw new LmmError('refresh_already_attempted', ALREADY_ATTEMPTED);
      }
      throw new LmmError('refresh_storage_unavailable', FIXED_FAILURE);
    }
  }
}
