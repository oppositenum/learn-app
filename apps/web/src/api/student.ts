export interface SpeechSegment { id: string; text: string; start_ms: number; end_ms: number }
export type TutorAction = 'INTRO' | 'ASK' | 'WAIT' | 'ANALYZE' | 'PROBE' | 'HINT' | 'SCAFFOLD' | 'ANALOGY' | 'BACKTRACK' | 'EXPLAIN' | 'VOICE_EXPLAIN' | 'RETURN' | 'VARIANT' | 'ABSTRACT' | 'VERIFY' | 'REVIEW' | 'BREAK' | 'COMPLETE'
export type SessionStatus = 'ACTIVE' | 'PAUSED' | 'COMPLETED' | 'ABANDONED'
export type PlanBlockStatus = 'AVAILABLE' | 'ACTIVE' | 'COMPLETED'
export interface SubmitAnswerResult {
  session_id: string
  version: number
  timing_version: number
  action: TutorAction
  socratic_round: number
  message: string
  status?: SessionStatus
  active_seconds?: number
  current_active_seconds?: number
  timing_observed_at: string
  voice_segments?: SpeechSegment[]
  voice_audio?: string
  mastery_state?: string
  energy?: number
  tomorrow_plan_changed?: boolean
}

export interface PlanBlock { id: string; sequence: number; subject: string; knowledge_point_id: string; minutes: number; mode: string; reason: string; focus: string; original_task_id?: string | null; status: PlanBlockStatus; session_id?: string | null; session_status?: SessionStatus | null }
export interface TodayPlan { id: string; date: string; target_minutes: number; blocks: PlanBlock[] }
export interface TodayPlanResponse { learning_date: string; plans: TodayPlan[] }
export interface StudentSessionTurn { sequence: number; actor: 'STUDENT' | 'TUTOR' | 'SYSTEM'; action?: string; message: string; at: string }
export interface StudentSession {
  id: string
  version: number
  timing_version: number
  plan_block_id?: string
  subject_code: string
  subject_name: string
  knowledge_point: string
  difficulty: string
  question_id: string
  prompt: string
  scene: Record<string, unknown>
  input_schema: Record<string, unknown>
  started_at: string
  target_minutes: number
  status: SessionStatus
  active_seconds: number
  current_active_seconds: number
  active_since?: string
  timing_observed_at: string
  state: TutorAction
  socratic_round: number
  timeline: StudentSessionTurn[]
  voice_segments?: SpeechSegment[]
  voice_audio?: string
}

export interface SessionTiming {
  session_id: string
  version: number
  timing_version: number
  status: SessionStatus
  active_seconds: number
  current_active_seconds: number
  active_since?: string
  timing_observed_at: string
}

export interface StudentGrowth {
  student_id: string
  learning_date: string
  total_energy: number
  streak_days: number
  buildings: {
    completed_days?: number
    completed_sessions?: number
    mastered_knowledge_points?: number
    mastered_by_subject?: Partial<Record<'MATH' | 'CHINESE' | 'ENGLISH' | 'PHYSICS' | 'CHEMISTRY', number>>
    corrected_misconceptions?: number
    cross_subject_insights?: number
  }
}

export class ApiError extends Error {
  constructor(message: string, public status: number, public code?: string) {
    super(message)
  }
}

const tutorReviewUnavailableCode = 'TUTOR_REVIEW_TEMPORARILY_UNAVAILABLE'
const tutorOutputRephraseRequiredCode = 'TUTOR_OUTPUT_REPHRASE_REQUIRED'
const tutorHintRephraseMessage = '刚才的提示不太合适，老师换个问法。请再点一次『一点提示』'
const tutorExplainRephraseMessage = '刚才的讲解不太合适，老师换个说法。请再点一次『我不会』'
const tutorAnswerRephraseMessage = '刚才的回应不太合适，老师换个问法。请再提交一次'

async function studentApiError(response: Response, fallback: string, rephraseMessage = tutorAnswerRephraseMessage): Promise<ApiError> {
  let code: string | undefined
  try {
    const payload = await response.json() as unknown
    if (payload && typeof payload === 'object' && 'code' in payload && typeof payload.code === 'string') code = payload.code
  } catch {
    // Existing endpoints may return plain-text errors; keep their status-based messages.
  }
  if (code === tutorReviewUnavailableCode) return new ApiError('老师正在想，等一下再试一次', response.status, code)
  if (code === tutorOutputRephraseRequiredCode) return new ApiError(rephraseMessage, response.status, code)
  return new ApiError(`${fallback}（${response.status}）`, response.status, code)
}

async function studentJSON<T>(path: string, options?: RequestInit, rephraseMessage?: string): Promise<T> {
  const response = await fetch(path, { credentials: 'same-origin', ...options })
  if (!response.ok) throw await studentApiError(response, '学习数据暂时不可用', rephraseMessage)
  return response.json() as Promise<T>
}

export async function getTodayPlan(): Promise<TodayPlanResponse> {
  return studentJSON<TodayPlanResponse>('/api/v1/student/today')
}
export async function startStudentSession(planBlockID: string): Promise<StudentSession> {
  return studentJSON<StudentSession>('/api/v1/student/sessions', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ plan_block_id: planBlockID }) })
}
export async function getStudentSession(sessionID: string): Promise<StudentSession> {
  return studentJSON<StudentSession>(`/api/v1/student/sessions/${encodeURIComponent(sessionID)}`)
}
export async function getCurrentStudentSession(): Promise<StudentSession | null> {
  const response = await fetch('/api/v1/student/sessions/current', { credentials: 'same-origin' })
  if (response.status === 204) return null
  if (!response.ok) throw new Error(`课堂状态暂时不可用（${response.status}）`)
  return response.json() as Promise<StudentSession>
}

async function changeStudentSession(sessionID: string, action: 'pause' | 'resume' | 'abandon' | 'heartbeat', keepalive = false): Promise<SessionTiming> {
	return studentJSON<SessionTiming>(`/api/v1/student/sessions/${encodeURIComponent(sessionID)}/${action}`, {
		method: 'POST',
		keepalive,
	})
}

export const pauseStudentSession = (sessionID: string, keepalive = false) => changeStudentSession(sessionID, 'pause', keepalive)
export const resumeStudentSession = (sessionID: string) => changeStudentSession(sessionID, 'resume')
export const abandonStudentSession = (sessionID: string) => changeStudentSession(sessionID, 'abandon')
export const heartbeatStudentSession = (sessionID: string) => changeStudentSession(sessionID, 'heartbeat')

export async function submitStudentAnswer(sessionID: string, answer: string): Promise<SubmitAnswerResult> {
  const response = await fetch(`/api/v1/student/sessions/${encodeURIComponent(sessionID)}/answers`, {
    method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ answer }),
  })
  if (!response.ok) throw await studentApiError(response, '课堂暂时无法提交')
  return response.json() as Promise<SubmitAnswerResult>
}

export async function requestStudentSupport(sessionID: string, type: 'HINT' | 'EXPLAIN'): Promise<SubmitAnswerResult> {
  return studentJSON<SubmitAnswerResult>(`/api/v1/student/sessions/${encodeURIComponent(sessionID)}/support`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ type }),
  }, type === 'HINT' ? tutorHintRephraseMessage : tutorExplainRephraseMessage)
}

export async function completeStudentVoiceExplanation(sessionID: string): Promise<SubmitAnswerResult> {
  return studentJSON<SubmitAnswerResult>(`/api/v1/student/sessions/${encodeURIComponent(sessionID)}/voice/complete`, {
    method: 'POST',
  })
}

export async function submitSessionReflection(sessionID: string, willingness: 'CONTINUE_TOMORROW' | 'PAUSE' | 'STOP'): Promise<void> {
  await studentJSON(`/api/v1/student/sessions/${encodeURIComponent(sessionID)}/reflection`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ willingness }),
  })
}

export async function getStudentGrowth(): Promise<StudentGrowth> {
  const response = await fetch('/api/v1/student/growth', { credentials: 'same-origin' })
  if (!response.ok) throw new Error(`成长数据暂时不可用（${response.status}）`)
  return response.json() as Promise<StudentGrowth>
}

export async function transcribeStudentAudio(audio: Blob, durationSeconds: number, sessionID: string): Promise<string> {
  const body = new FormData()
  body.set('audio', audio, 'student-answer.webm')
  body.set('duration_seconds', durationSeconds.toFixed(3))
  body.set('session_id', sessionID)
  const response = await fetch('/api/v1/student/speech/transcriptions', { method: 'POST', credentials: 'same-origin', body })
  if (!response.ok) throw new Error(`语音识别暂时不可用（${response.status}）`)
  const result = await response.json() as { transcript: string; requires_confirmation: boolean }
  if (!result.requires_confirmation || !result.transcript.trim()) throw new Error('语音识别没有返回可确认的文字')
  return result.transcript
}
