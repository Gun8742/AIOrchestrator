import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { TaskEvent } from './api.ts'
import { latestSubagentActivities } from './subagents.ts'

function event(sequence: number, kind: string, agentThreadId = 'child'): TaskEvent {
  return {sequence, taskId: 1, type: 'activity', timestamp: '2026-01-01T00:00:00Z', payload: {itemType: 'subAgentActivity', parentThreadId: 'parent', agentThreadId, kind}}
}

test('uses only the latest verified activity for each subagent', () => {
  const result = latestSubagentActivities([event(3, 'completed'), event(2, 'started'), event(4, 'started', 'other'), event(5, 'unknown')])
  assert.deepEqual(result, [
    {agentThreadId: 'child', parentThreadId: 'parent', kind: 'completed', sequence: 3},
    {agentThreadId: 'other', parentThreadId: 'parent', kind: 'started', sequence: 4},
  ])
})
