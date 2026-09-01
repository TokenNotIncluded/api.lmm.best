/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { api } from '@/lib/api'

import {
  COMPANY_BILLING_PROFILE_FIELDS,
  type CompanyBillingProfile,
  type CompanyBillingProfileField,
  type CompanyBillingProfileInput,
  type CompanyBillingProfileResponse,
} from './types'

const COMPANY_BILLING_PROFILE_PATH = '/api/user/company-billing-profile'
const FIELD_BY_TOKEN = new Map(
  COMPANY_BILLING_PROFILE_FIELDS.map((field) => [
    field.replaceAll(/[^a-z]/gi, '').toLowerCase(),
    field,
  ])
)

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function fieldFromToken(value: string): CompanyBillingProfileField | null {
  const token = value
    .split(/[.[\]/]/)
    .at(-1)
    ?.replaceAll(/[^a-z]/gi, '')
    .toLowerCase()
  return token ? (FIELD_BY_TOKEN.get(token) ?? null) : null
}

function collectFieldTokens(
  value: unknown,
  fields: Set<CompanyBillingProfileField>
) {
  if (typeof value === 'string') {
    const field = fieldFromToken(value)
    if (field) fields.add(field)
    return
  }
  if (Array.isArray(value)) {
    value.forEach((item) => collectFieldTokens(item, fields))
  }
}

function extractValidationFields(
  payload: unknown
): CompanyBillingProfileField[] {
  const fields = new Set<CompanyBillingProfileField>()

  function visit(value: unknown, depth: number) {
    if (depth > 4) return
    if (Array.isArray(value)) {
      value.forEach((item) => visit(item, depth + 1))
      return
    }
    if (!isRecord(value)) return

    for (const [key, nestedValue] of Object.entries(value)) {
      const field = fieldFromToken(key)
      if (field) fields.add(field)

      const token = key.replaceAll(/[^a-z]/gi, '').toLowerCase()
      if (['field', 'path', 'name', 'param', 'loc'].includes(token)) {
        collectFieldTokens(nestedValue, fields)
      } else if (
        [
          'data',
          'detail',
          'details',
          'errors',
          'fielderror',
          'fielderrors',
          'violations',
        ].includes(token)
      ) {
        visit(nestedValue, depth + 1)
      }
    }
  }

  visit(payload, 0)
  return COMPANY_BILLING_PROFILE_FIELDS.filter((field) => fields.has(field))
}

function isCanceledRequest(error: unknown): boolean {
  if (!isRecord(error)) return false
  return (
    error.code === 'ERR_CANCELED' ||
    error.name === 'CanceledError' ||
    error.name === 'AbortError'
  )
}

function getHttpError(error: unknown): { status?: number; data?: unknown } {
  if (!isRecord(error) || !isRecord(error.response)) return {}
  return {
    status:
      typeof error.response.status === 'number'
        ? error.response.status
        : undefined,
    data: error.response.data,
  }
}

export class CompanyBillingProfileRequestError extends Error {
  constructor() {
    super('Company billing profile request failed')
    this.name = 'CompanyBillingProfileRequestError'
  }
}

export class CompanyBillingProfileValidationError extends Error {
  readonly fields: readonly CompanyBillingProfileField[]

  constructor(fields: readonly CompanyBillingProfileField[]) {
    super('Company billing profile validation failed')
    this.name = 'CompanyBillingProfileValidationError'
    this.fields = fields
  }
}

export async function getCompanyBillingProfile(
  signal?: AbortSignal
): Promise<CompanyBillingProfile | null> {
  try {
    const response = await api.get<
      CompanyBillingProfileResponse<CompanyBillingProfile | null>
    >(COMPANY_BILLING_PROFILE_PATH, {
      signal,
      skipBusinessError: true,
      skipErrorHandler: true,
    })

    if (!response.data.success) throw new CompanyBillingProfileRequestError()
    return response.data.data
  } catch (error) {
    if (
      error instanceof CompanyBillingProfileRequestError ||
      isCanceledRequest(error)
    ) {
      throw error
    }
    throw new CompanyBillingProfileRequestError()
  }
}

export async function updateCompanyBillingProfile(
  input: CompanyBillingProfileInput
): Promise<CompanyBillingProfile> {
  const payload: CompanyBillingProfileInput = {
    country: input.country,
    isBusiness: input.isBusiness,
    postcode: input.postcode,
    state: input.state,
    businessName: input.businessName,
    taxId: input.taxId,
    useForInvoices: input.useForInvoices,
  }
  try {
    const response = await api.put<
      CompanyBillingProfileResponse<CompanyBillingProfile>
    >(COMPANY_BILLING_PROFILE_PATH, payload, {
      skipBusinessError: true,
      skipErrorHandler: true,
    })

    if (!response.data.success || !response.data.data) {
      const fields = extractValidationFields(response.data)
      if (fields.length > 0) {
        throw new CompanyBillingProfileValidationError(fields)
      }
      throw new CompanyBillingProfileRequestError()
    }
    return response.data.data
  } catch (error) {
    if (
      error instanceof CompanyBillingProfileRequestError ||
      error instanceof CompanyBillingProfileValidationError ||
      isCanceledRequest(error)
    ) {
      throw error
    }

    const httpError = getHttpError(error)
    if (httpError.status === 422) {
      const fields = extractValidationFields(httpError.data)
      if (fields.length > 0) {
        throw new CompanyBillingProfileValidationError(fields)
      }
    }
    throw new CompanyBillingProfileRequestError()
  }
}
