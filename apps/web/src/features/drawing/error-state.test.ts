/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  getDrawingRequestErrorKind,
  getDrawingRequestErrorMessage,
  getDrawingRequestStatus,
} from './error-state'

describe('drawing request error state', () => {
  test('does not classify an expired session as an L1 permission denial', () => {
    const error = { response: { status: 401 } }

    assert.equal(getDrawingRequestStatus(error), 401)
    assert.equal(getDrawingRequestErrorKind(error), 'unauthenticated')
  })

  test('keeps an actual forbidden response distinguishable', () => {
    const error = { response: { status: 403 } }

    assert.equal(getDrawingRequestErrorKind(error), 'forbidden')
  })

  test('marks upstream outages and network failures for retry UI', () => {
    assert.equal(
      getDrawingRequestErrorKind({ response: { status: 503 } }),
      'unavailable'
    )
    assert.equal(getDrawingRequestErrorKind(new Error('network')), 'network')
    assert.equal(getDrawingRequestStatus(new Error('network')), null)
  })

  test('prefers the relay error detail over a generic Axios status message', () => {
    const error = Object.assign(
      new Error('Request failed with status code 503'),
      {
        response: {
          status: 503,
          data: {
            error: {
              message:
                ' No available channel for image-2 (request id: request-123) ',
            },
            message: 'Request failed',
          },
        },
      }
    )

    assert.equal(
      getDrawingRequestErrorMessage(error, 'Please try again later.'),
      'No available channel for image-2 (request id: request-123)'
    )
  })

  test('accepts a business envelope message when the nested message is invalid', () => {
    for (const nestedMessage of [undefined, null, '', '  ', 503, {}]) {
      assert.equal(
        getDrawingRequestErrorMessage(
          {
            response: {
              data: {
                error: { message: nestedMessage },
                message: 'Group is unavailable',
              },
            },
          },
          'Fallback'
        ),
        'Group is unavailable'
      )
    }
  })

  test('uses a localized fallback for missing, HTML, and malformed responses', () => {
    for (const data of [
      undefined,
      null,
      '<html><body>503 Service Unavailable</body></html>',
      [],
      { error: null, message: {} },
      { error: { message: ['invalid'] }, message: 503 },
    ]) {
      assert.equal(
        getDrawingRequestErrorMessage(
          { response: { status: 503, data } },
          '服务暂时不可用'
        ),
        '服务暂时不可用'
      )
    }
    for (const error of [undefined, null, 503, new Error('Network Error')]) {
      assert.equal(
        getDrawingRequestErrorMessage(error, '网络连接失败'),
        '网络连接失败'
      )
    }
  })
})
