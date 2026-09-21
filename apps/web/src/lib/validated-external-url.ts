/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
type TrustedUrlMatch = readonly string[] | 'any'

type TrustedPathPolicy =
  | { exact: readonly string[] }
  | { prefixes: readonly string[] }
  | 'any'

export type TrustedUrlPolicy = {
  protocols: readonly string[]
  origins: TrustedUrlMatch
  hosts: TrustedUrlMatch
  paths: TrustedPathPolicy
  allowHash?: boolean
}

const localObjectUrls = new Set<string>()
const TEMPLATE_TOKEN_SEGMENT_PATTERN =
  /^(?:%7B(?:key|address|cherryConfig|aionuiConfig|deepchatConfig)%7D)$/i

function matches(value: string, allowed: TrustedUrlMatch): boolean {
  return allowed === 'any' || allowed.includes(value)
}

function matchesPath(pathname: string, policy: TrustedPathPolicy): boolean {
  if (!pathname.startsWith('/')) return false
  if (policy === 'any') return true
  if ('exact' in policy) return policy.exact.includes(pathname)

  return policy.prefixes.some((prefix) => {
    if (!prefix.startsWith('/')) return false
    return prefix.endsWith('/')
      ? pathname.startsWith(prefix)
      : pathname === prefix || pathname.startsWith(`${prefix}/`)
  })
}

function parseTrustedUrl(
  value: string,
  policy: TrustedUrlPolicy,
  base?: string
): URL | null {
  if (!value || value.trim() !== value) return null

  try {
    const url = base ? new URL(value, base) : new URL(value)
    if (
      !policy.protocols.includes(url.protocol) ||
      !url.hostname ||
      url.username ||
      url.password ||
      (!policy.allowHash && url.hash) ||
      !matches(url.origin, policy.origins) ||
      !matches(url.host, policy.hosts) ||
      !matchesPath(url.pathname, policy.paths)
    ) {
      return null
    }
    return url
  } catch {
    return null
  }
}

export function validatedExternalUrl(
  value: string,
  policy: TrustedUrlPolicy,
  base?: string
): string | null {
  return parseTrustedUrl(value, policy, base)?.toString() ?? null
}

export function getTrustedUrlFromSource(
  value: string,
  source: string,
  protocols: readonly string[],
  options: { allowHash?: boolean } = {}
): string | null {
  const openPolicy: TrustedUrlPolicy = {
    protocols,
    origins: 'any',
    hosts: 'any',
    paths: 'any',
    allowHash: options.allowHash,
  }
  const target = parseTrustedUrl(value, openPolicy)
  const trustedSource = parseTrustedUrl(source, openPolicy)

  if (
    !target ||
    !trustedSource ||
    target.protocol !== trustedSource.protocol ||
    target.origin !== trustedSource.origin ||
    target.host !== trustedSource.host ||
    target.pathname !== trustedSource.pathname
  ) {
    return null
  }

  return target.toString()
}

function matchesTemplatedPath(pathname: string, templatePathname: string) {
  const pathSegments = pathname.split('/')
  const templateSegments = templatePathname.split('/')
  if (pathSegments.length !== templateSegments.length) return false

  return templateSegments.every((segment, index) => {
    const pathSegment = pathSegments[index]
    if (TEMPLATE_TOKEN_SEGMENT_PATTERN.test(segment)) {
      return typeof pathSegment === 'string' && pathSegment.length > 0
    }
    return pathSegment === segment
  })
}

export function getTrustedTemplatedUrl(
  value: string,
  template: string,
  protocols: readonly string[]
): string | null {
  const openPolicy: TrustedUrlPolicy = {
    protocols,
    origins: 'any',
    hosts: 'any',
    paths: 'any',
    allowHash: true,
  }
  const target = parseTrustedUrl(value, openPolicy)
  const trustedTemplate = parseTrustedUrl(template, openPolicy)

  if (
    !target ||
    !trustedTemplate ||
    target.protocol !== trustedTemplate.protocol ||
    target.origin !== trustedTemplate.origin ||
    target.host !== trustedTemplate.host ||
    !matchesTemplatedPath(target.pathname, trustedTemplate.pathname)
  ) {
    return null
  }

  return target.toString()
}

export function createTrustedObjectUrl(blob: Blob): string {
  const url = URL.createObjectURL(blob)
  localObjectUrls.add(url)
  return url
}

export function getTrustedLocalObjectUrl(
  value: string,
  expectedOrigin: string
): string | null {
  if (!localObjectUrls.has(value)) return null

  try {
    const url = new URL(value)
    return url.protocol === 'blob:' && url.origin === expectedOrigin
      ? url.toString()
      : null
  } catch {
    return null
  }
}

export function revokeTrustedObjectUrl(value: string): void {
  if (!localObjectUrls.delete(value)) return
  URL.revokeObjectURL(value)
}
