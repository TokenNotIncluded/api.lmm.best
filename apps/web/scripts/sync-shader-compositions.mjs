/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { createHash } from 'node:crypto'
import { readFile, realpath, writeFile } from 'node:fs/promises'
import { dirname, isAbsolute, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

import { parse } from '@babel/parser'

export const FORGE_SHADER_ID = '4740184'
export const DERIVED_FILE =
  'src/components/shaders/forge-ambient.generated.json'
const GENERATED_DIRECTORY = 'src/components/shaders/generated/'
const numberRanges = {
  MeshGradient: {
    count: [2, 8, true],
    drift: [0, 1],
    seed: [0, 100, true],
    smoothness: [0, 5],
    speed: [0, 10],
    swirl: [-1, 1],
    variation: [0, 1],
  },
  Grid: {
    cells: [1, 50, true],
    opacity: [0, 1],
    softness: [0, 1],
    thickness: [0, 20],
    rotation: [0, 360],
  },
}
const colorSpaces = new Set(['linear', 'oklch', 'oklab', 'hsl', 'hsv', 'lch'])

function fail(message) {
  throw new Error(`Shader sync: ${message}`)
}

function literal(node) {
  if (!node) fail('missing literal value')
  if (node.type === 'NumericLiteral' || node.type === 'StringLiteral') {
    return node.value
  }
  if (node.type === 'NullLiteral') return null
  if (
    node.type === 'UnaryExpression' &&
    (node.operator === '-' || node.operator === '+') &&
    node.argument.type === 'NumericLiteral'
  ) {
    return node.operator === '-' ? -node.argument.value : node.argument.value
  }
  if (node.type === 'ArrayExpression') return node.elements.map(literal)
  if (node.type === 'ObjectExpression') {
    const entries = []
    const keys = new Set()
    for (const property of node.properties) {
      if (
        property.type !== 'ObjectProperty' ||
        property.computed ||
        property.shorthand ||
        !['Identifier', 'StringLiteral'].includes(property.key.type)
      ) {
        fail('only explicit literal object properties are supported')
      }
      const key = property.key.name ?? property.key.value
      if (
        keys.has(key) ||
        ['__proto__', 'constructor', 'prototype'].includes(key)
      ) {
        fail('duplicate or unsafe object property')
      }
      keys.add(key)
      entries.push([key, literal(property.value)])
    }
    return Object.fromEntries(entries)
  }
  fail(`dynamic value (${node.type}) is unsupported`)
}

function number(value, range, label) {
  const [min, max, integer = false] = range
  if (
    typeof value !== 'number' ||
    !Number.isFinite(value) ||
    value < min ||
    value > max ||
    (integer && !Number.isInteger(value))
  ) {
    fail(
      `${label} must be ${integer ? 'an integer' : 'a number'} in ${min}..${max}`
    )
  }
}

function color(value, label) {
  if (
    typeof value === 'string' &&
    (/^#(?:[\da-f]{3}|[\da-f]{4}|[\da-f]{6}|[\da-f]{8})$/i.test(value) ||
      /^[a-z]+$/i.test(value) ||
      /^(?:rgb|rgba|hsl|hsla)\([\d.,%\s+/-]+\)$/i.test(value))
  ) {
    return
  }
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    const keys = Object.keys(value).sort().join(',')
    if (keys === 'b,g,r' || keys === 'a,b,g,r') {
      for (const channel of ['r', 'g', 'b']) {
        number(value[channel], [0, 1], label)
      }
      if ('a' in value) number(value.a, [0, 1], label)
      return
    }
  }
  fail(`${label} must be a literal color`)
}

function validateProp(component, key, value) {
  const range = Object.hasOwn(numberRanges[component], key)
    ? numberRanges[component][key]
    : undefined
  if (range) return number(value, range, `${component}.${key}`)
  if (key === 'colorSpace' && component === 'MeshGradient') {
    if (!colorSpaces.has(value)) fail('MeshGradient.colorSpace is unsupported')
    return
  }
  if (
    (component === 'MeshGradient' && ['colorA', 'colorB'].includes(key)) ||
    (component === 'Grid' && ['color', 'cellColor'].includes(key))
  ) {
    return color(value, `${component}.${key}`)
  }
  if (component === 'MeshGradient' && key === 'stops') {
    if (value === null) return
    if (!Array.isArray(value) || value.length < 2 || value.length > 16) {
      fail('MeshGradient.stops must contain 2..16 literal stops, or null')
    }
    let previous = -1
    for (const stop of value) {
      if (!stop || Object.keys(stop).sort().join(',') !== 'color,position') {
        fail('each gradient stop must contain only color and position')
      }
      color(stop.color, 'gradient stop color')
      number(stop.position, [0, 1], 'gradient stop position')
      if (stop.position < previous) fail('gradient stops must be ordered')
      previous = stop.position
    }
    return
  }
  fail(`unsupported property ${component}.${key}`)
}

function jsxName(node) {
  if (node?.type !== 'JSXIdentifier') {
    fail('only named shader elements are supported')
  }
  return node.name
}

function children(node) {
  return node.children.filter((child) => {
    if (child.type === 'JSXText' && child.value.trim() === '') return false
    return true
  })
}

function props(node, component) {
  if (
    jsxName(node.openingElement.name) !== component ||
    children(node).length
  ) {
    fail(`expected an empty ${component} element`)
  }
  const result = {}
  for (const attribute of node.openingElement.attributes) {
    if (
      attribute.type !== 'JSXAttribute' ||
      attribute.name.type !== 'JSXIdentifier'
    ) {
      fail('spread or namespaced attributes are unsupported')
    }
    const key = attribute.name.name
    if (Object.hasOwn(result, key)) {
      fail(`duplicate property ${component}.${key}`)
    }
    const value = literal(
      attribute.value?.type === 'JSXExpressionContainer'
        ? attribute.value.expression
        : attribute.value
    )
    validateProp(component, key, value)
    Object.defineProperty(result, key, { value, enumerable: true })
  }
  return result
}

/** Read the limited, pure CLI export without evaluating or importing its code. */
export function extractShaderComposition(source) {
  if (typeof source !== 'string' || Buffer.byteLength(source) > 256 * 1024) {
    fail('source must be a TSX string smaller than 256 KiB')
  }
  const ast = parse(source, {
    sourceType: 'module',
    plugins: ['jsx', 'typescript'],
  })
  const body = ast.program.body
  const imported = body[0]
  const exported = body[1]
  if (
    body.length !== 2 ||
    ast.program.directives.length ||
    imported?.type !== 'ImportDeclaration' ||
    imported.source.value !== 'shaders/react' ||
    imported.importKind === 'type' ||
    imported.specifiers.length !== 3 ||
    imported.specifiers.some(
      (specifier) =>
        specifier.type !== 'ImportSpecifier' ||
        specifier.importKind === 'type' ||
        specifier.imported.name !== specifier.local.name
    ) ||
    imported.specifiers
      .map((specifier) => specifier.local.name)
      .sort()
      .join(',') !== 'Grid,MeshGradient,Shader' ||
    exported?.type !== 'ExportDefaultDeclaration'
  ) {
    fail('expected only the three official imports and one default component')
  }
  const component = exported.declaration
  if (
    component.type !== 'FunctionDeclaration' ||
    component.async ||
    component.generator ||
    component.params.length ||
    component.body.directives.length ||
    component.body.body.length !== 1 ||
    component.body.body[0].type !== 'ReturnStatement'
  ) {
    fail('default component must contain only a direct literal return')
  }
  const root = component.body.body[0].argument
  if (
    root?.type !== 'JSXElement' ||
    jsxName(root.openingElement.name) !== 'Shader' ||
    root.openingElement.attributes.length
  ) {
    fail('expected a Shader root without attributes')
  }
  const layers = children(root)
  if (
    layers.length !== 2 ||
    layers.some((node) => node.type !== 'JSXElement')
  ) {
    fail('expected exactly a MeshGradient followed by a Grid')
  }
  return {
    meshGradient: props(layers[0], 'MeshGradient'),
    grid: props(layers[1], 'Grid'),
  }
}

export function buildShaderArtifact(source, { shaderId, file, officialHash }) {
  const sha256 = createHash('sha256').update(source).digest('hex')
  if (officialHash !== `sha256:${sha256}`) {
    fail(
      'installed source differs from the official CLI lock hash; reinstall or update it'
    )
  }
  return {
    schemaVersion: 1,
    shaderId,
    source: { file, sha256, officialHash },
    ...extractShaderComposition(source),
  }
}

export async function syncShaderCompositions({
  webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..'),
  check = false,
} = {}) {
  const lock = JSON.parse(
    await readFile(resolve(webRoot, 'shaders.lock.json'), 'utf8')
  )
  const entry = lock.shaders?.[FORGE_SHADER_ID]
  if (
    lock.version !== 1 ||
    !entry ||
    entry.framework !== 'react' ||
    entry.pending ||
    typeof entry.file !== 'string' ||
    !entry.file.startsWith(GENERATED_DIRECTORY) ||
    !entry.file.endsWith('.tsx') ||
    isAbsolute(entry.file) ||
    entry.file.includes('\\') ||
    entry.file.split('/').includes('..')
  ) {
    fail('the Forge shader must be installed by the official React CLI first')
  }
  const sourcePath = await realpath(resolve(webRoot, entry.file))
  const rootPath = await realpath(webRoot)
  const sourceRelative = relative(rootPath, sourcePath)
  if (
    sourceRelative === '..' ||
    sourceRelative.startsWith(`..${sep}`) ||
    isAbsolute(sourceRelative)
  ) {
    fail('installed source must remain inside the web project')
  }
  const source = await readFile(sourcePath, 'utf8')
  const artifact = buildShaderArtifact(source, {
    shaderId: FORGE_SHADER_ID,
    file: entry.file,
    officialHash: entry.hash,
  })
  const outputPath = resolve(webRoot, DERIVED_FILE)
  const output = `${JSON.stringify(artifact, null, 2)}\n`
  let current = null
  try {
    current = await readFile(outputPath, 'utf8')
  } catch (error) {
    if (error.code !== 'ENOENT') throw error
  }
  const changed = current !== output
  if (check && changed) {
    fail('derived shader composition is stale; run bun run shaders:sync')
  }
  if (!check && changed) await writeFile(outputPath, output)
  return { changed, artifact, outputPath }
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const args = process.argv.slice(2)
  if (args.some((arg) => arg !== '--check') || args.length > 1) {
    console.error('Usage: node scripts/sync-shader-compositions.mjs [--check]')
    process.exitCode = 1
  } else {
    syncShaderCompositions({ check: args.includes('--check') })
      .then(({ changed }) =>
        console.log(`Shader sync: ${changed ? 'updated' : 'current'}`)
      )
      .catch((error) => {
        console.error(error.message)
        process.exitCode = 1
      })
  }
}
