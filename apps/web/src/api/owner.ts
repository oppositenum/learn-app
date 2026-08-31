export interface CostRecord { date: string; model: string; purpose: string; requests: number; input_tokens: number; cached_input_tokens: number; output_tokens: number; audio_input_seconds: number; audio_output_seconds: number; estimated_cost_usd: string }
export interface CostSummary { total_cost_usd: string; cost_per_active_student_day_usd: string; cost_per_20_minute_lesson_usd: string; cost_per_mastered_skill_usd: string; cached_ratio: string; stt_cost_usd: string; tts_cost_usd: string; strong_model_ratio: string; average_tokens_per_request: string }
export interface CostFilters { student_id?: string; subject?: string; date_from?: string; date_to?: string; model?: string; purpose?: string; session_id?: string }
export interface ContentRecord { id: string; status: string; content_version: string; subject: string; knowledge_point: string; prompt: string; automatic_validation_passed: boolean; secondary_review_passed: boolean }
export interface ContentKnowledgePointOption {
	id: string
	subject_code: string
	subject_name: string
	knowledge_point_code: string
	name: string
	description: string
	grade_band_code: string
	grade_band_name: string
	domain_code: string
	domain_name: string
	unit_code: string
	unit_name: string
	default_difficulty: string
	curriculum_source_name: string
	curriculum_source_ref: string
}
export interface ContentSourceOption { id: string; name: string; source_type: string; license_code: string; attribution: string }
export interface ContentGenerationOptions { generator_available: boolean; knowledge_points: ContentKnowledgePointOption[]; sources: ContentSourceOption[] }
export interface GenerateContentInput { knowledge_point_id: string; source_id: string; difficulty: string; question_type: 'FREE_TEXT' | 'MULTIPLE_CHOICE'; count: number; requirements: string }
export interface GeneratedContentDraft { question_id: string; status: 'DRAFT'; prompt: string }
export interface TrialDay { date: string; completed_sessions: number; active_seconds: number; willingness?: 'CONTINUE_TOMORROW' | 'PAUSE' | 'STOP' }
export interface TrialRecord { student_id: string; display_name: string; longest_activity_streak_days: number; longest_willing_streak_days: number; current_willing_streak_days: number; seven_day_core_complete: boolean; days: TrialDay[] }
export interface OwnerStudentAccount { user_id: string; student_id: string; email: string; display_name: string; grade_level: number; created_at: string }
export interface OwnerParentAccount { user_id: string; email: string; display_name: string; created_at: string }
export interface OwnerAccountLink { parent_user_id: string; student_id: string; status: 'ACTIVE' | 'REVOKED'; created_at: string }
export interface OwnerAccounts { students: OwnerStudentAccount[]; parents: OwnerParentAccount[]; links: OwnerAccountLink[] }
async function ownerGet<T>(path: string): Promise<T> { const response = await fetch(path, { credentials: 'same-origin' }); if (!response.ok) throw new Error(`Owner 数据不可用（${response.status}）`); return response.json() as Promise<T> }
export async function getOwnerCosts(filters: CostFilters = {}): Promise<{ records: CostRecord[]; summary: CostSummary }> {
	const query = new URLSearchParams(Object.entries(filters).filter((entry): entry is [string, string] => Boolean(entry[1])))
	return ownerGet<{ records: CostRecord[]; summary: CostSummary }>(`/api/v1/owner/costs?${query}`)
}
export async function getOwnerContent(): Promise<ContentRecord[]> { return (await ownerGet<{ records: ContentRecord[] }>('/api/v1/owner/content')).records }
export async function getContentGenerationOptions(): Promise<ContentGenerationOptions> { return ownerGet('/api/v1/owner/content/generation-options') }
export async function getOwnerTrials(): Promise<{ criteria: string; generated_at: string; records: TrialRecord[] }> { return ownerGet('/api/v1/owner/trials') }
export async function getOwnerAccounts(): Promise<OwnerAccounts> { return ownerGet('/api/v1/owner/accounts') }

async function ownerPost<T>(path: string, body?: unknown): Promise<T> {
	const response = await fetch(path, { method: 'POST', credentials: 'same-origin', headers: body === undefined ? undefined : { 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) })
	if (!response.ok) throw new Error((await response.text()).trim() || `Owner 操作失败（${response.status}）`)
	return response.json() as Promise<T>
}

export async function importContentDraft(input: unknown): Promise<void> { await ownerPost('/api/v1/owner/content/drafts', input) }
export async function generateContentDrafts(input: GenerateContentInput): Promise<GeneratedContentDraft[]> { return (await ownerPost<{ records: GeneratedContentDraft[] }>('/api/v1/owner/content/generate', input)).records }
export async function validateContent(id: string): Promise<void> { await ownerPost(`/api/v1/owner/content/${encodeURIComponent(id)}/validate`) }
export async function reviewContent(id: string): Promise<void> { await ownerPost(`/api/v1/owner/content/${encodeURIComponent(id)}/review`) }
export async function releaseContent(id: string, reason: string): Promise<void> { await ownerPost(`/api/v1/owner/content/${encodeURIComponent(id)}/release`, { reason }) }
export async function quarantineContent(id: string, reason: string): Promise<void> { await ownerPost(`/api/v1/owner/content/${encodeURIComponent(id)}/quarantine`, { reason }) }
export async function createStudentAccount(input: { email: string; display_name: string; password: string; grade_level: number }): Promise<OwnerStudentAccount> { return ownerPost('/api/v1/owner/accounts/students', input) }
export async function createParentAccount(input: { email: string; display_name: string; password: string; student_ids: string[] }): Promise<OwnerParentAccount> { return ownerPost('/api/v1/owner/accounts/parents', input) }
export async function createParentLink(parentUserID: string, studentID: string): Promise<void> { await ownerPost('/api/v1/owner/accounts/links', { parent_user_id: parentUserID, student_id: studentID }) }
