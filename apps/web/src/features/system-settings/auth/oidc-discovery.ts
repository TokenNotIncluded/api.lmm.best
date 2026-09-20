/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
type OIDCSettings = {
  enabled: boolean
  well_known: string
  authorization_endpoint: string
  token_endpoint: string
  user_info_endpoint: string
}

export class OIDCDiscoveryError extends Error {
  readonly kind: 'url' | 'fetch'

  constructor(kind: 'url' | 'fetch') {
    super(`OIDC discovery ${kind} error`)
    this.kind = kind
  }
}

export async function discoverOIDCSettings<T extends OIDCSettings>(
  values: T,
  saved: Pick<OIDCSettings, 'enabled' | 'well_known'>,
  fetchDocument: (url: string) => Promise<unknown>
): Promise<{ oidc: T; discovered: boolean }> {
  const wellKnown = values.well_known.trim()
  const urlChanged = wellKnown !== saved.well_known.trim()
  const enabling = values.enabled && !saved.enabled
  const disabling = !values.enabled && saved.enabled

  // Unrelated saves and disabling must not depend on a discovery server.
  // Keep unchanged values intact, including legacy whitespace-only differences.
  if (!wellKnown || disabling || (!urlChanged && !enabling)) {
    return { oidc: values, discovered: false }
  }

  try {
    const url = new URL(wellKnown)
    if (url.protocol !== 'http:' && url.protocol !== 'https:') {
      throw new Error('unsupported protocol')
    }
  } catch {
    throw new OIDCDiscoveryError('url')
  }

  try {
    const document = await fetchDocument(wellKnown)
    if (!document || typeof document !== 'object' || Array.isArray(document)) {
      throw new Error('invalid discovery document')
    }
    const data = document as Record<string, unknown>
    const endpoint = (key: string) => {
      const value = data[key] ?? ''
      if (typeof value !== 'string') throw new Error('invalid endpoint')
      return value
    }
    return {
      oidc: {
        ...values,
        well_known: wellKnown,
        authorization_endpoint: endpoint('authorization_endpoint'),
        token_endpoint: endpoint('token_endpoint'),
        user_info_endpoint: endpoint('userinfo_endpoint'),
      },
      discovered: true,
    }
  } catch {
    // Do not expose Axios request data or provider response bodies to the UI.
    throw new OIDCDiscoveryError('fetch')
  }
}
