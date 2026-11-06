import type { TaskEvent } from './api'

export interface SubagentActivity {
  agentThreadId: string
  parentThreadId: string
  kind: string
  sequence: number
}

export function latestSubagentActivities(events: TaskEvent[]): SubagentActivity[] {
  const agents = new Map<string, SubagentActivity>()
  for (const event of events) {
    const {itemType, agentThreadId, parentThreadId, kind} = event.payload
    if (event.type !== 'activity' || itemType !== 'subAgentActivity' || typeof agentThreadId !== 'string' || !agentThreadId || typeof parentThreadId !== 'string' || !parentThreadId || typeof kind !== 'string' || !['started', 'interacted', 'interrupted', 'completed'].includes(kind)) continue
    const previous = agents.get(agentThreadId)
    if (!previous || event.sequence > previous.sequence) agents.set(agentThreadId, {agentThreadId, parentThreadId, kind, sequence: event.sequence})
  }
  return [...agents.values()]
}
