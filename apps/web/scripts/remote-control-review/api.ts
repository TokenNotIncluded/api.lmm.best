// Only used by the loopback browser test build. No production credentials.
export const api = {
  async post<T>(path: string, body: unknown, options: { signal?: AbortSignal; skipBusinessError?: boolean }) {
    const response = await fetch(path, { method: 'POST', signal: options.signal, headers: { authorization: 'Bearer lmm_at_fixture', 'content-type': 'application/json' }, body: JSON.stringify(body) })
    if (!response.ok) throw new Error('Fixture request failed')
    return { data: await response.json() as T }
  },
}
