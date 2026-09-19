import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, expect, test, vi } from 'vitest'

import type { StudentSession } from '../../api/student'
import StudentVoicePage from './StudentVoicePage.vue'

function response(body: unknown, status = 200): Response {
	return { ok: status >= 200 && status < 300, status, json: async () => body } as Response
}

function session(overrides: Partial<StudentSession> = {}): StudentSession {
	return {
		id: 'session-1',
		version: 1,
		timing_version: 2,
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
		status: 'PAUSED',
		active_seconds: 20,
		current_active_seconds: 0,
		timing_observed_at: '2026-08-26T12:00:20Z',
		state: 'ASK',
		socratic_round: 0,
		timeline: [],
		voice_audio: '/api/v1/student/sessions/session-1/voice/audio',
		voice_segments: [{ id: 'segment-1', text: '先把两个小格画成一样大。', start_ms: 0, end_ms: 2000 }],
		...overrides,
	}
}

async function mountPage(fetch: ReturnType<typeof vi.fn>) {
	vi.stubGlobal('fetch', fetch)
	const pinia = createPinia()
	setActivePinia(pinia)
	const router = createRouter({
		history: createMemoryHistory(),
		routes: [
			{ path: '/student/session/:id/voice', component: StudentVoicePage },
			{ path: '/student/session/:id', component: { template: '<div>课堂</div>' } },
		],
	})
	await router.push('/student/session/session-1/voice')
	await router.isReady()
	const wrapper = mount(StudentVoicePage, { global: { plugins: [pinia, router] } })
	await flushPromises()
	return { router, wrapper }
}

afterEach(() => {
	vi.unstubAllGlobals()
	vi.restoreAllMocks()
})

test('loads a paused voice page without resuming or redirecting the classroom', async () => {
	const requests: string[] = []
	const { router, wrapper } = await mountPage(vi.fn(async (input: string | URL | Request) => {
		requests.push(String(input))
		return response(session())
	}))

	expect(requests).toEqual(['/api/v1/student/sessions/session-1'])
	expect(requests.some((path) => path.endsWith('/resume'))).toBe(false)
	expect(router.currentRoute.value.path).toBe('/student/session/session-1/voice')
	expect(wrapper.get('[data-testid="paused-classroom-notice"]').text()).toContain('课堂已暂停')
	expect(wrapper.get('[data-testid="paused-classroom-notice"] a').attributes('href')).toBe('/student/session/session-1')
	for (const label of ['播放', '再听一句', '我懂了，回原题']) {
		const action = wrapper.findAll('button').find((button) => button.text().includes(label))
		expect(action?.attributes('disabled')).toBe('')
	}
	wrapper.unmount()
})

test('redirects an active session whose Tutor action does not belong on the voice page', async () => {
	const { router, wrapper } = await mountPage(vi.fn(async () => response(session({
		status: 'ACTIVE',
		current_active_seconds: 20,
		state: 'HINT',
	}))))

	expect(router.currentRoute.value.path).toBe('/student/session/session-1')
	wrapper.unmount()
})
