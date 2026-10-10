/*
Copyright (C) 2026 LIghtJUNction
*/

const MAX_CATALOGUE_BYTES = 1_048_576
const MAX_MODELS = 10_000
const MAX_MODEL_ID_LENGTH = 512

/** Use this instance's relay authorization, never the console session or a configured external URL. */
export async function getKeyModels(
  tokenKey: string,
  signal: AbortSignal
): Promise<string[]> {
  try {
    if (!tokenKey || tokenKey.trim() !== tokenKey) {
      throw new Error('Invalid key')
    }
    const key = tokenKey.startsWith('sk-') ? tokenKey : `sk-${tokenKey}`
    const response = await fetch('/v1/models', {
      method: 'GET',
      headers: { Authorization: `Bearer ${key}` },
      credentials: 'omit',
      cache: 'no-store',
      redirect: 'error',
      signal: AbortSignal.any([signal, AbortSignal.timeout(15_000)]),
    })
    if (!response.ok || !response.body) throw new Error('Catalogue unavailable')
    const reader = response.body.getReader()
    const decoder = new TextDecoder('utf-8', { fatal: true })
    let bytes = 0
    let text = ''
    try {
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        bytes += value.byteLength
        if (bytes > MAX_CATALOGUE_BYTES) {
          await reader.cancel()
          throw new Error('Catalogue too large')
        }
        text += decoder.decode(value, { stream: true })
      }
      text += decoder.decode()
    } finally {
      reader.releaseLock()
    }
    const payload: unknown = JSON.parse(text)
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
      throw new Error('Invalid catalogue')
    }
    if (
      'error' in payload ||
      ('success' in payload && payload.success === false)
    ) {
      throw new Error('Catalogue unavailable')
    }
    const data = (payload as { data?: unknown }).data
    if (!Array.isArray(data) || data.length > MAX_MODELS) {
      throw new Error('Invalid catalogue')
    }
    const models: string[] = []
    for (const item of data) {
      if (!item || typeof item !== 'object' || Array.isArray(item)) {
        throw new Error('Invalid model')
      }
      const id: unknown = item.id
      if (
        typeof id !== 'string' ||
        !id ||
        id.length > MAX_MODEL_ID_LENGTH ||
        id.trim() !== id ||
        id.includes(tokenKey) ||
        id.includes(key)
      ) {
        throw new Error('Invalid model')
      }
      models.push(id)
    }
    return [...new Set(models)]
  } catch {
    // Fetch/JSON errors and provider bodies can contain credentials. Do not retain their cause or response.
    throw new Error('Failed to fetch models')
  }
}
