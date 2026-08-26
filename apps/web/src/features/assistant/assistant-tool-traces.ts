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
/*
Copyright (C) 2026 LIghtJUNction
*/
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
