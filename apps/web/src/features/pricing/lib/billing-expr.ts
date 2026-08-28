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
/**
 * Billing expression parsing utilities.
 *
 * Parses the dynamic billing expression format so that the pricing breakdown
 * UI can be rendered from the same backend expressions.
 *
 * The grammar is intentionally narrow: we only support the shapes that the
 * server emits (tiered pricing + request-rule conditional multipliers), so
 * the regular expressions are exact rather than tolerant of arbitrary
 * expression syntax.
 */

// ---------------------------------------------------------------------------
// Variable registry
// ---------------------------------------------------------------------------

export type BillingVar = {
  key: string
  field: string | null
  tierField: string | null
  label: string
  shortLabel: string
  side: 'input' | 'output' | 'condition'
  isBase?: boolean
  isConditionOnly?: boolean
  group?: string
}

export const BILLING_VARS: BillingVar[] = [
  {
    key: 'p',
    field: 'inputPrice',
    tierField: 'input_unit_cost',
    label: 'Input price',
    shortLabel: 'Input',
    side: 'input',
    isBase: true,
  },
  {
    key: 'c',
    field: 'outputPrice',
    tierField: 'output_unit_cost',
    label: 'Completion price',
    shortLabel: 'Output',
    side: 'output',
    isBase: true,
  },
  {
    key: 'len',
    field: null,
    tierField: null,
    label: 'Input length',
    shortLabel: 'Length',
    side: 'condition',
    isConditionOnly: true,
  },
  {
    key: 'cr',
    field: 'cacheReadPrice',
    tierField: 'cache_read_unit_cost',
    label: 'Cache read price',
    shortLabel: 'Cache Read',
    side: 'input',
    group: 'cache',
  },
  {
    key: 'cc',
    field: 'cacheCreatePrice',
    tierField: 'cache_create_unit_cost',
    label: 'Cache create price',
    shortLabel: 'Cache Write',
    side: 'input',
    group: 'cache',
  },
  {
    key: 'cc1h',
    field: 'cacheCreate1hPrice',
    tierField: 'cache_create_1h_unit_cost',
    label: 'Cache create (1h) price',
    shortLabel: 'Cache Write (1h)',
    side: 'input',
    group: 'cache',
  },
  {
    key: 'img',
    field: 'imagePrice',
    tierField: 'image_unit_cost',
    label: 'Image input price',
    shortLabel: 'Image In',
    side: 'input',
    group: 'media',
  },
  {
    key: 'img_o',
    field: 'imageOutputPrice',
    tierField: 'image_output_unit_cost',
    label: 'Image output price',
    shortLabel: 'Image Out',
    side: 'output',
    group: 'media',
  },
  {
    key: 'ai',
    field: 'audioInputPrice',
    tierField: 'audio_input_unit_cost',
    label: 'Audio input price',
    shortLabel: 'Audio In',
    side: 'input',
    group: 'media',
  },
  {
    key: 'ao',
    field: 'audioOutputPrice',
    tierField: 'audio_output_unit_cost',
    label: 'Audio output price',
    shortLabel: 'Audio Out',
    side: 'output',
    group: 'media',
  },
]

/** Vars that have real price fields (excludes condition-only vars like `len`) */
export const BILLING_PRICING_VARS: BillingVar[] = BILLING_VARS.filter(
  (v) => !v.isConditionOnly
)

/** Vars valid in tier conditions (`p`, `c`, `len`) */
export const BILLING_CONDITION_VARS: string[] = BILLING_VARS.filter(
  (v) => v.isBase || v.isConditionOnly
).map((v) => v.key)

const BILLING_VAR_KEY_TO_FIELD = Object.fromEntries(
  BILLING_PRICING_VARS.map((v) => [v.key, v.field as string])
) as Record<string, string>

export const BILLING_EXTRA_VARS: BillingVar[] = BILLING_VARS.filter(
  (v) => !v.isBase && !v.isConditionOnly
)

export const BILLING_CACHE_VAR_MAP = BILLING_EXTRA_VARS.map((v) => ({
  field: v.tierField as string,
  exprVar: v.key,
}))

// ---------------------------------------------------------------------------
// Request rule constants
// ---------------------------------------------------------------------------

export const SOURCE_PARAM = 'param'
export const SOURCE_HEADER = 'header'
export const SOURCE_TIME = 'time'

export const MATCH_EQ = 'eq'
export const MATCH_CONTAINS = 'contains'
export const MATCH_GT = 'gt'
export const MATCH_GTE = 'gte'
export const MATCH_LT = 'lt'
export const MATCH_LTE = 'lte'
export const MATCH_EXISTS = 'exists'
export const MATCH_RANGE = 'range'

export const TIME_FUNCS = ['hour', 'minute', 'weekday', 'month', 'day'] as const
export type TimeFunc = (typeof TIME_FUNCS)[number]

export const COMMON_TIMEZONES: { value: string; label: string }[] = [
  { value: 'Asia/Shanghai', label: 'UTC+8 Shanghai (Asia/Shanghai)' },
  { value: 'UTC', label: 'UTC' },
  { value: 'America/New_York', label: 'UTC-5 New York (America/New_York)' },
  {
    value: 'America/Los_Angeles',
    label: 'UTC-8 Los Angeles (America/Los_Angeles)',
  },
  { value: 'America/Chicago', label: 'UTC-6 Chicago (America/Chicago)' },
  { value: 'Europe/London', label: 'UTC+0 London (Europe/London)' },
  { value: 'Europe/Berlin', label: 'UTC+1 Berlin (Europe/Berlin)' },
  { value: 'Asia/Tokyo', label: 'UTC+9 Tokyo (Asia/Tokyo)' },
  { value: 'Asia/Singapore', label: 'UTC+8 Singapore (Asia/Singapore)' },
  { value: 'Asia/Seoul', label: 'UTC+9 Seoul (Asia/Seoul)' },
  { value: 'Australia/Sydney', label: 'UTC+10 Sydney (Australia/Sydney)' },
]

const NUMERIC_LITERAL_REGEX = /^-?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/

export type ParamHeaderCondition = {
  source: 'param' | 'header'
  path: string
  mode: string
  value: string
}

export type TimeCondition = {
  source: 'time'
  timeFunc: TimeFunc
  timezone: string
  mode: string
  value: string
  rangeStart: string
  rangeEnd: string
}

export type RequestCondition = TimeCondition | ParamHeaderCondition

export type RequestRuleGroup = {
  conditions: RequestCondition[]
  multiplier: string
  conditionText?: string
  matched?: boolean
}

export type RequestRuleTrace = {
  cond: string
  multiplier: number
  matched: boolean
}

export type TierCondition = {
  var: 'p' | 'c' | 'len'
  op: '<' | '<=' | '>' | '>='
  value: number
}

export type ParsedTier = {
  label: string
  conditions: TierCondition[]
  [field: string]: unknown
}

// ---------------------------------------------------------------------------
// Tier parser
// ---------------------------------------------------------------------------

function stripExprVersion(exprStr: string): { version: number; body: string } {
  if (!exprStr) return { version: 1, body: '' }
  const m = exprStr.match(/^v(\d+):([\s\S]*)$/)
  if (m) return { version: Number(m[1]), body: m[2] }
  return { version: 1, body: exprStr }
}

const MAX_EXPR_LENGTH = 16_384
const MAX_EXPR_TOKENS = 2_048
const MAX_EXPR_DEPTH = 64
const MAX_CALL_ARGUMENTS = 16
const MAX_EVAL_STEPS = 4_096

// Security boundary: this is a data grammar, not JavaScript. Only numeric/string
// literals, identifiers, calls, parentheses, and the operators listed here are
// tokenized; properties, assignments, statements, templates, and comments fail.
type ExprValue = number | string | boolean

type ExprNode =
  | { kind: 'literal'; value: ExprValue }
  | { kind: 'identifier'; name: string }
  | { kind: 'unary'; op: '+' | '-' | '!'; value: ExprNode }
  | { kind: 'binary'; op: string; left: ExprNode; right: ExprNode }
  | {
      kind: 'conditional'
      test: ExprNode
      consequent: ExprNode
      alternate: ExprNode
    }
  | { kind: 'call'; name: string; args: ExprNode[] }

type ExprToken = {
  kind: 'number' | 'string' | 'identifier' | 'operator' | 'eof'
  text: string
  value?: ExprValue
}

function isDigit(char: string | undefined): boolean {
  return char !== undefined && char >= '0' && char <= '9'
}

function isIdentifierStart(char: string | undefined): boolean {
  if (!char) return false
  return (
    (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || char === '_'
  )
}

function isIdentifierPart(char: string | undefined): boolean {
  return isIdentifierStart(char) || isDigit(char)
}

function hexValue(char: string | undefined): number {
  if (char && char >= '0' && char <= '9') return char.charCodeAt(0) - 48
  if (char && char >= 'a' && char <= 'f') return char.charCodeAt(0) - 87
  if (char && char >= 'A' && char <= 'F') return char.charCodeAt(0) - 55
  return -1
}

function decodeStringLiteral(text: string): string {
  let decoded = ''
  for (let index = 1; index < text.length - 1; index += 1) {
    const char = text[index]
    if (char !== '\\') {
      if (char.charCodeAt(0) < 32)
        {throw new Error('control character in string literal')}
      decoded += char
      continue
    }
    index += 1
    const escape = text[index]
    const simpleEscapes: Record<string, string> = {
      '"': '"',
      '\\': '\\',
      '/': '/',
      b: '\b',
      f: '\f',
      n: '\n',
      r: '\r',
      t: '\t',
    }
    if (escape in simpleEscapes) {
      decoded += simpleEscapes[escape]
      continue
    }
    if (escape !== 'u' || index + 4 >= text.length) {
      throw new Error('invalid string escape')
    }
    let codeUnit = 0
    for (let offset = 1; offset <= 4; offset += 1) {
      const digit = hexValue(text[index + offset])
      if (digit < 0) throw new Error('invalid unicode escape')
      codeUnit = codeUnit * 16 + digit
    }
    decoded += String.fromCharCode(codeUnit)
    index += 4
  }
  return decoded
}

function tryDecodeStringLiteral(text: string): string | null {
  try {
    return decodeStringLiteral(text)
  } catch {
    return null
  }
}

function tokenizeExpr(source: string): ExprToken[] {
  if (source.length > MAX_EXPR_LENGTH) throw new Error('expression is too long')
  const tokens: ExprToken[] = []
  let index = 0

  const push = (token: ExprToken) => {
    tokens.push(token)
    if (tokens.length > MAX_EXPR_TOKENS) {
      throw new Error('expression has too many tokens')
    }
  }

  while (index < source.length) {
    const char = source[index]
    if (char === ' ' || char === '\t' || char === '\n' || char === '\r') {
      index += 1
      continue
    }

    if (isDigit(char) || (char === '.' && isDigit(source[index + 1]))) {
      const start = index
      while (isDigit(source[index])) index += 1
      if (source[index] === '.') {
        index += 1
        while (isDigit(source[index])) index += 1
      }
      if (source[index] === 'e' || source[index] === 'E') {
        index += 1
        if (source[index] === '+' || source[index] === '-') index += 1
        const exponentStart = index
        while (isDigit(source[index])) index += 1
        if (index === exponentStart) throw new Error('invalid numeric exponent')
      }
      const text = source.slice(start, index)
      const value = Number(text)
      if (!Number.isFinite(value))
        {throw new Error('numeric literal is not finite')}
      push({ kind: 'number', text, value })
      continue
    }

    if (char === '"') {
      const start = index
      index += 1
      let escaped = false
      let closed = false
      while (index < source.length) {
        const current = source[index]
        index += 1
        if (escaped) {
          escaped = false
        } else if (current === '\\') {
          escaped = true
        } else if (current === '"') {
          closed = true
          break
        }
      }
      const text = source.slice(start, index)
      if (!closed) throw new Error('unterminated string literal')
      push({ kind: 'string', text, value: decodeStringLiteral(text) })
      continue
    }

    if (isIdentifierStart(char)) {
      const start = index
      index += 1
      while (isIdentifierPart(source[index])) index += 1
      push({ kind: 'identifier', text: source.slice(start, index) })
      continue
    }

    const pair = source.slice(index, index + 2)
    if (['&&', '||', '==', '!=', '<=', '>='].includes(pair)) {
      push({ kind: 'operator', text: pair })
      index += 2
      continue
    }
    if ('+-*/%<>!?:(),'.includes(char)) {
      push({ kind: 'operator', text: char })
      index += 1
      continue
    }
    throw new Error(`unsupported expression character at ${index}`)
  }
  tokens.push({ kind: 'eof', text: '' })
  return tokens
}

class RestrictedExprParser {
  private index = 0

  constructor(private readonly tokens: ExprToken[]) {}

  parse(): ExprNode {
    const node = this.parseConditional(0)
    if (this.peek().kind !== 'eof') {
      throw new Error(`unexpected token: ${this.peek().text}`)
    }
    return node
  }

  private peek(): ExprToken {
    return this.tokens[this.index]
  }

  private take(text?: string): ExprToken {
    const token = this.peek()
    if (text !== undefined && token.text !== text) {
      throw new Error(`expected ${text || 'end of expression'}`)
    }
    this.index += 1
    return token
  }

  private parseConditional(depth: number): ExprNode {
    if (depth > MAX_EXPR_DEPTH)
      {throw new Error('expression nesting is too deep')}
    const test = this.parseOr(depth)
    if (this.peek().text !== '?') return test
    this.take('?')
    const consequent = this.parseConditional(depth + 1)
    this.take(':')
    const alternate = this.parseConditional(depth + 1)
    return { kind: 'conditional', test, consequent, alternate }
  }

  private parseOr(depth: number): ExprNode {
    return this.parseBinary(depth, () => this.parseAnd(depth), ['||'])
  }

  private parseAnd(depth: number): ExprNode {
    return this.parseBinary(depth, () => this.parseEquality(depth), ['&&'])
  }

  private parseEquality(depth: number): ExprNode {
    return this.parseBinary(depth, () => this.parseComparison(depth), [
      '==',
      '!=',
    ])
  }

  private parseComparison(depth: number): ExprNode {
    return this.parseBinary(depth, () => this.parseAdditive(depth), [
      '<',
      '<=',
      '>',
      '>=',
    ])
  }

  private parseAdditive(depth: number): ExprNode {
    return this.parseBinary(depth, () => this.parseMultiplicative(depth), [
      '+',
      '-',
    ])
  }

  private parseMultiplicative(depth: number): ExprNode {
    return this.parseBinary(depth, () => this.parseUnary(depth), [
      '*',
      '/',
      '%',
    ])
  }

  private parseBinary(
    depth: number,
    next: () => ExprNode,
    operators: string[]
  ): ExprNode {
    if (depth > MAX_EXPR_DEPTH)
      {throw new Error('expression nesting is too deep')}
    let node = next()
    while (operators.includes(this.peek().text)) {
      const op = this.take().text
      node = { kind: 'binary', op, left: node, right: next() }
    }
    return node
  }

  private parseUnary(depth: number): ExprNode {
    const operators: Array<'+' | '-' | '!'> = []
    while (['+', '-', '!'].includes(this.peek().text)) {
      if (operators.length >= MAX_EXPR_DEPTH) {
        throw new Error('expression nesting is too deep')
      }
      operators.push(this.take().text as '+' | '-' | '!')
    }
    let node = this.parsePrimary(depth)
    while (operators.length > 0) {
      node = {
        kind: 'unary',
        op: operators.pop() as '+' | '-' | '!',
        value: node,
      }
    }
    return node
  }

  private parsePrimary(depth: number): ExprNode {
    const token = this.peek()
    if (token.kind === 'number' || token.kind === 'string') {
      this.take()
      return { kind: 'literal', value: token.value as ExprValue }
    }
    if (token.kind === 'identifier') {
      this.take()
      if (token.text === 'true' || token.text === 'false') {
        return { kind: 'literal', value: token.text === 'true' }
      }
      if (this.peek().text !== '(')
        {return { kind: 'identifier', name: token.text }}
      this.take('(')
      const args: ExprNode[] = []
      if (this.peek().text !== ')') {
        while (true) {
          if (args.length >= MAX_CALL_ARGUMENTS)
            {throw new Error('too many call arguments')}
          args.push(this.parseConditional(depth + 1))
          if (this.peek().text !== ',') break
          this.take(',')
        }
      }
      this.take(')')
      return { kind: 'call', name: token.text, args }
    }
    if (token.text === '(') {
      this.take('(')
      const node = this.parseConditional(depth + 1)
      this.take(')')
      return node
    }
    throw new Error(`unexpected token: ${token.text || 'end of expression'}`)
  }
}

function parseRestrictedExpr(exprStr: string): ExprNode {
  if (exprStr.length > MAX_EXPR_LENGTH)
    {throw new Error('expression is too long')}
  const { body } = stripExprVersion(exprStr.trim())
  return new RestrictedExprParser(tokenizeExpr(body)).parse()
}

function numericLiteral(node: ExprNode): number | null {
  if (node.kind === 'literal' && typeof node.value === 'number')
    {return node.value}
  if (
    node.kind === 'unary' &&
    (node.op === '+' || node.op === '-') &&
    node.value.kind === 'literal' &&
    typeof node.value.value === 'number'
  ) {
    return node.op === '-' ? -node.value.value : node.value.value
  }
  return null
}

function tierConditions(node: ExprNode): TierCondition[] | null {
  const parts: ExprNode[] = []
  const collect = (part: ExprNode) => {
    if (part.kind === 'binary' && part.op === '&&') {
      collect(part.left)
      collect(part.right)
    } else {
      parts.push(part)
    }
  }
  collect(node)

  const conditions: TierCondition[] = []
  for (const part of parts) {
    if (
      part.kind !== 'binary' ||
      !['<', '<=', '>', '>='].includes(part.op) ||
      part.left.kind !== 'identifier' ||
      !BILLING_CONDITION_VARS.includes(part.left.name)
    ) {
      return null
    }
    const value = numericLiteral(part.right)
    if (value === null) return null
    conditions.push({
      var: part.left.name as TierCondition['var'],
      op: part.op as TierCondition['op'],
      value,
    })
  }
  return conditions
}

function priceCoefficients(node: ExprNode): Record<string, number> | null {
  const terms: ExprNode[] = []
  const collect = (part: ExprNode) => {
    if (part.kind === 'binary' && part.op === '+') {
      collect(part.left)
      collect(part.right)
    } else {
      terms.push(part)
    }
  }
  collect(node)

  const coefficients: Record<string, number> = {}
  for (const term of terms) {
    let name: string | null = null
    let coefficient = 1
    if (term.kind === 'identifier') {
      name = term.name
    } else if (term.kind === 'binary' && term.op === '*') {
      if (term.left.kind === 'identifier') {
        name = term.left.name
        coefficient = numericLiteral(term.right) ?? Number.NaN
      } else if (term.right.kind === 'identifier') {
        name = term.right.name
        coefficient = numericLiteral(term.left) ?? Number.NaN
      }
    }
    if (
      !name ||
      !Object.hasOwn(BILLING_VAR_KEY_TO_FIELD, name) ||
      !Number.isFinite(coefficient)
    ) {
      return null
    }
    coefficients[name] = (coefficients[name] || 0) + coefficient
  }
  return coefficients
}

function parsedTier(
  node: ExprNode,
  conditions: TierCondition[]
): ParsedTier | null {
  if (
    node.kind !== 'call' ||
    node.name !== 'tier' ||
    node.args.length !== 2 ||
    node.args[0].kind !== 'literal' ||
    typeof node.args[0].value !== 'string'
  ) {
    return null
  }
  const coefficients = priceCoefficients(node.args[1])
  if (!coefficients) return null
  const tier: ParsedTier = { label: node.args[0].value, conditions }
  for (const [varName, field] of Object.entries(BILLING_VAR_KEY_TO_FIELD)) {
    tier[field] = coefficients[varName] || 0
  }
  return tier
}

function collectParsedTiers(node: ExprNode, tiers: ParsedTier[]): void {
  if (node.kind === 'conditional') {
    const conditions = tierConditions(node.test)
    const consequent = conditions
      ? parsedTier(node.consequent, conditions)
      : null
    if (consequent) tiers.push(consequent)
    else collectParsedTiers(node.consequent, tiers)
    collectParsedTiers(node.alternate, tiers)
    return
  }
  const tier = parsedTier(node, [])
  if (tier) {
    tiers.push(tier)
    return
  }
  if (node.kind === 'binary') {
    collectParsedTiers(node.left, tiers)
    collectParsedTiers(node.right, tiers)
  } else if (node.kind === 'unary') {
    collectParsedTiers(node.value, tiers)
  } else if (node.kind === 'call') {
    node.args.forEach((arg) => collectParsedTiers(arg, tiers))
  }
}

export function parseTiersFromExpr(exprStr: string): ParsedTier[] {
  if (!exprStr) return []
  try {
    const tiers: ParsedTier[] = []
    collectParsedTiers(parseRestrictedExpr(exprStr), tiers)
    return tiers
  } catch {
    return []
  }
}

function requireNumber(value: ExprValue): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) {
    throw new Error('numeric operand must be finite')
  }
  return value
}

export function evaluateBillingExpression(
  exprStr: string,
  variables: Readonly<Record<string, number>>
): { value: number; matchedTier: string } {
  const root = parseRestrictedExpr(exprStr)
  const allowedVariables = new Set(BILLING_VARS.map((item) => item.key))
  let steps = 0
  let matchedTier = ''

  const evaluate = (node: ExprNode): ExprValue => {
    steps += 1
    if (steps > MAX_EVAL_STEPS) throw new Error('expression is too complex')

    switch (node.kind) {
      case 'literal':
        return node.value
      case 'identifier': {
        if (!allowedVariables.has(node.name)) {
          throw new Error(`identifier is not allowed: ${node.name}`)
        }
        return requireNumber(variables[node.name] ?? 0)
      }
      case 'unary': {
        const value = evaluate(node.value)
        if (node.op === '!') return !value
        const number = requireNumber(value)
        return node.op === '-' ? -number : number
      }
      case 'conditional': {
        if (evaluate(node.test)) return evaluate(node.consequent)
        return evaluate(node.alternate)
      }
      case 'binary': {
        if (node.op === '&&') {
          return Boolean(evaluate(node.left)) && Boolean(evaluate(node.right))
        }
        if (node.op === '||') {
          return Boolean(evaluate(node.left)) || Boolean(evaluate(node.right))
        }
        const left = evaluate(node.left)
        const right = evaluate(node.right)
        switch (node.op) {
          case '==':
            return left === right
          case '!=':
            return left !== right
          case '<':
            return requireNumber(left) < requireNumber(right)
          case '<=':
            return requireNumber(left) <= requireNumber(right)
          case '>':
            return requireNumber(left) > requireNumber(right)
          case '>=':
            return requireNumber(left) >= requireNumber(right)
          case '+':
            return requireNumber(left) + requireNumber(right)
          case '-':
            return requireNumber(left) - requireNumber(right)
          case '*':
            return requireNumber(left) * requireNumber(right)
          case '/':
            return requireNumber(left) / requireNumber(right)
          case '%':
            return requireNumber(left) % requireNumber(right)
        }
        throw new Error(`operator is not allowed: ${node.op}`)
      }
      case 'call': {
        const args = node.args.map(evaluate)
        if (node.name === 'tier') {
          if (args.length !== 2 || typeof args[0] !== 'string') {
            throw new Error('tier expects a label and numeric value')
          }
          matchedTier = args[0]
          return requireNumber(args[1])
        }
        const numbers = args.map(requireNumber)
        switch (node.name) {
          case 'max':
            if (numbers.length === 0) throw new Error('max expects an argument')
            return Math.max(...numbers)
          case 'min':
            if (numbers.length === 0) throw new Error('min expects an argument')
            return Math.min(...numbers)
          case 'abs':
            if (numbers.length !== 1)
              {throw new Error('abs expects one argument')}
            return Math.abs(numbers[0])
          case 'ceil':
            if (numbers.length !== 1)
              {throw new Error('ceil expects one argument')}
            return Math.ceil(numbers[0])
          case 'floor':
            if (numbers.length !== 1)
              {throw new Error('floor expects one argument')}
            return Math.floor(numbers[0])
          default:
            throw new Error(`function is not allowed: ${node.name}`)
        }
      }
    }
  }

  return { value: requireNumber(evaluate(root)), matchedTier }
}

export function normalizeTierLabel(label: string | undefined): string {
  if (!label) return ''
  return label
    .replaceAll(/<[=＝]?|≤|＜[=＝]?/g, '<')
    .replaceAll(/>[=＝]?|≥|＞[=＝]?/g, '>')
    .replaceAll(/\s+/g, '')
    .toLowerCase()
}

// ---------------------------------------------------------------------------
// Request rule parser
// ---------------------------------------------------------------------------

function splitTopLevelMultiply(expr: string): string[] {
  const parts: string[] = []
  let start = 0
  let depth = 0
  for (let index = 0; index < expr.length; index += 1) {
    const char = expr[index]
    if (char === '(') depth += 1
    if (char === ')') depth -= 1
    if (depth === 0 && expr.slice(index, index + 3) === ' * ') {
      parts.push(expr.slice(start, index).trim())
      start = index + 3
      index += 2
    }
  }
  parts.push(expr.slice(start).trim())
  return parts.filter(Boolean)
}

function splitTopLevelAnd(expr: string): string[] {
  const parts: string[] = []
  let start = 0
  let depth = 0
  for (let i = 0; i < expr.length; i += 1) {
    const c = expr[i]
    if (c === '(') depth += 1
    if (c === ')') depth -= 1
    if (depth === 0 && expr.slice(i, i + 4) === ' && ') {
      parts.push(expr.slice(start, i).trim())
      start = i + 4
      i += 3
    }
  }
  parts.push(expr.slice(start).trim())
  return parts.filter(Boolean)
}

function parseExprLiteral(raw: string): string | null {
  const text = raw.trim()
  if (text === 'true' || text === 'false') return text
  if (NUMERIC_LITERAL_REGEX.test(text)) return text
  try {
    return JSON.parse(text) as string
  } catch {
    return null
  }
}

function tryParseTimeCondition(expr: string): RequestCondition | null {
  let m = expr.match(
    /^(hour|minute|weekday|month|day)\("([^"]+)"\) >= ([\d.eE+-]+) \|\| \1\("\2"\) < ([\d.eE+-]+)$/
  )
  if (m) {
    return {
      source: 'time',
      timeFunc: m[1] as TimeFunc,
      timezone: m[2],
      mode: MATCH_RANGE,
      value: '',
      rangeStart: m[3],
      rangeEnd: m[4],
    }
  }
  m = expr.match(
    /^\((hour|minute|weekday|month|day)\("([^"]+)"\) >= ([\d.eE+-]+) \|\| \1\("\2"\) < ([\d.eE+-]+)\)$/
  )
  if (m) {
    return {
      source: 'time',
      timeFunc: m[1] as TimeFunc,
      timezone: m[2],
      mode: MATCH_RANGE,
      value: '',
      rangeStart: m[3],
      rangeEnd: m[4],
    }
  }
  m = expr.match(
    /^(hour|minute|weekday|month|day)\("([^"]+)"\) (==|>=|<) ([\d.eE+-]+)$/
  )
  if (m) {
    const opMap: Record<string, string> = {
      '==': MATCH_EQ,
      '>=': MATCH_GTE,
      '<': MATCH_LT,
    }
    return {
      source: 'time',
      timeFunc: m[1] as TimeFunc,
      timezone: m[2],
      mode: opMap[m[3]] || MATCH_EQ,
      value: m[4],
      rangeStart: '',
      rangeEnd: '',
    }
  }
  return null
}

function tryParseRequestCondition(expr: string): RequestCondition | null {
  const tc = tryParseTimeCondition(expr)
  if (tc) return tc

  let m = expr.match(/^header\("([^"]+)"\) != ""$/)
  if (m) return { source: 'header', path: m[1], mode: MATCH_EXISTS, value: '' }

  m = expr.match(/^param\("([^"]+)"\) != nil$/)
  if (m) return { source: 'param', path: m[1], mode: MATCH_EXISTS, value: '' }

  m = expr.match(/^has\(header\("([^"]+)"\), ((?:"(?:[^"\\]|\\.)*"))\)$/)
  if (m) {
    const value = tryDecodeStringLiteral(m[2])
    if (value === null) return null
    return {
      source: 'header',
      path: m[1],
      mode: MATCH_CONTAINS,
      value,
    }
  }

  m = expr.match(
    /^param\("([^"]+)"\) != nil && has\(param\("([^"]+)"\), ((?:"(?:[^"\\]|\\.)*"))\)$/
  )
  if (m && m[1] === m[2]) {
    const value = tryDecodeStringLiteral(m[3])
    if (value === null) return null
    return {
      source: 'param',
      path: m[1],
      mode: MATCH_CONTAINS,
      value,
    }
  }

  m = expr.match(
    /^param\("([^"]+)"\) != nil && param\("([^"]+)"\) (>|>=|<|<=) ([\d.eE+-]+)$/
  )
  if (m && m[1] === m[2]) {
    const opMap: Record<string, string> = {
      '>': MATCH_GT,
      '>=': MATCH_GTE,
      '<': MATCH_LT,
      '<=': MATCH_LTE,
    }
    return { source: 'param', path: m[1], mode: opMap[m[3]], value: m[4] }
  }

  m = expr.match(/^(param|header)\("([^"]+)"\) == (.+)$/)
  if (m) {
    const parsedValue = parseExprLiteral(m[3])
    if (parsedValue === null) return null
    return {
      source: m[1] as 'param' | 'header',
      path: m[2],
      mode: MATCH_EQ,
      value: String(parsedValue),
    }
  }

  return null
}

function tryParseRuleGroupFactor(part: string): RequestRuleGroup | null {
  const m = part.match(/^\((.+) \? ([\d.eE+-]+) : 1\)$/s)
  if (!m) return null

  const conditions = tryParseRequestConditions(m[1])
  if (!conditions) return null
  return { conditions, multiplier: m[2] }
}

function tryParseRequestConditions(
  conditionStr: string
): RequestCondition[] | null {
  const andParts = splitTopLevelAnd(conditionStr)
  const conditions: RequestCondition[] = []
  for (const ap of andParts) {
    const cond = tryParseRequestCondition(ap.trim())
    if (!cond) return null
    conditions.push(cond)
  }
  return conditions.length > 0 ? conditions : null
}

export function requestRuleGroupsFromTrace(
  requestRules: RequestRuleTrace[]
): RequestRuleGroup[] {
  return requestRules.map((rule) => {
    const conditionText = rule.cond.trim()
    return {
      conditions: tryParseRequestConditions(conditionText) || [],
      multiplier: String(rule.multiplier),
      conditionText,
      matched: rule.matched,
    }
  })
}

export function tryParseRequestRuleExpr(
  expr: string
): RequestRuleGroup[] | null {
  const trimmed = (expr || '').trim()
  if (!trimmed) return []

  const parts = splitTopLevelMultiply(trimmed)
  const groups: RequestRuleGroup[] = []
  for (const part of parts) {
    const group = tryParseRuleGroupFactor(part)
    if (!group) return null
    groups.push(group)
  }
  return groups
}

// ---------------------------------------------------------------------------
// Combine / split billing expr and request rules
// ---------------------------------------------------------------------------

function hasFullOuterParens(expr: string): boolean {
  if (!expr.startsWith('(') || !expr.endsWith(')')) return false
  let depth = 0
  for (let i = 0; i < expr.length; i += 1) {
    if (expr[i] === '(') depth += 1
    if (expr[i] === ')') depth -= 1
    if (depth === 0 && i < expr.length - 1) return false
  }
  return depth === 0
}

function unwrapOuterParens(expr: string): string {
  let current = (expr || '').trim()
  while (hasFullOuterParens(current)) {
    current = current.slice(1, -1).trim()
  }
  return current
}

export function splitBillingExprAndRequestRules(expr: string): {
  billingExpr: string
  requestRuleExpr: string
} {
  const trimmed = (expr || '').trim()
  if (!trimmed) return { billingExpr: '', requestRuleExpr: '' }

  const parts = splitTopLevelMultiply(trimmed)
  if (parts.length <= 1) return { billingExpr: trimmed, requestRuleExpr: '' }

  const ruleParts: string[] = []
  const baseParts: string[] = []

  parts.forEach((part) => {
    const parsed = tryParseRequestRuleExpr(part)
    if (parsed && parsed.length > 0) {
      ruleParts.push(part)
    } else {
      baseParts.push(part)
    }
  })

  if (ruleParts.length === 0 || baseParts.length !== 1) {
    return { billingExpr: trimmed, requestRuleExpr: '' }
  }

  return {
    billingExpr: unwrapOuterParens(baseParts[0]),
    requestRuleExpr: ruleParts.join(' * '),
  }
}

export function combineBillingExpr(
  baseExpr: string,
  requestRuleExpr: string
): string {
  const base = (baseExpr || '').trim()
  const rules = (requestRuleExpr || '').trim()
  if (!base) return ''
  if (!rules) return base
  return `(${base}) * ${rules}`
}

// ---------------------------------------------------------------------------
// Editor: empty constructors
// ---------------------------------------------------------------------------

export function createEmptyCondition(): ParamHeaderCondition {
  return { source: 'param', path: '', mode: MATCH_EQ, value: '' }
}

export function createEmptyTimeCondition(): TimeCondition {
  return {
    source: 'time',
    timeFunc: 'hour',
    timezone: 'Asia/Shanghai',
    mode: MATCH_GTE,
    value: '',
    rangeStart: '',
    rangeEnd: '',
  }
}

export function createEmptyRuleGroup(): RequestRuleGroup {
  return { conditions: [createEmptyCondition()], multiplier: '' }
}

export function createEmptyTimeRuleGroup(): RequestRuleGroup {
  return { conditions: [createEmptyTimeCondition()], multiplier: '' }
}

// ---------------------------------------------------------------------------
// Editor: match option helpers
// ---------------------------------------------------------------------------

export type MatchOption = { value: string; labelKey: string }

export function getRequestRuleMatchOptions(source: string): MatchOption[] {
  if (source === SOURCE_TIME) {
    return [
      { value: MATCH_EQ, labelKey: 'Equals' },
      { value: MATCH_GTE, labelKey: 'Greater than or equal' },
      { value: MATCH_LT, labelKey: 'Less than' },
      { value: MATCH_RANGE, labelKey: 'Overnight range' },
    ]
  }
  const base: MatchOption[] = [
    { value: MATCH_EQ, labelKey: 'Equals' },
    { value: MATCH_CONTAINS, labelKey: 'Contains' },
    { value: MATCH_EXISTS, labelKey: 'Exists' },
  ]
  if (source === SOURCE_HEADER) return base
  return [
    ...base,
    { value: MATCH_GT, labelKey: 'Greater than' },
    { value: MATCH_GTE, labelKey: 'Greater than or equal' },
    { value: MATCH_LT, labelKey: 'Less than' },
    { value: MATCH_LTE, labelKey: 'Less than or equal' },
  ]
}

// ---------------------------------------------------------------------------
// Editor: normalize a single condition
// ---------------------------------------------------------------------------

function isTimeFunc(value: unknown): value is TimeFunc {
  return typeof value === 'string' && TIME_FUNCS.includes(value as TimeFunc)
}

export function normalizeCondition(
  cond: Partial<RequestCondition> | null | undefined
): RequestCondition {
  const source =
    cond?.source === 'time'
      ? 'time'
      : cond?.source === 'header'
        ? 'header'
        : 'param'

  if (source === 'time') {
    const timeCond = cond as Partial<TimeCondition> | null | undefined
    const timeFunc: TimeFunc = isTimeFunc(timeCond?.timeFunc)
      ? timeCond.timeFunc
      : 'hour'
    const options = getRequestRuleMatchOptions(SOURCE_TIME)
    const mode = options.some((item) => item.value === timeCond?.mode)
      ? (timeCond?.mode as string)
      : MATCH_GTE
    return {
      source: 'time',
      timeFunc,
      timezone: timeCond?.timezone || 'Asia/Shanghai',
      mode,
      value: timeCond?.value == null ? '' : String(timeCond.value),
      rangeStart:
        timeCond?.rangeStart == null ? '' : String(timeCond.rangeStart),
      rangeEnd: timeCond?.rangeEnd == null ? '' : String(timeCond.rangeEnd),
    }
  }

  const phCond = cond as Partial<ParamHeaderCondition> | null | undefined
  const options = getRequestRuleMatchOptions(source)
  const mode = options.some((item) => item.value === phCond?.mode)
    ? (phCond?.mode as string)
    : MATCH_EQ
  return {
    source,
    path: phCond?.path || '',
    mode,
    value: phCond?.value == null ? '' : String(phCond.value),
  }
}

// ---------------------------------------------------------------------------
// Editor: build expression strings
// ---------------------------------------------------------------------------

function buildExprLiteral(mode: string, value: string): string {
  const text = String(value || '').trim()
  if (mode === MATCH_CONTAINS) return JSON.stringify(text)
  if (text === 'true' || text === 'false') return text
  if (NUMERIC_LITERAL_REGEX.test(text)) return text
  return JSON.stringify(text)
}

function buildTimeConditionExpr(cond: TimeCondition): string {
  const normalized = normalizeCondition(cond) as TimeCondition
  const { timeFunc, timezone, mode } = normalized
  const tz = JSON.stringify(timezone)
  const fn = `${timeFunc}(${tz})`

  if (mode === MATCH_RANGE) {
    const s = normalized.rangeStart.trim()
    const e = normalized.rangeEnd.trim()
    if (!NUMERIC_LITERAL_REGEX.test(s) || !NUMERIC_LITERAL_REGEX.test(e)) {
      return ''
    }
    return `${fn} >= ${s} || ${fn} < ${e}`
  }
  const v = normalized.value.trim()
  if (!NUMERIC_LITERAL_REGEX.test(v)) return ''
  const opMap: Record<string, string> = {
    [MATCH_EQ]: '==',
    [MATCH_GTE]: '>=',
    [MATCH_LT]: '<',
  }
  return `${fn} ${opMap[mode] || '=='} ${v}`
}

function buildRequestConditionExpr(cond: RequestCondition): string {
  if (cond.source === 'time') return buildTimeConditionExpr(cond)
  const normalized = normalizeCondition(cond) as ParamHeaderCondition
  const path = normalized.path.trim()
  if (!path) return ''

  const sourceExpr =
    normalized.source === 'header'
      ? `header(${JSON.stringify(path)})`
      : `param(${JSON.stringify(path)})`

  switch (normalized.mode) {
    case MATCH_EXISTS:
      return normalized.source === 'header'
        ? `${sourceExpr} != ""`
        : `${sourceExpr} != nil`
    case MATCH_CONTAINS:
      return normalized.source === 'header'
        ? `has(${sourceExpr}, ${buildExprLiteral(normalized.mode, normalized.value)})`
        : `${sourceExpr} != nil && has(${sourceExpr}, ${buildExprLiteral(normalized.mode, normalized.value)})`
    case MATCH_GT:
    case MATCH_GTE:
    case MATCH_LT:
    case MATCH_LTE: {
      const opMap: Record<string, string> = {
        [MATCH_GT]: '>',
        [MATCH_GTE]: '>=',
        [MATCH_LT]: '<',
        [MATCH_LTE]: '<=',
      }
      const numText = String(normalized.value).trim()
      if (!NUMERIC_LITERAL_REGEX.test(numText)) return ''
      return `${sourceExpr} != nil && ${sourceExpr} ${opMap[normalized.mode]} ${numText}`
    }
    case MATCH_EQ:
    default:
      return `${sourceExpr} == ${buildExprLiteral(normalized.mode, normalized.value)}`
  }
}

function buildRuleGroupFactor(group: RequestRuleGroup): string {
  const multiplier = (group.multiplier || '').trim()
  if (!NUMERIC_LITERAL_REGEX.test(multiplier)) return ''
  const condExprs = (group.conditions || [])
    .map(buildRequestConditionExpr)
    .filter(Boolean)
  if (condExprs.length === 0) return ''

  const combined =
    condExprs.length === 1
      ? condExprs[0]
      : condExprs.map((e) => (e.includes(' || ') ? `(${e})` : e)).join(' && ')
  return `(${combined} ? ${multiplier} : 1)`
}

export function buildRequestRuleExpr(groups: RequestRuleGroup[]): string {
  return (groups || []).map(buildRuleGroupFactor).filter(Boolean).join(' * ')
}
