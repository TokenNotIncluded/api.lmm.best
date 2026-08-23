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

import type {
  JsonConfigurationSpecification,
  JsonFieldSpecification,
} from '@/components/json-code-editor'

type JsonValue =
  | string
  | number
  | boolean
  | null
  | readonly JsonValue[]
  | { readonly [key: string]: JsonValue }

type SystemJsonConfiguration = Readonly<{
  example: string
  specification: JsonConfigurationSpecification
}>

function stringify(value: JsonValue) {
  return JSON.stringify(value, null, 2)
}

function configuration(
  example: JsonValue,
  rootType: string,
  fields: readonly JsonFieldSpecification[]
): SystemJsonConfiguration {
  return {
    example: stringify(example),
    specification: { rootType, fields },
  }
}

function numberRecord(
  example: Record<string, number>,
  path: string,
  rules = 'minimum: 0; finite: true'
) {
  const exampleValue = Object.values(example)[0]
  return configuration(example, 'Record<string, number>', [
    {
      path,
      type: 'number',
      rules,
      example: String(exampleValue),
    },
  ])
}

function stringRecord(
  example: Record<string, string>,
  path: string,
  rules?: string
) {
  const exampleValue = Object.values(example)[0]
  return configuration(example, 'Record<string, string>', [
    {
      path,
      type: 'string',
      rules,
      example: JSON.stringify(exampleValue),
    },
  ])
}

export const SYSTEM_JSON_CONFIGURATIONS = {
  GroupRatio: numberRecord({ default: 1, premium: 1.2 }, '<group>'),
  TopupGroupRatio: numberRecord({ default: 1, premium: 0.9 }, '<group>'),
  UserUsableGroups: stringRecord(
    { default: 'Default group', premium: 'Premium group' },
    '<group>'
  ),
  GroupGroupRatio: configuration(
    { premium: { default: 0.9, premium: 1 } },
    'Record<string, Record<string, number>>',
    [
      {
        path: '<sourceGroup>',
        type: 'object',
        rules: 'additionalProperties: number',
        example: '"premium"',
      },
      {
        path: '<sourceGroup>.<targetGroup>',
        type: 'number',
        rules: 'minimum: 0; finite: true',
        example: '0.9',
      },
    ]
  ),
  'group_ratio_setting.group_warnings': configuration(
    {
      premium: {
        enabled: true,
        message: 'Premium routing may consume quota faster.',
        mode: 'modal',
        confirmations: 2,
      },
    },
    'Record<string, GroupWarning>',
    [
      { path: '<group>', type: 'object', example: '"premium"' },
      {
        path: '<group>.enabled',
        type: 'boolean',
        required: true,
        example: 'true',
      },
      {
        path: '<group>.message',
        type: 'string',
        required: true,
        rules: 'minLength: 1',
        example: '"Premium routing may consume quota faster."',
      },
      {
        path: '<group>.mode',
        type: 'string',
        required: true,
        rules: 'enum: [modal, banner, inline]',
        example: '"modal"',
      },
      {
        path: '<group>.confirmations',
        type: 'integer',
        required: true,
        rules: 'minimum: 1; maximum: 3',
        example: '2',
      },
    ]
  ),
  AutoGroups: configuration(['premium', 'default'], 'string[]', [
    {
      path: '[]',
      type: 'string',
      required: true,
      rules: 'uniqueItems: true; item.minLength: 1',
      example: '"premium"',
    },
  ]),
  'group_ratio_setting.group_special_usable_group': configuration(
    {
      premium: {
        '+:default': 'Standard access',
        '-:legacy': '',
      },
    },
    'Record<string, Record<string, string>>',
    [
      { path: '<userGroup>', type: 'object', example: '"premium"' },
      {
        path: '<userGroup>.<rule>',
        type: 'string',
        rules: 'rule: <group> | +:<group> | -:<group>',
        example: '"+:default": "Standard access"',
      },
    ]
  ),
  AssistantReviewGroupPolicies: configuration(
    { default: { probability: 1, intensity: 'standard' } },
    'Record<string, AssistantReviewGroupPolicy>',
    [
      { path: '<group>', type: 'object', example: '"default"' },
      {
        path: '<group>.probability',
        type: 'number',
        required: true,
        rules: 'minimum: 0; maximum: 100; finite: true',
        example: '1',
      },
      {
        path: '<group>.intensity',
        type: 'string',
        required: false,
        rules: 'enum: [off, low, standard, high]',
        example: '"standard"',
      },
    ]
  ),
  AssistantSkillFiles: configuration(
    [
      {
        path: 'skills/review/SKILL.md',
        content:
          '---\nname: review\ndescription: Review request scope and evidence.\n---\n\n# Review\n\nVerify the request scope.',
        enabled: true,
      },
    ],
    'AssistantSkillFile[]',
    [
      {
        path: '[].path',
        type: 'string',
        required: true,
        rules: 'pattern: ^skills/[a-z0-9-]+/SKILL\\.md$; uniqueItems: true',
        example: '"skills/review/SKILL.md"',
      },
      {
        path: '[].content',
        type: 'string',
        required: true,
        rules: 'frontMatter.required: [name, description]; maxLength: 12000',
        example: '"---\\nname: review\\ndescription: Review scope.\\n---"',
      },
      {
        path: '[].enabled',
        type: 'boolean',
        required: false,
        rules: 'default: true',
        example: 'true',
      },
    ]
  ),
  'global.thinking_model_blacklist': configuration(
    ['gpt-4o', 'gpt-4.1'],
    'string[]',
    [
      {
        path: '[]',
        type: 'string',
        required: true,
        rules: 'uniqueItems: true; item.minLength: 1',
        example: '"gpt-4o"',
      },
    ]
  ),
  'global.chat_completions_to_responses_policy': configuration(
    {
      enabled: true,
      all_channels: false,
      channel_ids: [1, 2],
      channel_types: [1],
      model_patterns: ['^gpt-4o.*$', '^gpt-5.*$'],
    },
    'ChatCompletionsToResponsesPolicy',
    [
      {
        path: 'enabled',
        type: 'boolean',
        required: false,
        rules: 'default: false',
        example: 'true',
      },
      {
        path: 'all_channels',
        type: 'boolean',
        required: false,
        rules: 'default: false',
        example: 'false',
      },
      {
        path: 'channel_ids',
        type: 'integer[]',
        required: false,
        rules: 'item.minimum: 1; uniqueItems: true',
        example: '[1, 2]',
      },
      {
        path: 'channel_types',
        type: 'integer[]',
        required: false,
        rules: 'uniqueItems: true',
        example: '[1]',
      },
      {
        path: 'model_patterns',
        type: 'string[]',
        required: false,
        rules: 'item.format: regex',
        example: '["^gpt-4o.*$"]',
      },
    ]
  ),
  'gemini.safety_settings': stringRecord(
    {
      default: 'OFF',
      HARM_CATEGORY_HARASSMENT: 'BLOCK_MEDIUM_AND_ABOVE',
    },
    '<category>',
    'enum: [OFF, BLOCK_NONE, BLOCK_LOW_AND_ABOVE, BLOCK_MEDIUM_AND_ABOVE, BLOCK_ONLY_HIGH, HARM_BLOCK_THRESHOLD_UNSPECIFIED]'
  ),
  'gemini.version_settings': stringRecord(
    { default: 'v1beta', 'gemini-2.5-pro': 'v1beta' },
    '<model>',
    'minLength: 1'
  ),
  'gemini.supported_imagine_models': configuration(
    ['gemini-2.0-flash-exp-image-generation'],
    'string[]',
    [
      {
        path: '[]',
        type: 'string',
        required: true,
        rules: 'uniqueItems: true; item.minLength: 1',
        example: '"gemini-2.0-flash-exp-image-generation"',
      },
    ]
  ),
  'claude.model_headers_settings': configuration(
    {
      'claude-opus-5': {
        'anthropic-beta': ['context-1m-2025-08-07'],
      },
    },
    'Record<string, Record<string, string[]>>',
    [
      { path: '<model>', type: 'object', example: '"claude-opus-5"' },
      {
        path: '<model>.<header>',
        type: 'string[]',
        rules: 'item.minLength: 1',
        example: '["context-1m-2025-08-07"]',
      },
    ]
  ),
  'claude.default_max_tokens': configuration(
    { default: 8192, 'claude-sonnet-4-6': 8192 },
    'Record<string, integer>',
    [
      {
        path: '<model>',
        type: 'integer',
        rules: 'minimum: 1',
        example: '8192',
      },
    ]
  ),
  ModelRequestRateLimitGroup: configuration(
    { default: [100, 20], premium: [500, 100] },
    'Record<string, [requestLimit, successLimit]>',
    [
      {
        path: '<group>',
        type: '[integer, integer]',
        rules: 'items: 2; [0].minimum: 0; [1].minimum: 1',
        example: '[100, 20]',
      },
      {
        path: '<group>[0]',
        type: 'integer',
        required: true,
        rules: 'minimum: 0',
        example: '100',
      },
      {
        path: '<group>[1]',
        type: 'integer',
        required: true,
        rules: 'minimum: 1',
        example: '20',
      },
    ]
  ),
  'channel_affinity_setting.rules': configuration(
    [
      {
        name: 'Client session',
        model_regex: ['^gpt-.*'],
        path_regex: ['^/v1/responses$'],
        user_agent_include: [],
        key_sources: [{ type: 'request_header', key: 'Session-Id' }],
        value_regex: '',
        ttl_seconds: 3600,
        skip_retry_on_failure: false,
        include_using_group: true,
        include_model_name: true,
        include_rule_name: true,
      },
    ],
    'ChannelAffinityRule[]',
    [
      {
        path: '[].id',
        type: 'integer',
        required: false,
        rules: 'readOnly: true',
        example: '1',
      },
      {
        path: '[].name',
        type: 'string',
        required: true,
        rules: 'minLength: 1',
        example: '"Client session"',
      },
      {
        path: '[].model_regex',
        type: 'string[]',
        required: true,
        rules: 'minItems: 1; item.format: regex',
        example: '["^gpt-.*"]',
      },
      {
        path: '[].path_regex',
        type: 'string[]',
        required: true,
        rules: 'item.format: regex',
        example: '["^/v1/responses$"]',
      },
      {
        path: '[].user_agent_include',
        type: 'string[]',
        required: false,
        example: '[]',
      },
      {
        path: '[].key_sources',
        type: 'KeySource[]',
        required: true,
        rules: 'minItems: 1',
        example: '[{"type":"request_header","key":"Session-Id"}]',
      },
      {
        path: '[].key_sources[].type',
        type: 'string',
        required: true,
        rules: 'enum: [context_int, context_string, request_header, gjson]',
        example: '"request_header"',
      },
      {
        path: '[].key_sources[].key',
        type: 'string',
        required: false,
        rules: 'requiredWhen: type=context_int|context_string|request_header',
        example: '"Session-Id"',
      },
      {
        path: '[].key_sources[].path',
        type: 'string',
        required: false,
        rules: 'requiredWhen: type=gjson',
        example: '"metadata.session_id"',
      },
      {
        path: '[].value_regex',
        type: 'string',
        required: true,
        rules: 'format: regex; "": no-filter',
        example: '""',
      },
      {
        path: '[].ttl_seconds',
        type: 'integer',
        required: true,
        rules: 'minimum: 0',
        example: '3600',
      },
      {
        path: '[].param_override_template',
        type: 'object',
        required: false,
        example: '{"operations":[]}',
      },
      {
        path: '[].skip_retry_on_failure',
        type: 'boolean',
        required: false,
        rules: 'default: false',
        example: 'false',
      },
      {
        path: '[].include_using_group',
        type: 'boolean',
        required: true,
        example: 'true',
      },
      {
        path: '[].include_model_name',
        type: 'boolean',
        required: true,
        example: 'true',
      },
      {
        path: '[].include_rule_name',
        type: 'boolean',
        required: true,
        example: 'true',
      },
    ]
  ),
  'channel_affinity_setting.param_override_template': configuration(
    {
      operations: [
        {
          mode: 'set',
          path: 'temperature',
          value: 0.7,
          conditions: [{ path: 'model', mode: 'prefix', value: 'gpt-' }],
        },
      ],
    },
    'ParameterOverrideTemplate',
    [
      {
        path: 'operations',
        type: 'Operation[]',
        required: true,
        rules: 'minItems: 1',
        example: '[{"mode":"set","path":"temperature","value":0.7}]',
      },
      {
        path: 'operations[].mode',
        type: 'string',
        required: true,
        rules:
          'enum: [delete, set, move, copy, prepend, append, trim_prefix, trim_suffix, ensure_prefix, ensure_suffix, trim_space, to_lower, to_upper, replace, regex_replace, return_error, prune_objects, set_header, delete_header, copy_header, move_header, pass_headers, sync_fields]',
        example: '"set"',
      },
      {
        path: 'operations[].path',
        type: 'string',
        required: false,
        rules: 'requiredWhen: mode uses path',
        example: '"temperature"',
      },
      {
        path: 'operations[].value',
        type: 'any',
        required: false,
        rules: 'requiredWhen: mode=set|copy|pass_headers|...',
        example: '0.7',
      },
      {
        path: 'operations[].from',
        type: 'string',
        required: false,
        rules:
          'requiredWhen: mode=move|copy|copy_header|move_header|sync_fields',
        example: '"source.path"',
      },
      {
        path: 'operations[].to',
        type: 'string',
        required: false,
        rules: 'requiredWhen: mode=copy_header|move_header|sync_fields',
        example: '"target.path"',
      },
      {
        path: 'operations[].logic',
        type: 'string',
        required: false,
        rules: 'enum: [AND, OR]; default: OR',
        example: '"AND"',
      },
      {
        path: 'operations[].conditions',
        type: 'Condition[]',
        required: false,
        example: '[{"path":"model","mode":"prefix","value":"gpt-"}]',
      },
      {
        path: 'operations[].conditions[].path',
        type: 'string',
        required: true,
        rules: 'minLength: 1',
        example: '"model"',
      },
      {
        path: 'operations[].conditions[].mode',
        type: 'string',
        required: true,
        rules: 'enum: [full, prefix, suffix, contains, gt, gte, lt, lte]',
        example: '"prefix"',
      },
      {
        path: 'operations[].conditions[].value',
        type: 'any',
        required: true,
        example: '"gpt-"',
      },
      {
        path: 'operations[].conditions[].invert',
        type: 'boolean',
        required: false,
        rules: 'default: false',
        example: 'false',
      },
      {
        path: 'operations[].conditions[].pass_missing_key',
        type: 'boolean',
        required: false,
        rules: 'default: false',
        example: 'false',
      },
      {
        path: 'operations[].keep_origin',
        type: 'boolean',
        required: false,
        rules: 'default: false',
        example: 'true',
      },
    ]
  ),
  'dynamic_pricing_setting.channel_costs': numberRecord(
    { '12': 0.5, '34': 1.2 },
    '<channelId>',
    'exclusiveMinimum: 0; finite: true'
  ),
  'dynamic_pricing_setting.per_model': configuration(
    {
      'gpt-5': {
        target_tpm: 50000,
        target_rpm: 60,
        target_cost_rate: 1,
        base_price_usd_per_million: 8,
      },
    },
    'Record<string, ModelPricingOverride>',
    [
      { path: '<model>', type: 'object', example: '"gpt-5"' },
      {
        path: '<model>.target_tpm',
        type: 'number',
        required: false,
        rules: 'minimum: 0; 0: inherit-global',
        example: '50000',
      },
      {
        path: '<model>.target_rpm',
        type: 'number',
        required: false,
        rules: 'minimum: 0; 0: inherit-global',
        example: '60',
      },
      {
        path: '<model>.target_cost_rate',
        type: 'number',
        required: false,
        rules: 'minimum: 0; 0: inherit-global',
        example: '1',
      },
      {
        path: '<model>.base_price_usd_per_million',
        type: 'number',
        required: false,
        rules: 'minimum: 0; 0: inherit-global',
        example: '8',
      },
    ]
  ),
  'billing_setting.billing_mode': stringRecord(
    { 'gpt-5': 'tiered_expr', 'gpt-4o': 'ratio' },
    '<model>',
    'enum: [ratio, tiered_expr]'
  ),
  'billing_setting.billing_expr': stringRecord(
    { 'gpt-5': 'tier("base", p * 2.5 + c * 15)' },
    '<model>',
    'format: billing-expression; result.minimum: 0'
  ),
  AdvancedSecurityRules: configuration(
    {
      version: 1,
      rules: [
        {
          id: 'custom-blocked-term',
          name: 'Blocked term',
          category: 'custom',
          layer: 'custom',
          severity: 'medium',
          source: 'local_custom',
          version: '1',
          description: 'Example operator rule',
          enabled: true,
          groups: ['default'],
          patterns: ['example blocked phrase'],
        },
      ],
    },
    'AdvancedSecurityRuleSet',
    [
      {
        path: 'version',
        type: 'integer',
        required: true,
        rules: 'const: 1',
        example: '1',
      },
      {
        path: 'rules',
        type: 'AdvancedSecurityRule[]',
        required: true,
        rules: 'maxItems: 512; uniqueBy: id',
        example: '[{...}]',
      },
      {
        path: 'rules[].id',
        type: 'string',
        required: true,
        rules: 'minLength: 1; maxLength: 64',
        example: '"custom-blocked-term"',
      },
      {
        path: 'rules[].name',
        type: 'string',
        required: true,
        rules: 'minLength: 1; maxLength: 128',
        example: '"Blocked term"',
      },
      {
        path: 'rules[].category',
        type: 'string',
        required: true,
        rules: 'maxLength: 64',
        example: '"custom"',
      },
      {
        path: 'rules[].layer',
        type: 'string',
        required: false,
        rules: 'maxLength: 32',
        example: '"custom"',
      },
      {
        path: 'rules[].severity',
        type: 'string',
        required: false,
        rules: 'maxLength: 16',
        example: '"medium"',
      },
      {
        path: 'rules[].source',
        type: 'string',
        required: false,
        rules: 'maxLength: 64',
        example: '"local_custom"',
      },
      {
        path: 'rules[].version',
        type: 'string',
        required: false,
        rules: 'maxLength: 32',
        example: '"1"',
      },
      {
        path: 'rules[].description',
        type: 'string',
        required: false,
        rules: 'maxLength: 512',
        example: '"Example operator rule"',
      },
      {
        path: 'rules[].enabled',
        type: 'boolean',
        required: true,
        example: 'true',
      },
      {
        path: 'rules[].groups',
        type: 'string[]',
        required: true,
        rules: 'maxItems: 64; item.maxLength: 64',
        example: '["default"]',
      },
      {
        path: 'rules[].patterns',
        type: 'string[]',
        required: true,
        rules: 'minItems: 1; maxItems: 64; item.maxLength: 256',
        example: '["example blocked phrase"]',
      },
    ]
  ),
  'console_setting.api_info': configuration(
    [
      {
        id: 1,
        url: 'https://api.example.com',
        route: 'Primary',
        description: 'Primary API route',
        color: 'blue',
      },
    ],
    'ApiInfo[]',
    [
      {
        path: '[]',
        type: 'ApiInfo',
        rules: 'maxItems: 50',
        example: '{...}',
      },
      {
        path: '[].id',
        type: 'integer',
        required: false,
        rules: 'minimum: 1; localIdentity: true',
        example: '1',
      },
      {
        path: '[].url',
        type: 'string',
        required: true,
        rules: 'format: http-uri; maxLength: 500',
        example: '"https://api.example.com"',
      },
      {
        path: '[].route',
        type: 'string',
        required: true,
        rules: 'minLength: 1; maxLength: 200',
        example: '"Primary"',
      },
      {
        path: '[].description',
        type: 'string',
        required: true,
        rules: 'minLength: 1; maxLength: 500',
        example: '"Primary API route"',
      },
      {
        path: '[].color',
        type: 'string',
        required: true,
        rules:
          'enum: [blue, green, cyan, purple, pink, red, orange, amber, yellow, lime, light-green, teal, light-blue, indigo, violet, grey, slate]',
        example: '"blue"',
      },
    ]
  ),
  'console_setting.announcements': configuration(
    [
      {
        id: 1,
        content: 'Scheduled maintenance at 02:00 UTC.',
        publishDate: '2026-08-23T02:00:00Z',
        type: 'warning',
        extra: 'Expected duration: 15 minutes',
      },
    ],
    'Announcement[]',
    [
      {
        path: '[]',
        type: 'Announcement',
        rules: 'maxItems: 100',
        example: '{...}',
      },
      {
        path: '[].id',
        type: 'integer',
        required: false,
        rules: 'minimum: 1; localIdentity: true',
        example: '1',
      },
      {
        path: '[].content',
        type: 'string',
        required: true,
        rules: 'minLength: 1; maxLength: 500; safeText: true',
        example: '"Scheduled maintenance at 02:00 UTC."',
      },
      {
        path: '[].publishDate',
        type: 'string',
        required: true,
        rules: 'format: date-time',
        example: '"2026-08-23T02:00:00Z"',
      },
      {
        path: '[].type',
        type: 'string',
        required: true,
        rules: 'enum: [default, ongoing, success, warning, error]',
        example: '"warning"',
      },
      {
        path: '[].extra',
        type: 'string',
        required: false,
        rules: 'maxLength: 100; safeText: true',
        example: '"Expected duration: 15 minutes"',
      },
    ]
  ),
  'console_setting.faq': configuration(
    [
      {
        id: 1,
        question: 'How do I create a key?',
        answer: 'Open Keys and select Create.',
        enabled: true,
      },
    ],
    'FAQ[]',
    [
      {
        path: '[]',
        type: 'FAQ',
        rules: 'maxItems: 100',
        example: '{...}',
      },
      {
        path: '[].id',
        type: 'integer',
        required: false,
        rules: 'minimum: 1; localIdentity: true',
        example: '1',
      },
      {
        path: '[].question',
        type: 'string',
        required: true,
        rules: 'minLength: 1; maxLength: 200; safeText: true',
        example: '"How do I create a key?"',
      },
      {
        path: '[].answer',
        type: 'string',
        required: true,
        rules: 'minLength: 1; maxLength: 1000; safeText: true',
        example: '"Open Keys and select Create."',
      },
      {
        path: '[].enabled',
        type: 'boolean',
        required: false,
        rules: 'default: true',
        example: 'true',
      },
    ]
  ),
  'payment_setting.amount_options': configuration([10, 20, 50], 'number[]', [
    {
      path: '[]',
      type: 'number',
      required: true,
      rules: 'minimum: 0; finite: true',
      example: '20',
    },
  ]),
  'payment_setting.amount_discount': configuration(
    { '10': 1, '100': 0.95 },
    'Record<string, number>',
    [
      {
        path: '<amount>',
        type: 'number',
        rules: 'minimum: 0; finite: true',
        example: '0.95',
      },
    ]
  ),
  PayMethods: configuration(
    [
      {
        name: 'Alipay',
        icon: 'SiAlipay',
        type: 'alipay',
        min_topup: '10',
      },
    ],
    'PayMethod[]',
    [
      {
        path: '[].name',
        type: 'string',
        required: true,
        rules: 'minLength: 1',
        example: '"Alipay"',
      },
      {
        path: '[].icon',
        type: 'string',
        required: true,
        rules: 'oneOf: icon-name | data:image/*',
        example: '"SiAlipay"',
      },
      {
        path: '[].type',
        type: 'string',
        required: true,
        rules: 'uniqueItems: true; minLength: 1',
        example: '"alipay"',
      },
      {
        path: '[].min_topup',
        type: 'numeric string',
        required: false,
        rules: 'minimum: 0',
        example: '"10"',
      },
    ]
  ),
  CreemProducts: configuration(
    [
      {
        productId: 'prod_example',
        name: 'Starter credit',
        price: 10,
        currency: 'USD',
        quota: 500000,
      },
    ],
    'CreemProduct[]',
    [
      {
        path: '[].productId',
        type: 'string',
        required: true,
        rules: 'minLength: 1; uniqueItems: true',
        example: '"prod_example"',
      },
      {
        path: '[].name',
        type: 'string',
        required: true,
        rules: 'minLength: 1',
        example: '"Starter credit"',
      },
      {
        path: '[].price',
        type: 'number',
        required: true,
        rules: 'minimum: 0; finite: true',
        example: '10',
      },
      {
        path: '[].currency',
        type: 'string',
        required: true,
        rules: 'ISO 4217',
        example: '"USD"',
      },
      {
        path: '[].quota',
        type: 'integer',
        required: true,
        rules: 'minimum: 1',
        example: '500000',
      },
    ]
  ),
  WaffoPayMethods: configuration(
    [
      {
        name: 'Card',
        icon: '/pay-card.png',
        payMethodType: 'CREDITCARD,DEBITCARD',
        payMethodName: '',
      },
    ],
    'WaffoPayMethod[]',
    [
      {
        path: '[].name',
        type: 'string',
        required: true,
        rules: 'minLength: 1',
        example: '"Card"',
      },
      {
        path: '[].icon',
        type: 'string',
        required: true,
        rules: 'oneOf: path | data:image/*',
        example: '"/pay-card.png"',
      },
      {
        path: '[].payMethodType',
        type: 'string',
        required: true,
        rules: 'format: csv<waffo-method-type>',
        example: '"CREDITCARD,DEBITCARD"',
      },
      {
        path: '[].payMethodName',
        type: 'string',
        required: true,
        rules: '"": auto',
        example: '""',
      },
    ]
  ),
  Chats: configuration(
    [
      { ChatGPT: 'https://chat.openai.com' },
      { 'Example client': 'https://example.com/chat' },
    ],
    'Record<string, string>[]',
    [
      {
        path: '[].<clientName>',
        type: 'string',
        required: true,
        rules: 'format: http-uri; maxProperties: 1',
        example: '"https://chat.openai.com"',
      },
    ]
  ),
  'custom_oauth.access_policy': configuration(
    {
      logic: 'and',
      conditions: [
        { field: 'email_verified', op: 'eq', value: true },
        { field: 'groups', op: 'contains', value: 'developers' },
      ],
      groups: [],
    },
    'AccessPolicy',
    [
      {
        path: 'logic',
        type: 'string',
        required: false,
        rules: 'enum: [and, or]; default: and',
        example: '"and"',
      },
      {
        path: 'conditions',
        type: 'AccessCondition[]',
        required: false,
        rules: 'conditions.length + groups.length >= 1',
        example: '[{"field":"email_verified","op":"eq","value":true}]',
      },
      {
        path: 'conditions[].field',
        type: 'string',
        required: true,
        rules: 'GJSON path; minLength: 1',
        example: '"email_verified"',
      },
      {
        path: 'conditions[].op',
        type: 'string',
        required: true,
        rules:
          'enum: [eq, ne, gt, gte, lt, lte, in, not_in, contains, not_contains, exists, not_exists]',
        example: '"eq"',
      },
      {
        path: 'conditions[].value',
        type: 'any',
        required: false,
        rules: 'in|not_in => array; exists|not_exists => ignored',
        example: 'true',
      },
      {
        path: 'groups',
        type: 'AccessPolicy[]',
        required: false,
        rules: 'items: AccessPolicy',
        example: '[]',
      },
    ]
  ),
  ModelRatio: numberRecord({ 'gpt-4o': 1 }, '<model>'),
  ModelPrice: numberRecord({ 'gpt-4o': 2.5 }, '<model>'),
  CacheRatio: numberRecord({ 'gpt-4o': 0.5 }, '<model>'),
  CreateCacheRatio: numberRecord({ 'gpt-4o': 1.25 }, '<model>'),
  CompletionRatio: numberRecord({ 'gpt-4o': 2 }, '<model>'),
  ImageRatio: numberRecord({ 'gpt-image-1': 1 }, '<model>'),
  AudioRatio: numberRecord({ 'gpt-4o-audio': 1 }, '<model>'),
  AudioCompletionRatio: numberRecord({ 'gpt-4o-audio': 2 }, '<model>'),
  CachedTokensRatio: numberRecord({ 'gpt-4o': 0.5 }, '<model>'),
  'tool_price_setting.prices': numberRecord(
    {
      web_search: 10,
      web_search_preview: 10,
      'web_search_preview:gpt-4o*': 25,
    },
    '<tool[:model-pattern]>'
  ),
} as const satisfies Record<string, SystemJsonConfiguration>

export type SystemJsonConfigurationKey = keyof typeof SYSTEM_JSON_CONFIGURATIONS

export function getSystemJsonConfiguration(
  key: SystemJsonConfigurationKey
): SystemJsonConfiguration {
  return SYSTEM_JSON_CONFIGURATIONS[key]
}
