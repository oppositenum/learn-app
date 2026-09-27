import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'

import ParentSettingsPage from './ParentSettingsPage.vue'

const savedPreferences = {
  daily_minutes: 45,
  priority_subject_codes: ['ENGLISH'],
  review_only: true,
  reduce_intensity: false,
  enabled_subject_codes: ['MATH', 'ENGLISH'],
  priority_domain_id: 'domain-math-equation',
  domain_options: [
    { id: 'domain-math-equation', subject_code: 'MATH', name: '方程与不等式' },
    { id: 'domain-english-vocab', subject_code: 'ENGLISH', name: '词汇' },
    { id: 'domain-physics-motion', subject_code: 'PHYSICS', name: '运动' },
  ],
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
  expect(wrapper.html()).not.toMatch(/\btext-(xs|sm)\b/)
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
  expect(wrapper.text()).toContain('已保存，今日计划已更新，生效日期是 2026-09-05')

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


function putPayloads(fetchMock: ReturnType<typeof vi.fn>) {
  return fetchMock.mock.calls
    .filter(([, init]) => (init as RequestInit | undefined)?.method === 'PUT')
    .map(([, init]) => JSON.parse(String((init as RequestInit).body)) as Record<string, unknown>)
}

async function mountWithSaves(update: object = immediateUpdate, preferences: object = savedPreferences) {
  const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const path = String(input)
    if (path.includes('/preferences') && init?.method === 'PUT') return { ok: true, json: async () => update } as Response
    if (path.includes('/preferences')) return { ok: true, json: async () => preferences } as Response
    return { ok: true, json: async () => ({ children: [] }) } as Response
  })
  vi.stubGlobal('fetch', fetchMock)
  const router = await testRouter('/parent/settings?student=student-1')
  const wrapper = mount(ParentSettingsPage, { global: { plugins: [router] } })
  await flushPromises()
  return { wrapper, fetchMock }
}

it('offers only knowledge domains of the opened subjects and saves the domain id', async () => {
  const { wrapper, fetchMock } = await mountWithSaves()
  const select = wrapper.get('[data-testid="parent-priority-domain"]')
  expect((select.element as HTMLSelectElement).value).toBe('domain-math-equation')
  const options = select.findAll('option')
  expect(options.map((option) => option.attributes('value'))).toEqual(['', 'domain-math-equation', 'domain-english-vocab'])
  expect(options[0].text()).toBe('不指定')
  expect(options[1].text()).toBe('数学 · 方程与不等式')

  await select.setValue('domain-english-vocab')
  await wrapper.get('form').trigger('submit')
  await flushPromises()
  expect(putPayloads(fetchMock).at(-1)?.priority_domain_id).toBe('domain-english-vocab')

  // Closing English drops its domain; the plan is left without a domain.
  const english = wrapper.findAll('input[type="checkbox"]').find((box) => (box.element as HTMLInputElement).value === 'ENGLISH')
  await english!.setValue(false)
  expect((select.element as HTMLSelectElement).value).toBe('')
  expect(select.findAll('option').map((option) => option.attributes('value'))).toEqual(['', 'domain-math-equation'])
  await wrapper.get('form').trigger('submit')
  await flushPromises()
  expect(putPayloads(fetchMock).at(-1)?.priority_domain_id).toBeNull()
})

it('records review only and reduced intensity together when today is not a good day', async () => {
  const { wrapper, fetchMock } = await mountWithSaves(immediateUpdate, { ...savedPreferences, review_only: false, reduce_intensity: false })
  const stateNotGood = wrapper.get('[data-testid="parent-state-not-good"]')
  expect((stateNotGood.element as HTMLInputElement).checked).toBe(false)

  await stateNotGood.setValue(true)
  expect((wrapper.get('[data-testid="parent-review-only"]').element as HTMLInputElement).checked).toBe(true)
  expect((wrapper.get('[data-testid="parent-reduce-intensity"]').element as HTMLInputElement).checked).toBe(true)
  await wrapper.get('form').trigger('submit')
  await flushPromises()
  const payload = putPayloads(fetchMock).at(-1)!
  expect(payload.state_not_good).toBe(true)
  expect(payload.review_only).toBe(true)
  expect(payload.reduce_intensity).toBe(true)
  expect(wrapper.get('[data-testid="parent-settings-status"]').text()).toBe('已保存，今日计划已更新，生效日期是 2026-09-05')

  // Unticking one of the two unticks "not a good day" as well.
  await wrapper.get('[data-testid="parent-review-only"]').setValue(false)
  expect((stateNotGood.element as HTMLInputElement).checked).toBe(false)
  expect((wrapper.get('[data-testid="parent-reduce-intensity"]').element as HTMLInputElement).checked).toBe(true)
})

it('shows the server date when today is kept and sends only plan fields', async () => {
  const { wrapper, fetchMock } = await mountWithSaves({ ...immediateUpdate, today_preserved: true, applies_from: '2026-09-06' })
  await wrapper.get('[data-testid="parent-state-not-good"]').setValue(true)
  await wrapper.get('form').trigger('submit')
  await flushPromises()

  expect(wrapper.get('[data-testid="parent-settings-status"]').text()).toBe('已保存，今日计划保持不变，将从 2026-09-06 生效')
  expect(Object.keys(putPayloads(fetchMock).at(-1)!).sort()).toEqual([
    'daily_minutes', 'enabled_subject_codes', 'priority_domain_id', 'priority_subject_codes', 'reduce_intensity', 'review_only', 'state_not_good',
  ])
  const html = wrapper.html()
  for (const phrase of ['发答案', '发送答案', '替孩子', '替答', '代答', '标准答案', '发送鼓励', '选择题目']) expect(html).not.toContain(phrase)
  expect(html).not.toMatch(/\btext-(xs|sm)\b/)
})
