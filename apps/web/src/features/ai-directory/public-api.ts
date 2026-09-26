/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
type PublicDirectoryPath =
  | '/api/ai-directory'
  | `/api/ai-directory/ads?offset=${number}`

/** Public lists must not inherit console credentials or refresh redirects. */
export async function getPublicDirectory<T>(
  path: PublicDirectoryPath
): Promise<{ data: T }> {
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), 10_000)
  try {
    const response = await fetch(path, {
      method: 'GET',
      credentials: 'omit',
      headers: { Accept: 'application/json' },
      signal: controller.signal,
    })
    if (!response.ok) throw new Error('Unable to load AI directory')
    return { data: (await response.json()) as T }
  } finally {
    clearTimeout(timeout)
  }
}
