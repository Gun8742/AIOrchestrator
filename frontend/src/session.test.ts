import assert from 'node:assert/strict'
import { test } from 'node:test'
import { sessionExpired } from './api.ts'

test('only an unauthorized response expires the session', async () => {
  const original = globalThis.fetch
  try {
    globalThis.fetch = async () => new Response('', {status: 401})
    assert.equal(await sessionExpired(), true)
    globalThis.fetch = async () => new Response('', {status: 503})
    assert.equal(await sessionExpired(), false)
    globalThis.fetch = async () => { throw new Error('network disconnected') }
    assert.equal(await sessionExpired(), false)
    globalThis.fetch = async () => new Response('{"csrfToken":"test"}', {status: 200})
    assert.equal(await sessionExpired(), false)
  } finally {
    globalThis.fetch = original
  }
})
