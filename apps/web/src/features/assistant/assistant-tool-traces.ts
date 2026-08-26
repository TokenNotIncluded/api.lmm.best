import type { AssistantToolTrace } from './api.js'

function toolTraceKey(trace: AssistantToolTrace) {
  const input = Object.entries(trace.input ?? {}).sort(([left], [right]) =>
    left.localeCompare(right)
  )
  return `${trace.name}:${JSON.stringify(input)}`
}

export function collapseAssistantToolTraces(traces: AssistantToolTrace[]) {
  let collapsed: AssistantToolTrace[] = []
  for (const trace of traces) {
    if (trace.status === 'output-available') {
      collapsed = collapsed.filter(
        (item) => item.name !== trace.name || item.status !== 'output-error'
      )
    }
    const key = toolTraceKey(trace)
    const existing = collapsed.findIndex(
      (item) => item.status === trace.status && toolTraceKey(item) === key
    )
    if (existing >= 0) collapsed[existing] = trace
    else collapsed.push(trace)
  }
  return collapsed
}

export function assistantToolTraceKey(trace: AssistantToolTrace) {
  return `${toolTraceKey(trace)}:${trace.status}`
}
