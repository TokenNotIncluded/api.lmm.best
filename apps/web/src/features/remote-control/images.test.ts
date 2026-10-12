/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import test from 'node:test'

import { createRemoteCommand } from './commands'
import { derivePiRemoteKey, encryptPiRemoteCommand } from './crypto'
import { validRemoteImages } from './images'

const image = {
  type: 'image' as const,
  mimeType: 'image/jpeg' as const,
  data: btoa('\xff\xd8\xff\xe0fixture\xff\xd9'),
}
test('one bounded canonical JPEG allows image-only prompts', () => {
  assert.equal(validRemoteImages([image]), true)
  const command = createRemoteCommand({
    action: 'prompt',
    content: '',
    images: [image],
  })
  assert.equal(command.action, 'prompt')
  if (command.action === 'prompt') {
    assert.equal(command.images?.[0].data, image.data)
  }
  for (const images of [
    [],
    [image, image],
    [{ ...image, data: 'not an image' }],
    [{ ...image, mimeType: 'image/svg+xml' }],
    [
      {
        ...image,
        data: btoa(`\xff\xd8\xff${'x'.repeat(24 * 1024)}\xff\xd9`),
      },
    ],
  ]) {
    assert.equal(validRemoteImages(images), false)
    assert.throws(() =>
      createRemoteCommand({
        action: 'prompt',
        content: 'test',
        images: images as [typeof image],
      })
    )
  }
})
test('image command stays sender-bound and encrypted within existing relay limit', async () => {
  const id = 'image-fixture-session'
  const key = await derivePiRemoteKey('fixture-pin', id)
  const command = createRemoteCommand({
    action: 'prompt',
    content: 'Inspect this image',
    images: [image],
  })
  const envelope = await encryptPiRemoteCommand(id, command, key)
  assert.ok(!envelope.ciphertext.includes(image.data))
  const plain = await crypto.subtle.decrypt(
    {
      name: 'AES-GCM',
      iv: Buffer.from(envelope.nonce, 'base64url'),
      additionalData: new TextEncoder().encode(
        `lmm-pi-remote:v1:message:${id}:controller`
      ),
    },
    key,
    Buffer.from(envelope.ciphertext, 'base64url')
  )
  assert.deepEqual(JSON.parse(new TextDecoder().decode(plain)), command)
  assert.throws(() =>
    createRemoteCommand({
      action: 'prompt',
      content: 'x'.repeat(32000),
      images: [
        {
          ...image,
          data: btoa(`\xff\xd8\xff${'x'.repeat(24000)}\xff\xd9`),
        },
      ],
    })
  )
  await assert.rejects(
    encryptPiRemoteCommand(
      id,
      {
        ...command,
        action: 'prompt',
        content: 'x'.repeat(50000),
        images: [image],
      },
      key
    )
  )
})
