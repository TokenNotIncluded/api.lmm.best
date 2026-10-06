/*
Copyright (C) 2025 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/

import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { describe, test } from 'node:test'

import {
  parsePaymentAmountDiscounts,
  parsePaymentAmountOptions,
} from '../../integrations/payment-amount-options'
import { parseModerationGroupPolicies } from '../../security/moderation-config'
import {
  getSystemJsonConfiguration,
  SYSTEM_JSON_CONFIGURATIONS,
} from '../system-json-configurations'

function collectTsxFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) return collectTsxFiles(path)
    return entry.name.endsWith('.tsx') ? [path] : []
  })
}

describe('system JSON configuration registry', () => {
  test('recharge JSON documents decimal USD presets and exact discount matching', () => {
    const options = SYSTEM_JSON_CONFIGURATIONS['payment_setting.amount_options']
    const discount =
      SYSTEM_JSON_CONFIGURATIONS['payment_setting.amount_discount']
    assert.equal(options.specification.rootType, 'number[]')
    assert.deepEqual(parsePaymentAmountOptions(options.example, 'USD'), [
      '3.5',
      '10',
      '20',
    ])
    assert.equal(
      parsePaymentAmountDiscounts(discount.example, 'USD')?.['3.5'],
      0.98
    )
    for (const configuration of [options, discount]) {
      assert.match(configuration.specification.fields[0].rules ?? '', /TOKENS/)
      assert.match(configuration.specification.fields[0].rules ?? '', /500000/)
    }
    assert.match(
      discount.specification.fields[0].rules ?? '',
      /exact amount matching/
    )
    const creditOptions = getSystemJsonConfiguration(
      'payment_setting.amount_options',
      'CREDIT'
    )
    const creditDiscount = getSystemJsonConfiguration(
      'payment_setting.amount_discount',
      'CREDIT'
    )
    assert.deepEqual(
      parsePaymentAmountOptions(creditOptions.example, 'CREDIT'),
      ['1', '10', '20']
    )
    assert.deepEqual(
      parsePaymentAmountDiscounts(creditDiscount.example, 'CREDIT'),
      { '10': 0.99, '100': 0.9 }
    )
  })

  test('provides valid examples and non-empty field contracts for every key', () => {
    const entries = Object.entries(SYSTEM_JSON_CONFIGURATIONS)
    assert.ok(entries.length >= 40)

    for (const [key, configuration] of entries) {
      assert.doesNotThrow(
        () => JSON.parse(configuration.example),
        `${key} must provide valid JSON`
      )
      assert.ok(
        configuration.specification.rootType.trim(),
        `${key} must declare a root type`
      )
      assert.ok(
        configuration.specification.fields.length > 0,
        `${key} must declare at least one field`
      )

      const paths = configuration.specification.fields.map((field) => {
        assert.ok(field.path.trim(), `${key} has an empty field path`)
        assert.ok(field.type.trim(), `${key}.${field.path} has no type`)
        return field.path
      })
      assert.equal(
        new Set(paths).size,
        paths.length,
        `${key} contains duplicate field paths`
      )
    }
  })

  test('requires every settings JSON editor to use the documented wrapper', () => {
    const systemSettingsRoot = resolve(import.meta.dirname, '../..')
    const wrapperPath = resolve(
      systemSettingsRoot,
      'components/system-json-code-editor.tsx'
    )
    let documentedEditorCount = 0
    let moderationEditorCount = 0

    for (const file of collectTsxFiles(systemSettingsRoot)) {
      const source = readFileSync(file, 'utf8')
      if (file !== wrapperPath) {
        assert.equal(
          source.includes("from '@/components/json-code-editor'"),
          false,
          `${file} bypasses SystemJsonCodeEditor`
        )
      }

      for (const match of source.matchAll(
        /<SystemJsonCodeEditor\b([\s\S]*?)\/>/g
      )) {
        assert.match(match[1] ?? '', /configurationKey=/, `${file} has no key`)
        const props = match[1] ?? ''
        const literalKey = /configurationKey=['"]([^'"]+)['"]/.exec(props)?.[1]
        if (literalKey) {
          assert.ok(
            Object.hasOwn(SYSTEM_JSON_CONFIGURATIONS, literalKey),
            `${file} uses an unregistered key ${literalKey}`
          )
        }
        if (file.endsWith('moderation-group-policy-editor.tsx')) {
          assert.equal(
            literalKey,
            'ModerationGroupPolicies',
            `${file} must document the Moderation policy contract`
          )
          moderationEditorCount += 1
        }
        documentedEditorCount += 1
      }
    }

    assert.ok(
      documentedEditorCount > 0,
      'the settings editor inventory must not be empty'
    )
    assert.ok(
      moderationEditorCount > 0,
      'the Moderation JSON fallback must use the documented wrapper'
    )
  })

  test('documents explicit USD and inherited legacy Moderation price bases', () => {
    const configuration = SYSTEM_JSON_CONFIGURATIONS.ModerationGroupPolicies
    const example = JSON.parse(configuration.example)
    assert.deepEqual(example.default, {
      mode: 'off',
      amount_currency: 'USD',
      category_fines_usd: {},
    })
    assert.ok(parseModerationGroupPolicies(configuration.example))
    const fields = configuration.specification.fields
    assert.match(
      fields.find((field) => field.path === '<userGroup>.amount_currency')
        ?.rules ?? '',
      /omitted means legacy_pricing_unit/
    )
    assert.match(
      fields.find((field) => field.path === '<userGroup>.amount_currency')
        ?.rules ?? '',
      /convert legacy amounts before changing this marker/
    )
    assert.match(
      fields.find(
        (field) => field.path === '<userGroup>.category_fines_usd.<category>'
      )?.rules ?? '',
      /maximum: 1000/
    )
    assert.match(
      fields.find(
        (field) => field.path === '<userGroup>.category_fines_usd.<category>'
      )?.rules ?? '',
      /precision: 6/
    )
  })

  test('keeps high-risk examples aligned with their backend contracts', () => {
    const warnings = JSON.parse(
      SYSTEM_JSON_CONFIGURATIONS['group_ratio_setting.group_warnings'].example
    )
    assert.deepEqual(Object.keys(warnings.premium), [
      'enabled',
      'message',
      'mode',
      'confirmations',
    ])

    const skills = JSON.parse(
      SYSTEM_JSON_CONFIGURATIONS.AssistantSkillFiles.example
    )
    assert.match(skills[0].path, /^skills\/[a-z0-9-]+\/SKILL\.md$/)
    assert.match(skills[0].content, /^---\nname:/)

    const products = JSON.parse(
      SYSTEM_JSON_CONFIGURATIONS.CreemProducts.example
    )
    assert.deepEqual(Object.keys(products[0]), [
      'productId',
      'name',
      'price',
      'currency',
      'quota',
    ])
  })
})
