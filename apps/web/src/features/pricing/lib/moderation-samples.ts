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

type ModerationSampleLanguage = 'curl' | 'python' | 'typescript' | 'javascript'

type ModerationSampleContext = {
  baseUrl: string
  apiKeyEnv: string
  modelName: string
  endpointPath: string
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`
}

export function buildModerationSample(
  lang: ModerationSampleLanguage,
  ctx: ModerationSampleContext
): string {
  const url = `${ctx.baseUrl}${ctx.endpointPath}`
  const body = JSON.stringify(
    {
      model: ctx.modelName,
      input: 'I forgot my password and need to reset it.',
    },
    null,
    2
  )

  if (lang === 'curl') {
    return [
      `curl ${shellQuote(url)} \\`,
      `  -H "Authorization: Bearer $${ctx.apiKeyEnv}" \\`,
      `  -H "Content-Type: application/json" \\`,
      `  -d ${shellQuote(body.replaceAll('\n', '\n     '))}`,
    ].join('\n')
  }

  if (lang === 'python') {
    return [
      'import json',
      'import os',
      'from urllib.request import Request, urlopen',
      '',
      `payload = ${body}`,
      `request = Request(`,
      `    ${JSON.stringify(url)},`,
      `    data=json.dumps(payload).encode("utf-8"),`,
      `    headers={`,
      `        "Authorization": f"Bearer {os.environ['${ctx.apiKeyEnv}']}",`,
      `        "Content-Type": "application/json",`,
      `    },`,
      `    method="POST",`,
      `)`,
      '',
      'with urlopen(request) as response:',
      '    data = json.load(response)',
      '',
      'print(data["results"])',
    ].join('\n')
  }

  return [
    `const response = await fetch(${JSON.stringify(url)}, {`,
    `  method: 'POST',`,
    `  headers: {`,
    `    Authorization: \`Bearer \${process.env.${ctx.apiKeyEnv}}\`,`,
    `    'Content-Type': 'application/json',`,
    `  },`,
    `  body: JSON.stringify(${body}),`,
    `})`,
    '',
    `const data = await response.json()`,
    `console.log(data.results)`,
  ].join('\n')
}
