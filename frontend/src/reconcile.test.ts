import assert from 'node:assert/strict'
import { test } from 'node:test'
import { Reconciler } from './reconcile.ts'

test('reloads when an event occurs while a snapshot is in flight', async () => {
  let resolveFirst!: (value: number) => void
  const first = new Promise<number>(resolve => { resolveFirst = resolve })
  const published: number[] = []
  let calls = 0
  const sync = new Reconciler(() => ++calls === 1 ? first : Promise.resolve(2), value => published.push(value), () => assert.fail('load failed'))
  sync.changed()
  sync.changed()
  resolveFirst(1)
  await new Promise(resolve => setTimeout(resolve, 0))
  assert.deepEqual(published, [2])
  assert.equal(calls, 2)
  sync.close()
})

test('reloads when an event arrives while publishing a snapshot', async () => {
  const published: number[] = []
  let calls = 0
  const sync = new Reconciler(
    async () => ++calls,
    value => {
      published.push(value)
      if (value === 1) sync.changed()
    },
    () => assert.fail('load failed'),
  )
  sync.changed()
  await new Promise(resolve => setTimeout(resolve, 0))
  assert.deepEqual(published, [1, 2])
  assert.equal(calls, 2)
  sync.close()
})

test('does not publish an in-flight snapshot after the connection closes', async () => {
  let resolveSnapshot!: (value: number) => void
  const snapshot = new Promise<number>(resolve => { resolveSnapshot = resolve })
  const published: number[] = []
  const sync = new Reconciler(() => snapshot, value => published.push(value), () => assert.fail('load failed'))
  sync.changed()
  sync.close()
  resolveSnapshot(1)
  await new Promise(resolve => setTimeout(resolve, 0))
  assert.deepEqual(published, [])
})

test('reports snapshot failure so the stream can reconnect', async () => {
  let failed = false
  const sync = new Reconciler(() => Promise.reject(new Error('offline')), () => assert.fail('unexpected snapshot'), () => { failed = true })
  sync.changed()
  await new Promise(resolve => setTimeout(resolve, 0))
  assert.equal(failed, true)
  sync.close()
})

test('ignores buffered and duplicate events already covered by the snapshot', async () => {
  let resolveSnapshot!: (value: number) => void
  const snapshot = new Promise<number>(resolve => { resolveSnapshot = resolve })
  const published: number[] = []
  let calls = 0
  const sync = new Reconciler(() => { calls++; return snapshot }, value => published.push(value), () => assert.fail('load failed'), value => value)
  sync.changed()
  sync.changed(4)
  resolveSnapshot(5)
  await new Promise(resolve => setTimeout(resolve, 0))
  sync.changed(4)
  assert.equal(calls, 1)
  assert.deepEqual(published, [5])
  sync.close()
})

test('reconnects when the event buffer overflows', async () => {
  let resolveSnapshot!: (value: number) => void
  const snapshot = new Promise<number>(resolve => { resolveSnapshot = resolve })
  let failed = 0
  const sync = new Reconciler(() => snapshot, () => assert.fail('unexpected snapshot'), () => { failed++ }, value => value)
  sync.changed()
  for (let sequence = 1; sequence <= 513; sequence++) sync.changed(sequence)
  resolveSnapshot(513)
  await new Promise(resolve => setTimeout(resolve, 0))
  assert.equal(failed, 1)
})

test('reloads when a buffered event is newer than the snapshot', async () => {
  let resolveFirst!: (value: number) => void
  const first = new Promise<number>(resolve => { resolveFirst = resolve })
  const published: number[] = []
  let calls = 0
  const sync = new Reconciler(() => ++calls === 1 ? first : Promise.resolve(6), value => published.push(value), () => assert.fail('load failed'), value => value)
  sync.changed()
  sync.changed(6)
  resolveFirst(5)
  await new Promise(resolve => setTimeout(resolve, 0))
  assert.deepEqual(published, [6])
  assert.equal(calls, 2)
  sync.close()
})

test('reloads unversioned project data even when the task watermark covers the event', async () => {
  let resolveFirst!: (value: number) => void
  const first = new Promise<number>(resolve => { resolveFirst = resolve })
  const published: number[] = []
  let calls = 0
  const sync = new Reconciler(() => ++calls === 1 ? first : Promise.resolve(5), value => published.push(value), () => assert.fail('load failed'), value => value)
  sync.changed()
  sync.changed()
  resolveFirst(5)
  await new Promise(resolve => setTimeout(resolve, 0))
  assert.equal(calls, 2)
  assert.deepEqual(published, [5])
  sync.close()
})
