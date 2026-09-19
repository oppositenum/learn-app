import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import type { TodayPlan } from '../../api/student'
import { useAuthSession } from '../../stores/auth'
import { useGrowthStore } from '../../stores/growth'
import StudentHomePage from './StudentHomePage.vue'

function response(body: unknown, status = 200): Response {
  return { ok: status >= 200 && status < 300, status, json: async () => body } as Response
}

function plan(date: string, id: string, focus: string): TodayPlan {
  return {
    id,
    date,
    target_minutes: 20,
    blocks: [{
      id: `${id}-block`,
      sequence: 1,
      subject: 'MATH',
      knowledge_point_id: `${id}-knowledge`,
      minutes: 20,
      mode: 'REVIEW',
      reason: 'spaced_review_due',
      focus,
      status: 'AVAILABLE',
    }],
  }
}

function growth(learningDate: string) {
  return { student_id: 'student-1', learning_date: learningDate, total_energy: 6, streak_days: 2, buildings: {} }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((next) => { resolve = next })
  return { promise, resolve }
}

async function mountHome() {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthSession().user.value = { user_id: 'user-1', role: 'STUDENT', display_name: '学生', student_id: 'student-1' }
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/student', component: StudentHomePage },
      { path: '/student/session/:id', component: { template: '<div>课堂</div>' } },
    ],
  })
  await router.push('/student')
  await router.isReady()
  const wrapper = mount(StudentHomePage, { global: { plugins: [pinia, router] } })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-08-26T15:59:00Z'))
})

afterEach(() => {
  useGrowthStore().reset()
  useAuthSession().resetForTests()
  vi.useRealTimers()
  vi.restoreAllMocks()
})

test('refreshes the plan with growth when the Shanghai learning date changes', async () => {
  let todayRequests = 0
  let growthRequests = 0
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/student/today')) {
      todayRequests++
      const current = todayRequests === 1
        ? plan('2026-08-26', 'plan-26', '昨天的复习')
        : plan('2026-08-27', 'plan-27', '今天的新探索')
      return response({ learning_date: current.date, plans: [current] })
    }
    if (path.endsWith('/sessions/current')) return response(null, 204)
    if (path.endsWith('/student/growth')) {
      growthRequests++
      return response(growth(growthRequests === 1 ? '2026-08-26' : '2026-08-27'))
    }
    throw new Error(`unexpected request: ${path}`)
  }))
  const wrapper = await mountHome()

  expect(wrapper.text()).toContain('昨天的复习')
  await vi.advanceTimersByTimeAsync(61_000)
  await flushPromises()

  expect(todayRequests).toBe(2)
  expect(growthRequests).toBe(2)
  expect(wrapper.text()).toContain('今天的新探索')
  expect(wrapper.text()).not.toContain('昨天的复习')
  wrapper.unmount()
})

test('loads a new-day plan after an empty previous day', async () => {
  let todayRequests = 0
  let growthRequests = 0
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/student/today')) {
      todayRequests++
      if (todayRequests === 1) return response({ learning_date: '2026-08-26', plans: [] })
      return response({ learning_date: '2026-08-27', plans: [plan('2026-08-27', 'plan-27', '跨日出现的计划')] })
    }
    if (path.endsWith('/sessions/current')) return response(null, 204)
    if (path.endsWith('/student/growth')) {
      growthRequests++
      return response(growth(growthRequests === 1 ? '2026-08-26' : '2026-08-27'))
    }
    throw new Error(`unexpected request: ${path}`)
  }))
  const wrapper = await mountHome()

  expect(wrapper.text()).toContain('今天没有待完成内容')
  await vi.advanceTimersByTimeAsync(61_000)
  await flushPromises()

  expect(todayRequests).toBe(2)
  expect(wrapper.text()).toContain('跨日出现的计划')
  wrapper.unmount()
})

test('uses the server learning date even when the plan is empty and growth is unavailable', async () => {
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/student/today')) return response({ learning_date: '2026-08-25', plans: [] })
    if (path.endsWith('/sessions/current')) return response(null, 204)
    if (path.endsWith('/student/growth')) return response(null, 503)
    throw new Error(`unexpected request: ${path}`)
  }))
  const wrapper = await mountHome()

  expect(wrapper.text()).toContain('8月25日')
  expect(wrapper.text()).toContain('今天没有待完成内容')
  wrapper.unmount()
})

test('does not let an older home request overwrite a newer learning date', async () => {
  const oldPlan = deferred<Response>()
  let todayRequests = 0
  let growthRequests = 0
  vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/student/today')) {
      todayRequests++
      if (todayRequests === 1) return oldPlan.promise
      return Promise.resolve(response({ learning_date: '2026-08-27', plans: [plan('2026-08-27', 'plan-27', '新日期计划')] }))
    }
    if (path.endsWith('/sessions/current')) return Promise.resolve(response(null, 204))
    if (path.endsWith('/student/growth')) {
      growthRequests++
      return Promise.resolve(response(growth(growthRequests === 1 ? '2026-08-26' : '2026-08-27')))
    }
    throw new Error(`unexpected request: ${path}`)
  }))
  const mounting = mountHome()
  await flushPromises()
  await vi.advanceTimersByTimeAsync(61_000)
  const pageShow = new Event('pageshow')
  Object.defineProperty(pageShow, 'persisted', { value: true })
  window.dispatchEvent(pageShow)
  await flushPromises()

  oldPlan.resolve(response({ learning_date: '2026-08-26', plans: [plan('2026-08-26', 'plan-26', '迟到的旧计划')] }))
  const wrapper = await mounting
  await flushPromises()

  expect(todayRequests).toBeGreaterThanOrEqual(2)
  expect(wrapper.text()).toContain('新日期计划')
  expect(wrapper.text()).not.toContain('迟到的旧计划')
  wrapper.unmount()
})

test('retries once when growth finishes before midnight and today finishes after midnight', async () => {
  const firstToday = deferred<Response>()
  let todayRequests = 0
  let growthRequests = 0
  vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/student/today')) {
      todayRequests++
      if (todayRequests === 1) return firstToday.promise
      return Promise.resolve(response({ learning_date: '2026-08-27', plans: [plan('2026-08-27', 'plan-27', '午夜后的计划')] }))
    }
    if (path.endsWith('/sessions/current')) return Promise.resolve(response(null, 204))
    if (path.endsWith('/student/growth')) {
      growthRequests++
      return Promise.resolve(response(growth(growthRequests === 1 ? '2026-08-26' : '2026-08-27')))
    }
    throw new Error(`unexpected request: ${path}`)
  }))
  const wrapper = await mountHome()
  vi.setSystemTime(new Date('2026-08-26T16:00:01Z'))
  firstToday.resolve(response({ learning_date: '2026-08-27', plans: [plan('2026-08-27', 'plan-27', '午夜后的计划')] }))
  await flushPromises()

  expect(todayRequests).toBe(2)
  expect(growthRequests).toBe(2)
  expect(wrapper.text()).toContain('午夜后的计划')
  expect(wrapper.text()).toContain('2 天')
  wrapper.unmount()
})

test('retries once when today finishes before midnight and growth finishes after midnight', async () => {
  const firstGrowth = deferred<Response>()
  let todayRequests = 0
  let growthRequests = 0
  vi.stubGlobal('fetch', vi.fn((input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/student/today')) {
      todayRequests++
      const current = todayRequests === 1
        ? plan('2026-08-26', 'plan-26', '午夜前的计划')
        : plan('2026-08-27', 'plan-27', '午夜后的计划')
      return Promise.resolve(response({ learning_date: current.date, plans: [current] }))
    }
    if (path.endsWith('/sessions/current')) return Promise.resolve(response(null, 204))
    if (path.endsWith('/student/growth')) {
      growthRequests++
      if (growthRequests === 1) return firstGrowth.promise
      return Promise.resolve(response(growth('2026-08-27')))
    }
    throw new Error(`unexpected request: ${path}`)
  }))
  const wrapper = await mountHome()
  vi.setSystemTime(new Date('2026-08-26T16:00:01Z'))
  firstGrowth.resolve(response(growth('2026-08-27')))
  await flushPromises()

  expect(todayRequests).toBe(2)
  expect(growthRequests).toBe(2)
  expect(wrapper.text()).toContain('午夜后的计划')
  expect(wrapper.text()).not.toContain('午夜前的计划')
  expect(wrapper.text()).toContain('2 天')
  wrapper.unmount()
})

test('hides a mismatched streak and stops after one bounded retry', async () => {
  let todayRequests = 0
  let growthRequests = 0
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/student/today')) {
      todayRequests++
      return response({ learning_date: '2026-08-27', plans: [plan('2026-08-27', 'plan-27', '服务端计划')] })
    }
    if (path.endsWith('/sessions/current')) return response(null, 204)
    if (path.endsWith('/student/growth')) {
      growthRequests++
      return response(growth('2026-08-26'))
    }
    throw new Error(`unexpected request: ${path}`)
  }))
  const wrapper = await mountHome()
  await flushPromises()

  expect(todayRequests).toBe(2)
  expect(growthRequests).toBe(2)
  expect(wrapper.get('[aria-label="连续学习天数正在同步"]').text()).toBe('--')
  wrapper.unmount()
})

// A cross-subject MICRO_BACKTRACK block opens a session for its own
// prerequisite. Once the classroom returns to the original task, the session
// serves another subject while plan_block_id still points at this block. The
// card must stop presenting itself as that classroom.
function backtrackPlan(): TodayPlan {
  return {
    id: 'plan-guard',
    date: '2026-08-26',
    target_minutes: 20,
    blocks: [{
      id: 'block-chinese',
      sequence: 1,
      subject: 'CHINESE',
      knowledge_point_id: 'kp-info-extraction',
      minutes: 10,
      mode: 'MICRO_BACKTRACK',
      reason: 'cross_subject_prerequisite',
      focus: '信息提取',
      status: 'AVAILABLE',
    }],
  }
}

function currentSession(overrides: Record<string, unknown> = {}) {
  return {
    id: 'session-guard',
    version: 1,
    timing_version: 1,
    plan_block_id: 'block-chinese',
    subject_code: 'CHINESE',
    subject_name: '语文',
    knowledge_point: '信息提取',
    knowledge_point_id: 'kp-info-extraction',
    difficulty: 'L1',
    question_id: 'question-guard',
    prompt: '通知写着：周五下午三点集合。',
    scene: {},
    input_schema: {},
    started_at: '2026-08-26T12:00:00Z',
    target_minutes: 10,
    status: 'PAUSED',
    active_seconds: 30,
    current_active_seconds: 0,
    timing_observed_at: '2026-08-26T12:00:30Z',
    state: 'ASK',
    socratic_round: 0,
    timeline: [],
    ...overrides,
  }
}

async function mountWithCurrent(session: Record<string, unknown> | null) {
  vi.stubGlobal('fetch', vi.fn(async (input: string | URL | Request) => {
    const path = String(input)
    if (path.endsWith('/student/today')) return response({ learning_date: '2026-08-26', plans: [backtrackPlan()] })
    if (path.endsWith('/sessions/current')) return session ? response(session) : response(null, 204)
    if (path.endsWith('/student/growth')) return response(growth('2026-08-26'))
    throw new Error(`unexpected request: ${path}`)
  }))
  return mountHome()
}

test('a plan card that still describes the classroom can be continued', async () => {
  const wrapper = await mountWithCurrent(currentSession())
  const card = wrapper.findAll('button').find((button) => button.text().includes('信息提取'))!

  expect(card.text()).toContain('继续')
  expect(card.attributes('disabled')).toBeUndefined()
  expect(wrapper.find('[data-testid="plan-block-diverged"]').exists()).toBe(false)

  await card.trigger('click')
  await flushPromises()
  expect(wrapper.vm.$router.currentRoute.value.path).toBe('/student/session/session-guard')
  wrapper.unmount()
})

test('a plan card whose subject no longer matches the classroom cannot be continued', async () => {
  // The live case: the classroom returned to the English original task.
  const wrapper = await mountWithCurrent(currentSession({
    subject_code: 'ENGLISH', subject_name: '英语',
    knowledge_point: '英语阅读细节', knowledge_point_id: 'kp-reading-detail',
  }))
  const card = wrapper.findAll('button').find((button) => button.text().includes('信息提取'))!

  expect(card.text()).not.toContain('继续')
  expect(card.attributes('disabled')).toBe('')
  const notice = wrapper.get('[data-testid="plan-block-diverged"]')
  expect(notice.text()).toContain('英语')
  expect(notice.text()).toContain('英语阅读细节')

  await card.trigger('click')
  await flushPromises()
  expect(wrapper.vm.$router.currentRoute.value.path).toBe('/student')
  wrapper.unmount()
})

test('a plan card whose knowledge point changed within the same subject cannot be continued', async () => {
  // Identity is the knowledge point id, not the displayed focus text.
  const wrapper = await mountWithCurrent(currentSession({
    knowledge_point: '证据定位', knowledge_point_id: 'kp-evidence-location',
  }))
  const card = wrapper.findAll('button').find((button) => button.text().includes('信息提取'))!

  expect(card.text()).not.toContain('继续')
  expect(card.attributes('disabled')).toBe('')
  expect(wrapper.get('[data-testid="plan-block-diverged"]').text()).toContain('证据定位')
  wrapper.unmount()
})

test('a missing knowledge point id is not treated as a match', async () => {
  const session = currentSession()
  delete (session as Record<string, unknown>).knowledge_point_id
  const wrapper = await mountWithCurrent(session)
  const card = wrapper.findAll('button').find((button) => button.text().includes('信息提取'))!

  expect(card.text()).not.toContain('继续')
  expect(card.attributes('disabled')).toBe('')
  wrapper.unmount()
})

test('a plan card unrelated to the running classroom keeps the existing lock', async () => {
  const wrapper = await mountWithCurrent(currentSession({
    id: 'session-other', plan_block_id: 'block-other',
  }))
  const card = wrapper.findAll('button').find((button) => button.text().includes('信息提取'))!

  expect(card.text()).toContain('完成当前探索后解锁')
  expect(card.attributes('disabled')).toBe('')
  expect(wrapper.find('[data-testid="plan-block-diverged"]').exists()).toBe(false)
  wrapper.unmount()
})

test('a plan card sharing a knowledge point id under another subject cannot be continued', async () => {
  // Both halves of the identity are compared. Holding the knowledge point id
  // fixed isolates the subject check, so removing it cannot pass unnoticed.
  const wrapper = await mountWithCurrent(currentSession({
    subject_code: 'ENGLISH', subject_name: '英语', knowledge_point: '英语阅读细节',
  }))
  const card = wrapper.findAll('button').find((button) => button.text().includes('信息提取'))!

  expect(card.text()).not.toContain('继续')
  expect(card.attributes('disabled')).toBe('')
  expect(wrapper.get('[data-testid="plan-block-diverged"]').text()).toContain('英语')
  wrapper.unmount()
})
