import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, expect, test, vi } from 'vitest'

import type { StudentSession } from '../api/student'
import { useLearningStore } from './learning'

function session(id: string, overrides: Partial<StudentSession> = {}): StudentSession {
  return {
    id,
    version: 1,
		timing_version: 1,
    subject_code: 'MATH',
    subject_name: '数学',
    knowledge_point: '一元一次方程',
    difficulty: 'L1',
    question_id: 'question-1',
    prompt: `安全公开题目 ${id}`,
    scene: {},
    input_schema: {},
    started_at: '2026-08-26T12:00:00Z',
    target_minutes: 20,
    status: 'ACTIVE',
    active_seconds: 10,
    current_active_seconds: 10,
    active_since: '2026-08-26T12:00:00Z',
    timing_observed_at: '2026-08-26T12:00:10Z',
    state: 'ASK',
    socratic_round: 0,
    timeline: [{ sequence: 1, actor: 'TUTOR', action: 'ASK', message: '先说说你的想法。', at: '2026-08-26T12:00:00Z' }],
    ...overrides,
  }
}

function stageSession(overrides: Partial<StudentSession> = {}): StudentSession {
	return session('session-stage', {
		question_id: 'question-1',
		interaction: {
			version: 'student-interaction-v1', renderer: 'FILL_BLANKS', fallback: false,
			accessible_fallback: '填写内容。', answer_schema: {},
			scene: {
				version: 'student-interaction-v1', renderer: 'FILL_BLANKS', accessible_fallback: '填写内容。',
				slots: [{ id: 'slot-1', label: '内容' }],
			},
		},
		stage_flow: { version: 'classroom-stage-flow-v1', stage: 'ORIGINAL', task_version: 'content-v1' },
		state: 'ORIGINAL',
		...overrides,
	})
}

function jsonResponse(body: unknown, status = 200): Response {
  return { ok: status >= 200 && status < 300, status, json: async () => body } as Response
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((next) => { resolve = next })
  return { promise, resolve }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.restoreAllMocks()
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
})

test('recovers persisted voice explanation after a page reload', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(session('session-voice', {
    state: 'VOICE_EXPLAIN',
    socratic_round: 3,
    voice_audio: 'data:audio/wav;base64,UklGRg==',
    voice_segments: [{ id: 'segment-1', text: '先看平行例子。', start_ms: 0, end_ms: 1800 }],
    timeline: [{ sequence: 1, actor: 'TUTOR', action: 'VOICE_EXPLAIN', message: '先看平行例子。', at: '2026-08-26T12:01:00Z' }],
  }))))
  const learning = useLearningStore()

  await learning.loadSession('session-voice')

  expect(learning.sessionID).toBe('session-voice')
  expect(learning.tutorAction).toBe('VOICE_EXPLAIN')
  expect(learning.voiceAudio).toBe('data:audio/wav;base64,UklGRg==')
  expect(learning.voiceSegments).toEqual([{ id: 'segment-1', text: '先看平行例子。', start_ms: 0, end_ms: 1800 }])
})

test('does not let a slower session A response overwrite session B', async () => {
  const first = deferred<Response>()
  const second = deferred<Response>()
  vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => String(input).endsWith('/session-a') ? first.promise : second.promise))
  const learning = useLearningStore()

  const loadingA = learning.loadSession('session-a')
  const loadingB = learning.loadSession('session-b')
  second.resolve(jsonResponse(session('session-b')))
  await loadingB
  first.resolve(jsonResponse(session('session-a')))
  await loadingA

  expect(learning.sessionID).toBe('session-b')
  expect(learning.prompt).toContain('session-b')
})

test('ignores a heartbeat that returns after the classroom completes', async () => {
  const heartbeat = deferred<Response>()
  vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/heartbeat')) return heartbeat.promise
    if (path.endsWith('/answers')) {
      return Promise.resolve(jsonResponse({
        session_id: 'session-1',
				version: 2,
				timing_version: 2,
        action: 'COMPLETE',
        socratic_round: 0,
        message: '这次思路已经记录。',
        status: 'COMPLETED',
        active_seconds: 12,
        current_active_seconds: 0,
        timing_observed_at: '2026-08-26T12:00:12Z',
      }))
    }
		if (path.endsWith('/sessions/session-1')) return Promise.resolve(jsonResponse(session('session-1', {
			version: 2,
			timing_version: 2,
			status: 'COMPLETED',
			state: 'COMPLETE',
			active_seconds: 12,
			current_active_seconds: 0,
			timing_observed_at: '2026-08-26T12:00:12Z',
		})))
		throw new Error(`unexpected request: ${path}`)
  }))
  const learning = useLearningStore()
  learning.applySession(session('session-1'))

  const pendingHeartbeat = learning.heartbeat()
  await learning.submitAnswer('session-1', '我的想法')
  heartbeat.resolve(jsonResponse({
    session_id: 'session-1',
		version: 1,
		timing_version: 1,
    status: 'ACTIVE',
    active_seconds: 11,
    current_active_seconds: 11,
    active_since: '2026-08-26T12:00:00Z',
    timing_observed_at: '2026-08-26T12:00:11Z',
  }))
  await pendingHeartbeat

  expect(learning.status).toBe('COMPLETED')
  expect(learning.version).toBe(2)
  expect(learning.activeSeconds).toBe(12)
})

test('shows a fixed safety notice without retaining the submitted private text', async () => {
	const privateInput = 'PRIVATE_SAFETY_INPUT_CANARY'
	vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
		const path = String(input)
		if (path.endsWith('/answers')) {
			return Promise.resolve(jsonResponse({
				session_id: 'session-1',
				version: 1,
				timing_version: 1,
				action: 'ASK',
				socratic_round: 0,
				message: '请先去找身边可信任的大人。',
				status: 'ACTIVE',
				active_seconds: 10,
				current_active_seconds: 10,
				timing_observed_at: '2026-08-26T12:00:10Z',
				safety: {
					policy_version: 'minor-safety-v1',
					category: 'SELF_HARM',
					severity: 'CRITICAL',
					fixed_action: 'SEEK_URGENT_HELP',
					parent_notified: true,
				},
			}))
		}
		if (path.endsWith('/sessions/session-1')) return Promise.resolve(jsonResponse(session('session-1')))
		throw new Error(`unexpected request: ${path}`)
	}))
	const learning = useLearningStore()
	learning.applySession(session('session-1'))

	const accepted = await learning.submitAnswer('session-1', privateInput)

	expect(accepted).toBe(true)
	expect(learning.safetyNotice).toMatchObject({ category: 'SELF_HARM', parent_notified: true })
	expect(learning.safetyNotice?.message).toBe('请先去找身边可信任的大人。')
	expect(learning.studentAnswer).toBe('')
	expect(learning.timeline.some((turn) => turn.text.includes(privateInput))).toBe(false)
})

test('visibility pause checkpoints time even while an answer is loading', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({
    session_id: 'session-1',
		version: 1,
		timing_version: 2,
    status: 'PAUSED',
    active_seconds: 20,
    current_active_seconds: 0,
    timing_observed_at: '2026-08-26T12:00:20Z',
  })))
  const learning = useLearningStore()
  learning.applySession(session('session-1'))
  learning.loading = true

  const paused = await learning.pauseForVisibility(true)

  expect(paused).toBe(true)
  expect(learning.loading).toBe(true)
  expect(learning.status).toBe('PAUSED')
  expect(learning.activeSeconds).toBe(20)
})

test('authoritative refresh cannot roll a completed classroom back to active', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(session('session-1', {
    version: 1,
    status: 'ACTIVE',
    timing_observed_at: '2026-08-26T12:00:11Z',
  }))))
  const learning = useLearningStore()
  learning.applySession(session('session-1', {
    version: 2,
    status: 'COMPLETED',
    state: 'COMPLETE',
    current_active_seconds: 0,
    timing_observed_at: '2026-08-26T12:00:12Z',
  }))

  await learning.refreshSession()

  expect(learning.status).toBe('COMPLETED')
  expect(learning.version).toBe(2)
  expect(learning.tutorAction).toBe('COMPLETE')
})

test('refreshes teaching content when a heartbeat reports a newer teaching version', async () => {
	const fetch = vi.fn()
		.mockResolvedValueOnce(jsonResponse({
			session_id: 'session-1',
			version: 2,
			timing_version: 2,
			status: 'ACTIVE',
			active_seconds: 12,
			current_active_seconds: 12,
			active_since: '2026-08-26T12:00:00Z',
			timing_observed_at: '2026-08-26T12:00:12Z',
		}))
		.mockResolvedValueOnce(jsonResponse(session('session-1', {
			version: 2,
			timing_version: 2,
			state: 'PROBE',
			socratic_round: 1,
			timeline: [
				{ sequence: 1, actor: 'TUTOR', action: 'ASK', message: '先说说你的想法。', at: '2026-08-26T12:00:00Z' },
				{ sequence: 2, actor: 'STUDENT', message: '我的想法', at: '2026-08-26T12:00:05Z' },
				{ sequence: 3, actor: 'TUTOR', action: 'PROBE', message: '先观察两边。', at: '2026-08-26T12:00:06Z' },
			],
		})))
	vi.stubGlobal('fetch', fetch)
	const learning = useLearningStore()
	learning.applySession(session('session-1'))

	await learning.heartbeat()

	expect(fetch).toHaveBeenCalledTimes(2)
	expect(learning.snapshotVersion).toBe(2)
	expect(learning.tutorAction).toBe('PROBE')
	expect(learning.timeline).toHaveLength(3)
})

test('does not append a mutation result already present in an authoritative snapshot', async () => {
	const answer = deferred<Response>()
	const persisted = session('session-1', {
		version: 2,
		timing_version: 1,
		state: 'PROBE',
		socratic_round: 1,
		timeline: [
			{ sequence: 1, actor: 'TUTOR', action: 'ASK', message: '先说说你的想法。', at: '2026-08-26T12:00:00Z' },
			{ sequence: 2, actor: 'STUDENT', message: '我的想法', at: '2026-08-26T12:00:05Z' },
			{ sequence: 3, actor: 'TUTOR', action: 'PROBE', message: '先观察两边。', at: '2026-08-26T12:00:06Z' },
		],
	})
	vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
		const path = String(input)
		if (path.endsWith('/answers')) return answer.promise
		if (path.endsWith('/sessions/session-1')) return Promise.resolve(jsonResponse(persisted))
		throw new Error(`unexpected request: ${path}`)
	}))
	const learning = useLearningStore()
	learning.applySession(session('session-1'))

	const submitting = learning.submitAnswer('session-1', '我的想法')
	learning.applySession(persisted)
	answer.resolve(jsonResponse({
		session_id: 'session-1',
		version: 2,
		timing_version: 1,
		action: 'PROBE',
		socratic_round: 1,
		message: '先观察两边。',
		status: 'ACTIVE',
		active_seconds: 10,
		current_active_seconds: 10,
		timing_observed_at: '2026-08-26T12:00:10Z',
	}))
	await submitting

	expect(learning.timeline).toHaveLength(3)
	expect(learning.timeline.filter((turn) => turn.text === '先观察两边。')).toHaveLength(1)
})

test('rejects a conflicting status with the same timing version and timestamp', () => {
	const learning = useLearningStore()
	learning.applySession(session('session-1', {
		timing_version: 3,
		status: 'ACTIVE',
		timing_observed_at: '2026-08-26T12:00:20Z',
	}))

	const applied = learning.applyTiming({
		session_id: 'session-1',
		version: 1,
		timing_version: 3,
		status: 'PAUSED',
		active_seconds: 20,
		current_active_seconds: 0,
		timing_observed_at: '2026-08-26T12:00:20Z',
	})

	expect(applied).toBe(false)
	expect(learning.status).toBe('ACTIVE')
})

test('binds a stage operation id to the exact canonical response', async () => {
	const bodies: Array<Record<string, unknown>> = []
	vi.stubGlobal('fetch', vi.fn(async (_input: string | URL | Request, init?: RequestInit) => {
		bodies.push(JSON.parse(String(init?.body)) as Record<string, unknown>)
		throw new Error('connection lost')
	}))
	const learning = useLearningStore()
	learning.applySession(stageSession())

	await learning.submitStageResponse('session-stage', { values: [{ slot_id: 'slot-1', value: '甲' }] })
	await learning.submitStageResponse('session-stage', { values: [{ value: '甲', slot_id: 'slot-1' }] })
	await learning.submitStageResponse('session-stage', { values: [{ slot_id: 'slot-1', value: '乙' }] })

	expect(bodies).toHaveLength(3)
	expect(bodies[1].operation_id).toBe(bodies[0].operation_id)
	expect(bodies[2].operation_id).not.toBe(bodies[0].operation_id)
})

test('reconciles a committed stage response after a stale changed draft gets 409', async () => {
	const advanced = stageSession({
		version: 2,
		question_id: 'question-2',
		stage_flow: { version: 'classroom-stage-flow-v1', stage: 'VARIANT', task_version: 'content-v2' },
		state: 'VARIANT',
	})
	let answerCalls = 0
	let refreshCalls = 0
	vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
		const path = String(input)
		if (path.endsWith('/answers')) {
			answerCalls++
			if (answerCalls === 1) return jsonResponse({
				session_id: 'session-stage', version: 2, timing_version: 1,
				action: 'VARIANT', stage: 'VARIANT', task_id: 'question-2', task_version: 'content-v2',
				evidence_kind: 'INDEPENDENT', stage_completed: true, message: '继续下一阶段。',
				status: 'ACTIVE', active_seconds: 10, current_active_seconds: 10,
				timing_observed_at: '2026-08-26T12:00:10Z',
			})
			return jsonResponse({ code: 'CLASSROOM_CHANGED' }, 409)
		}
		if (path.endsWith('/sessions/session-stage')) {
			refreshCalls++
			return refreshCalls === 1 ? jsonResponse({}, 503) : jsonResponse(advanced)
		}
		throw new Error(`unexpected request: ${path}`)
	}))
	const learning = useLearningStore()
	learning.applySession(stageSession())

	expect(await learning.submitStageResponse('session-stage', { values: [{ slot_id: 'slot-1', value: '甲' }] })).toBe(false)
	expect(await learning.submitStageResponse('session-stage', { values: [{ slot_id: 'slot-1', value: '乙' }] })).toBe(false)

	expect(answerCalls).toBe(2)
	expect(refreshCalls).toBe(2)
	expect(learning.questionID).toBe('question-2')
	expect(learning.stageFlow?.stage).toBe('VARIANT')
	expect(learning.stageAnswerOperationID).toBe('')
	expect(learning.structuredDraftKey).toContain('question-2')
})

test('handles a structured safety result without retaining child-authored fill text', async () => {
	const privateInput = 'PRIVATE_STRUCTURED_SAFETY_CANARY'
	vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
		const path = String(input)
		if (path.endsWith('/answers')) return jsonResponse({
			session_id: 'session-stage', version: 1, timing_version: 1,
			action: 'ORIGINAL', stage: 'ORIGINAL', task_id: 'question-1', task_version: 'content-v1',
			message: '请先去找身边可信任的大人。', status: 'ACTIVE', active_seconds: 10,
			current_active_seconds: 10, timing_observed_at: '2026-08-26T12:00:10Z',
			safety: {
				policy_version: 'minor-safety-v1', category: 'SELF_HARM', severity: 'CRITICAL',
				fixed_action: 'SEEK_URGENT_HELP', parent_notified: true,
			},
		})
		if (path.endsWith('/sessions/session-stage')) return jsonResponse(stageSession())
		throw new Error(`unexpected request: ${path}`)
	}))
	const learning = useLearningStore()
	learning.applySession(stageSession())
	learning.setStructuredDraft(learning.structuredDraftKey, { values: [{ slot_id: 'slot-1', value: privateInput }] })

	expect(await learning.submitStageResponse('session-stage', learning.structuredDraft)).toBe(true)
	expect(learning.safetyNotice).toMatchObject({ category: 'SELF_HARM', parent_notified: true })
	expect(learning.structuredDraft).toEqual({})
	expect(JSON.stringify(learning.timeline)).not.toContain(privateInput)
})
