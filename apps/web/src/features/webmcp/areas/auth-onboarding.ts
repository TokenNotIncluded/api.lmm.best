/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { requestAssistantOpen } from '@/features/assistant/assistant-events'
import {
  getDisabledOAuthRegistrationMethods,
  hasOAuthRegistrationProvider,
  hasRegistrationMethod,
  isOAuthProviderConfigured,
  isPasswordRegistrationEnabled,
  isRegistrationEnabled,
} from '@/features/auth/lib/registration'
import type { SystemStatus } from '@/features/auth/types'
import { getL0PaidAccess } from '@/features/onboarding/l0-paid-access'
import { getAccountNextStep } from '@/features/onboarding/next-step'
import { getStatus } from '@/lib/api'
import { getOnboardingState } from '@/lib/console-activation'
import { useAuthStore } from '@/stores/auth-store'

import {
  clip,
  EMPTY_INPUT_SCHEMA,
  ensureNotAborted,
  ensureObject,
  optionalEnum,
  requireSignedIn,
  type ModelContextTool,
  type WebMcpToolFactory,
} from '../tool-kit'

/** Sign-in entry points a browser agent may open. It never submits a form. */
const AUTH_PAGES = [
  '/sign-in',
  '/sign-up',
  '/forgot-password',
  '/otp',
  '/reset',
] as const
type AuthPage = (typeof AUTH_PAGES)[number]

/** Presets an agent may hand to the assistant; free-form text stays a human's job. */
const ASSISTANT_PRESETS = ['onboarding', 'human', 'plan'] as const

const OAUTH_METHODS = [
  'github',
  'discord',
  'oidc',
  'linuxdo',
  'telegram',
  'wechat',
] as const

function readStatus(status: SystemStatus | null): {
  method: string
  label: string
  sign_in: boolean
  register: boolean
}[] {
  const disabled = getDisabledOAuthRegistrationMethods(status)
  const registrationOpen = hasOAuthRegistrationProvider(status)
  const rows = OAUTH_METHODS.map((method) => ({
    method,
    label:
      method === 'github'
        ? 'GitHub'
        : method === 'oidc'
          ? (status?.oidc_display_name ?? 'OIDC')
          : method === 'linuxdo'
            ? 'LinuxDO'
            : method[0].toUpperCase() + method.slice(1),
    sign_in: isOAuthProviderConfigured(status, method),
    register:
      registrationOpen &&
      isOAuthProviderConfigured(status, method) &&
      !disabled.has(method),
  }))
  const custom = (status?.custom_oauth_providers ?? [])
    .map((provider) => provider.slug.trim())
    .filter(Boolean)
    .slice(0, 50)
    .map((slug) => ({
      method: `custom:${slug}`,
      label: slug,
      sign_in: isOAuthProviderConfigured(status, `custom:${slug}`),
      register:
        registrationOpen &&
        isOAuthProviderConfigured(status, `custom:${slug}`) &&
        !disabled.has(`custom:${slug}`.toLowerCase()),
    }))
  return [...rows, ...custom].filter((row) => row.sign_in || row.register)
}

/** Live capabilities only: a cached snapshot must not advertise a disabled path. */
async function readSystemStatus(signal: AbortSignal): Promise<SystemStatus> {
  ensureNotAborted(signal)
  const data = await getStatus()
  ensureNotAborted(signal)
  return data as SystemStatus
}

function onboardingSnapshot() {
  const user = useAuthStore.getState().auth.user
  const state = getOnboardingState(user)
  const access = getL0PaidAccess(user)
  const next = getAccountNextStep(user)
  return {
    authenticated: Boolean(user),
    developer_access_granted: user?.developer_access_granted === true,
    stage: state.stage,
    activation_complete: state.activationComplete,
    credential_complete: state.credentialComplete,
    first_request_complete: state.firstRequestComplete,
    trust_level: user?.trust_level_info?.level ?? null,
    // Presentation only; the server is the sole authority on access.
    paid_access: {
      mode: access.mode,
      paid_amount_usd: access.paid,
      threshold_usd: access.threshold,
      remaining_usd: access.remaining,
      note: 'Displayed progress only. Topping up does not itself grant developer access; the server decides.',
    },
    next_step: { path: next.to, label: next.label },
  }
}

export const authOnboardingTools: WebMcpToolFactory = ({ router }) => {
  const tools: ModelContextTool[] = [
    {
      name: 'lmm_auth_methods',
      title: 'Read available sign-in methods',
      description:
        'Read which sign-in and registration methods the server currently offers: password login, password registration, open registration, and each configured OAuth provider with whether it can sign in and whether it can create an account. Never returns credentials, secrets, client IDs, or tokens.',
      inputSchema: EMPTY_INPUT_SCHEMA,
      annotations: { readOnlyHint: true, untrustedContentHint: true },
      execute: async (_input, options) => {
        const status = await readSystemStatus(options.signal)
        ensureNotAborted(options.signal)
        const providers = readStatus(status)
        return {
          registration_open: isRegistrationEnabled(status),
          // Absent means the server did not restrict it; never advertise a
          // sign-in path as closed on missing data.
          password_login:
            (status.password_login_enabled ??
              status.data?.password_login_enabled ??
              true) !== false,
          password_registration: isPasswordRegistrationEnabled(status),
          can_create_account: hasRegistrationMethod(status),
          providers,
          sign_in_path: '/sign-in',
          sign_up_path: '/sign-up',
          note: 'A method is listed only when the server reports it configured. Opening a sign-in page never signs anyone in.',
        }
      },
    },
    {
      name: 'lmm_auth_open',
      title: 'Open a sign-in or account-recovery page',
      description:
        'Open one of the authentication pages in the current tab: sign in, create an account, forgot password, one-time code, or password reset. This only navigates. It never fills a form, submits a password or verification code, or signs in or registers on the user’s behalf — the human completes the form.',
      inputSchema: {
        type: 'object',
        properties: {
          page: { type: 'string', enum: AUTH_PAGES },
        },
        required: ['page'],
        additionalProperties: false,
      },
      annotations: { readOnlyHint: true },
      execute: async (rawInput, options) => {
        const input = ensureObject(rawInput)
        const page = optionalEnum(input, 'page', AUTH_PAGES)
        if (!page) throw new TypeError('page is required')
        ensureNotAborted(options.signal)
        await router.navigate({ to: page as AuthPage })
        return { path: page, opened: true }
      },
    },
    {
      name: 'lmm_onboarding_status',
      title: 'Read onboarding stage and next step',
      description:
        'Read the current account onboarding state: the stage, which of activation, credential, and first-request steps are complete, the trust level, the L0 to L1 progress (credited amount, threshold, amount still missing), a recommended next step, and which assistant preset fits. Works while signed out. Never returns credentials or private profile data.',
      inputSchema: EMPTY_INPUT_SCHEMA,
      annotations: { readOnlyHint: true },
      execute: async (_input, options) => {
        ensureNotAborted(options.signal)
        return onboardingSnapshot()
      },
    },
    {
      name: 'lmm_onboarding_open_assistant',
      title: 'Open onboarding help',
      description:
        'Open onboarding help for a signed-in account. Unactivated accounts use the existing inline getting-started page; plan and human presets open the wallet or support page. Activated accounts use the console assistant. This tool sends no message and never opens a second assistant for an L0 account.',
      inputSchema: {
        type: 'object',
        properties: {
          preset: { type: 'string', enum: ASSISTANT_PRESETS },
        },
        required: ['preset'],
        additionalProperties: false,
      },
      annotations: { readOnlyHint: true },
      execute: async (rawInput, options) => {
        const user = requireSignedIn()
        const input = ensureObject(rawInput)
        const preset = optionalEnum(input, 'preset', ASSISTANT_PRESETS)
        if (!preset) throw new TypeError('preset is required')
        ensureNotAborted(options.signal)
        if (!getOnboardingState(user).activationComplete) {
          const path =
            preset === 'plan'
              ? '/wallet'
              : preset === 'human'
                ? '/support'
                : '/getting-started'
          await router.navigate({ to: path })
          return { preset, opened: true, path, mode: 'inline' }
        }
        requestAssistantOpen(preset)
        return { preset, opened: true }
      },
    },
    {
      name: 'lmm_setup_status',
      title: 'Read first-install setup status',
      description:
        'Read the public first-install setup flag, version and system name. It never reads or writes setup credentials, and it cannot complete setup for anyone; the operator fills that form by hand.',
      inputSchema: EMPTY_INPUT_SCHEMA,
      annotations: { readOnlyHint: true, untrustedContentHint: true },
      execute: async (_input, options) => {
        const status = await readSystemStatus(options.signal)
        ensureNotAborted(options.signal)
        const setup = status.setup ?? status.data?.setup
        return {
          setup_required: typeof setup === 'boolean' ? !setup : null,
          setup_path: '/setup',
          version: clip(status.version ?? status.data?.version, 40),
          system_name: clip(status.system_name ?? status.data?.system_name, 80),
        }
      },
    },
  ]
  return tools
}
