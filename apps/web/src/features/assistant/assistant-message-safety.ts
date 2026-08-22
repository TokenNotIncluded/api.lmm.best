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
export type AssistantMessageSafetyResult = {
  content: string
  redacted: boolean
}

const ASSISTANT_REDACTION_PLACEHOLDERS = {
  apiKey: '[REDACTED_API_KEY]',
  credential: '[REDACTED_CREDENTIAL]',
  email: '[REDACTED_EMAIL]',
  phone: '[REDACTED_PHONE]',
  token: '[REDACTED_TOKEN]',
} as const

type AssistantRedactionKind = keyof typeof ASSISTANT_REDACTION_PLACEHOLDERS

type AssistantSensitivePattern = {
  pattern: RegExp
  kind: AssistantRedactionKind
}

// Keep this list in one place so display, request, retry, and history paths
// cannot silently drift apart. Request redaction uses the stable kind-specific
// placeholders below; display redaction may still provide a friendlier copy.
const assistantSensitivePatterns: AssistantSensitivePattern[] = [
  {
    pattern: /\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/gi,
    kind: 'email',
  },
  {
    pattern:
      /\b(?:api[\s_-]?key|access[\s_-]?token|refresh[\s_-]?token|password|passwd|passcode|credential|secret|密碼|密码|密钥|令牌)\s*(?:is|:|=|：)\s*[^\s,;]+/gi,
    kind: 'credential',
  },
  {
    pattern: /\bauthorization\s*:\s*bearer\s+[^\s,;]+/gi,
    kind: 'token',
  },
  {
    pattern: /\bbearer\s+[A-Za-z0-9._~+\-/]+=*/gi,
    kind: 'token',
  },
  {
    // Include short, explicitly labelled examples such as sk-secret... while
    // retaining a conservative minimum for unlabelled token-like values.
    pattern:
      /\b(?:sk|rk|pk|ak|tok|token|key|secret)[_-][A-Za-z0-9][A-Za-z0-9._~+\-/]{4,}(?:\.\.\.)?/gi,
    kind: 'apiKey',
  },
  {
    pattern:
      /\b(?:ghp|gho|github_pat|glpat|xoxb|xoxp|AIza|ya29)[-_A-Za-z0-9./~+]{8,}\b/g,
    kind: 'apiKey',
  },
  {
    pattern: /\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b/g,
    kind: 'token',
  },
  {
    pattern:
      /(?<![\w])(?:\+\d{1,3}[\s.-]?)?(?:\(\d{2,4}\)|\d{2,4})[\s.-]?\d{3,4}[\s.-]?\d{4}(?!\w)/g,
    kind: 'phone',
  },
]

function redactAssistantMessage(
  content: string,
  replacementFor: (kind: AssistantRedactionKind) => string
): AssistantMessageSafetyResult {
  let redacted = false
  let safeContent = content
  for (const { pattern, kind } of assistantSensitivePatterns) {
    safeContent = safeContent.replaceAll(pattern, () => {
      redacted = true
      return replacementFor(kind)
    })
  }
  return { content: safeContent, redacted }
}

export function redactAssistantMessageForDisplay(
  content: string,
  replacement: string
): AssistantMessageSafetyResult {
  return redactAssistantMessage(content, () => replacement)
}

/**
 * Sanitize text at the browser request boundary. The returned content is
 * safe to put into React state, a retry closure, or an API payload. Replacing
 * each class with a stable marker preserves enough context for the assistant
 * without retaining the original value anywhere in the conversation data.
 */
export function redactAssistantMessageForRequest(
  content: string
): AssistantMessageSafetyResult {
  const result = redactAssistantMessage(
    content,
    (kind) => ASSISTANT_REDACTION_PLACEHOLDERS[kind]
  )
  // The composer sanitizes before invoking the panel submit handler, and the
  // panel sanitizes again as a defense-in-depth boundary. Preserve the fact
  // that this is already-redacted text so the UI can show a non-blocking
  // notice without ever reintroducing the original value.
  if (
    !result.redacted &&
    Object.values(ASSISTANT_REDACTION_PLACEHOLDERS).some((marker) =>
      result.content.includes(marker)
    )
  ) {
    return { ...result, redacted: true }
  }
  return result
}

/** A message made only of redaction markers and formatting has no useful task. */
export function hasAssistantMessageSubstantialMeaning(
  content: string
): boolean {
  const withoutPlaceholders = content
    .replace(/\[(?:REDACTED_[A-Z0-9_]+)\]/g, '')
    .trim()
  return /[\p{L}\p{N}]/u.test(withoutPlaceholders)
}
