/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// Kept in step with model/merchant_store_media_animation.go. SMIL gets its own
// attribute contract so it cannot mutate URLs/styles or start on user events.
export const storeAnimationTags = ['animate', 'animateTransform'] as const
export const storeAnimationAttrs =
  'attributeName attributeType from to by values dur begin repeatCount fill calcMode keyTimes keySplines additive accumulate type'.split(
    ' '
  )
const parents = new Set(
  'svg g path rect circle ellipse line polyline polygon text tspan stop'.split(
    ' '
  )
)
const opacity = new Set(
  'opacity fill-opacity stroke-opacity stop-opacity'.split(' ')
)
const colors = new Set('fill stroke color stop-color'.split(' '))
const numeric = /^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/
const clock = /^(?:\d+\.?\d*|\.\d+)(?:ms|s|min|h)?$/
const color =
  /^(?:#[a-fA-F0-9]{3,4}|#[a-fA-F0-9]{6}|#[a-fA-F0-9]{8}|[a-zA-Z]{1,32})$/

export function isStoreAnimationTag(name: string) {
  return name === 'animate' || name === 'animateTransform'
}

function number(value: string, min: number, max: number) {
  const result = Number(value)
  return (
    numeric.test(value) &&
    Number.isFinite(result) &&
    result >= min &&
    result <= max
  )
}

function time(value: string, positive: boolean) {
  if (!clock.test(value)) return false
  const result = Number(value.replace(/(?:min|ms|s|h)$/, ''))
  return Number.isFinite(result) && result >= 0 && (!positive || result > 0)
}

function transform(value: string, type: string) {
  const parts = value.replaceAll(',', ' ').trim().split(/\s+/)
  const length = parts.length
  if (
    !(
      ((type === 'translate' || type === 'scale') &&
        (length === 1 || length === 2)) ||
      (type === 'rotate' && (length === 1 || length === 3)) ||
      ((type === 'skewX' || type === 'skewY') && length === 1)
    )
  ) {
    return false
  }
  return parts.every((part) => number(part, -4096, 4096))
}

export function safeStoreAnimation(element: Element, parent: string) {
  if (!parents.has(parent)) return false
  const attrs = new Map<string, string>()
  for (const attribute of element.attributes) {
    if (
      !storeAnimationAttrs.includes(attribute.name) ||
      attribute.value.trim() !== attribute.value
    ) {
      return false
    }
    attrs.set(attribute.name, attribute.value)
  }
  const get = (name: string) => attrs.get(name) ?? ''
  if (
    !time(get('dur'), true) ||
    (get('attributeType') !== '' && get('attributeType') !== 'XML')
  ) {
    return false
  }
  if (attrs.has('begin') && !time(get('begin'), false)) return false
  if (
    attrs.has('repeatCount') &&
    get('repeatCount') !== 'indefinite' &&
    !number(get('repeatCount'), Number.MIN_VALUE, Number.MAX_VALUE)
  ) {
    return false
  }
  for (const [name, allowed] of Object.entries({
    fill: ['remove', 'freeze'],
    calcMode: ['linear', 'discrete', 'paced', 'spline'],
    additive: ['replace', 'sum'],
    accumulate: ['none', 'sum'],
  })) {
    if (attrs.has(name) && !allowed.includes(get(name))) return false
  }
  let validValue: (value: string) => boolean
  if (element.localName === 'animateTransform') {
    if (
      get('attributeName') !== 'transform' ||
      parent === 'stop' ||
      !['translate', 'scale', 'rotate', 'skewX', 'skewY'].includes(get('type'))
    ) {
      return false
    }
    validValue = (value) => transform(value, get('type'))
  } else {
    const name = get('attributeName')
    if (attrs.has('type') || (name.startsWith('stop-') && parent !== 'stop')) {
      return false
    }
    if (opacity.has(name)) validValue = (value) => number(value, 0, 1)
    else if (name === 'stroke-dashoffset') {
      validValue = (value) => number(value, -4096, 4096)
    } else if (colors.has(name)) validValue = (value) => color.test(value)
    else return false
  }
  let frames = 2
  if (attrs.has('values')) {
    if (['from', 'to', 'by'].some((field) => attrs.has(field))) return false
    const values = get('values').split(';')
    frames = values.length
    if (
      frames < 2 ||
      frames > 256 ||
      !values.every((value) => validValue(value.trim()))
    ) {
      return false
    }
  } else {
    if (attrs.has('to') === attrs.has('by')) return false
    if (
      ['from', 'to', 'by'].some(
        (field) => attrs.has(field) && !validValue(get(field))
      )
    ) {
      return false
    }
  }
  if (attrs.has('keyTimes')) {
    const times = get('keyTimes').split(';')
    if (times.length !== frames) return false
    let previous = 0
    for (const [index, value] of times.entries()) {
      const part = value.trim()
      if (!number(part, previous, 1)) return false
      const current = Number(part)
      if (
        (index === 0 && current !== 0) ||
        (index === frames - 1 && current !== 1)
      ) {
        return false
      }
      previous = current
    }
  }
  if (attrs.has('keySplines')) {
    const splines = get('keySplines').split(';')
    if (get('calcMode') !== 'spline' || splines.length !== frames - 1) {
      return false
    }
    if (
      !splines.every((spline) => {
        const coordinates = spline.replaceAll(',', ' ').trim().split(/\s+/)
        return (
          coordinates.length === 4 &&
          coordinates.every((coordinate) => number(coordinate, 0, 1))
        )
      })
    ) {
      return false
    }
  } else if (get('calcMode') === 'spline') return false
  return true
}
