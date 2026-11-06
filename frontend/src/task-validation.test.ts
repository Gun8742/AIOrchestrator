import assert from 'node:assert/strict'
import { test } from 'node:test'
import { taskInputError } from './task-validation.ts'

test('validates the UTF-8 size of Thai and ASCII prompts', () => {
  assert.equal(taskInputError(1, 'ก'.repeat(4000), 'codex'), null)
  assert.notEqual(taskInputError(1, 'ก'.repeat(4001), 'codex'), null)
  assert.equal(taskInputError(1, 'a'.repeat(12000), 'codex'), null)
  assert.notEqual(taskInputError(1, 'a'.repeat(12001), 'codex'), null)
})

test('checks the encoded JSON request size as well as prompt bytes', () => {
  assert.notEqual(taskInputError(1, '\n'.repeat(10000), 'codex'), null)
})
