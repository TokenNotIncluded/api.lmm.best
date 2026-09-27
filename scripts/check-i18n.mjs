/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { parseArgs } from 'node:util'
import { pathToFileURL } from 'node:url'

export const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']
const localeDirectory = 'apps/web/src/i18n/locales'
const isObject = (value) => value !== null && typeof value === 'object' && !Array.isArray(value)
const isTranslation = (value) => typeof value === 'string' && value.trim().length > 0

export function parseLocale(source, filename) {
  const document = JSON.parse(source)
  if (!isObject(document) || !isObject(document.translation)) {
    throw new Error(`${filename}: expected a translation object`)
  }
  return document.translation
}

function translationPlaceholders(value) {
  return [...String(value).matchAll(/\{\{\s*([^{}]+?)\s*\}\}/g)]
    .map((match) => match[1].trim())
    .sort()
}

export function findTranslationQualityFailures(before, after) {
  const failures = []

  for (const locale of locales) {
    if (locale === 'en') continue

    for (const [key, englishValue] of Object.entries(after.en)) {
      const translatedValue = after[locale][key]
      if (!isTranslation(englishValue) || !isTranslation(translatedValue)) {
        continue
      }

      const isNewKey = !Object.hasOwn(before.en, key)
      const englishChanged =
        Object.hasOwn(before.en, key) && before.en[key] !== englishValue
      const translationChanged =
        !Object.hasOwn(before[locale], key) ||
        before[locale][key] !== translatedValue

      if (!isNewKey && !englishChanged && !translationChanged) continue

      const expected = translationPlaceholders(englishValue)
      const actual = translationPlaceholders(translatedValue)
      if (JSON.stringify(expected) !== JSON.stringify(actual)) {
        failures.push(
          `${locale}.json: placeholder mismatch: ${JSON.stringify(key)} expected ${JSON.stringify(expected)} got ${JSON.stringify(actual)}`
        )
      }

      if (/__\s*PH_\d+\s*__/i.test(translatedValue)) {
        failures.push(
          `${locale}.json: leaked machine-translation placeholder: ${JSON.stringify(key)}`
        )
      }
    }
  }

  return failures
}

export function findRegressions(before, after) {
  const failures = []
  for (const locale of locales) {
    for (const key of Object.keys(after.en)) {
      const added = !Object.hasOwn(before.en, key)
      const previouslyTranslated = Object.hasOwn(before[locale], key) && isTranslation(before[locale][key])
      if ((added || previouslyTranslated) &&
          (!Object.hasOwn(after[locale], key) || !isTranslation(after[locale][key]))) {
        failures.push(`${locale}.json: ${added ? 'new key needs a translation' : 'existing translation removed or emptied'}: ${JSON.stringify(key)}`)
      }
    }
  }
  return [...failures, ...findTranslationQualityFailures(before, after)]
}

export function checkRepository({ cwd = process.cwd(), base = 'HEAD', mergeBase = false } = {}) {
  const git = (...args) => execFileSync('git', args, { cwd, encoding: 'utf8', maxBuffer: 16 * 1024 * 1024 }).trimEnd()
  // Resolve an object ID first so user-supplied refs never become git options.
  let revision = git('rev-parse', '--verify', '--end-of-options', `${base}^{commit}`)
  if (mergeBase) revision = git('merge-base', revision, 'HEAD')
  const before = {}
  const after = {}
  for (const locale of locales) {
    const filename = `${localeDirectory}/${locale}.json`
    before[locale] = parseLocale(git('show', `${revision}:${filename}`), `${revision}:${filename}`)
    after[locale] = parseLocale(readFileSync(resolve(cwd, filename), 'utf8'), filename)
  }
  return findRegressions(before, after)
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try {
    const { values } = parseArgs({
      options: {
        base: { type: 'string', default: 'HEAD' },
        'merge-base': { type: 'boolean', default: false },
      },
    })
    const failures = checkRepository({ base: values.base, mergeBase: values['merge-base'] })
    if (failures.length) {
      console.error(`i18n regression check failed (${failures.length}):\n${failures.join('\n')}`)
      process.exitCode = 1
    } else {
      console.log('i18n regression check passed: no missing, emptied, or structurally broken translations.')
    }
  } catch (error) {
    console.error(`i18n check could not run: ${error.message}`)
    process.exitCode = 1
  }
}
