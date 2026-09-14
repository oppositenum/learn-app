import type { GrowthEvidenceProjection } from './learning'

export interface ParentLiveSession {
  session_id: string
  student_id: string
  subject: string
  knowledge_point: string
  started_at: string
  status: string
  current_state: string
  socratic_round: number
  engagement: string
  question_prompt: string
  correct_answer: unknown
  full_solution: string
  detail_mode: 'LIVE' | 'REPORT'
  student_answer_visibility: 'NONE' | 'SHORT_CURRENT' | 'WITHHELD_LONG' | 'WITHHELD_NOT_ACTIVE'
  student_answer_preview?: string
  answer_correct: boolean | null
  error_type: string
  misconceptions: string[]
  tutor_action: string
  tutor_reason: string
	target_minutes: number
	active_seconds: number
	mastery_state: string
	mastery_score: number
	timeline: Array<{ sequence: number; actor: 'STUDENT' | 'TUTOR' | 'SYSTEM'; action?: string; message: string; reason?: string; at: string }>
}

export interface ParentChild { student_id: string; display_name: string; grade_level: number; active_session_id: string | null; subject: string | null; knowledge_point: string | null; started_at: string | null }

export interface ParentAbility {
	student_id: string
	subjects: Array<{ code: string; name: string; started_knowledge_points: number; understood_knowledge_points: number; mastered_knowledge_points: number; average_score: number }>
	core_abilities: Array<{ code: string; name: string; score: number; evidence_count: number }>
	misconceptions: Array<{ code: string; name: string; subject: string; knowledge_point: string; occurrences: number; successful_corrections: number; status: string; last_seen_at: string }>
}

export interface ParentReport {
	student_id: string
	summary: { completed_sessions: number; active_seconds: number; total_energy: number; streak_days: number; reward_events: number }
	activity_days: Array<{ date: string; completed_sessions: number; active_seconds: number }>
	recent_sessions: Array<{ id: string; subject: string; knowledge_point: string; status: string; state: string; started_at: string; ended_at?: string; active_seconds: number }>
	growth_evidence: GrowthEvidenceProjection
}

export interface ParentSafetyEvent {
	id: string
	policy_version: string
	category: string
	severity: 'LOW' | 'MODERATE' | 'HIGH' | 'CRITICAL'
	fixed_action: string
	occurred_at: string
}

export async function getParentChildren(): Promise<ParentChild[]> {
	const response = await fetch('/api/v1/parent/children', { credentials: 'same-origin' })
	if (!response.ok) throw new Error(`孩子信息暂时不可用（${response.status}）`)
	return (await response.json() as { children?: ParentChild[] }).children ?? []
}

export async function getParentAbility(studentID: string): Promise<ParentAbility> {
	const response = await fetch(`/api/v1/parent/child/${encodeURIComponent(studentID)}/ability`, { credentials: 'same-origin' })
	if (!response.ok) throw new Error(`能力数据暂时不可用（${response.status}）`)
	return response.json() as Promise<ParentAbility>
}

export async function getParentReport(studentID: string): Promise<ParentReport> {
	const response = await fetch(`/api/v1/parent/child/${encodeURIComponent(studentID)}/report`, { credentials: 'same-origin' })
	if (!response.ok) throw new Error(`学习报告暂时不可用（${response.status}）`)
	return response.json() as Promise<ParentReport>
}

export async function getParentSafetyEvents(studentID: string): Promise<ParentSafetyEvent[]> {
	const response = await fetch(`/api/v1/parent/child/${encodeURIComponent(studentID)}/safety-events`, { credentials: 'same-origin' })
	if (!response.ok) throw new Error(`安全摘要暂时不可用（${response.status}）`)
	return (await response.json() as { events?: ParentSafetyEvent[] }).events ?? []
}

export async function sendParentIntervention(studentID: string, type: 'ENCOURAGEMENT' | 'REDUCE_INTENSITY' | 'REVIEW_ONLY' | 'STATE_NOT_GOOD'): Promise<void> {
	const response = await fetch(`/api/v1/parent/child/${encodeURIComponent(studentID)}/interventions`, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ type }) })
	if (!response.ok) throw new Error(`家长操作未保存（${response.status}）`)
}

export async function getParentLiveSession(studentID: string, sessionID: string): Promise<ParentLiveSession> {
  const response = await fetch(`/api/v1/parent/child/${encodeURIComponent(studentID)}/session/${encodeURIComponent(sessionID)}`, {
    credentials: 'include',
    headers: { Accept: 'application/json' },
  })
  if (!response.ok) {
    throw new Error(`Parent live session request failed with status ${response.status}`)
  }
  return response.json() as Promise<ParentLiveSession>
}
