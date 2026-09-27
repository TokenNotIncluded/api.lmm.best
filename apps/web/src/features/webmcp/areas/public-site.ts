/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { getAboutContent } from '@/features/about/api'
import {
  LMM_ISSUER,
  LMM_SOURCE,
} from '@/features/developers/integration-prompts'
import {
  buildRequestBody,
  buildRequestSnippet,
} from '@/features/developers/request-snippet'
import { getPrivacyPolicy, getUserAgreement } from '@/features/legal/api'
import {
  splitLegalSections,
  type LegalHeading,
  type LegalSection,
} from '@/features/legal/legal-reader'
import { listBounties } from '@/features/open-source-bounties/api'
import type { BountyProject } from '@/features/open-source-bounties/types'
import { getLatestUnreadReleaseNote } from '@/features/release-notes/api'
import { getStatus } from '@/lib/api'

import {
  clip,
  EMPTY_INPUT_SCHEMA,
  ensureNotAborted,
  ensureObject,
  optionalEnum,
  optionalString,
  requiredString,
  type WebMcpToolFactory,
} from '../tool-kit'

/**
 * Public-site area tools: the read-only, marketing-facing surfaces an agent
 * may explore without an account — home, about, developers, the getting
 * started guide, release notes, the legal documents and the open challenges.
 *
 * Every tool here is read-only. Nothing in this module writes, spends credit
 * or opens a private page; anything that needs an account or moves money
 * belongs to another area.
 */

/** The guide's jump targets, in reading order, mirrored from the page TOC. */
const GUIDE_SECTIONS = [
  {
    id: 'client-setup',
    title: 'Choose a client and install it',
    anchors: ['#client-setup'],
  },
  {
    id: 'guide-first-message',
    title: 'Send your first message',
    anchors: ['#guide-first-message'],
  },
  {
    id: 'guide-troubleshooting',
    title: 'Common errors: 401, 404 and 429',
    anchors: ['#guide-troubleshooting'],
  },
  {
    id: 'guide-support',
    title: 'Account setup and support',
    anchors: ['#guide-support'],
  },
] as const

/** The developer guide's sections, mirrored from the page nav. */
const DEVELOPER_SECTIONS = [
  {
    id: 'request-builder',
    title: 'Request builder',
    summary: 'Build a cURL, JavaScript or Python request for any protocol.',
    anchors: ['#request-builder'],
  },
  {
    id: 'pricing-api',
    title: 'Pricing API',
    summary: 'Read public prices and the account catalog from your backend.',
    anchors: ['#pricing-api'],
  },
  {
    id: 'oauth-integration',
    title: 'OAuth integration',
    summary: 'Register a native client and connect with PKCE (S256).',
    anchors: ['#oauth-integration'],
  },
] as const

/** The three request shapes the request builder can produce. */
const PROTOCOL_PATHS = {
  'chat-completions': { path: '/v1/chat/completions', body: 'openai' },
  'claude-messages': { path: '/v1/messages', body: 'anthropic' },
  'gemini-generate': {
    path: '/v1beta/models/{model}:generateContent',
    body: 'gemini',
  },
} as const

const LEGAL_DOCUMENTS = {
  'user-agreement': { path: '/user-agreement', title: 'User agreement' },
  'privacy-policy': { path: '/privacy-policy', title: 'Privacy policy' },
  'terms-of-service': { path: '/terms-of-service', title: 'Terms of service' },
} as const

type LegalDocumentKey = keyof typeof LEGAL_DOCUMENTS

const LEGAL_DOCUMENT_KEYS = [
  'user-agreement',
  'privacy-policy',
  'terms-of-service',
] as const satisfies readonly LegalDocumentKey[]

const PROTOCOL_KEYS = [
  'chat-completions',
  'claude-messages',
  'gemini-generate',
] as const

type ProtocolKey = keyof typeof PROTOCOL_PATHS

const LANGUAGES = ['curl', 'javascript', 'python'] as const

function siteOrigin() {
  return typeof window === 'undefined' ? LMM_ISSUER : window.location.origin
}

/** Public challenges arrive from a probe that may legitimately be absent. */
function challengeSummary(project: BountyProject) {
  return {
    id: project.id,
    title: clip(project.title, 200),
    repository: clip(project.repository_url, 200),
    owner: project.owner_username,
    status: project.status,
    reward_credit: project.net_reward_quota || project.reward_quota,
    reward_slots: project.reward_slots,
    approved_slots: project.approved_challenge_count,
    open: project.status === 'published',
  }
}

async function readLegalDocument(
  document: LegalDocumentKey,
  language: string | undefined
) {
  const response =
    document === 'privacy-policy'
      ? await getPrivacyPolicy(language)
      : await getUserAgreement(language)
  if (!response?.success || typeof response.data !== 'string') {
    throw new Error(response?.message || 'Legal document unavailable')
  }
  return response.data
}

/**
 * The legal endpoints return markdown or html depending on the deployment, so
 * the splitter is asked for markdown first and falls back to html when it
 * found no headings at all.
 */
function readLegalSections(content: string): LegalSection[] {
  const markdown = splitLegalSections(content, 'markdown').sections
  if (markdown.some((section) => section.heading)) return markdown
  return splitLegalSections(content, 'html').sections
}

/** Only sections that actually carry a heading can serve as jump targets. */
function headedSections(sections: LegalSection[]) {
  return sections.flatMap((section) =>
    section.heading ? [{ section, heading: section.heading }] : []
  )
}

function legalSectionOutline(
  entry: { section: LegalSection; heading: LegalHeading },
  index: number
) {
  return {
    index,
    level: entry.heading.level,
    title: clip(entry.heading.text, 160),
    anchor: `#${entry.heading.id}`,
  }
}

export const publicSiteTools: WebMcpToolFactory = ({ router }) => [
  {
    name: 'lmm_home_read',
    title: 'Read the LMM home page summary',
    description:
      'Read what LMM is in one call: the products behind the single endpoint, the connection methods, the public entry points and whether sign-up or a top-up is needed next. Read-only.',
    inputSchema: EMPTY_INPUT_SCHEMA,
    annotations: { readOnlyHint: true },
    execute: async (_rawInput, options) => {
      ensureNotAborted(options.signal)
      const status = await getStatus().catch(() => null)
      ensureNotAborted(options.signal)
      return {
        page: '/',
        product: {
          name: 'LMM',
          summary: 'One base URL for chat, reasoning, vision and audio models.',
          base_url: `${siteOrigin()}/v1`,
          protocols: [
            'OpenAI Chat Completions',
            'Claude Messages',
            'Gemini generateContent',
            'OpenAI-compatible image generation',
          ],
          client_install_required: false,
        },
        start_here: [
          {
            step: 1,
            action: 'Create an account',
            path: '/sign-up',
            note: 'Or sign in with OAuth from Pi or another supported client.',
          },
          {
            step: 2,
            action: 'Top up credit to unlock developer access',
            path: '/wallet',
            note: 'Wallet shows the current access requirements and credited progress.',
          },
          {
            step: 3,
            action: 'Create an API key, or skip it with OAuth',
            path: '/keys',
            note: 'Clients that support IAM OAuth need no manual key.',
          },
          {
            step: 4,
            action: 'Point your client at the base URL',
            path: '/guide',
          },
        ],
        entry_points: [
          { path: '/guide', purpose: 'Getting started guide' },
          { path: '/developers', purpose: 'API and OAuth documentation' },
          { path: '/pricing', purpose: 'Model prices before you spend' },
        ],
        assistant_enabled:
          (status?.assistant as { enabled?: boolean } | undefined)?.enabled ??
          null,
        capabilities_reachable: Boolean(status),
      }
    },
  },
  {
    name: 'lmm_about_read',
    title: 'Read the about page content',
    description:
      'Read the public about content: the site description the administrator published, the licence and the source repository. Read-only.',
    inputSchema: EMPTY_INPUT_SCHEMA,
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (_rawInput, options) => {
      ensureNotAborted(options.signal)
      const response = await getAboutContent()
      ensureNotAborted(options.signal)
      const content = typeof response?.data === 'string' ? response.data : ''
      return {
        page: '/about',
        configured: content.trim().length > 0,
        content: content.trim() ? clip(content, 1200) : null,
        licence: 'AGPL-3.0-or-later',
        source_repository: LMM_SOURCE,
        note: content.trim()
          ? null
          : 'No about content is published yet. The page shows the licence and quick links.',
      }
    },
  },
  {
    name: 'lmm_developers_read',
    title: 'Read the developer endpoints and snippets',
    description:
      'Read the public developer documentation as data: every documented endpoint with its method, the request-builder protocols and the section anchors. Read-only.',
    inputSchema: EMPTY_INPUT_SCHEMA,
    annotations: { readOnlyHint: true },
    execute: async (_rawInput, options) => {
      ensureNotAborted(options.signal)
      const origin = siteOrigin()
      return {
        page: '/developers',
        base_url: `${origin}/v1`,
        issuer: origin,
        sections: DEVELOPER_SECTIONS.map((section) => ({
          id: section.id,
          title: section.title,
          summary: section.summary,
          anchor: `#${section.id}`,
        })),
        endpoints: [
          {
            method: 'POST',
            path: '/v1/chat/completions',
            purpose: 'OpenAI-compatible chat completions',
          },
          {
            method: 'POST',
            path: '/v1/messages',
            purpose: 'Claude Messages compatible chat',
          },
          {
            method: 'POST',
            path: '/v1beta/models/{model}:generateContent',
            purpose: 'Gemini compatible generation',
          },
          {
            method: 'GET',
            path: '/api/pricing',
            purpose: 'Public model prices',
          },
          {
            method: 'GET',
            path: '/api/oauth2/catalog',
            purpose: 'Account catalog for OAuth clients',
          },
          {
            method: 'GET',
            path: '/.well-known/oauth-authorization-server',
            purpose: 'OAuth discovery document',
          },
          {
            method: 'GET',
            path: '/api/oauth2/authorize',
            purpose: 'OAuth authorization endpoint',
          },
          {
            method: 'POST',
            path: '/api/oauth2/token',
            purpose: 'OAuth token exchange',
          },
          {
            method: 'POST',
            path: '/api/oauth2/revoke',
            purpose: 'OAuth token revocation',
          },
        ],
        oauth: {
          flow: 'Authorization Code with PKCE (S256)',
          openid_connect: false,
          limitation:
            'No userinfo endpoint, ID token or public client registration.',
        },
      }
    },
  },
  {
    name: 'lmm_developers_example',
    title: 'Read a request example snippet',
    description:
      'Build a copy-ready request example for one protocol and language, using the public base URL and a placeholder key. Nothing is sent. Read-only.',
    inputSchema: {
      type: 'object',
      properties: {
        protocol: { type: 'string', enum: PROTOCOL_KEYS },
        language: { type: 'string', enum: LANGUAGES },
        model: { type: 'string', maxLength: 128 },
      },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true },
    execute: async (rawInput, options) => {
      const input = ensureObject(rawInput)
      const protocol: ProtocolKey =
        optionalEnum(input, 'protocol', PROTOCOL_KEYS) ?? 'chat-completions'
      const language = optionalEnum(input, 'language', LANGUAGES) ?? 'curl'
      const model = optionalString(input, 'model', 128) ?? 'your-model-id'
      ensureNotAborted(options.signal)
      const origin = siteOrigin()
      const target = PROTOCOL_PATHS[protocol]
      const url = `${origin}${target.path.replace('{model}', encodeURIComponent(model))}`
      const body = buildRequestBody(target.body, model, 'Hello')
      const snippet = buildRequestSnippet(language, url, body)
      return {
        page: '/developers',
        protocol,
        language,
        url,
        shape: target.body,
        snippet,
        copy_paste_safe: true,
        note: 'Placeholder key only. The request is never sent from the browser.',
      }
    },
  },
  {
    name: 'lmm_guide_read',
    title: 'Read the getting started guide contents',
    description:
      'Read the guide as data: the five setup stages, the account checklist, the jump targets and the troubleshooting error codes. Read-only.',
    inputSchema: EMPTY_INPUT_SCHEMA,
    annotations: { readOnlyHint: true },
    execute: async (_rawInput, options) => {
      ensureNotAborted(options.signal)
      return {
        page: '/guide',
        base_url: `${siteOrigin()}/v1`,
        stages: [
          'Choose an app',
          'Download and install',
          'Request access and create a key',
          'Configure your client',
          'Send your first message',
        ],
        account_steps: [
          'Get API access',
          'Create an API key',
          'Complete your first request',
        ],
        oauth_clients: ['Pi', 'DSH Desktop'],
        oauth_note:
          'Pi and DSH use browser authorization; no manual key is pasted.',
        troubleshooting: [
          {
            code: '401',
            title: 'API key authentication failed',
            fix: 'Recopy the key, check for spaces, and confirm it is not revoked.',
          },
          {
            code: '404',
            title: 'Check the endpoint and model',
            fix: 'Avoid a duplicated /v1 and use an exact model ID.',
          },
          {
            code: '429',
            title: 'A request limit was reached',
            fix: 'Wait, lower concurrency, or check key quota and balance.',
          },
        ],
        support: {
          email: 'support@lmm.best',
          ticket_path: '/support',
          page_anchor: '#guide-support',
        },
      }
    },
  },
  {
    name: 'lmm_guide_open_section',
    title: 'Open a chapter of the guide',
    description:
      'Open the getting started guide and scroll to one of its chapters. Navigates the current tab. Read-only.',
    inputSchema: {
      type: 'object',
      properties: {
        section: { type: 'string', enum: GUIDE_SECTIONS.map((s) => s.id) },
      },
      required: ['section'],
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true },
    execute: async (rawInput, options) => {
      const input = ensureObject(rawInput)
      const section = optionalEnum(
        input,
        'section',
        GUIDE_SECTIONS.map((s) => s.id)
      )
      if (!section) throw new TypeError('section is required')
      ensureNotAborted(options.signal)
      await router.navigate({ to: '/guide' })
      return {
        page: '/guide',
        section,
        anchor: `#${section}`,
        navigated: true,
      }
    },
  },
  {
    name: 'lmm_release_notes_read',
    title: 'Read the latest release note',
    description:
      'Read the newest unread release note for the signed-in account, with its version, timestamp and content. Read-only.',
    inputSchema: EMPTY_INPUT_SCHEMA,
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (_rawInput, options) => {
      ensureNotAborted(options.signal)
      const note = await getLatestUnreadReleaseNote()
      ensureNotAborted(options.signal)
      if (!note) {
        return {
          available: false,
          note: 'No release notes are available to this account.',
        }
      }
      return {
        available: true,
        version: note.version,
        revision: note.revision,
        published_at: new Date(note.published_at * 1000).toISOString(),
        content: clip(note.content, 1200),
        content_was_clipped: note.content.length > 1200,
      }
    },
  },
  {
    name: 'lmm_legal_outline',
    title: 'Read a legal document table of contents',
    description:
      'Read the section headings of the user agreement, privacy policy or terms of service, with the anchor for each section. Read-only.',
    inputSchema: {
      type: 'object',
      properties: {
        document: { type: 'string', enum: LEGAL_DOCUMENT_KEYS },
        language: { type: 'string', maxLength: 16 },
      },
      required: ['document'],
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (rawInput, options) => {
      const input = ensureObject(rawInput)
      const document: LegalDocumentKey | undefined = optionalEnum(
        input,
        'document',
        LEGAL_DOCUMENT_KEYS
      )
      if (!document) throw new TypeError('document is required')
      const language = optionalString(input, 'language', 16)
      ensureNotAborted(options.signal)
      const content = await readLegalDocument(document, language)
      ensureNotAborted(options.signal)
      const sections = headedSections(readLegalSections(content))
      return {
        page: LEGAL_DOCUMENTS[document].path,
        document,
        title: LEGAL_DOCUMENTS[document].title,
        section_count: sections.length,
        sections: sections.slice(0, 50).map(legalSectionOutline),
      }
    },
  },
  {
    name: 'lmm_legal_section_read',
    title: 'Read one legal document section',
    description:
      'Read the full text of a single legal section by its heading text, matched case-insensitively. Returns the closest match when no heading matches exactly. Read-only.',
    inputSchema: {
      type: 'object',
      properties: {
        document: { type: 'string', enum: LEGAL_DOCUMENT_KEYS },
        heading: { type: 'string', maxLength: 160 },
        language: { type: 'string', maxLength: 16 },
        confirm: { type: 'boolean' },
      },
      required: ['document', 'heading'],
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (rawInput, options) => {
      const input = ensureObject(rawInput)
      const document: LegalDocumentKey | undefined = optionalEnum(
        input,
        'document',
        LEGAL_DOCUMENT_KEYS
      )
      if (!document) throw new TypeError('document is required')
      const heading = requiredString(input, 'heading', 160).toLowerCase()
      const language = optionalString(input, 'language', 16)
      ensureNotAborted(options.signal)
      const content = await readLegalDocument(document, language)
      ensureNotAborted(options.signal)
      const sections = headedSections(readLegalSections(content))
      if (sections.length === 0) {
        throw new Error('This document has no sections to read')
      }
      const match =
        sections.find(
          (entry) => entry.heading.text.toLowerCase() === heading
        ) ??
        sections.find((entry) =>
          entry.heading.text.toLowerCase().includes(heading)
        )
      if (!match) {
        return {
          page: LEGAL_DOCUMENTS[document].path,
          document,
          matched: false,
          available_headings: sections
            .slice(0, 30)
            .map((entry) => clip(entry.heading.text, 120)),
        }
      }
      return {
        page: LEGAL_DOCUMENTS[document].path,
        document,
        matched: true,
        title: clip(match.heading.text, 160),
        anchor: `#${match.heading.id}`,
        body: clip(match.section.body, 2000),
        body_was_clipped: match.section.body.length > 2000,
      }
    },
  },
  {
    name: 'lmm_challenges_list',
    title: 'List the open challenges',
    description:
      'List the public open-source bounty challenges: reward credit, funded slots, repository and owner, plus the detail path for each. Read-only.',
    inputSchema: {
      type: 'object',
      properties: {
        open_only: { type: 'boolean' },
        limit: { type: 'integer', minimum: 1, maximum: 50 },
      },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, untrustedContentHint: true },
    execute: async (rawInput, options) => {
      const input = ensureObject(rawInput)
      const limit = typeof input.limit === 'number' ? input.limit : 20
      if (!Number.isInteger(limit) || limit < 1 || limit > 50) {
        throw new TypeError('limit must be an integer from 1 to 50')
      }
      const openOnly = input.open_only === true
      ensureNotAborted(options.signal)
      const result = await listBounties()
      ensureNotAborted(options.signal)
      const items = result.items
        .filter((project) => !openOnly || project.status === 'published')
        .slice(0, limit)
        .map(challengeSummary)
      return {
        page: '/challenges',
        total_published: result.total,
        returned: items.length,
        items: items.map((item) => ({
          ...item,
          detail_path: `/challenges/${item.id}`,
        })),
      }
    },
  },
  {
    name: 'lmm_challenge_open',
    title: 'Open a challenge detail page',
    description:
      'Open the public detail page for one open-source bounty challenge by its id. Navigates the current tab. Read-only.',
    inputSchema: {
      type: 'object',
      properties: { challenge_id: { type: 'integer', minimum: 1 } },
      required: ['challenge_id'],
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true },
    execute: async (rawInput, options) => {
      const input = ensureObject(rawInput)
      const id = input.challenge_id
      if (typeof id !== 'number' || !Number.isInteger(id) || id < 1) {
        throw new TypeError('challenge_id must be a positive integer')
      }
      ensureNotAborted(options.signal)
      // `WebMcpRouter.navigate` only models `to` + `search`; the concrete path
      // is fully interpolated here, so no `params` bag is needed.
      await router.navigate({ to: `/challenges/${id}` })
      return {
        page: `/challenges/${id}`,
        challenge_id: id,
        navigated: true,
      }
    },
  },
]
