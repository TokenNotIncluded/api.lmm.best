/*
Copyright (C) 2026 LIghtJUNction
*/

export function resolveApiBaseUrl(
  configured: unknown,
  origin: string
): string | null {
  const address =
    typeof configured === 'string' && configured.trim()
      ? configured.trim()
      : origin
  try {
    const url = new URL(address)
    if (
      !['https:', 'http:'].includes(url.protocol) ||
      url.username ||
      url.password ||
      url.search ||
      url.hash
    )
      {return null}
    const root = url.pathname.replace(/\/+$/, '').replace(/(?:\/v1)+$/, '')
    url.pathname = `${root}/v1`
    return url.toString().replace(/\/+$/, '')
  } catch {
    return null
  }
}
