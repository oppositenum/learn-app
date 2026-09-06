import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, expect, test, vi } from 'vitest'

import type { StudentSession } from '../api/student'
import { studentInteractionEvent, studentInteractionFreshnessMS } from '../lib/studentInteraction'
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

test('sends heartbeats only while interaction is fresh and restarts after later interaction', async () => {
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

	window.dispatchEvent(new KeyboardEvent('keydown', { key: 'a' }))
	await vi.advanceTimersByTimeAsync(30_000)
	expect(requests).toEqual(['/api/v1/student/sessions/session-1/heartbeat'])

	await vi.advanceTimersByTimeAsync(studentInteractionFreshnessMS - 30_000 + 1)
	await vi.advanceTimersByTimeAsync(30_000)
	expect(requests).toHaveLength(1)

	window.dispatchEvent(new Event('pointerdown'))
	await vi.advanceTimersByTimeAsync(30_000)
	expect(requests).toHaveLength(2)
	expect(requests.every((path) => path.endsWith('/heartbeat'))).toBe(true)
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
