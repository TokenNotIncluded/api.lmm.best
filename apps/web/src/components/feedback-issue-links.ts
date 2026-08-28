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
import { normalizeInterfaceLanguage } from '@/i18n/languages'

export type FeedbackIssueCategory = 'frontend' | 'feature' | 'bug'

const ISSUE_FORM_URL =
  'https://github.com/LIghtJUNction/api.lmm.best/issues/new'

const ISSUE_TEMPLATES = {
  frontend: {
    zh: 'frontend_improvement.yml',
    en: 'frontend_improvement_en.yml',
  },
  feature: {
    zh: 'feature_request.yml',
    en: 'feature_request_en.yml',
  },
  bug: {
    zh: 'bug_report.yml',
    en: 'bug_report_en.yml',
  },
} as const satisfies Record<FeedbackIssueCategory, { zh: string; en: string }>

export function getFeedbackIssueUrl(
  category: FeedbackIssueCategory,
  language: string
): string {
  const locale = normalizeInterfaceLanguage(language)
  const templateLanguage = locale === 'zhCN' || locale === 'zhTW' ? 'zh' : 'en'
  const template = ISSUE_TEMPLATES[category]?.[templateLanguage]
  if (!template) return ISSUE_FORM_URL
  return `${ISSUE_FORM_URL}?template=${template}`
}
