import type { StageIdentity, StudentInteraction, StudentInteractionRenderer, StudentStageFlow } from '../api/student'

export const studentInteractionEvent = 'student-learning-interaction'
export const studentInteractionFreshnessMS = 45_000
// Mirrored from classroom.SubmitOverallTimeout. In-flight submissions bypass
// freshness expiry without pretending the request is a user gesture.
export const studentSubmitBudgetMS = 75_000

export function submissionFreshnessOrderingValid(): boolean {
  return studentInteractionFreshnessMS < studentSubmitBudgetMS
}

export const classroomStages = ['ORIGINAL', 'VARIANT', 'ABSTRACT', 'VERIFY'] as const
export const classroomStageLabels: Record<(typeof classroomStages)[number], string> = {
  ORIGINAL: '原题',
  VARIANT: '变式',
  ABSTRACT: '抽象',
  VERIFY: '验证',
}

const structuredRenderers = new Set<StudentInteractionRenderer>([
  'SINGLE_CHOICE', 'MULTIPLE_CHOICE', 'ORDERING', 'MATCHING', 'GROUPING', 'NUMBER_LINE', 'FILL_BLANKS',
])

export function stageIdentity(sessionID: string, questionID: string, flow?: StudentStageFlow): StageIdentity | null {
  if (!sessionID || !questionID || !flow || flow.version !== 'classroom-stage-flow-v1' || !classroomStages.includes(flow.stage as (typeof classroomStages)[number]) || !flow.task_version) return null
  return { stage: flow.stage as StageIdentity['stage'], task_id: questionID, task_version: flow.task_version }
}

export function isStructuredInteraction(interaction?: StudentInteraction, flow?: StudentStageFlow): interaction is StudentInteraction & { scene: NonNullable<StudentInteraction['scene']> } {
  return Boolean(
    flow
    && interaction
    && interaction.version === 'student-interaction-v1'
    && interaction.fallback === false
    && structuredRenderers.has(interaction.renderer)
    && interaction.scene?.version === 'student-interaction-v1'
    && interaction.scene.renderer === interaction.renderer
    && interaction.accessible_fallback.trim(),
  )
}

export function initialStructuredResponse(interaction: StudentInteraction): Record<string, unknown> {
  const scene = interaction.scene
  if (!scene) return {}
  switch (interaction.renderer) {
    case 'SINGLE_CHOICE':
    case 'MULTIPLE_CHOICE':
      return { selected_option_ids: [] }
    case 'ORDERING':
      return { ordered_item_ids: (scene.items ?? []).map((item) => item.id) }
    case 'MATCHING':
      return { pairs: [] }
    case 'GROUPING':
      return { placements: [] }
    case 'NUMBER_LINE':
      return { value: scene.number_line?.min }
    case 'FILL_BLANKS':
      return { values: [] }
    default:
      return {}
  }
}

export function structuredResponseComplete(interaction: StudentInteraction, response: Record<string, unknown>): boolean {
  const scene = interaction.scene
  if (!scene) return false
  switch (interaction.renderer) {
    case 'SINGLE_CHOICE':
      return stringArray(response.selected_option_ids).length === 1
    case 'MULTIPLE_CHOICE':
      return stringArray(response.selected_option_ids).length > 0
    case 'ORDERING':
      return stringArray(response.ordered_item_ids).length === (scene.items?.length ?? 0) && (scene.items?.length ?? 0) > 1
    case 'MATCHING': {
      const pairs = objectArray(response.pairs)
      const leftIDs = new Set((scene.left ?? []).map((item) => item.id))
      const rightIDs = new Set((scene.right ?? []).map((item) => item.id))
      return pairs.length === leftIDs.size && leftIDs.size > 0
        && new Set(pairs.map((pair) => pair.left_id)).size === pairs.length
        && new Set(pairs.map((pair) => pair.right_id)).size === pairs.length
        && pairs.every((pair) => typeof pair.left_id === 'string' && leftIDs.has(pair.left_id)
          && typeof pair.right_id === 'string' && rightIDs.has(pair.right_id))
    }
    case 'GROUPING':
      return objectArray(response.placements).length === (scene.items?.length ?? 0) && (scene.items?.length ?? 0) > 0
    case 'NUMBER_LINE':
      return typeof response.value === 'number' && Number.isFinite(response.value)
    case 'FILL_BLANKS':
      return objectArray(response.values).length === (scene.slots?.length ?? 0)
        && objectArray(response.values).every((value) => typeof value.value === 'string' && value.value.trim().length > 0)
    default:
      return false
  }
}

export function stageTaskKey(sessionID: string, identity: StageIdentity): string {
  return `${sessionID}:${identity.stage}:${identity.task_id}:${identity.task_version}`
}

export function stageResponseKey(sessionID: string, identity: StageIdentity, response: Record<string, unknown>): string {
  return `${stageTaskKey(sessionID, identity)}:${canonicalStructuredResponse(response)}`
}

export function canonicalStructuredResponse(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonicalStructuredResponse).join(',')}]`
  if (value && typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>)
      .filter(([, item]) => item !== undefined)
      .sort(([left], [right]) => left.localeCompare(right))
    return `{${entries.map(([key, item]) => `${JSON.stringify(key)}:${canonicalStructuredResponse(item)}`).join(',')}}`
  }
  return JSON.stringify(value) ?? 'null'
}

export function newOperationID(): string {
  if (typeof globalThis.crypto?.randomUUID === 'function') return globalThis.crypto.randomUUID()
  const bytes = new Uint8Array(16)
  if (typeof globalThis.crypto?.getRandomValues === 'function') globalThis.crypto.getRandomValues(bytes)
  else for (let index = 0; index < bytes.length; index++) bytes[index] = Math.floor(Math.random() * 256)
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = [...bytes].map((value) => value.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

function stringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : []
}

function objectArray(value: unknown): Record<string, unknown>[] {
  return Array.isArray(value) ? value.filter((item): item is Record<string, unknown> => Boolean(item) && typeof item === 'object' && !Array.isArray(item)) : []
}
