/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import DOMPurify from 'dompurify'

import {
  isStoreAnimationTag,
  safeStoreAnimation,
  storeAnimationAttrs,
  storeAnimationTags,
} from './product-media-animation'
import { safeStoreUrl } from './utils'

export const STORE_SVG_MAX_BYTES = 131072
export const STORE_SVG_DATA_PREFIX = 'data:image/svg+xml;base64,'
const SVG_NAMESPACE = 'http://www.w3.org/2000/svg'
const tags = new Set([
  ...'svg g defs path rect circle ellipse line polyline polygon text tspan title desc linearGradient radialGradient stop clipPath'.split(
    ' '
  ),
  ...storeAnimationTags,
])
const attrs = new Set(
  'id viewBox width height x y x1 y1 x2 y2 cx cy r rx ry d points transform fill fill-opacity fill-rule stroke stroke-width stroke-opacity stroke-linecap stroke-linejoin stroke-miterlimit stroke-dasharray stroke-dashoffset opacity clip-path clip-rule clipPathUnits gradientUnits gradientTransform spreadMethod offset stop-color stop-opacity color font-size font-family font-weight font-style text-anchor dominant-baseline letter-spacing preserveAspectRatio aria-label aria-labelledby role'.split(
    ' '
  )
)
const svgNumber = /^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/

function safeValue(value: string) {
  return (
    !/\\|:\/\/|javascript:|data:|vbscript:|@import|\/\*/i.test(value) &&
    (!/url\s*\(/i.test(value) ||
      /^url\(#[A-Za-z_][A-Za-z0-9_.:-]*\)$/.test(value))
  )
}

function dimension(value: string) {
  const source = value.trim().replace(/px$/, '')
  const percent = source.endsWith('%')
  const numeric = percent ? source.slice(0, -1) : source
  if (!svgNumber.test(numeric)) return false
  const number = Number(numeric)
  return (
    Number.isFinite(number) && number > 0 && number <= (percent ? 100 : 4096)
  )
}

function viewBox(value: string) {
  const parts = value.trim().split(/[\s,]+/)
  return (
    parts.length === 4 &&
    parts.every((part, index) => {
      if (!svgNumber.test(part)) return false
      const number = Number(part)
      return (
        Number.isFinite(number) &&
        Math.abs(number) <= 4096 &&
        (index < 2 || number > 0)
      )
    })
  )
}

function encode(source: string) {
  const bytes = new TextEncoder().encode(source)
  if (bytes.length > STORE_SVG_MAX_BYTES) return undefined
  // The bounded chunks also avoid overflowing the browser's argument stack.
  let binary = ''
  for (let index = 0; index < bytes.length; index += 4096) {
    binary += String.fromCharCode(...bytes.subarray(index, index + 4096))
  }
  return STORE_SVG_DATA_PREFIX + btoa(binary)
}

function decode(value: string) {
  if (!value.startsWith(STORE_SVG_DATA_PREFIX)) return undefined
  const encoded = value.slice(STORE_SVG_DATA_PREFIX.length)
  if (
    encoded.length > Math.ceil(STORE_SVG_MAX_BYTES / 3) * 4 ||
    !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(
      encoded
    )
  ) {
    return undefined
  }
  try {
    const binary = atob(encoded)
    if (binary.length > STORE_SVG_MAX_BYTES || btoa(binary) !== encoded) {
      return undefined
    }
    return new TextDecoder('utf-8', { fatal: true }).decode(
      Uint8Array.from(binary, (character) => character.charCodeAt(0))
    )
  } catch {
    return undefined
  }
}

function visualSVG(source: string): string | undefined {
  if (
    new TextEncoder().encode(source).length > STORE_SVG_MAX_BYTES ||
    /<!DOCTYPE|<!ENTITY|<\?/i.test(source) ||
    typeof DOMParser === 'undefined' ||
    !DOMPurify.isSupported
  ) {
    return undefined
  }
  const document = new DOMParser().parseFromString(source, 'image/svg+xml')
  const root = document.documentElement
  if (document.querySelector('parsererror') || root?.localName !== 'svg') {
    return undefined
  }
  let nodes = 0
  function valid(element: Element, depth: number, parent = ''): boolean {
    if (
      depth > 32 ||
      ++nodes > 4096 ||
      !tags.has(element.localName) ||
      (element.namespaceURI !== null &&
        element.namespaceURI !== SVG_NAMESPACE) ||
      element.attributes.length > 64 ||
      (depth > 1 && element.localName === 'svg')
    ) {
      return false
    }
    const animation = isStoreAnimationTag(element.localName)
    if (isStoreAnimationTag(parent)) return false
    for (const attribute of element.attributes) {
      if (
        depth === 1 &&
        attribute.name === 'xmlns' &&
        attribute.value === SVG_NAMESPACE
      ) {
        continue
      }
      if (
        attribute.namespaceURI !== null ||
        (!animation && !attrs.has(attribute.name)) ||
        !safeValue(attribute.value) ||
        (depth === 1 &&
          (((attribute.name === 'width' || attribute.name === 'height') &&
            !dimension(attribute.value)) ||
            (attribute.name === 'viewBox' && !viewBox(attribute.value))))
      ) {
        return false
      }
    }
    if (
      animation &&
      (!safeStoreAnimation(element, parent) || element.textContent?.trim())
    ) {
      return false
    }
    return Array.from(element.children).every((child) =>
      valid(child, depth + 1, element.localName)
    )
  }
  if (!valid(root, 1)) return undefined
  root.setAttribute('xmlns', SVG_NAMESPACE)
  const clean = DOMPurify.sanitize(
    new XMLSerializer().serializeToString(root),
    {
      ALLOWED_TAGS: [...tags],
      ALLOWED_ATTR: ['xmlns', ...attrs, ...storeAnimationAttrs],
      ALLOW_DATA_ATTR: false,
      ALLOW_ARIA_ATTR: false,
      RETURN_TRUSTED_TYPE: false,
    }
  )
  const cleanDocument = new DOMParser().parseFromString(clean, 'image/svg+xml')
  return cleanDocument.documentElement?.localName === 'svg' &&
    cleanDocument.documentElement.namespaceURI === SVG_NAMESPACE &&
    !cleanDocument.querySelector('parsererror')
    ? encode(
        new XMLSerializer().serializeToString(cleanDocument.documentElement)
      )
    : undefined
}

// A small bounded cache avoids parsing the same card/gallery SVG on rerenders.
const validated = new Map<string, string | undefined>()

/** Only validated image URLs are suitable for img.src; raw SVG is never HTML. */
export function safeStoreMediaUrl(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined
  if (value.length <= 4096) {
    const url = safeStoreUrl(value)
    if (url) return url
  }
  if (!value.startsWith(STORE_SVG_DATA_PREFIX)) return undefined
  if (validated.has(value)) return validated.get(value)
  const source = decode(value)
  const result = source === undefined ? undefined : visualSVG(source)
  if (validated.size >= 16) {
    const oldest = validated.keys().next()
    if (!oldest.done) validated.delete(oldest.value)
  }
  validated.set(value, result)
  return result
}

/** Product input accepts validated SVG and stores a standalone image URI. */
export function normalizeStoreImageSource(raw: string): string | undefined {
  const source = raw.trim()
  if (source.startsWith('<')) return visualSVG(source)
  return safeStoreMediaUrl(source)
}

export function storeImageEditorText(value: string): string {
  return safeStoreMediaUrl(value) ? (decode(value) ?? value) : value
}
