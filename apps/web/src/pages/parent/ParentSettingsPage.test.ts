import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'

import ParentSettingsPage from './ParentSettingsPage.vue'

const savedPreferences = {
  daily_minutes: 45,
  priority_subject_codes: ['ENGLISH'],
  review_only: true,
  reduce_intensity: false,
  enabled_subject_codes: ['MATH', 'ENGLISH'],
  configured: true,
}

const immediateUpdate = {
  saved: true,
  plan_updated: true,
  today_preserved: false,
  applies_from: '2026-09-05',
  answer_controls_available: false,
}

function testRouter(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/parent/settings', component: ParentSettingsPage }],
  })
  return router.push(path).then(() => router)
}

afterEach(() => vi.unstubAllGlobals())

it('hydrates saved preferences including enabled subjects', async () => {
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (path.includes('/preferences')) return { ok: true, json: async () => savedPreferences } as Response
    return { ok: true, json: async () => ({ children: [] }) } as Response
  }))
  const router = await testRouter('/parent/settings?student=student-1')
  const wrapper = mount(ParentSettingsPage, { global: { plugins: [router] } })
  await flushPromises()

  const checked = wrapper.findAll('input[type="checkbox"]:checked').map((box) => (box.element as HTMLInputElement).value)
  expect(checked).toContain('MATH')
  expect(checked).toContain('ENGLISH')
  expect(checked).not.toContain('CHEMISTRY')
  expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('ENGLISH')
})

it('saves the checked subject codes and blocks an empty selection', async () => {
  const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const path = String(input)
    if (path.includes('/preferences') && init?.method === 'PUT') return { ok: true, json: async () => immediateUpdate } as Response
    if (path.includes('/preferences')) return { ok: true, json: async () => savedPreferences } as Response
    return { ok: true, json: async () => ({ children: [] }) } as Response
  })
  vi.stubGlobal('fetch', fetchMock)
  const router = await testRouter('/parent/settings?student=student-1')
  const wrapper = mount(ParentSettingsPage, { global: { plugins: [router] } })
  await flushPromises()

  await wrapper.get('form').trigger('submit')
  await flushPromises()
  const putCall = fetchMock.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === 'PUT')
  expect(putCall).toBeTruthy()
  const payload = JSON.parse(String((putCall?.[1] as RequestInit).body))
  expect(payload.enabled_subject_codes).toEqual(['MATH', 'ENGLISH'])
  expect(wrapper.text()).toContain('已保存并更新今日计划')

  for (const box of wrapper.findAll('input[type="checkbox"]')) {
    if ((box.element as HTMLInputElement).checked) await box.setValue(false)
  }
  const putCallsBefore = fetchMock.mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method === 'PUT').length
  await wrapper.get('form').trigger('submit')
  await flushPromises()
  const putCallsAfter = fetchMock.mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method === 'PUT').length
  expect(putCallsAfter).toBe(putCallsBefore)
  expect(wrapper.text()).toContain('至少开放一个学科')
})

it('explains when a started today plan is preserved', async () => {
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const path = String(input)
    if (path.includes('/preferences') && init?.method === 'PUT') {
      return {
        ok: true,
        json: async () => ({ ...immediateUpdate, today_preserved: true, applies_from: '2026-09-06' }),
      } as Response
    }
    if (path.includes('/preferences')) return { ok: true, json: async () => savedPreferences } as Response
    return { ok: true, json: async () => ({ children: [] }) } as Response
  }))
  const router = await testRouter('/parent/settings?student=student-1')
  const wrapper = mount(ParentSettingsPage, { global: { plugins: [router] } })
  await flushPromises()

  await wrapper.get('form').trigger('submit')
  await flushPromises()

  expect(wrapper.text()).toContain('已保存，今日计划保持不变，将从 2026-09-06 生效')
})
