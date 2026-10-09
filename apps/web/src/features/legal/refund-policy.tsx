/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useTranslation } from 'react-i18next'
import { getRefundPolicy } from './api'
import { LegalDocument } from './legal-document'

export function RefundPolicy() {
 const { t } = useTranslation()
 return <LegalDocument title={t('Refund Policy')} queryKey='refund-policy' fetchDocument={getRefundPolicy} emptyMessage={t('The administrator has not configured a separate refund policy yet. Check the user agreement and service terms for existing refund conditions.')} />
}
