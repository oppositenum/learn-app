import { flushPromises, mount } from '@vue/test-utils'

import AdminAccountsPage from './AdminAccountsPage.vue'

const accountReport = {
  students: [{ user_id: 'student-user-1', student_id: 'student-1', email: 'student@example.test', display_name: '小航', grade_level: 7, created_at: '2026-08-27T00:00:00Z' }],
  parents: [],
  links: [],
}

it('creates a student account without rendering the submitted password', async () => {
  const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
		void init
    const path = String(input)
    if (path === '/api/v1/owner/accounts/students') {
      return { ok: true, status: 201, json: async () => ({ user_id: 'student-user-2', student_id: 'student-2', email: 'new.student@example.test', display_name: '新同学', grade_level: 6 }) } as Response
    }
    return { ok: true, status: 200, json: async () => accountReport } as Response
  })
  vi.stubGlobal('fetch', fetchMock)

  const wrapper = mount(AdminAccountsPage)
  await flushPromises()
  await wrapper.get('input[name="student-display-name"]').setValue('新同学')
  await wrapper.get('input[name="student-email"]').setValue('new.student@example.test')
  await wrapper.get('input[name="student-password"]').setValue('Student password 2026!')
  await wrapper.get('select[name="student-grade"]').setValue('6')
  await wrapper.get('form').trigger('submit')
  await flushPromises()

  const createCall = fetchMock.mock.calls.find(([input]) => String(input) === '/api/v1/owner/accounts/students')
  expect(createCall).toBeTruthy()
  expect(JSON.parse(String(createCall?.[1]?.body))).toEqual({ display_name: '新同学', email: 'new.student@example.test', password: 'Student password 2026!', grade_level: 6 })
  expect(wrapper.text()).toContain('已创建学生 新同学')
  expect(wrapper.text()).not.toContain('Student password 2026!')
})

it('requires a student selection when creating a parent account', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, status: 200, json: async () => accountReport } as Response)))
  const wrapper = mount(AdminAccountsPage)
  await flushPromises()

  await wrapper.get('[aria-label="账号类型"] button:last-child').trigger('click')

  expect((wrapper.get('select[name="parent-student"]').element as HTMLSelectElement).value).toBe('student-1')
  expect(wrapper.text()).toContain('创建家长并绑定')
  expect(wrapper.text()).toContain('小航 · student@example.test')
})
