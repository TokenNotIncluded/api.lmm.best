/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
export const REPOSITORIES = {
  project: 'TokenNotIncluded/api.lmm.best',
  scripts: 'TokenNotIncluded/lmm-scripts',
} as const
export type RepositoryKind = keyof typeof REPOSITORIES
export const repositoryUrl = (kind: RepositoryKind) =>
  `https://github.com/${REPOSITORIES[kind]}`

export async function fetchRepositoryStars(
  kind: RepositoryKind,
  signal?: AbortSignal,
  request: typeof fetch = fetch
): Promise<number> {
  const controller = new AbortController()
  const abort = () => controller.abort()
  if (signal?.aborted) controller.abort()
  signal?.addEventListener('abort', abort, { once: true })
  const timeout = setTimeout(abort, 8000)
  try {
    const response = await request(
      `https://api.github.com/repos/${REPOSITORIES[kind]}`,
      {
        credentials: 'omit',
        headers: { Accept: 'application/vnd.github+json' },
        signal: controller.signal,
      }
    )
    if (!response.ok) {
      throw new Error('GitHub repository statistics unavailable')
    }
    const value: unknown = await response.json()
    if (
      !value ||
      typeof value !== 'object' ||
      !('full_name' in value) ||
      value.full_name !== REPOSITORIES[kind] ||
      !('stargazers_count' in value) ||
      typeof value.stargazers_count !== 'number' ||
      !Number.isSafeInteger(value.stargazers_count) ||
      value.stargazers_count < 0
    ) {
      throw new Error('Invalid GitHub repository statistics')
    }
    return value.stargazers_count
  } finally {
    clearTimeout(timeout)
    signal?.removeEventListener('abort', abort)
  }
}
