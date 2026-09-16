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
export const CODE_TABS = ['Chat', 'API', 'Claude', 'Gemini'] as const
export type CodeTab = (typeof CODE_TABS)[number]

export function codeForTab(tab: CodeTab) {
  if (tab === 'Claude') {
    return `curl https://api.lmm.best/v1/messages \\
  -H "x-api-key: $LMM_API_KEY" \\
  -H "anthropic-version: 2023-06-01" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "model-name",
    "max_tokens": 256,
    "messages": [{ "role": "user", "content": "Hello" }]
  }'`
  }
  if (tab === 'Gemini') {
    return `curl "https://api.lmm.best/v1beta/models/model-name:generateContent" \\
  -H "x-goog-api-key: $LMM_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "contents": [{
      "role": "user",
      "parts": [{ "text": "Hello" }]
    }]
  }'`
  }
  if (tab === 'API') {
    return `import OpenAI from "openai"

const client = new OpenAI({
  baseURL: "https://api.lmm.best/v1",
  apiKey: process.env.LMM_API_KEY,
})

const response = await client.chat.completions.create({
  model: "model-name",
  messages: [{ role: "user", content: "your prompt" }],
})`
  }
  return `curl -X POST "https://api.lmm.best/v1/chat/completions" \\
  -H "Authorization: Bearer $LMM_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "model-name",
    "messages": [{ "role": "user", "content": "your prompt" }]
  }'`
}
