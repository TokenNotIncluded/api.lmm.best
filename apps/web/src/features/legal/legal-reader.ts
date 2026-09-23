/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
export type LegalHeading = { id: string; text: string; level: number }

export type LegalSection = { heading: LegalHeading | null; body: string }

/** A stable, URL-safe id. Duplicates get a numeric suffix so anchors stay unique. */
export function slugifyHeading(
  text: string,
  seen: Map<string, number>
): string {
  const base =
    text
      .toLowerCase()
      .replaceAll(/[^\p{L}\p{N}]+/gu, '-')
      .replaceAll(/^-+|-+$/g, '')
      .slice(0, 60) || 'section'
  const count = seen.get(base) ?? 0
  seen.set(base, count + 1)
  return count === 0 ? base : `${base}-${count + 1}`
}

function stripHtml(text: string) {
  return text
    .replaceAll(/<[^>]+>/g, '')
    .replaceAll(/&nbsp;/g, ' ')
    .replaceAll(/&amp;/g, '&')
    .replaceAll(/&lt;/g, '<')
    .replaceAll(/&gt;/g, '>')
    .trim()
}

function markdownHeading(line: string): { text: string; level: number } | null {
  const match = /^(#{1,3})\s+(\S.*)$/.exec(line)
  if (!match) return null
  const text = stripHtml(match[2].replace(/#+\s*$/, ''))
  return text ? { text, level: match[1].length } : null
}

/**
 * Split a document into sections at its headings so the reader can build a
 * table of contents and deep links. Markdown code fences are skipped: a `#`
 * comment inside a fenced example must not become a heading.
 */
export function splitLegalSections(
  content: string,
  mode: 'markdown' | 'html'
): { sections: LegalSection[]; headings: LegalHeading[] } {
  const seen = new Map<string, number>()
  const headings: LegalHeading[] = []
  const sections: LegalSection[] = []
  let current: LegalSection = { heading: null, body: '' }
  let inFence = false

  const push = () => {
    if (current.heading || current.body.trim()) sections.push(current)
  }

  if (mode === 'html') {
    const pattern = /<h([1-6])(?:\s[^>]*)?>([\s\S]*?)<\/h\1>/gi
    let offset = 0
    let match: RegExpExecArray | null
    while ((match = pattern.exec(content))) {
      const text = stripHtml(match[2])
      if (!text) continue
      current.body += content.slice(offset, match.index)
      push()
      const heading = {
        id: slugifyHeading(text, seen),
        text,
        level: Math.min(Number(match[1]), 3),
      }
      headings.push(heading)
      current = { heading, body: '' }
      offset = pattern.lastIndex
    }
    current.body += content.slice(offset)
    push()
    return { sections, headings }
  }

  for (const line of content.split('\n')) {
    const trimmed = line.trim()

    if (/^(```|~~~)/.test(trimmed)) {
      inFence = !inFence
      current.body += `${line}\n`
      continue
    }

    const found = inFence ? null : markdownHeading(trimmed)

    if (found) {
      push()
      const heading: LegalHeading = {
        id: slugifyHeading(found.text, seen),
        text: found.text,
        level: found.level,
      }
      headings.push(heading)
      current = { heading, body: '' }
      continue
    }

    current.body += `${line}\n`
  }
  push()

  return { sections, headings }
}
