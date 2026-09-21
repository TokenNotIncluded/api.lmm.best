/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

export const COMPANY_BILLING_PROFILE_FIELDS = [
  'country',
  'isBusiness',
  'postcode',
  'state',
  'businessName',
  'taxId',
  'useForInvoices',
] as const

export type CompanyBillingProfileField =
  (typeof COMPANY_BILLING_PROFILE_FIELDS)[number]

export interface CompanyBillingProfileInput {
  country: string
  isBusiness: boolean
  postcode: string
  state: string
  businessName: string
  taxId: string
  useForInvoices: boolean
}

export interface CompanyBillingProfile extends CompanyBillingProfileInput {
  createdAt: number
  updatedAt: number
}

export interface CompanyBillingProfileResponse<T> {
  success: boolean
  message: string
  data: T
}
