import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'

import App from './App.vue'
import { router } from './routes'
import { authChangeStorageKey, useAuthSession } from './stores/auth'

function sessionResponse(role: 'STUDENT' | 'PARENT' | 'OWNER') {
  return {
    ok: true,
    status: 200,
    json: async () => ({ user_id: 'user-1', student_id: role === 'STUDENT' ? 'student-1' : null, display_name: '测试用户', role }),
  } as Response
}

function deferred<T>() {
	let resolve!: (value: T) => void
	const promise = new Promise<T>((next) => { resolve = next })
	return { promise, resolve }
}

describe('App', () => {
  it('renders the student daily learning workspace', async () => {
		useAuthSession().resetForTests()
		vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
			const path = String(input)
			if (path.includes('/auth/me')) return sessionResponse('STUDENT')
			if (path.includes('/student/today')) return { ok: true, status: 200, json: async () => ({ plans: [{ id: 'plan-1', date: '2026-08-26', target_minutes: 20, blocks: [{ id: 'block-1', sequence: 1, subject: 'MATH', knowledge_point_id: 'kp-1', minutes: 20, mode: 'REVIEW', reason: 'spaced_review_due', focus: '分数通分' }] }] }) } as Response
			if (path.includes('/sessions/current')) return { ok: true, status: 204 } as Response
			return { ok: true, status: 200, json: async () => ({ student_id: 'student-1', learning_date: '2026-08-26', total_energy: 12, streak_days: 4, buildings: {} }) } as Response
		}))
    await router.push('/student')
    await router.isReady()
    const wrapper = mount(App, { global: { plugins: [createPinia(), router] } })
		await flushPromises()

		expect(wrapper.get('h1').text()).toContain('测试用户')
    expect(wrapper.text()).toContain('今日计划')
		expect(wrapper.text()).toContain('分数通分')
    expect(wrapper.text()).not.toContain('标准答案')
    expect(wrapper.text()).not.toContain('每杯 10 元')
		wrapper.unmount()
  })

	it('keeps a parent out of student routes', async () => {
		useAuthSession().resetForTests()
		vi.stubGlobal('fetch', vi.fn(async () => sessionResponse('PARENT')))
		await router.push('/student/session/not-owned')
		expect(router.currentRoute.value.path).toBe('/parent')
	})

	it('reloads protected student pages when another tab changes accounts', async () => {
		useAuthSession().resetForTests()
		let authRequests = 0
		let todayRequests = 0
		vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
			const path = String(input)
			if (path.includes('/auth/me')) {
				authRequests++
				const suffix = authRequests === 1 ? '1' : '2'
				return { ok: true, status: 200, json: async () => ({ user_id: `user-${suffix}`, student_id: `student-${suffix}`, display_name: `学生${suffix}`, role: 'STUDENT' }) } as Response
			}
			if (path.includes('/student/today')) {
				todayRequests++
				const suffix = todayRequests === 1 ? '1' : '2'
				return { ok: true, status: 200, json: async () => ({ learning_date: '2026-08-26', plans: [{ id: `plan-${suffix}`, date: '2026-08-26', target_minutes: 20, blocks: [{ id: `block-${suffix}`, sequence: 1, subject: 'MATH', knowledge_point_id: `kp-${suffix}`, minutes: 20, mode: 'REVIEW', reason: 'spaced_review_due', focus: `学生${suffix}的计划`, status: 'AVAILABLE' }] }] }) } as Response
			}
			if (path.includes('/sessions/current')) return { ok: true, status: 204 } as Response
			const suffix = authRequests === 1 ? '1' : '2'
			return { ok: true, status: 200, json: async () => ({ student_id: `student-${suffix}`, learning_date: '2026-08-26', total_energy: 2, streak_days: 1, buildings: {} }) } as Response
		}))
		await router.push('/student')
		await router.isReady()
		const wrapper = mount(App, { global: { plugins: [createPinia(), router] } })
		await flushPromises()
		expect(wrapper.text()).toContain('学生1的计划')

		window.dispatchEvent(new StorageEvent('storage', { key: authChangeStorageKey, newValue: 'changed-in-another-tab' }))
		await flushPromises()
		await flushPromises()

		expect(authRequests).toBe(2)
		expect(todayRequests).toBe(2)
		expect(wrapper.text()).toContain('学生2的计划')
		expect(wrapper.text()).not.toContain('学生1的计划')
		wrapper.unmount()
	})

	it('processes a second cross-tab account change while the first identity refresh is pending', async () => {
		useAuthSession().resetForTests()
		const staleIdentity = deferred<Response>()
		let authRequests = 0
		let todayRequests = 0
		vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
			const path = String(input)
			if (path.includes('/auth/me')) {
				authRequests++
				if (authRequests === 2) return staleIdentity.promise
				const suffix = authRequests === 1 ? '1' : '3'
				return Promise.resolve(sessionResponse('STUDENT')).then(async (result) => ({
					...result,
					json: async () => ({ user_id: `user-${suffix}`, student_id: `student-${suffix}`, display_name: `学生${suffix}`, role: 'STUDENT' }),
				}) as Response)
			}
			if (path.includes('/student/today')) {
				todayRequests++
				const suffix = todayRequests === 1 ? '1' : '3'
				return Promise.resolve({ ok: true, status: 200, json: async () => ({ learning_date: '2026-08-26', plans: [{ id: `plan-${suffix}`, date: '2026-08-26', target_minutes: 20, blocks: [{ id: `block-${suffix}`, sequence: 1, subject: 'MATH', knowledge_point_id: `kp-${suffix}`, minutes: 20, mode: 'REVIEW', reason: 'spaced_review_due', focus: `学生${suffix}的计划`, status: 'AVAILABLE' }] }] }) } as Response)
			}
			if (path.includes('/sessions/current')) return Promise.resolve({ ok: true, status: 204 } as Response)
			const suffix = authRequests === 1 ? '1' : '3'
			return Promise.resolve({ ok: true, status: 200, json: async () => ({ student_id: `student-${suffix}`, learning_date: '2026-08-26', total_energy: 2, streak_days: 1, buildings: {} }) } as Response)
		}))
		await router.push('/student')
		await router.isReady()
		const wrapper = mount(App, { global: { plugins: [createPinia(), router] } })
		await flushPromises()
		expect(wrapper.text()).toContain('学生1的计划')

		window.dispatchEvent(new StorageEvent('storage', { key: authChangeStorageKey, newValue: 'first-change' }))
		await flushPromises()
		expect(authRequests).toBe(2)
		window.dispatchEvent(new StorageEvent('storage', { key: authChangeStorageKey, newValue: 'second-change' }))
		staleIdentity.resolve({ ok: true, status: 200, json: async () => ({ user_id: 'user-2', student_id: 'student-2', display_name: '学生2', role: 'STUDENT' }) } as Response)
		await flushPromises()
		await flushPromises()

		expect(authRequests).toBe(3)
		expect(todayRequests).toBe(2)
		expect(wrapper.text()).toContain('学生3的计划')
		expect(wrapper.text()).not.toContain('学生1的计划')
		expect(wrapper.text()).not.toContain('学生2的计划')
		wrapper.unmount()
	})
})
