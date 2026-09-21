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
const label = (value: string | null) =>
  value &&
  /^[\p{L}\p{N} _.-]{1,80}$/u.test(value) &&
  !/sk-|bearer/i.test(value) &&
  !/^[A-Za-z0-9]{41,}$/.test(value) &&
  !/^[\w-]+\.[\w-]+\.[\w-]+$/.test(value)
    ? value
    : ''
export function sourceEntry(href: string, referrer: string) {
  try {
    return buildSourceEntry(href, referrer)
  } catch {
    return null
  }
}
function buildSourceEntry(href: string, referrer: string) {
  const nonce = globalThis.crypto?.randomUUID?.()
  if (!nonce) return null
  const url = new URL(href)
  const callback =
    /^\/(oauth|api|callback|payment)(\/|$)/.test(url.pathname) ||
    /callback|\/return/.test(url.pathname)
  const landing = [
    '/',
    '/guide',
    '/pricing',
    '/challenges',
    '/sign-up',
    '/sign-in',
  ].includes(url.pathname)
    ? url.pathname
    : null
  const returnFlow = [
    'code',
    'state',
    'session_id',
    'payment_intent',
    'trade_no',
    'redirect_status',
  ].some((key) => url.searchParams.has(key))
  if (
    callback ||
    returnFlow ||
    !landing ||
    url.searchParams.get('source_test') === '1'
  ) {
    return null
  }
  let host = ''
  try {
    const ref = new URL(referrer)
    if (
      /^https?:$/.test(ref.protocol) &&
      !ref.username &&
      !ref.password &&
      !/^(?:[0-9]{1,3}\.){3}[0-9]{1,3}$/.test(ref.hostname) &&
      !ref.hostname.includes(':') &&
      ref.hostname.includes('.') &&
      !ref.hostname.endsWith('.local')
    ) {
      host = ref.origin
    }
  } catch {
    /* Missing referrer is unknown. */
  }
  return {
    landing,
    referrer: host,
    source: label(url.searchParams.get('utm_source')),
    medium: label(url.searchParams.get('utm_medium')),
    campaign: label(url.searchParams.get('utm_campaign')),
    content: label(url.searchParams.get('utm_content')),
    link_id: /^[a-f0-9]{32}$/.test(url.searchParams.get('lmm_source') ?? '')
      ? url.searchParams.get('lmm_source')
      : '',
    nonce: nonce.replaceAll('-', ''),
    consent: true,
    consent_version: 2,
  }
}
