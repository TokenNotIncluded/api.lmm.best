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
import type { ToolUIPart } from 'ai'
import { useTranslation } from 'react-i18next'

import {
  Tool,
  ToolContent,
  ToolHeader,
  ToolInput,
  ToolOutput,
} from '@/components/ai-elements/tool'

import type { AssistantToolTrace } from './api.js'
import {
  assistantToolTraceKey,
  collapseAssistantToolTraces,
} from './assistant-tool-traces.js'

const TOOL_TITLE_KEYS = {
  navigate_to_page: 'Navigate within console',
  get_user_overview: 'Inspect user account',
  get_user_usage_summary: 'Analyze user usage',
  prepare_user_action: 'Prepare user action',
  get_service_facts: 'Read service connection facts',
  get_usage_summary: 'Analyze usage',
  get_available_models: 'Read available models',
  get_model_pricing: 'Read model pricing',
  get_account_access: 'Read account access',
  get_setup_guide: 'Read setup guide',
  get_plan_offers: 'Read plan offers',
  calculate_math: 'Calculate math',
  calculate_cost: 'Calculate cost',
  set_conversation_title: 'Update conversation title',
} satisfies Record<string, string>

const TOOL_SUMMARY_KEYS = {
  navigate_to_page: 'Navigation prepared',
  get_user_overview: 'Account overview loaded',
  get_user_usage_summary: 'Usage summary loaded',
  prepare_user_action: 'User action prepared',
  get_service_facts: 'Service facts loaded',
  get_usage_summary: 'Usage summary loaded',
  get_available_models: 'Available models loaded',
  get_model_pricing: 'Model pricing loaded',
  get_account_access: 'Account access loaded',
  get_setup_guide: 'Setup guide loaded',
  get_plan_offers: 'Plan offers loaded',
  calculate_math: 'Calculation completed',
  calculate_cost: 'Cost estimate calculated',
  set_conversation_title: 'Conversation title updated',
} satisfies Record<string, string>

function toolErrorText(
  errorCode: AssistantToolTrace['errorCode'],
  t: ReturnType<typeof useTranslation>['t']
) {
  if (errorCode === 'missing_math_expression') {
    return t('A math expression is required.')
  }
  return t('The math expression could not be evaluated.')
}

function toolLabel(
  labels: Readonly<Record<string, string>>,
  name: string
): string | undefined {
  return labels[name]
}

function toolTitle(name: string): string {
  return toolLabel(TOOL_TITLE_KEYS, name) ?? name.replaceAll('_', ' ')
}

function toolStatusText(
  trace: AssistantToolTrace,
  t: ReturnType<typeof useTranslation>['t']
) {
  if (trace.status === 'output-error') return t('Tool failed')
  if (trace.status === 'approval-requested') {
    return t('Waiting for confirmation')
  }
  return t('Tool completed')
}

function toolSummary({
  trace,
  parameterCount,
  completedSummary,
  statusText,
  t,
}: {
  trace: AssistantToolTrace
  parameterCount: number
  completedSummary: string
  statusText: string
  t: ReturnType<typeof useTranslation>['t']
}) {
  if (trace.result !== undefined) {
    const expression = trace.input?.expression
    if (expression) return `${String(expression)} = ${trace.result}`
    return `${completedSummary} · ${trace.result}`
  }
  if (
    trace.status === 'output-error' ||
    trace.status === 'approval-requested'
  ) {
    return statusText
  }
  if (parameterCount > 0) {
    return `${completedSummary} · ${t('{{count}} input parameters', { count: parameterCount })}`
  }
  return completedSummary
}

export function AssistantToolCalls(props: { traces: AssistantToolTrace[] }) {
  const { t } = useTranslation()
  const traces = collapseAssistantToolTraces(props.traces)
  if (traces.length === 0) return null

  return (
    <div className='w-full space-y-1' data-testid='assistant-tool-calls'>
      {traces.map((trace, index) => {
        const isError = trace.status === 'output-error'
        const isApproval = trace.status === 'approval-requested'
        const parameterCount = Object.keys(trace.input ?? {}).length
        const statusText = toolStatusText(trace, t)
        const completedSummary = t(
          toolLabel(TOOL_SUMMARY_KEYS, trace.name) ?? 'Tool completed'
        )
        const summary = toolSummary({
          trace,
          parameterCount,
          completedSummary,
          statusText,
          t,
        })
        let errorText: string | undefined
        if (trace.errorCode) errorText = toolErrorText(trace.errorCode, t)
        else if (isError) errorText = statusText
        const hasContent =
          parameterCount > 0 ||
          errorText !== undefined ||
          trace.result !== undefined
        let output: string | undefined
        if (trace.result !== undefined) output = String(trace.result)
        return (
          <Tool
            key={assistantToolTraceKey(trace)}
            defaultOpen={isError || isApproval}
            data-testid={`assistant-tool-${index}`}
          >
            <ToolHeader
              title={t(toolTitle(trace.name))}
              type={`tool-${trace.name}` as ToolUIPart['type']}
              state={trace.status}
              summary={summary}
            />
            {hasContent ? (
              <ToolContent>
                {parameterCount > 0 ? (
                  <ToolInput input={trace.input ?? {}} />
                ) : null}
                <ToolOutput output={output} errorText={errorText} />
              </ToolContent>
            ) : null}
          </Tool>
        )
      })}
    </div>
  )
}
