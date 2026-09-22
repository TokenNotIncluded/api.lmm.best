/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { Window } from 'happy-dom'

import { L0_MAX_FLIGHTS } from './l0-flight-path'
import { mountL0TextFlow, visualTokens } from './l0-text-flow'

function fixture() {
  const dom = new Window({ url: 'https://l0.example.test/' })
  const doc = dom.document
  const media = dom.matchMedia('(prefers-reduced-motion: reduce)')
  Object.defineProperty(dom, 'matchMedia', { value: () => media })
  const animations: Array<{ node: HTMLElement; animation: Animation }> = []
  Object.defineProperty(dom.HTMLElement.prototype, 'animate', {
    configurable: true,
    value(this: HTMLElement) {
      const animation = {
        onfinish: null,
        oncancel: null,
        cancel() {},
      } as unknown as Animation
      animations.push({ node: this, animation })
      return animation
    },
  })
  const cloud = doc.createElement('div')
  const toggle = doc.createElement('button')
  toggle.setAttribute('data-cloud-pause', '')
  cloud.append(toggle)
  doc.body.append(cloud)
  Object.defineProperty(cloud, 'getBoundingClientRect', {
    value: () => new dom.DOMRect(50, 10, 600, 260),
  })
  const flow = mountL0TextFlow(cloud as unknown as HTMLElement)
  const text = (value: string, wrap = 1000) => {
    const box = doc.createElement('div')
    for (const [index, token] of visualTokens(value).entries()) {
      const node = doc.createElement('span')
      node.textContent = token.text
      node.setAttribute('data-l0-arrival', '')
      node.setAttribute('data-l0-source', '')
      const rect = new dom.DOMRect(
        80 + (index % wrap) * 16,
        420 + Math.floor(index / wrap) * 26,
        16,
        20
      )
      Object.defineProperty(node, 'getBoundingClientRect', {
        value: () => rect,
      })
      Object.defineProperty(node, 'getClientRects', { value: () => [rect] })
      box.append(node)
    }
    doc.body.append(box)
    return box as unknown as HTMLElement
  }
  return {
    dom,
    doc,
    flow,
    text,
    animations,
    media,
    close() {
      flow.dispose()
      dom.close()
    },
  }
}

test('adjacent graphemes form small packets; wraps and emoji stay intact', () => {
  const f = fixture()
  try {
    const source = f.text('你好👩‍💻云间飞行', 4)
    f.flow.sentence(source)
    assert.deepEqual(
      f.animations.map(({ node }) => node.textContent),
      ['你好👩‍💻', '云', '间飞行']
    )
    f.flow.clear()
    for (const node of source.children) {
      assert.equal((node as HTMLElement).style.opacity, '')
    }
    assert.equal(f.doc.querySelectorAll('[data-l0-flight]').length, 0)
  } finally {
    f.close()
  }
})

test('large stream bursts preserve all text while capping visual flights', () => {
  const f = fixture()
  try {
    const value = '云'.repeat(210)
    const answer = f.text(value, 21)
    f.flow.receive(answer)
    assert.equal(f.animations.length, L0_MAX_FLIGHTS)
    assert.equal(answer.textContent, value)
    f.flow.clear()
    assert.equal(
      [...answer.children].filter(
        (node) => (node as HTMLElement).style.opacity === '0'
      ).length,
      0
    )
  } finally {
    f.close()
  }
})

test('hidden arrivals are recorded without flying or replaying on return', () => {
  const f = fixture()
  try {
    const answer = f.text('只在真实增量时飞行')
    f.flow.receive(answer, false)
    f.flow.receive(answer, true)
    assert.equal(f.animations.length, 0)
    answer.children[0].textContent = '新'
    f.flow.receive(answer)
    assert.equal(f.animations.length, 1)
    assert.equal(f.animations[0].node.textContent, '新')
  } finally {
    f.close()
  }
})

test('changing a partial emoji settles its old packet and displays the new grapheme', () => {
  const f = fixture()
  try {
    const answer = f.text('👩你好')
    f.flow.receive(answer)
    answer.children[0].textContent = '👩‍💻'
    f.flow.receive(answer)
    assert.equal(f.animations.at(-1)?.node.textContent, '👩‍💻')
    assert.equal((answer.children[1] as HTMLElement).style.opacity, '')
    f.flow.clearResponses()
    assert.equal(answer.textContent, '👩‍💻你好')
    assert.equal((answer.children[0] as HTMLElement).style.opacity, '')
  } finally {
    f.close()
  }
})

test('resize, reduced motion and disposal immediately reveal authoritative text', () => {
  const f = fixture()
  try {
    const answer = f.text('实时返回的完整答案')
    f.flow.receive(answer)
    f.dom.dispatchEvent(new f.dom.Event('resize'))
    assert.equal(f.doc.querySelectorAll('[data-l0-flight]').length, 0)
    f.flow.sentence(answer)
    Object.defineProperty(f.media, 'matches', {
      configurable: true,
      value: true,
    })
    f.media.dispatchEvent(new f.dom.Event('change'))
    assert.equal(f.doc.querySelectorAll('[data-l0-flight]').length, 0)
    f.flow.sentence(answer)
    assert.equal(f.doc.querySelectorAll('[data-l0-flight]').length, 0)
    f.flow.dispose()
    assert.equal(f.doc.querySelectorAll('.l0-flight-layer').length, 0)
    for (const node of answer.children) {
      assert.equal((node as HTMLElement).style.opacity, '')
    }
  } finally {
    f.close()
  }
})
