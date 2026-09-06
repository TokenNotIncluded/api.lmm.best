import { appendFileSync, readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'

const normalize = (text) => text.trim().replace(/\s+/g, ' ')
const visible = (text) => text.replace(/<!--[\s\S]*?-->/g, '')

function sections(markdown, omitCode = false) {
  const result = new Map()
  let heading
  let fence
  for (const line of visible(markdown).split(/\r?\n/)) {
    const marker = line.match(/^\s*(`{3,}|~{3,})/)
    if (marker) {
      if (!fence) fence = marker[1]
      else if (marker[1][0] === fence[0] && marker[1].length >= fence.length) fence = null
    }
    const match = !fence && !marker && line.match(/^##\s+(.+?)\s*#*$/)
    if (match) {
      heading = normalize(match[1])
      result.set(heading, '')
    } else if (heading && !(omitCode && (fence || marker))) {
      result.set(heading, result.get(heading) + line + '\n')
    }
  }
  return result
}

export function validateDescription(body, template) {
  const expected = sections(template)
  const actual = sections(body ?? '')
  const prose = sections(body ?? '', true)
  const problems = []
  for (const [heading, content] of expected) {
    const value = actual.get(heading)
    if (value === undefined) {
      problems.push(`Missing section: ${heading}`)
      continue
    }
    if (/\/ (Summary|Verification)$/.test(heading)) {
      const text = visible(value).replace(/^\s*(`{3,}|~{3,}).*$/gm, '').trim()
      if (!text || !text.replace(/\b(command|result)\s*:/gi, '').trim()) {
        problems.push(`Describe the change or actual verification in: ${heading}`)
      }
    }
    const items = [...content.matchAll(/^\s*- \[[ xX]\]\s+(.+)$/gm)]
    const checked = new Set([...(prose.get(heading) ?? '').matchAll(/^\s*- \[[xX]\]\s+(.+)$/gm)].map((m) => normalize(m[1])))
    if (/\/ Checklist$/.test(heading)) {
      const missing = items.filter((m) => !checked.has(normalize(m[1])))
      if (missing.length) problems.push(`Complete all ${items.length} template items in: ${heading} (${missing.length} unchecked or missing)`)
    } else if (items.length && !items.some((m) => checked.has(normalize(m[1])))) {
      problems.push(`Select an applicable template option in: ${heading}`)
    }
  }
  return problems
}

async function main() {
  const event = JSON.parse(readFileSync(process.env.GITHUB_EVENT_PATH, 'utf8'))
  const number = event.pull_request.number
  // Read the latest body so a queued run does not report already-corrected text.
  const response = await fetch(`${process.env.GITHUB_API_URL}/repos/${process.env.GITHUB_REPOSITORY}/pulls/${number}`, {
    headers: { authorization: `Bearer ${process.env.GH_TOKEN}`, accept: 'application/vnd.github+json' },
  })
  if (!response.ok) throw new Error(`Unable to read PR metadata: HTTP ${response.status}`)
  const pr = await response.json()
  if (pr.state !== 'open') return
  // Dependency bots generate their own descriptions; code CI still applies.
  const problems = ['dependabot[bot]', 'renovate[bot]'].includes(pr.user?.login)
    ? []
    : validateDescription(pr.body, readFileSync('.github/PULL_REQUEST_TEMPLATE.md', 'utf8'))
  const summary = problems.length
    ? `## PR description needs attention\n\n${problems.map((p) => `- ${p}`).join('\n')}\n\nEdit the PR description to rerun this check. Report actual results or why a check was skipped.\n`
    : '## PR description check passed\n'
  appendFileSync(process.env.GITHUB_STEP_SUMMARY, summary + '\nThis read-only check never closes, labels, or locks a PR. Maintainers review code and CI separately.\n')
  for (const problem of problems) console.error(problem)
  if (problems.length) process.exitCode = 1
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    console.error(error.message)
    process.exitCode = 1
  })
}
