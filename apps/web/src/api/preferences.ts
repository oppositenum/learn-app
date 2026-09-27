export interface ParentPreferences {
  daily_minutes: number
  priority_subject_codes: string[]
  review_only: boolean
  reduce_intensity: boolean
  enabled_subject_codes: string[]
  priority_domain_id: string | null
  domain_options: ParentDomainOption[]
  configured: boolean
}

// A knowledge domain the parent may ask the plan to lean toward. It names a
// domain only, never a knowledge point or a question.
export interface ParentDomainOption {
  id: string
  subject_code: string
  name: string
}

export interface ParentPreferencesUpdate {
  saved: boolean
  plan_updated: boolean
  today_preserved: boolean
  applies_from: string
  answer_controls_available: false
}

export async function getParentPreferences(studentID: string): Promise<ParentPreferences> {
  const response = await fetch(`/api/v1/parent/child/${encodeURIComponent(studentID)}/preferences`, { credentials: 'same-origin' })
  if (!response.ok) throw new Error(`计划偏好读取失败（${response.status}）`)
  return await response.json() as ParentPreferences
}

export async function saveParentPreferences(studentID: string, input: { daily_minutes: number; priority_subject_codes: string[]; review_only: boolean; reduce_intensity: boolean; enabled_subject_codes: string[]; priority_domain_id: string | null; state_not_good: boolean }): Promise<ParentPreferencesUpdate> {
  const response = await fetch(`/api/v1/parent/child/${encodeURIComponent(studentID)}/preferences`, { method: 'PUT', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) })
  if (!response.ok) throw new Error(`计划偏好未保存（${response.status}）`)
  return await response.json() as ParentPreferencesUpdate
}
