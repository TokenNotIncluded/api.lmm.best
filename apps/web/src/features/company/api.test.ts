/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { afterEach, describe, test } from 'node:test'

import { api } from '@/lib/api'

import {
  CompanyBillingProfileValidationError,
  getCompanyBillingProfile,
  updateCompanyBillingProfile,
} from './api'
import type { CompanyBillingProfile, CompanyBillingProfileInput } from './types'

const originalGet = api.get
const originalPut = api.put

const savedProfile: CompanyBillingProfile = {
  country: 'US',
  isBusiness: true,
  postcode: '10001',
  state: 'NY',
  businessName: 'Example Company',
  taxId: 'TEST-TAX-ID',
  useForInvoices: false,
  createdAt: 1_767_225_600,
  updatedAt: 1_767_312_000,
}

afterEach(() => {
  api.get = originalGet
  api.put = originalPut
})

describe('company billing profile API contract', () => {
  test('GET uses the authenticated self-service endpoint and accepts null data', async () => {
    const calls: unknown[] = []
    const controller = new AbortController()
    api.get = (async (url: string, config: unknown) => {
      calls.push({ url, config })
      return {
        data: { success: true, message: '', data: null },
      }
    }) as typeof api.get

    assert.equal(await getCompanyBillingProfile(controller.signal), null)
    assert.deepEqual(calls, [
      {
        url: '/api/user/company-billing-profile',
        config: {
          signal: controller.signal,
          skipBusinessError: true,
          skipErrorHandler: true,
        },
      },
    ])
  })

  test('PUT sends only the seven writable fields and returns server data', async () => {
    const calls: Array<{ url: string; body: unknown; config: unknown }> = []
    api.put = (async (url: string, body: unknown, config: unknown) => {
      calls.push({ url, body, config })
      return {
        data: { success: true, message: '', data: savedProfile },
      }
    }) as typeof api.put

    const input: CompanyBillingProfileInput = {
      country: savedProfile.country,
      isBusiness: savedProfile.isBusiness,
      postcode: savedProfile.postcode,
      state: savedProfile.state,
      businessName: savedProfile.businessName,
      taxId: savedProfile.taxId,
      useForInvoices: savedProfile.useForInvoices,
    }
    const runtimeInput = {
      ...input,
      createdAt: 1,
      updatedAt: 2,
      requiredFields: ['taxId'],
    }
    const result = await updateCompanyBillingProfile(runtimeInput)

    assert.equal(result.updatedAt, savedProfile.updatedAt)
    assert.equal(calls.length, 1)
    const firstCall = calls[0]
    assert.ok(firstCall)
    assert.equal(firstCall.url, '/api/user/company-billing-profile')
    assert.deepEqual(
      Object.keys(firstCall.body as Record<string, unknown>).sort(),
      [
        'businessName',
        'country',
        'isBusiness',
        'postcode',
        'state',
        'taxId',
        'useForInvoices',
      ]
    )
    assert.equal(
      'requiredFields' in (firstCall.body as Record<string, unknown>),
      false
    )
    assert.deepEqual(firstCall.config, {
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  })

  test('maps the backend 422 errors envelope without exposing its values', async () => {
    api.put = (async () => {
      throw {
        response: {
          status: 422,
          data: {
            success: false,
            message: 'Invalid company billing profile',
            errors: {
              country: 'invalid_country',
              taxId: 'too_long',
            },
          },
        },
      }
    }) as typeof api.put

    await assert.rejects(
      updateCompanyBillingProfile({
        country: 'US',
        isBusiness: true,
        postcode: '',
        state: '',
        businessName: '',
        taxId: '',
        useForInvoices: false,
      }),
      (error: unknown) => {
        assert.ok(error instanceof CompanyBillingProfileValidationError)
        assert.deepEqual(error.fields, ['country', 'taxId'])
        assert.equal(error.message.includes('invalid_country'), false)
        assert.equal(error.message.includes('too_long'), false)
        return true
      }
    )
  })
})
