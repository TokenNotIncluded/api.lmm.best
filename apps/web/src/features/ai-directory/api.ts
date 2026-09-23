/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { getStatus } from '@/lib/api'

export const AI_DIRECTORY_CATEGORIES = [
  'chat',
  'research',
  'developer',
  'creative',
  'other',
] as const

export type AIDirectoryCategory = (typeof AI_DIRECTORY_CATEGORIES)[number]

export type AIDirectoryLink = {
  id: string
  name: string
  url: string
  category: AIDirectoryCategory
  summary: string
  description: string
  enabled: boolean
}

export const DEFAULT_AI_DIRECTORY_LINKS: AIDirectoryLink[] = [
  { id: 'chatgpt', name: 'ChatGPT', url: 'https://chatgpt.com', category: 'chat', summary: "OpenAI's AI assistant", description: "Talk through ideas, write, study, and work with files in OpenAI's chat app.", enabled: true },
  { id: 'gemini', name: 'Gemini', url: 'https://gemini.google.com', category: 'chat', summary: "Google's AI assistant", description: "Explore ideas, write, plan, and work with Google's Gemini app.", enabled: true },
  { id: 'grok', name: 'Grok', url: 'https://grok.com', category: 'chat', summary: "xAI's AI assistant", description: "Chat, search, and create with xAI's Grok web app.", enabled: true },
  { id: 'deepseek', name: 'DeepSeek', url: 'https://chat.deepseek.com', category: 'chat', summary: "DeepSeek's AI chat", description: "Use DeepSeek's web app for questions, writing, and reasoning.", enabled: true },
  { id: 'claude', name: 'Claude', url: 'https://claude.ai', category: 'chat', summary: "Anthropic's AI assistant", description: "Work through writing, analysis, and coding with Anthropic's Claude.", enabled: true },
  { id: 'perplexity', name: 'Perplexity', url: 'https://www.perplexity.ai', category: 'research', summary: 'AI answers with sources', description: 'Research questions on the web and follow the cited sources.', enabled: true },
  { id: 'copilot', name: 'Microsoft Copilot', url: 'https://copilot.microsoft.com', category: 'chat', summary: "Microsoft's AI assistant", description: "Chat, create, and search with Microsoft's Copilot on the web.", enabled: true },
  { id: 'kimi', name: 'Kimi', url: 'https://www.kimi.com', category: 'chat', summary: "Moonshot AI's assistant", description: 'Use Kimi for conversations, research, and working with documents.', enabled: true },
  { id: 'qwen', name: 'Qwen', url: 'https://chat.qwen.ai', category: 'chat', summary: "Qwen's AI chat", description: 'Chat and create with the Qwen web app.', enabled: true },
  { id: 'mistral', name: 'Mistral Vibe', url: 'https://chat.mistral.ai', category: 'chat', summary: "Mistral's AI workspace", description: "Open Mistral's web workspace for chat and productivity.", enabled: true },
  { id: 'huggingface', name: 'Hugging Face', url: 'https://huggingface.co/models', category: 'developer', summary: 'Explore open models', description: 'Browse model pages, demos, and resources from the Hugging Face community.', enabled: true },
]

export function safeDirectoryUrl(value: string): string | null {
  try {
    const parsed = new URL(value)
    if (
      !['https:', 'http:'].includes(parsed.protocol) ||
      !parsed.hostname ||
      parsed.username ||
      parsed.password
    ) {
      return null
    }
    return parsed.href
  } catch {
    return null
  }
}

export function parseDirectoryLinks(value: string): AIDirectoryLink[] | null {
  if (!value.trim()) return DEFAULT_AI_DIRECTORY_LINKS
  try {
    const container: unknown = JSON.parse(value)
    if (!container || typeof container !== 'object' || Array.isArray(container)) return null
    const parsed = (container as Record<string, unknown>).aiDirectoryLinks
    if (parsed === undefined) return DEFAULT_AI_DIRECTORY_LINKS
    if (!Array.isArray(parsed)) return null
    if (
      parsed.some(
        (item) =>
          !item ||
          typeof item.id !== 'string' ||
          typeof item.name !== 'string' ||
          typeof item.url !== 'string' ||
          typeof item.category !== 'string' ||
          typeof item.summary !== 'string' ||
          typeof item.description !== 'string' ||
          typeof item.enabled !== 'boolean'
      )
    ) {
      return null
    }
    if (parsed.length > 60) return null
    const ids = new Set<string>()
    for (const item of parsed as AIDirectoryLink[]) {
      if (
        !/^[a-zA-Z0-9_-]{1,64}$/.test(item.id) ||
        ids.has(item.id) ||
        !item.name.trim() ||
        item.name.length > 80 ||
        item.summary.length > 180 ||
        item.description.length > 1200 ||
        !AI_DIRECTORY_CATEGORIES.includes(item.category) ||
        !safeDirectoryUrl(item.url)
      ) {
        return null
      }
      ids.add(item.id)
    }
    return parsed as AIDirectoryLink[]
  } catch {
    return null
  }
}

export async function getAIDirectory(): Promise<AIDirectoryLink[]> {
  const status = await getStatus()
  const raw = status.HeaderNavModules
  return parseDirectoryLinks(typeof raw === 'string' ? raw : '') ?? DEFAULT_AI_DIRECTORY_LINKS
}
