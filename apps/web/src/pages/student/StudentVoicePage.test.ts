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

function textElement(wrapper: Awaited<ReturnType<typeof mountPage>>['wrapper'], selector: string, text: string) {
	const found = wrapper.findAll(selector).filter((node) => node.text().includes(text)).at(-1)
	expect(found, `no ${selector} containing ${text}`).toBeDefined()
	return found!
}

function expectBaseSize(classes: string[]) {
	expect(classes).not.toContain('text-sm')
	expect(classes).toContain('text-base')
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

test('shows the preparing line at 16px', async () => {
	const { wrapper } = await mountPage(vi.fn(() => new Promise<Response>(() => {})))

	expectBaseSize(textElement(wrapper, 'p', '正在准备语音讲解').classes())
	wrapper.unmount()
})

test('keeps voice page text at 16px and main actions at 48px', async () => {
	const { wrapper } = await mountPage(vi.fn(async () => response(session({ voice_audio: '' }))))

	expectBaseSize(textElement(wrapper, 'p', '才能继续播放讲解').classes())
	expectBaseSize(textElement(wrapper, 'div', 'AI 老师正在讲解').classes())
	expectBaseSize(textElement(wrapper, 'div', '00:02').classes())
	expectBaseSize(textElement(wrapper, 'p', '语音服务尚未连接').classes())
	expect(textElement(wrapper, 'a', '回到课堂继续探索').classes()).toContain('home-action')
	for (const label of ['播放', '再听一句', '我懂了，回原题']) {
		expect(textElement(wrapper, 'button', label).classes()).toContain('home-action')
	}
	wrapper.unmount()
})

test('shows a failed voice load as a warm 16px notice, not a red error', async () => {
	const { wrapper } = await mountPage(vi.fn(async () => response({ error: { message: '课堂暂时不可用' } }, 503)))

	const notice = wrapper.get('[data-testid="voice-error"]')
	expect(notice.classes()).toContain('notice-warm')
	expectBaseSize(notice.classes())
	for (const name of notice.classes()) expect(name).not.toMatch(/red|error|wrong|danger/)
	wrapper.unmount()
})
