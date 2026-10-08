/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export function commerceImportTrustedOrigins(
  input: string
): string[] | undefined {
  const origins = input
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
  if (
    input.length > 32768 ||
    origins.length > 50 ||
    new Set(origins).size !== origins.length
  ) {
    return undefined
  }
  for (const origin of origins) {
    if (
      !/^https:\/\/[^/?#\\]+$/.test(origin) ||
      Array.from(origin).some((character) => character.charCodeAt(0) > 127)
    ) {
      return undefined
    }
    try {
      const url = new URL(origin)
      const host = url.hostname.toLowerCase()
      const labels = host.split('.')
      if (
        url.protocol !== 'https:' ||
        url.username ||
        url.password ||
        host.length > 253 ||
        !host.includes('.') ||
        host.endsWith('.') ||
        /^[\d.]+$/.test(host) ||
        host.includes(':')
      ) {
        return undefined
      }
      if (
        [
          '.localhost',
          '.local',
          '.internal',
          '.lan',
          '.home',
          '.onion',
          '.invalid',
        ].some((suffix) => host.endsWith(suffix))
      ) {
        return undefined
      }
      if (
        labels.some(
          (label) => !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label)
        )
      ) {
        return undefined
      }
      const authority = origin.slice('https://'.length)
      const port = authority.includes(':')
        ? authority.slice(authority.lastIndexOf(':') + 1)
        : undefined
      if (
        port !== undefined &&
        (!/^[1-9]\d*$/.test(port) || Number(port) > 65535)
      ) {
        return undefined
      }
    } catch {
      return undefined
    }
  }
  return origins
}
