import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, expect, test, vi } from 'vitest'

import type { StudentSession } from '../../api/student'
import { useLearningStore } from '../../stores/learning'
import StudentSupplyPage from './StudentSupplyPage.vue'

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
			{ path: '/student/session/:id/supply', component: StudentSupplyPage },
			{ path: '/student/session/:id', component: { template: '<div>课堂</div>' } },
		],
	})
	await router.push('/student/session/session-1/supply')
	await router.isReady()
	const wrapper = mount(StudentSupplyPage, { global: { plugins: [pinia, router] } })
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

test('loads a paused supply page without resuming or redirecting the classroom', async () => {
	const requests: string[] = []
	const { router, wrapper } = await mountPage(vi.fn(async (input: string | URL | Request) => {
		requests.push(String(input))
		return response(session())
	}))

	expect(requests).toEqual(['/api/v1/student/sessions/session-1'])
	expect(requests.some((path) => path.endsWith('/resume'))).toBe(false)
	expect(router.currentRoute.value.path).toBe('/student/session/session-1/supply')
	expect(wrapper.get('[data-testid="paused-classroom-notice"]').text()).toContain('课堂已暂停')
	expect(wrapper.get('[data-testid="paused-classroom-notice"] a').attributes('href')).toBe('/student/session/session-1')
	const anotherExample = wrapper.findAll('button').find((button) => button.text().includes('换个例子'))
	expect(anotherExample?.attributes('disabled')).toBe('')
	wrapper.unmount()
})

test('redirects an active session whose Tutor action does not belong on the supply page', async () => {
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

	expectBaseSize(textElement(wrapper, 'p', '正在准备知识补给').classes())
	wrapper.unmount()
})

test('keeps supply page text at 16px and main actions at 48px', async () => {
	const { wrapper } = await mountPage(vi.fn(async () => response(session())))

	expectBaseSize(textElement(wrapper, 'p', '才能继续使用知识补给').classes())
	expectBaseSize(textElement(wrapper, 'p', '知识补给站').classes())
	expectBaseSize(textElement(wrapper, 'p', '当前讲解').classes())
	expect(textElement(wrapper, 'a', '回到课堂继续探索').classes()).toContain('home-action')
	expect(textElement(wrapper, 'button', '换个例子').classes()).toContain('home-action')
	expect(textElement(wrapper, 'a', '返回原题').classes()).toContain('home-action')
	wrapper.unmount()
})

test('a cross-subject backtrack names what to fill in first and returns to the original question', async () => {
	const requests: string[] = []
	let returned = false
	const { router, wrapper } = await mountPage(vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
		const path = String(input)
		requests.push(`${init?.method ?? 'GET'} ${path}`)
		if (path.endsWith('/backtrack/return')) {
			returned = true
			return response({
				session_id: 'session-1', version: 3, timing_version: 2, action: 'RETURN', socratic_round: 0,
				message: '先补的这一步放在这里，现在回到原题接着想。', status: 'ACTIVE', active_seconds: 20,
				current_active_seconds: 1, timing_observed_at: '2026-08-26T12:00:21Z',
			})
		}
		return response(returned
			? session({ version: 3, status: 'ACTIVE', state: 'RETURN', subject_code: 'PHYSICS', subject_name: '物理', knowledge_point: '速度', question_id: 'question-original', prompt: '原题' })
			: session({ version: 2, status: 'ACTIVE', state: 'BACKTRACK', subject_code: 'MATH', subject_name: '数学', knowledge_point: '分数通分', question_id: 'question-prerequisite' }))
	}))

	expect(router.currentRoute.value.path).toBe('/student/session/session-1/supply')
	const reason = wrapper.get('[data-testid="backtrack-reason"]')
	expect(reason.text()).toBe('不是这科不会，是先补一下')
	expect(wrapper.get('[data-testid="backtrack-subject"]').text()).toBe('数学')
	expect(wrapper.get('[data-testid="backtrack-knowledge-point"]').text()).toBe('分数通分')
	expectBaseSize(wrapper.get('[data-testid="backtrack-subject"]').classes())
	for (const text of ['分钟', '当前讲解', '换个例子', '分数：', '答案']) expect(wrapper.text()).not.toContain(text)
	const back = wrapper.get('[data-testid="backtrack-return"]')
	expect(back.text()).toContain('返回原题')
	expect(back.classes()).toContain('home-action')

	await back.trigger('click')
	await flushPromises()

	expect(router.currentRoute.value.path).toBe('/student/session/session-1')
	expect(requests.filter((request) => request.startsWith('POST'))).toEqual(['POST /api/v1/student/sessions/session-1/backtrack/return'])
	const learning = useLearningStore()
	expect(learning.questionID).toBe('question-original')
	expect(learning.tutorAction).toBe('RETURN')
	wrapper.unmount()
})
