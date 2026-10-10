#!/usr/bin/env node
// Apply a complete translation patch only after every locale passes validation.
import { readdir, readFile, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const defaultLocalesDir = fileURLToPath(
  new URL('../../../../apps/web/src/i18n/locales/', import.meta.url),
)
const isObject = (value) => value !== null && typeof value === 'object' && !Array.isArray(value)
const sameKeys = (a, b) => JSON.stringify(Object.keys(a).sort()) === JSON.stringify(Object.keys(b).sort())
const placeholders = (value) => [...value.matchAll(/\{\{\s*([^{}]+?)\s*\}\}/g)].map((m) => m[1].trim()).sort()
const samePlaceholders = (a, b) => JSON.stringify(placeholders(a)) === JSON.stringify(placeholders(b))

export async function planTranslations(localesDir, updates) {
  const files = (await readdir(localesDir, { withFileTypes: true }))
    .filter((entry) => entry.isFile() && entry.name.endsWith('.json'))
    .map((entry) => entry.name).sort()
  const locales = Object.fromEntries(files.map((file) => [file.slice(0, -5), file]))
  if (!Object.hasOwn(locales, 'en') || !isObject(updates) || !sameKeys(locales, updates)) {
    throw new Error('Patch must contain every locale file, including en, with no extra locales.')
  }
  if (!isObject(updates.en) || Object.keys(updates.en).length === 0) {
    throw new Error('The en patch must contain at least one translation.')
  }
  for (const locale of Object.keys(locales)) {
    const values = updates[locale]
    if (!isObject(values) || !sameKeys(updates.en, values)) {
      throw new Error(`${locale}: patch keys must match en exactly.`)
    }
    for (const [key, value] of Object.entries(values)) {
      if (!key.trim() || typeof value !== 'string' || !value.trim()) {
        throw new Error(`${locale}: keys and translations must be non-empty strings.`)
      }
    }
  }
  for (const locale of Object.keys(locales)) {
    for (const [key, value] of Object.entries(updates[locale])) {
      if (!samePlaceholders(updates.en[key], value) ||
          (placeholders(key).length > 0 && !samePlaceholders(key, value))) {
        throw new Error(`${locale}: interpolation placeholders differ from the source.`)
      }
    }
  }
  const planned = []
  for (const [locale, file] of Object.entries(locales)) {
    const filename = path.join(localesDir, file)
    const original = await readFile(filename, 'utf8')
    const document = JSON.parse(original)
    if (!isObject(document) || !isObject(document.translation)) {
      throw new Error(`${locale}: expected a translation object.`)
    }
    if (Object.entries(updates[locale]).every(([key, value]) =>
      Object.hasOwn(document.translation, key) && document.translation[key] === value)) continue
    document.translation = Object.fromEntries(
      Object.entries({ ...document.translation, ...updates[locale] })
        .sort(([a], [b]) => a.localeCompare(b)),
    )
    planned.push({ filename, content: JSON.stringify(document, null, 2) + '\n' })
  }
  return planned
}

export async function applyTranslations(patchFile, { check = false, localesDir = defaultLocalesDir } = {}) {
  const updates = JSON.parse(await readFile(patchFile, 'utf8'))
  const planned = await planTranslations(localesDir, updates)
  // Validation is complete. Writes are sequential, not a multi-file transaction.
  if (!check) {
    for (const { filename, content } of planned) await writeFile(filename, content, 'utf8')
  }
  return planned.length
}

async function main(args) {
  const check = args.includes('--check')
  const files = args.filter((arg) => arg !== '--check')
  if (files.length !== 1 || files[0].startsWith('-') || args.length > 2) {
    throw new Error('Usage: node apply-translations.mjs PATCH.json [--check]')
  }
  const count = await applyTranslations(files[0], { check })
  console.log(`${check ? 'Validated; would update' : 'Updated'} ${count} locale files. Run i18n:sync after applying.`)
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  main(process.argv.slice(2)).catch((error) => {
    console.error(error.message)
    process.exitCode = 1
  })
}
