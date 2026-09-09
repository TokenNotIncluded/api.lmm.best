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
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  isExplicitAssistantHandoff,
  isAssistantSupportActive,
  isAssistantSupportAIPaused,
  type AssistantSupportRequest,
} from './assistant-support-api'

test('transfer intent accepts commands and leaves questions, quotations and negation to AI', () => {
  for (const command of [
    '转人工',
    '我要转人工',
    '现在转人工',
    '接口不通，转人工吧',
    '請幫我轉人工，我的接口還是報錯',
    'I need to speak to a human agent',
    '请帮我转人工客服！',
    '麻烦联系人工技术支持',
    'please connect me to a human agent',
  ]) {
    assert.equal(isExplicitAssistantHandoff(command), true, command)
  }
  for (const other of [
    '不要转人工',
    '怎么转人工？',
    '他说“转人工”',
    '示例：“接口不通，转人工吧”',
    '不要现在转人工',
    '现在转人工吗？',
    'Can I speak to a human agent?',
    'Explain `transfer me to a human`',
    '不需要人工客服',
    '取消转人工',
    'Explain what transfer to a human means',
  ]) {
    assert.equal(isExplicitAssistantHandoff(other), false, other)
  }
})
test('only pending handoffs and accepted support pause AI', () => {
  for (const kind of ['handoff', 'appointment'] as const) {
    for (const status of [
      'pending',
      'accepted',
      'completed',
      'cancelled',
    ] as const) {
      const request = { kind, status } as AssistantSupportRequest
      assert.equal(
        isAssistantSupportActive(request),
        status === 'pending' || status === 'accepted'
      )
      assert.equal(
        isAssistantSupportAIPaused(request),
        status === 'accepted' || (status === 'pending' && kind === 'handoff')
      )
    }
  }
})
