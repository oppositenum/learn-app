import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, expect, test, vi } from 'vitest'

import type { StudentSession } from '../api/student'
import { studentInteractionEvent, studentInteractionFreshnessMS, submissionFreshnessOrderingValid, studentSubmitBudgetMS } from '../lib/studentInteraction'
import StudentSessionPage from '../pages/student/StudentSessionPage.vue'
import { useAuthSession } from '../stores/auth'
import { useLearningStore } from '../stores/learning'
import StudentLayout from './StudentLayout.vue'

function session(overrides: Partial<StudentSession> = {}): StudentSession {
  return {
    id: 'session-1',
    version: 1,
		timing_version: 1,
    subject_code: 'MATH',
    subject_name: '数学',
    knowledge_point: '分数通分',
    knowledge_point_id: 'kp-fraction',
    difficulty: 'L1',
    question_id: 'question-1',
    prompt: '三分之一和四分之一的小格一样大吗？',
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
    timeline: [],
    ...overrides,
  }
}

async function classroomHarness() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({
    history: createMemoryHistory(),
		routes: [
			{ path: '/login', component: { template: '<div>登录</div>' } },
			{ path: '/student', component: { template: '<div>首页</div>' } },
			{ path: '/student/growth', component: { template: '<div>成长</div>' } },
			{ path: '/student/profile', component: { template: '<div>我的</div>' } },
			{ path: '/student/session/:id', component: { template: '<div>课堂</div>' }, meta: { classroom: true, hideStudentNav: true } },
    ],
  })
  await router.push('/student/session/session-1')
  await router.isReady()
  const learning = useLearningStore()
	useAuthSession().user.value = { user_id: 'user-1', role: 'STUDENT', display_name: '学生', student_id: 'student-1' }
  learning.applySession(session())
  const wrapper = mount(StudentLayout, { global: { plugins: [pinia, router] } })
  return { learning, router, wrapper }
}

afterEach(() => {
	vi.useRealTimers()
  vi.restoreAllMocks()
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
})

test('pauses immediately when hidden while answer analysis is loading', async () => {
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  const fetch = vi.fn(async () => ({
    ok: true,
    status: 200,
    json: async () => ({
      session_id: 'session-1',
      version: 1,
		timing_version: 2,
      status: 'PAUSED',
      active_seconds: 20,
      current_active_seconds: 0,
      timing_observed_at: '2026-08-26T12:00:20Z',
    }),
  } as Response))
  vi.stubGlobal('fetch', fetch)
  const { learning, wrapper } = await classroomHarness()
  learning.loading = true

  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
  document.dispatchEvent(new Event('visibilitychange'))
  await flushPromises()

  expect(fetch).toHaveBeenCalledWith('/api/v1/student/sessions/session-1/pause', expect.objectContaining({ method: 'POST' }))
  expect(learning.status).toBe('PAUSED')
  expect(learning.loading).toBe(true)
  wrapper.unmount()
})

test('keeps an in-flight submission alive after the interaction freshness window', async () => {
	vi.useFakeTimers()
	vi.setSystemTime(new Date('2026-08-26T12:00:00Z'))
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	const answer = deferred<Response>()
	const requests: string[] = []
	vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
		const path = String(input)
		requests.push(path)
		if (path.endsWith('/answers')) return answer.promise
		if (path.endsWith('/heartbeat')) return Promise.resolve(jsonResponse({
			session_id: 'session-1', version: 1, timing_version: 1, status: 'ACTIVE',
			active_seconds: 10, current_active_seconds: 10, timing_observed_at: '2026-08-26T12:00:10Z',
		}))
		if (path.endsWith('/sessions/session-1')) return Promise.resolve(jsonResponse(session({
			version: 2, timing_version: 2, timing_observed_at: '2026-08-26T12:01:00Z',
		})))
		throw new Error(`unexpected request: ${path}`)
	}))
	const { learning, wrapper } = await classroomHarness()
	const submission = learning.submitAnswer('session-1', '正在思考')
	await Promise.resolve()
	expect(learning.submissionInFlight).toBe(true)
	expect(submissionFreshnessOrderingValid()).toBe(true)
	expect(studentInteractionFreshnessMS).toBeLessThan(studentSubmitBudgetMS)

	await vi.advanceTimersByTimeAsync(studentInteractionFreshnessMS + 15_000)
	expect(learning.status).toBe('ACTIVE')
	expect(learning.submissionInFlight).toBe(true)
	expect(requests.some((path) => path.endsWith('/pause'))).toBe(false)
	expect(requests.filter((path) => path.endsWith('/heartbeat')).length).toBeGreaterThan(0)

	answer.resolve(jsonResponse({
		session_id: 'session-1', version: 2, timing_version: 2, status: 'ACTIVE', action: 'PROBE',
		message: '继续检查。', socratic_round: 1, active_seconds: 10, current_active_seconds: 10,
		timing_observed_at: '2026-08-26T12:01:00Z',
	}))
	await submission
	expect(learning.submissionInFlight).toBe(false)
	wrapper.unmount()
})

test('defers pagehide pause until an in-flight submission closes', async () => {
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	const answer = deferred<Response>()
	const requests: string[] = []
	vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
		const path = String(input)
		requests.push(path)
		if (path.endsWith('/answers')) return answer.promise
		if (path.endsWith('/pause')) return Promise.resolve(jsonResponse({
			session_id: 'session-1', version: 3, timing_version: 3, status: 'PAUSED',
			active_seconds: 10, current_active_seconds: 0, timing_observed_at: '2026-08-26T12:01:00Z',
		}))
		if (path.endsWith('/sessions/session-1')) return Promise.resolve(jsonResponse(session({
			version: 2, timing_version: 2, status: 'ACTIVE', current_active_seconds: 10,
			timing_observed_at: '2026-08-26T12:01:00Z',
		})))
		throw new Error(`unexpected request: ${path}`)
	}))
	const { learning, wrapper } = await classroomHarness()
	const submission = learning.submitAnswer('session-1', '正在思考')
	await Promise.resolve()
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
	document.dispatchEvent(new Event('visibilitychange'))
	window.dispatchEvent(new Event('pagehide'))
	await flushPromises()
	expect(requests.some((path) => path.endsWith('/pause'))).toBe(false)
	expect(learning.status).toBe('ACTIVE')

	answer.resolve(jsonResponse({
		session_id: 'session-1', version: 2, timing_version: 2, status: 'ACTIVE', action: 'PROBE',
		message: '继续检查。', socratic_round: 1, active_seconds: 10, current_active_seconds: 10,
		timing_observed_at: '2026-08-26T12:01:00Z',
	}))
	await submission
	await flushPromises()
	expect(requests.some((path) => path.endsWith('/pause'))).toBe(true)
	expect(learning.status).toBe('PAUSED')
	wrapper.unmount()
})

test('explicit resume after a legal pause restores controls without losing the draft', async () => {
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	const requests: string[] = []
	vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
		const path = String(input)
		requests.push(path)
		if (path.endsWith('/pause')) return jsonResponse({
			session_id: 'session-1', version: 2, timing_version: 2, status: 'PAUSED',
			active_seconds: 10, current_active_seconds: 0, timing_observed_at: '2026-08-26T12:01:00Z',
		})
		if (path.endsWith('/resume')) return jsonResponse({
			session_id: 'session-1', version: 3, timing_version: 3, status: 'ACTIVE',
			active_seconds: 10, current_active_seconds: 0, active_since: '2026-08-26T12:01:01Z',
			timing_observed_at: '2026-08-26T12:01:01Z',
		})
		throw new Error(`unexpected request: ${path}`)
	}))
	const { learning, wrapper } = await classroomHarness()
	learning.setAnswerDraft('session-1', '尚未提交的想法')
	expect(await learning.pauseSession()).toBe(true)
	expect(learning.status).toBe('PAUSED')
	expect(learning.answerDraftFor('session-1')).toBe('尚未提交的想法')
	expect(await learning.resumeSession()).toBe(true)
	expect(learning.status).toBe('ACTIVE')
	expect(learning.answerDraftFor('session-1')).toBe('尚未提交的想法')
	expect(requests).toEqual([
		'/api/v1/student/sessions/session-1/pause',
		'/api/v1/student/sessions/session-1',
		'/api/v1/student/sessions/session-1/resume',
		'/api/v1/student/sessions/session-1',
	])
	wrapper.unmount()
})

test('pageshow reconciles a terminal session from the server without resuming', async () => {
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  const fetch = vi.fn(async (input: string | URL | Request) => String(input).endsWith('/auth/me')
		? jsonResponse({ user_id: 'user-1', role: 'STUDENT', display_name: '学生', student_id: 'student-1' })
		: jsonResponse(session({
			version: 2,
			timing_version: 2,
			status: 'ABANDONED',
			current_active_seconds: 0,
			timing_observed_at: '2026-08-27T12:00:00Z',
		})))
  vi.stubGlobal('fetch', fetch)
  const { learning, wrapper } = await classroomHarness()

  window.dispatchEvent(persistedPageShow())
  await flushPromises()

	expect(fetch).toHaveBeenCalledTimes(2)
  expect(learning.status).toBe('ABANDONED')
  expect(learning.version).toBe(2)
  wrapper.unmount()
})

test('does not leave the classroom when the required pause fails', async () => {
  Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false, status: 500 } as Response)))
  const { router, wrapper } = await classroomHarness()

  await router.push('/student')
  await flushPromises()

  expect(router.currentRoute.value.path).toBe('/student/session/session-1')
  wrapper.unmount()
})

test('waits for a pagehide pause before visible refresh without automatic resume', async () => {
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	const pause = deferred<Response>()
	const identity = deferred<Response>()
	const requests: string[] = []
	vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
		const path = String(input)
		requests.push(path)
		if (path.endsWith('/pause')) return pause.promise
		if (path.endsWith('/auth/me')) return identity.promise
		if (path.endsWith('/sessions/session-1')) return Promise.resolve(jsonResponse(session({
			timing_version: 2,
			status: 'PAUSED',
			active_seconds: 20,
			current_active_seconds: 0,
			timing_observed_at: '2026-08-26T12:00:20Z',
		})))
		throw new Error(`unexpected request: ${path}`)
	}))
	const { learning, wrapper } = await classroomHarness()

	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
	document.dispatchEvent(new Event('visibilitychange'))
	window.dispatchEvent(new Event('pagehide'))
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	document.dispatchEvent(new Event('visibilitychange'))
	await Promise.resolve()
	expect(requests).toEqual(['/api/v1/student/sessions/session-1/pause'])

	pause.resolve(jsonResponse({
		session_id: 'session-1',
		version: 1,
		timing_version: 2,
		status: 'PAUSED',
		active_seconds: 20,
		current_active_seconds: 0,
		timing_observed_at: '2026-08-26T12:00:20Z',
	}))
	await flushPromises()
	expect(requests).toEqual([
		'/api/v1/student/sessions/session-1/pause',
		'/api/v1/auth/me',
	])

	window.dispatchEvent(persistedPageShow())
	identity.resolve(jsonResponse({ user_id: 'user-1', role: 'STUDENT', display_name: '学生', student_id: 'student-1' }))
	await flushPromises()

	expect(requests).toEqual([
		'/api/v1/student/sessions/session-1/pause',
		'/api/v1/auth/me',
		'/api/v1/student/sessions/session-1',
	])
	expect(learning.status).toBe('PAUSED')
	expect(learning.timingVersion).toBe(2)
	wrapper.unmount()
})

test('identity sync and repeated visibility reconciliation never resume a paused classroom', async () => {
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	const firstIdentity = deferred<Response>()
	let identityRequests = 0
	const requests: string[] = []
	vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
		const path = String(input)
		requests.push(path)
		if (path.endsWith('/pause')) return Promise.resolve(jsonResponse({
			session_id: 'session-1',
			version: 1,
			timing_version: 2,
			status: 'PAUSED',
			active_seconds: 20,
			current_active_seconds: 0,
			timing_observed_at: '2026-08-26T12:00:20Z',
		}))
		if (path.endsWith('/auth/me')) {
			identityRequests++
			if (identityRequests === 1) return firstIdentity.promise
			return Promise.resolve(jsonResponse({ user_id: 'user-1', role: 'STUDENT', display_name: '学生', student_id: 'student-1' }))
		}
		if (path.endsWith('/sessions/session-1')) return Promise.resolve(jsonResponse(session({
			timing_version: 2,
			status: 'PAUSED',
			active_seconds: 20,
			current_active_seconds: 0,
			timing_observed_at: '2026-08-26T12:00:20Z',
		})))
		throw new Error(`unexpected request: ${path}`)
	}))
	const { learning, wrapper } = await classroomHarness()

	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
	document.dispatchEvent(new Event('visibilitychange'))
	await flushPromises()
	expect(learning.status).toBe('PAUSED')

	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	document.dispatchEvent(new Event('visibilitychange'))
	await Promise.resolve()
	expect(requests.at(-1)).toBe('/api/v1/auth/me')

	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
	document.dispatchEvent(new Event('visibilitychange'))
	firstIdentity.resolve(jsonResponse({ user_id: 'user-1', role: 'STUDENT', display_name: '学生', student_id: 'student-1' }))
	await flushPromises()

	expect(requests.some((path) => path.endsWith('/resume'))).toBe(false)
	expect(learning.status).toBe('PAUSED')

	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	document.dispatchEvent(new Event('visibilitychange'))
	await flushPromises()

	expect(identityRequests).toBe(2)
	expect(requests.at(-1)).toBe('/api/v1/student/sessions/session-1')
	expect(requests.some((path) => path.endsWith('/resume'))).toBe(false)
	expect(learning.status).toBe('PAUSED')
	wrapper.unmount()
})

test('ordinary pageshow does not resume or refresh a paused classroom', async () => {
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	const fetch = vi.fn()
	vi.stubGlobal('fetch', fetch)
	const { learning, wrapper } = await classroomHarness()
	learning.applySession(session({
		timing_version: 2,
		status: 'PAUSED',
		active_seconds: 20,
		current_active_seconds: 0,
		timing_observed_at: '2026-08-26T12:00:20Z',
	}))

	window.dispatchEvent(new Event('pageshow'))
	await flushPromises()

	expect(fetch).not.toHaveBeenCalled()
	expect(learning.status).toBe('PAUSED')
	wrapper.unmount()
})

test('does not send heartbeat before an explicit resume', async () => {
	vi.useFakeTimers()
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	const fetch = vi.fn()
	vi.stubGlobal('fetch', fetch)
	const { learning, wrapper } = await classroomHarness()
	learning.applySession(session({
		timing_version: 2,
		status: 'PAUSED',
		active_seconds: 20,
		current_active_seconds: 0,
		timing_observed_at: '2026-08-26T12:00:20Z',
	}))

	await vi.advanceTimersByTimeAsync(60_000)

	expect(fetch).not.toHaveBeenCalled()
	expect(learning.status).toBe('PAUSED')
	wrapper.unmount()
	vi.useRealTimers()
})

test('routes fresh interaction to heartbeat and stale intervals to read-only refresh', async () => {
	vi.useFakeTimers()
	vi.setSystemTime(new Date('2026-08-26T12:00:00Z'))
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	const requests: string[] = []
	vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
		const path = String(input)
		requests.push(path)
		if (path.endsWith('/heartbeat')) return jsonResponse({
			session_id: 'session-1',
			version: 1,
			timing_version: requests.length + 1,
			status: 'ACTIVE',
			active_seconds: 20,
			current_active_seconds: 10,
			active_since: '2026-08-26T12:00:00Z',
			timing_observed_at: '2026-08-26T12:00:20Z',
		})
		if (path.endsWith('/sessions/session-1')) return jsonResponse(session({
			version: 2,
			timing_version: 3,
			status: 'ACTIVE',
			active_seconds: 20,
			current_active_seconds: 10,
			active_since: '2026-08-26T12:00:00Z',
			timing_observed_at: '2026-08-26T12:01:00Z',
		}))
		throw new Error(`unexpected request: ${path}`)
	}))
	const { wrapper } = await classroomHarness()

	window.dispatchEvent(new KeyboardEvent('keydown', { key: 'a' }))
	await vi.advanceTimersByTimeAsync(30_000)
	expect(requests).toEqual(['/api/v1/student/sessions/session-1/heartbeat'])

	await vi.advanceTimersByTimeAsync(studentInteractionFreshnessMS - 30_000 + 1)
	await vi.advanceTimersByTimeAsync(30_000)
	expect(requests).toEqual([
		'/api/v1/student/sessions/session-1/heartbeat',
		'/api/v1/student/sessions/session-1',
	])
	expect(requests.filter((path) => path.endsWith('/heartbeat'))).toHaveLength(1)
	expect(requests.some((path) => path.endsWith('/pause'))).toBe(false)
	expect(requests.some((path) => path.endsWith('/resume'))).toBe(false)

	window.dispatchEvent(new Event('pointerdown'))
	await vi.advanceTimersByTimeAsync(30_000)
	expect(requests).toEqual([
		'/api/v1/student/sessions/session-1/heartbeat',
		'/api/v1/student/sessions/session-1',
		'/api/v1/student/sessions/session-1/heartbeat',
	])
	wrapper.unmount()
})

test('applies authoritative paused timing from idle refresh and stops polling', async () => {
	vi.useFakeTimers()
	vi.setSystemTime(new Date('2026-08-26T12:00:00Z'))
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	const requests: string[] = []
	vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
		const path = String(input)
		requests.push(path)
		if (path.endsWith('/sessions/session-1')) return jsonResponse(session({
			version: 2,
			timing_version: 2,
			status: 'PAUSED',
			active_seconds: 4143,
			current_active_seconds: 0,
			active_since: undefined,
			timing_observed_at: '2026-08-26T12:02:30Z',
		}))
		throw new Error(`unexpected request: ${path}`)
	}))
	const { learning, wrapper } = await classroomHarness()
	learning.applySession(session({
		active_seconds: 4122,
		current_active_seconds: 152,
	}))

	await vi.advanceTimersByTimeAsync(30_000)

	expect(requests).toEqual(['/api/v1/student/sessions/session-1'])
	expect(requests.some((path) => path.endsWith('/heartbeat'))).toBe(false)
	expect(requests.some((path) => path.endsWith('/pause'))).toBe(false)
	expect(requests.some((path) => path.endsWith('/resume'))).toBe(false)
	expect(learning.status).toBe('PAUSED')
	expect(learning.activeSeconds).toBe(4143)
	expect(learning.currentActiveSeconds).toBe(0)

	await vi.advanceTimersByTimeAsync(90_000)
	expect(requests).toEqual(['/api/v1/student/sessions/session-1'])
	wrapper.unmount()
})

test('keeps support observable when the 30-second refresh races an auto-pause boundary', async () => {
	vi.useFakeTimers()
	vi.setSystemTime(new Date('2026-08-26T12:00:00Z'))
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	const refresh = deferred<Response>()
	let sessionReads = 0
	let supportRequests = 0
	const requests: string[] = []
	vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
		const path = String(input)
		requests.push(path)
		if (path.endsWith('/support')) {
			supportRequests++
			return Promise.resolve({ ok: false, status: 409, json: async () => ({ code: 'SESSION_NOT_ACTIVE' }) } as Response)
		}
		if (path.endsWith('/sessions/session-1')) {
			sessionReads++
			if (sessionReads === 1) return Promise.resolve(jsonResponse(session()))
			return refresh.promise
		}
		throw new Error(`unexpected request: ${path}`)
	}))
	const pinia = createPinia()
	setActivePinia(pinia)
	const router = createRouter({
		history: createMemoryHistory(),
		routes: [
			{ path: '/student', component: { template: '<div>首页</div>' } },
			{ path: '/student/session/:id', component: StudentSessionPage, meta: { classroom: true, hideStudentNav: true } },
		],
	})
	await router.push('/student/session/session-1')
	await router.isReady()
	useAuthSession().user.value = { user_id: 'user-1', role: 'STUDENT', display_name: '学生', student_id: 'student-1' }
	const wrapper = mount(StudentLayout, { global: { plugins: [pinia, router] } })
	await flushPromises()
	const learning = useLearningStore()
	expect(learning.status).toBe('ACTIVE')
	expect(wrapper.get('[data-testid="hint-support"]').attributes('disabled')).toBeUndefined()

	await vi.advanceTimersByTimeAsync(30_000)
	expect(sessionReads).toBe(2)
	await wrapper.get('[data-testid="hint-support"]').trigger('click')
	await flushPromises()

	expect(supportRequests).toBe(1)
	expect(wrapper.get('[role="alert"]').text()).toContain('学习数据暂时不可用（409）')

	refresh.resolve(jsonResponse(session({
		version: 2,
		timing_version: 2,
		status: 'PAUSED',
		active_seconds: 40,
		current_active_seconds: 0,
		active_since: undefined,
		timing_observed_at: '2026-08-26T12:00:30Z',
	})))
	await flushPromises()

	expect(learning.status).toBe('PAUSED')
	expect(wrapper.get('[data-testid="paused-support-guidance"]').text()).toContain('继续探索')
	expect(wrapper.get('[data-testid="hint-support"]').attributes('disabled')).toBe('')
	await wrapper.get('[data-testid="hint-support"]').trigger('click')
	expect(supportRequests).toBe(1)
	expect(requests.filter((path) => path.endsWith('/support'))).toHaveLength(1)
	wrapper.unmount()
})

test('scroll and voice interactions each refresh heartbeat eligibility', async () => {
	vi.useFakeTimers()
	vi.setSystemTime(new Date('2026-08-26T12:00:00Z'))
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	const requests: string[] = []
	vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
		const path = String(input)
		requests.push(path)
		if (path.endsWith('/heartbeat')) return jsonResponse({
			session_id: 'session-1',
			version: 1,
			timing_version: requests.length + 1,
			status: 'ACTIVE',
			active_seconds: 20,
			current_active_seconds: 10,
			active_since: '2026-08-26T12:00:00Z',
			timing_observed_at: '2026-08-26T12:00:20Z',
		})
		throw new Error(`unexpected request: ${path}`)
	}))
	const { wrapper } = await classroomHarness()

	document.dispatchEvent(new Event('scroll'))
	await vi.advanceTimersByTimeAsync(30_000)
	window.dispatchEvent(new Event(studentInteractionEvent))
	await vi.advanceTimersByTimeAsync(30_000)

	expect(requests).toHaveLength(2)
	expect(requests.every((path) => path.endsWith('/heartbeat'))).toBe(true)
	wrapper.unmount()
})

test('visible reconciliation only synchronizes an active server session', async () => {
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	const requests: string[] = []
	vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
		const path = String(input)
		requests.push(path)
		if (path.endsWith('/auth/me')) return jsonResponse({ user_id: 'user-1', role: 'STUDENT', display_name: '学生', student_id: 'student-1' })
		if (path.endsWith('/sessions/session-1')) return jsonResponse(session({
			timing_version: 2,
			status: 'ACTIVE',
			active_seconds: 20,
			current_active_seconds: 10,
			active_since: '2026-08-26T12:00:00Z',
			timing_observed_at: '2026-08-26T12:00:10Z',
		}))
		throw new Error(`unexpected request: ${path}`)
	}))
	const { learning, wrapper } = await classroomHarness()

	document.dispatchEvent(new Event('visibilitychange'))
	await flushPromises()

	expect(requests).toEqual([
		'/api/v1/auth/me',
		'/api/v1/student/sessions/session-1',
	])
	expect(requests.some((path) => path.endsWith('/resume'))).toBe(false)
	expect(requests.some((path) => path.endsWith('/pause'))).toBe(false)
	expect(learning.status).toBe('ACTIVE')
	wrapper.unmount()
})

test('visible reconciliation preserves active state after a concurrent explicit resume settles', async () => {
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	const resume = deferred<Response>()
	const requests: string[] = []
	vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
		const path = String(input)
		requests.push(path)
		if (path.endsWith('/resume')) return resume.promise
		if (path.endsWith('/auth/me')) return Promise.resolve(jsonResponse({ user_id: 'user-1', role: 'STUDENT', display_name: '学生', student_id: 'student-1' }))
		if (path.endsWith('/sessions/session-1')) return Promise.resolve(jsonResponse(session({
			timing_version: 3,
			status: 'ACTIVE',
			active_seconds: 20,
			current_active_seconds: 0,
			active_since: '2026-08-26T12:00:21Z',
			timing_observed_at: '2026-08-26T12:00:21Z',
		})))
		throw new Error(`unexpected request: ${path}`)
	}))
	const { learning, wrapper } = await classroomHarness()
	learning.applySession(session({
		timing_version: 2,
		status: 'PAUSED',
		active_seconds: 20,
		current_active_seconds: 0,
		timing_observed_at: '2026-08-26T12:00:20Z',
	}))

	const resumeOperation = learning.resumeSession()
	document.dispatchEvent(new Event('visibilitychange'))
	await Promise.resolve()
	expect(requests).toEqual([
		'/api/v1/student/sessions/session-1/resume',
		'/api/v1/auth/me',
	])

	resume.resolve(jsonResponse({
		session_id: 'session-1',
		version: 1,
		timing_version: 3,
		status: 'ACTIVE',
		active_seconds: 20,
		current_active_seconds: 0,
		active_since: '2026-08-26T12:00:21Z',
		timing_observed_at: '2026-08-26T12:00:21Z',
	}))
	await resumeOperation
	await flushPromises()

	expect(requests).toEqual([
		'/api/v1/student/sessions/session-1/resume',
		'/api/v1/auth/me',
		'/api/v1/student/sessions/session-1',
	])
	expect(requests.filter((path) => path.endsWith('/resume'))).toHaveLength(1)
	expect(requests.some((path) => path.endsWith('/pause'))).toBe(false)
	expect(learning.status).toBe('ACTIVE')
	wrapper.unmount()
})

test('does not send heartbeat while an explicit resume is still pending', async () => {
	vi.useFakeTimers()
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	const resume = deferred<Response>()
	const requests: string[] = []
	vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
		const path = String(input)
		requests.push(path)
		if (path.endsWith('/resume')) return resume.promise
		throw new Error(`unexpected request: ${path}`)
	}))
	const { learning, wrapper } = await classroomHarness()
	learning.applySession(session({
		timing_version: 2,
		status: 'PAUSED',
		active_seconds: 20,
		current_active_seconds: 0,
		timing_observed_at: '2026-08-26T12:00:20Z',
	}))

	const resumeOperation = learning.resumeSession()
	await vi.advanceTimersByTimeAsync(60_000)
	expect(requests).toEqual(['/api/v1/student/sessions/session-1/resume'])

	resume.resolve(jsonResponse({
		session_id: 'session-1',
		version: 1,
		timing_version: 3,
		status: 'ACTIVE',
		active_seconds: 20,
		current_active_seconds: 0,
		active_since: '2026-08-26T12:00:21Z',
		timing_observed_at: '2026-08-26T12:00:21Z',
	}))
	await resumeOperation
	wrapper.unmount()
	vi.useRealTimers()
})

test('pauses again when the page is hidden while an explicit resume is in flight', async () => {
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	const resume = deferred<Response>()
	const requests: string[] = []
	vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
		const path = String(input)
		requests.push(path)
		if (path.endsWith('/pause')) return Promise.resolve(jsonResponse({
			session_id: 'session-1',
			version: 1,
			timing_version: 4,
			status: 'PAUSED',
			active_seconds: 21,
			current_active_seconds: 0,
			timing_observed_at: '2026-08-26T12:00:22Z',
		}))
		if (path.endsWith('/resume')) return resume.promise
		throw new Error(`unexpected request: ${path}`)
	}))
	const { learning, wrapper } = await classroomHarness()
	learning.applySession(session({
		timing_version: 2,
		status: 'PAUSED',
		active_seconds: 20,
		current_active_seconds: 0,
		timing_observed_at: '2026-08-26T12:00:20Z',
	}))

	const resumeOperation = learning.resumeSession()
	await Promise.resolve()
	expect(requests).toEqual(['/api/v1/student/sessions/session-1/resume'])

	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
	document.dispatchEvent(new Event('visibilitychange'))
	resume.resolve(jsonResponse({
		session_id: 'session-1',
		version: 1,
		timing_version: 3,
		status: 'ACTIVE',
		active_seconds: 20,
		current_active_seconds: 0,
		active_since: '2026-08-26T12:00:21Z',
		timing_observed_at: '2026-08-26T12:00:21Z',
	}))
	await resumeOperation
	await flushPromises()

	expect(requests).toEqual([
		'/api/v1/student/sessions/session-1/resume',
		'/api/v1/student/sessions/session-1/pause',
	])
	expect(requests.at(-1)).toBe('/api/v1/student/sessions/session-1/pause')
	expect(learning.status).toBe('PAUSED')
	wrapper.unmount()
})

test('waits for an explicitly requested resume and pauses before leaving the classroom', async () => {
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	vi.spyOn(window, 'confirm').mockReturnValue(true)
	const resume = deferred<Response>()
	const requests: string[] = []
	vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
		const path = String(input)
		requests.push(path)
		if (path.endsWith('/resume')) return resume.promise
		if (path.endsWith('/pause')) return Promise.resolve(jsonResponse({
			session_id: 'session-1',
			version: 1,
			timing_version: 4,
			status: 'PAUSED',
			active_seconds: 21,
			current_active_seconds: 0,
			timing_observed_at: '2026-08-26T12:00:22Z',
		}))
		throw new Error(`unexpected request: ${path}`)
	}))
	const { learning, router, wrapper } = await classroomHarness()
	learning.applySession(session({
		timing_version: 2,
		status: 'PAUSED',
		active_seconds: 20,
		current_active_seconds: 0,
		timing_observed_at: '2026-08-26T12:00:20Z',
	}))

	const resumeOperation = learning.resumeSession()
	const navigation = router.push('/student')
	await flushPromises()
	expect(router.currentRoute.value.path).toBe('/student/session/session-1')
	expect(requests).toEqual(['/api/v1/student/sessions/session-1/resume'])

	resume.resolve(jsonResponse({
		session_id: 'session-1',
		version: 1,
		timing_version: 3,
		status: 'ACTIVE',
		active_seconds: 20,
		current_active_seconds: 0,
		active_since: '2026-08-26T12:00:21Z',
		timing_observed_at: '2026-08-26T12:00:21Z',
	}))
	await resumeOperation
	await navigation
	await flushPromises()

	expect(requests).toEqual([
		'/api/v1/student/sessions/session-1/resume',
		'/api/v1/student/sessions/session-1/pause',
	])
	expect(router.currentRoute.value.path).toBe('/student')
	expect(learning.status).toBe('PAUSED')
	wrapper.unmount()
})

test('stops old-session lifecycle writes after a cross-tab identity invalidation', async () => {
	vi.useFakeTimers()
	Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
	const fetch = vi.fn(async (input: string | URL | Request) => {
		void input
		return { ok: false, status: 401 } as Response
	})
	vi.stubGlobal('fetch', fetch)
	const { learning, wrapper } = await classroomHarness()

	useAuthSession().invalidateExternalChange()
	document.dispatchEvent(new Event('visibilitychange'))
	window.dispatchEvent(new Event('pagehide'))
	await vi.advanceTimersByTimeAsync(30_000)
	await learning.heartbeat()
	await learning.pauseForVisibility()
	const resumed = await learning.resumeSession()

	expect(learning.sessionID).toBe('')
	expect(resumed).toBe(false)
	expect(fetch.mock.calls.map(([input]) => String(input)).filter((path) => path.includes('/student/sessions'))).toEqual([])
	wrapper.unmount()
	vi.useRealTimers()
})

function jsonResponse(body: unknown): Response {
	return { ok: true, status: 200, json: async () => body } as Response
}

function deferred<T>() {
	let resolve!: (value: T) => void
	const promise = new Promise<T>((next) => { resolve = next })
	return { promise, resolve }
}

function persistedPageShow(): Event {
	const event = new Event('pageshow')
	Object.defineProperty(event, 'persisted', { value: true })
	return event
}
