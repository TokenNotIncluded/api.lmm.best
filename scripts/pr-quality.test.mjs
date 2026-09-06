import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { validateDescription } from './pr-quality.mjs'

const template = readFileSync(new URL('../.github/PULL_REQUEST_TEMPLATE.md', import.meta.url), 'utf8')
const complete = template.replaceAll('- [ ]', '- [x]')
  .replace('<!-- 做了什么、为什么要改，以及关键实现方式。请使用自己确认过的准确描述。 -->', 'Fix the affected workflow and explain its behavior.')
  .replace('command:\nresult:', 'command: node --test scripts/pr-quality.test.mjs\nresult: passed')

test('current template with evidence passes without any author profile data', () => {
  assert.deepEqual(validateDescription(complete, template), [])
})

test('empty body reports the actual template headings', () => {
  const errors = validateDescription(null, template)
  assert.equal(errors.length, 7)
  assert.ok(errors.includes('Missing section: 提交检查 / Checklist'))
})

test('unfilled template reports empty evidence and unchecked choices', () => {
  const errors = validateDescription(template, template)
  assert.equal(errors.length, 5)
  assert.ok(errors.some((e) => e.includes('7 unchecked or missing')))
})

test('removing a required checklist item still fails', () => {
  const body = complete.replace(/^- \[x\] 代码、日志.*\n/m, '')
  assert.ok(validateDescription(body, template).some((e) => e.includes('1 unchecked or missing')))
})

test('checked boxes shown as a code example do not complete the checklist', () => {
  const body = complete.replace('## 提交检查 / Checklist', '## 提交检查 / Checklist\n\n```text') + '\n```\n'
  assert.ok(validateDescription(body, template).some((e) => e.includes('7 unchecked or missing')))
})

test('commented and fenced headings cannot substitute for real sections', () => {
  assert.equal(validateDescription(`<!--\n${complete.replace(/<!--[\s\S]*?-->/g, "")}\n-->`, template).length, 7)
  assert.equal(validateDescription(`~~~~\n${complete}\n~~~~`, template).length, 7)
})

test('CRLF, long descriptions, code references and AI disclosure do not cause rejection', () => {
  const body = complete.replace('Fix the affected', 'Generated with Claude Code. ' + 'Explanation with `code`. '.repeat(200) + '\nFix the affected')
  assert.deepEqual(validateDescription(body.replaceAll('\n', '\r\n'), template), [])
})

test('template heading changes are automatically reflected', () => {
  const heading = '提交检查 / Checklist'
  const renamed = '更新后的提交检查 / Checklist'
  assert.deepEqual(validateDescription(complete.replace(heading, renamed), template.replace(heading, renamed)), [])
  assert.ok(validateDescription(complete, template.replace(heading, renamed)).some((e) => e.includes(renamed)))
})
