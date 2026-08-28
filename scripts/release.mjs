#!/usr/bin/env node

import { execFileSync } from 'node:child_process'
import { readFileSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const REPOSITORY_URL = 'https://github.com/LIghtJUNction/api.lmm.best'
const WORKSPACE_PACKAGES = new Set([
  'lmm-api-rs',
  'lmm-application',
  'lmm-contracts',
  'lmm-db-migrate',
  'lmm-domain',
  'lmm-observability',
])

const PRODUCT_FILES = {
  version: 'VERSION',
  packageJson: 'package.json',
  cargoToml: 'apps/api-rust/Cargo.toml',
  cargoLock: 'apps/api-rust/Cargo.lock',
  changelog: 'CHANGELOG.md',
  localGoPkgbuild: 'packaging/local/lmm-api-go/PKGBUILD',
  goBinPkgbuild: 'packaging/aur/lmm-api-go-bin/PKGBUILD',
  goBinSrcinfo: 'packaging/aur/lmm-api-go-bin/.SRCINFO',
  rustBinPkgbuild: 'packaging/aur/lmm-api-rs-bin/PKGBUILD',
  rustBinSrcinfo: 'packaging/aur/lmm-api-rs-bin/.SRCINFO',
}
function fail(message) {
  throw new Error(message)
}

export function parseSemver(value) {
  const match = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.exec(value)
  if (!match) fail(`invalid stable semantic version: ${value}`)
  return {
    major: Number(match[1]),
    minor: Number(match[2]),
    patch: Number(match[3]),
    value,
  }
}

function compareSemver(left, right) {
  for (const key of ['major', 'minor', 'patch']) {
    if (left[key] !== right[key]) return left[key] - right[key]
  }
  return 0
}

function formatSemver(version) {
  return `${version.major}.${version.minor}.${version.patch}`
}

export function planVersion(currentValue, bump, explicitValue, tagValues = []) {
  const current = parseSemver(currentValue)
  const stableTags = tagValues
    .map((tag) => tag.replace(/^v/, ''))
    .filter((tag) => /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(tag))
    .map(parseSemver)

  const releaseLine = stableTags.filter(
    (tag) => tag.major === current.major && tag.minor === current.minor
  )
  const baseline = [current, ...releaseLine].sort(compareSemver).at(-1)

  const occupied = new Set(stableTags.map((tag) => tag.value))
  let target
  if (explicitValue) {
    target = parseSemver(explicitValue)
  } else {
    switch (bump) {
      case 'patch':
        target = { ...baseline, patch: baseline.patch + 1 }
        break
      case 'minor':
        target = { ...current, minor: current.minor + 1, patch: 0 }
        while (occupied.has(formatSemver(target))) target.minor += 1
        break
      case 'major':
        target = { ...current, major: current.major + 1, minor: 0, patch: 0 }
        while (occupied.has(formatSemver(target))) target.major += 1
        break
      default:
        fail(`bump must be patch, minor, or major: ${bump}`)
    }
  }

  if (compareSemver(target, baseline) <= 0) {
    fail(`target version ${formatSemver(target)} must be newer than ${formatSemver(baseline)}`)
  }

  const targetValue = formatSemver(target)
  if (occupied.has(targetValue)) {
    fail(`release tag already exists: v${targetValue}`)
  }

  return {
    baseline: formatSemver(baseline),
    target: targetValue,
  }
}

function visibleMarkdown(value) {
  let visible = value
  while (true) {
    const start = visible.indexOf('<!--')
    if (start === -1) break
    const end = visible.indexOf('-->', start + 4)
    visible = end === -1 ? visible.slice(0, start) : visible.slice(0, start) + visible.slice(end + 3)
  }
  return visible.trim()
}

const RELEASE_HEADER_PATTERN = /^## \[([0-9]+\.[0-9]+\.[0-9]+)\] - \d{4}-\d{2}-\d{2}\s*$/

function hasReleaseHeader(content, version) {
  return content.split('\n').some((line) => RELEASE_HEADER_PATTERN.exec(line)?.[1] === version)
}

function releaseSection(content, version) {
  let offset = 0
  for (const line of content.split('\n')) {
    if (RELEASE_HEADER_PATTERN.exec(line)?.[1] === version) {
      return sectionAt(content, offset, line.length)
    }
    offset += line.length + 1
  }
  fail(`missing release ${version} section in CHANGELOG.md`)
}

function sectionAt(content, headingStart, headingLength) {
  const contentStart = headingStart + headingLength
  const nextHeading = content.indexOf('\n## ', contentStart)
  const referenceOffset = content.slice(contentStart).search(/\n\[[^\]]+\]:\s+\S+/)
  const nextReference = referenceOffset === -1 ? -1 : contentStart + referenceOffset
  const boundaries = [nextHeading, nextReference].filter((offset) => offset !== -1)
  const contentEnd = boundaries.length ? Math.min(...boundaries) : content.length
  return {
    body: content.slice(contentStart, contentEnd).trim(),
    contentEnd,
    contentStart,
    headingStart,
  }
}

function section(content, headingPattern, description) {
  const match = headingPattern.exec(content)
  if (!match) fail(`missing ${description} section in CHANGELOG.md`)
  const contentStart = match.index + match[0].length
  const nextHeading = content.indexOf('\n## ', contentStart)
  const referenceOffset = content.slice(contentStart).search(/\n\[[^\]]+\]:\s+\S+/)
  const nextReference = referenceOffset === -1 ? -1 : contentStart + referenceOffset
  const boundaries = [nextHeading, nextReference].filter((offset) => offset !== -1)
  const contentEnd = boundaries.length ? Math.min(...boundaries) : content.length
  return {
    body: content.slice(contentStart, contentEnd).trim(),
    contentEnd,
    contentStart,
    headingStart: match.index,
  }
}

function splitReferenceDefinitions(content) {
  const lines = content.trimEnd().split('\n')
  const firstDefinition = lines.findIndex((line) => /^\[[^\]]+\]:\s+\S+/.test(line))
  if (firstDefinition === -1) return { body: lines.join('\n'), definitions: [] }
  return {
    body: lines.slice(0, firstDefinition).join('\n').trimEnd(),
    definitions: lines.slice(firstDefinition).filter((line) => line.trim()),
  }
}

export function finalizeChangelog(content, version, date, previousVersion) {
  parseSemver(version)
  parseSemver(previousVersion)
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date) || Number.isNaN(Date.parse(`${date}T00:00:00Z`))) {
    fail(`invalid release date: ${date}`)
  }
  if (hasReleaseHeader(content, version)) {
    fail(`CHANGELOG.md already contains release ${version}`)
  }

  const unreleased = section(content, /^## Unreleased\s*$/m, 'Unreleased')
  if (!visibleMarkdown(unreleased.body)) fail('CHANGELOG.md Unreleased section is empty')
  if (!/^[-*]\s+/m.test(visibleMarkdown(unreleased.body))) {
    fail('CHANGELOG.md Unreleased section must contain at least one list item')
  }

  const replacement = [
    '## Unreleased',
    '',
    '<!-- Add user-facing or operational changes here before the next release. -->',
    '',
    `## [${version}] - ${date}`,
    '',
    unreleased.body,
  ].join('\n')
  let updated =
    content.slice(0, unreleased.headingStart) + replacement + content.slice(unreleased.contentEnd)

  const { body, definitions } = splitReferenceDefinitions(updated)
  const retainedDefinitions = definitions.filter(
    (line) => !line.startsWith('[Unreleased]:') && !line.startsWith(`[${version}]:`)
  )
  const nextDefinitions = [
    `[Unreleased]: ${REPOSITORY_URL}/compare/v${version}...HEAD`,
    `[${version}]: ${REPOSITORY_URL}/compare/v${previousVersion}...v${version}`,
    ...retainedDefinitions,
  ]
  updated = `${body}\n\n${nextDefinitions.join('\n')}\n`
  return updated
}

export function extractReleaseNotes(content, version) {
  parseSemver(version)
  const release = releaseSection(content, version)
  if (!visibleMarkdown(release.body)) fail(`CHANGELOG.md release ${version} is empty`)
  return release.body
}

export function updateCargoToml(content, version) {
  const lines = content.split('\n')
  let inWorkspacePackage = false
  let replacements = 0
  const updated = lines.map((line) => {
    if (/^\[.+\]$/.test(line)) inWorkspacePackage = line === '[workspace.package]'
    if (inWorkspacePackage && /^version\s*=/.test(line)) {
      replacements += 1
      return `version = "${version}"`
    }
    return line
  })
  if (replacements !== 1) fail('expected one workspace.package version in Cargo.toml')
  return updated.join('\n')
}

export function updateCargoLock(content, version) {
  const chunks = content.split(/(?=\[\[package\]\]\n)/)
  const found = new Set()
  const updated = chunks.map((chunk) => {
    const name = /^\[\[package\]\]\nname = "([^"]+)"/m.exec(chunk)?.[1]
    if (!name || !WORKSPACE_PACKAGES.has(name)) return chunk
    found.add(name)
    const next = chunk.replace(/^version = "[^"]+"$/m, `version = "${version}"`)
    if (next === chunk) fail(`missing version for ${name} in Cargo.lock`)
    return next
  })
  const missing = [...WORKSPACE_PACKAGES].filter((name) => !found.has(name))
  if (missing.length) fail(`missing workspace packages in Cargo.lock: ${missing.join(', ')}`)
  return updated.join('')
}

function replaceOnce(content, pattern, replacement, description) {
  const matches = [...content.matchAll(pattern)]
  if (matches.length !== 1) fail(`expected one ${description}, found ${matches.length}`)
  return content.replace(pattern, replacement)
}

function updateJsonVersion(content, version) {
  return replaceOnce(content, /^  "version": "[^"]+",$/gm, `  "version": "${version}",`, 'JSON version')
}

function updatePkgbuildVersion(content, version) {
  return replaceOnce(content, /^pkgver=.*$/gm, `pkgver=${version}`, 'PKGBUILD pkgver')
}

function updateLocalPkgbuildVersion(content, version) {
  return replaceOnce(
    content,
    /^pkgver="\$\{LMM_API_PKGVER:-[^}]+\}"$/gm,
    `pkgver="\${LMM_API_PKGVER:-${version}}"`,
    'local PKGBUILD fallback version'
  )
}

export function updateSrcinfoVersion(content, version) {
  const current = /^\s*pkgver = (\S+)$/m.exec(content)?.[1]
  if (!current) fail('missing .SRCINFO pkgver')
  return content.replaceAll(current, version)
}

function read(root, file) {
  return readFileSync(resolve(root, file), 'utf8')
}

function write(root, file, content) {
  const normalized = content.endsWith('\n') ? content : `${content}\n`
  writeFileSync(resolve(root, file), normalized)
}

function readVersion(root) {
  const version = read(root, PRODUCT_FILES.version).trim()
  parseSemver(version)
  return version
}

function stableTags(root) {
  const output = execFileSync('git', ['tag', '--list'], {
    cwd: root,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  return output.split('\n').filter(Boolean)
}

export function prepareRelease({ root, bump, explicitVersion, date, tagValues }) {
  const current = readVersion(root)
  const versions = tagValues ?? stableTags(root)
  const plan = planVersion(current, bump, explicitVersion, versions)

  write(root, PRODUCT_FILES.version, plan.target)
  write(root, PRODUCT_FILES.packageJson, updateJsonVersion(read(root, PRODUCT_FILES.packageJson), plan.target))
  write(root, PRODUCT_FILES.cargoToml, updateCargoToml(read(root, PRODUCT_FILES.cargoToml), plan.target))
  write(root, PRODUCT_FILES.cargoLock, updateCargoLock(read(root, PRODUCT_FILES.cargoLock), plan.target))
  write(
    root,
    PRODUCT_FILES.changelog,
    finalizeChangelog(read(root, PRODUCT_FILES.changelog), plan.target, date, plan.baseline)
  )
  write(
    root,
    PRODUCT_FILES.localGoPkgbuild,
    updateLocalPkgbuildVersion(read(root, PRODUCT_FILES.localGoPkgbuild), plan.target)
  )

  for (const [pkgbuild, srcinfo] of [
    [PRODUCT_FILES.goBinPkgbuild, PRODUCT_FILES.goBinSrcinfo],
    [PRODUCT_FILES.rustBinPkgbuild, PRODUCT_FILES.rustBinSrcinfo],
  ]) {
    write(root, pkgbuild, updatePkgbuildVersion(read(root, pkgbuild), plan.target))
    write(root, srcinfo, updateSrcinfoVersion(read(root, srcinfo), plan.target))
  }

  verifyRelease({ root, version: plan.target })
  return plan
}

function jsonVersion(root, file) {
  const match = /^  "version": "([^"]+)",$/m.exec(read(root, file))
  if (!match) fail(`missing JSON version in ${file}`)
  return match[1]
}

function cargoTomlVersion(content) {
  let inWorkspacePackage = false
  for (const line of content.split('\n')) {
    if (/^\[.+\]$/.test(line)) inWorkspacePackage = line === '[workspace.package]'
    if (inWorkspacePackage) {
      const match = /^version\s*=\s*"([^"]+)"$/.exec(line)
      if (match) return match[1]
    }
  }
  fail('missing workspace.package version in Cargo.toml')
}

function cargoLockVersions(content) {
  const versions = new Map()
  for (const chunk of content.split(/(?=\[\[package\]\]\n)/)) {
    const name = /^\[\[package\]\]\nname = "([^"]+)"/m.exec(chunk)?.[1]
    if (!name || !WORKSPACE_PACKAGES.has(name)) continue
    const version = /^version = "([^"]+)"$/m.exec(chunk)?.[1]
    if (!version) fail(`missing version for ${name} in Cargo.lock`)
    versions.set(name, version)
  }
  return versions
}

function assertEqual(actual, expected, description) {
  if (actual !== expected) fail(`${description} is ${actual ?? '<missing>'}; expected ${expected}`)
}

export function verifyRelease({ root, version }) {
  parseSemver(version)
  assertEqual(readVersion(root), version, 'VERSION')
  assertEqual(jsonVersion(root, PRODUCT_FILES.packageJson), version, 'package.json version')
  assertEqual(cargoTomlVersion(read(root, PRODUCT_FILES.cargoToml)), version, 'Rust workspace version')

  const lockVersions = cargoLockVersions(read(root, PRODUCT_FILES.cargoLock))
  for (const packageName of WORKSPACE_PACKAGES) {
    assertEqual(lockVersions.get(packageName), version, `Cargo.lock ${packageName} version`)
  }

  for (const [name, pkgbuild, srcinfo] of [
    ['Go AUR binary', PRODUCT_FILES.goBinPkgbuild, PRODUCT_FILES.goBinSrcinfo],
    ['Rust AUR binary', PRODUCT_FILES.rustBinPkgbuild, PRODUCT_FILES.rustBinSrcinfo],
  ]) {
    assertEqual(/^pkgver=(\S+)$/m.exec(read(root, pkgbuild))?.[1], version, `${name} PKGBUILD version`)
    const srcinfoContent = read(root, srcinfo)
    assertEqual(/^\s*pkgver = (\S+)$/m.exec(srcinfoContent)?.[1], version, `${name} .SRCINFO version`)
    if (!srcinfoContent.includes(`/releases/download/v${version}/`)) {
      fail(`${name} .SRCINFO does not target release v${version}`)
    }
  }

  const localFallback = /^pkgver="\$\{LMM_API_PKGVER:-([^}]+)\}"$/m.exec(
    read(root, PRODUCT_FILES.localGoPkgbuild)
  )?.[1]
  assertEqual(localFallback, version, 'local Go PKGBUILD fallback version')

  const changelog = read(root, PRODUCT_FILES.changelog)
  extractReleaseNotes(changelog, version)
  if (!changelog.includes(`[Unreleased]: ${REPOSITORY_URL}/compare/v${version}...HEAD`)) {
    fail(`CHANGELOG.md Unreleased comparison does not start at v${version}`)
  }
  if (!changelog.includes(`[${version}]: ${REPOSITORY_URL}/compare/`)) {
    fail(`CHANGELOG.md is missing the ${version} comparison link`)
  }
}

function parseOptions(values) {
  const options = {}
  for (let index = 0; index < values.length; index += 1) {
    const key = values[index]
    if (!key.startsWith('--')) fail(`unexpected argument: ${key}`)
    const value = values[index + 1]
    if (!value || value.startsWith('--')) fail(`missing value for ${key}`)
    options[key.slice(2)] = value
    index += 1
  }
  return options
}

function usage() {
  console.error(`Usage:
  node scripts/release.mjs plan --bump patch|minor|major [--version X.Y.Z]
  node scripts/release.mjs prepare --bump patch|minor|major [--version X.Y.Z] [--date YYYY-MM-DD]
  node scripts/release.mjs verify --version X.Y.Z
  node scripts/release.mjs notes --version X.Y.Z --output PATH`)
}

function main() {
  const [command, ...values] = process.argv.slice(2)
  if (!command || command === '--help' || command === '-h') {
    usage()
    process.exit(command ? 0 : 2)
  }
  const options = parseOptions(values)
  const root = resolve(options.root ?? dirname(dirname(fileURLToPath(import.meta.url))))

  switch (command) {
    case 'plan': {
      const plan = planVersion(
        readVersion(root),
        options.bump ?? 'patch',
        options.version,
        stableTags(root)
      )
      console.log(plan.target)
      break
    }
    case 'prepare': {
      const date = options.date ?? new Date().toISOString().slice(0, 10)
      const plan = prepareRelease({
        root,
        bump: options.bump ?? 'patch',
        explicitVersion: options.version,
        date,
      })
      console.log(plan.target)
      break
    }
    case 'verify':
      if (!options.version) fail('--version is required')
      verifyRelease({ root, version: options.version })
      console.log(`release metadata verified: v${options.version}`)
      break
    case 'notes': {
      if (!options.version || !options.output) fail('--version and --output are required')
      const notes = extractReleaseNotes(read(root, PRODUCT_FILES.changelog), options.version)
      writeFileSync(resolve(root, options.output), `${notes}\n`)
      console.log(`release notes written: ${options.output}`)
      break
    }
    default:
      usage()
      fail(`unknown command: ${command}`)
  }
}

const invokedPath = process.argv[1] ? pathToFileURL(resolve(process.argv[1])).href : ''
if (import.meta.url === invokedPath) {
  try {
    main()
  } catch (error) {
    console.error(`release error: ${error.message}`)
    process.exit(1)
  }
}
