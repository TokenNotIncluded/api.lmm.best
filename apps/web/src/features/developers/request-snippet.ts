/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
export type RequestShape = 'openai' | 'anthropic' | 'gemini'
export type RequestLanguage = 'curl' | 'javascript' | 'python'

export function buildRequestBody(
  shape: RequestShape,
  model: string,
  prompt: string
) {
  if (shape === 'anthropic') {
    return {
      model,
      max_tokens: 512,
      messages: [{ role: 'user', content: prompt }],
    }
  }
  if (shape === 'gemini') return { contents: [{ parts: [{ text: prompt }] }] }
  return { model, messages: [{ role: 'user', content: prompt }] }
}

function quoteShell(value: string) {
  return `'${value.replaceAll("'", "'\\''")}'`
}

/** Literal encoding keeps copied examples valid when a prompt contains quotes. */
export function buildRequestSnippet(
  language: RequestLanguage,
  url: string,
  body: Record<string, unknown>
): string {
  const json = JSON.stringify(body, null, 2)
  if (language === 'curl') {
    return [
      `curl ${quoteShell(url)} \\`,
      '  -H "Authorization: Bearer $LMM_API_KEY" \\',
      '  -H "Content-Type: application/json" \\',
      `  -d ${quoteShell(JSON.stringify(body))}`,
    ].join('\n')
  }
  if (language === 'javascript') {
    return [
      `const response = await fetch(${JSON.stringify(url)}, {`,
      "  method: 'POST',",
      '  headers: {',
      "    Authorization: 'Bearer ' + process.env.LMM_API_KEY,",
      "    'Content-Type': 'application/json',",
      '  },',
      `  body: JSON.stringify(${json.replaceAll('\n', '\n  ')}),`,
      '});',
      "if (!response.ok) throw new Error('LMM: HTTP ' + response.status);",
      'const data = await response.json();',
    ].join('\n')
  }
  return [
    'import json, os, requests',
    '',
    'response = requests.post(',
    `    ${JSON.stringify(url)},`,
    "    headers={'Authorization': 'Bearer ' + os.environ['LMM_API_KEY']},",
    `    json=json.loads(${JSON.stringify(JSON.stringify(body))}),`,
    '    timeout=30,',
    ')',
    'response.raise_for_status()',
    'data = response.json()',
  ].join('\n')
}
