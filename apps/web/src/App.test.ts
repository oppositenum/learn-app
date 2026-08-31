import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'

import App from './App.vue'
import { router } from './routes'
import { useAuthSession } from './stores/auth'

function sessionResponse(role: 'STUDENT' | 'PARENT' | 'OWNER') {
  return {
    ok: true,
    status: 200,
    json: async () => ({ user_id: 'user-1', student_id: role === 'STUDENT' ? 'student-1' : null, display_name: '测试用户', role }),
  } as Response
}

describe('App', () => {
  it('renders the student daily learning workspace', async () => {
		useAuthSession().resetForTests()
		vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
			const path = String(input)
			if (path.includes('/auth/me')) return sessionResponse('STUDENT')
			if (path.includes('/student/today')) return { ok: true, status: 200, json: async () => ({ plans: [{ id: 'plan-1', date: '2026-08-26', target_minutes: 20, blocks: [{ id: 'block-1', sequence: 1, subject: 'MATH', knowledge_point_id: 'kp-1', minutes: 20, mode: 'REVIEW', reason: 'spaced_review_due', focus: '分数通分' }] }] }) } as Response
			if (path.includes('/sessions/current')) return { ok: true, status: 204 } as Response
			return { ok: true, status: 200, json: async () => ({ total_energy: 12, streak_days: 4 }) } as Response
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
  })

	it('keeps a parent out of student routes', async () => {
		useAuthSession().resetForTests()
		vi.stubGlobal('fetch', vi.fn(async () => sessionResponse('PARENT')))
		await router.push('/student/session/not-owned')
		expect(router.currentRoute.value.path).toBe('/parent')
	})
})
