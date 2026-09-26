import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

import mainCSS from '../../styles/main.css?raw'
import { useAuthSession } from '../../stores/auth'
import StudentGrowthPage from './StudentGrowthPage.vue'

it('renders growth-base counts from the Student growth response', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => ({
    ok: true,
    json: async () => ({
      student_id: 'student-1',
      learning_date: '2026-08-26',
      total_energy: 42,
      streak_days: 7,
      buildings: {
        completed_days: 7,
        completed_sessions: 9,
        mastered_knowledge_points: 6,
        mastered_by_subject: { MATH: 2, CHINESE: 1, ENGLISH: 1, PHYSICS: 1, CHEMISTRY: 1 },
        cross_subject_insights: 3,
      },
      growth_evidence: {
        policy_version: 'growth-evidence-v1',
        indicators: [
          { code: 'INDEPENDENT_SOLVING', label: '独立解决', count: 8, events: [{ source_kind: 'CLASSROOM_STAGE_EVIDENCE', source_id: 'event-1', subject: 'MATH', knowledge_point: '一元一次方程', occurred_at: '2026-08-26T10:00:00Z' }], events_truncated: false },
          { code: 'UNDERSTANDING_AFTER_HELP', label: '帮助后理解', count: 2, events: [], events_truncated: false },
          { code: 'SELF_CORRECTION', label: '自我纠正', count: 3, events: [], events_truncated: false },
          { code: 'TRANSFER_SUCCESS', label: '迁移成功', count: 4, events: [], events_truncated: false },
          { code: 'DELAYED_REVIEW', label: '延迟复习', count: 1, events: [], events_truncated: false },
        ],
      },
    }),
  } as Response)))

  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthSession().user.value = { user_id: 'user-1', role: 'STUDENT', display_name: '学生', student_id: 'student-1' }
  const wrapper = mount(StudentGrowthPage, { global: { plugins: [pinia] } })
  await flushPromises()

  expect(wrapper.text()).toContain('独立解决')
  expect(wrapper.text()).toContain('帮助后理解')
  expect(wrapper.text()).toContain('自我纠正')
  expect(wrapper.text()).toContain('迁移成功')
  expect(wrapper.text()).toContain('延迟复习')
  expect(wrapper.text()).toContain('兼容能量记录：42')
  expect(wrapper.text()).toContain('连续 7 天')
  expect(wrapper.text()).toContain('2 个数学知识点已点亮')
  expect(wrapper.text()).toContain('2 个语言知识点已点亮')
  expect(wrapper.text()).toContain('2 个科学知识点已点亮 · 3 次跨学科发现')
  expect(wrapper.text()).not.toContain('12 个知识点')
})

function growthResponse() {
  return {
    student_id: 'student-1',
    learning_date: '2026-08-26',
    total_energy: 42,
    streak_days: 7,
    buildings: { mastered_by_subject: { MATH: 2 }, cross_subject_insights: 3 },
    growth_evidence: {
      policy_version: 'growth-evidence-v1',
      indicators: [
        { code: 'INDEPENDENT_SOLVING', label: '独立解决', count: 8, events: [{ source_kind: 'CLASSROOM_STAGE_EVIDENCE', source_id: 'event-1', subject: 'MATH', knowledge_point: '一元一次方程', occurred_at: '2026-08-26T10:00:00Z' }], events_truncated: false },
        { code: 'UNDERSTANDING_AFTER_HELP', label: '帮助后理解', count: 2, events: [], events_truncated: false },
      ],
    },
  }
}

async function mountGrowth(response: { ok: boolean; status: number; body?: unknown }) {
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: response.ok, status: response.status, json: async () => response.body } as Response)))
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthSession().user.value = { user_id: 'user-1', role: 'STUDENT', display_name: '学生', student_id: 'student-1' }
  const wrapper = mount(StudentGrowthPage, { global: { plugins: [pinia] } })
  await flushPromises()
  return wrapper
}

// Tailwind is not compiled in unit tests, so the size is read from the class:
// text-base is 16px and every larger step is at least that.
function expectReadable(element: Element) {
  const classes = Array.from(element.classList)
  expect(classes.some((name) => ['text-base', 'text-lg', 'text-xl', 'text-2xl', 'text-3xl'].includes(name)), `${element.outerHTML} has no 16px-or-larger size`).toBe(true)
  expect(classes.some((name) => name === 'text-sm' || name === 'text-xs'), `${element.outerHTML} is below 16px`).toBe(false)
}

function elementWithText(wrapper: Awaited<ReturnType<typeof mountGrowth>>, selector: string, text: string) {
  const found = wrapper.findAll(selector).filter((node) => node.text().includes(text)).at(-1)
  expect(found, `no ${selector} containing ${text}`).toBeDefined()
  return found!.element
}

it('keeps every growth word a child reads at 16px or larger', async () => {
  const wrapper = await mountGrowth({ ok: true, status: 200, body: growthResponse() })

  expectReadable(elementWithText(wrapper, 'div', '本周成长'))
  expectReadable(elementWithText(wrapper, 'p', '学习证据'))
  expectReadable(elementWithText(wrapper, 'p', '连续 7 天'))
  expectReadable(elementWithText(wrapper, 'span', '独立解决'))
  await wrapper.get('[data-testid="growth-indicator-INDEPENDENT_SOLVING"]').trigger('click')
  expectReadable(elementWithText(wrapper, 'li', '数学 · 一元一次方程'))
  await wrapper.get('[data-testid="growth-indicator-UNDERSTANDING_AFTER_HELP"]').trigger('click')
  expectReadable(elementWithText(wrapper, 'p', '等待新的学习证据'))
  expectReadable(wrapper.get('[data-testid="growth-subject-MATH"]').element)
  expectReadable(elementWithText(wrapper, 'p', '2 个数学知识点已点亮'))
  expectReadable(elementWithText(wrapper, 'p', '次跨学科发现'))
  expectReadable(elementWithText(wrapper, 'p', '兼容能量记录：42'))
  expect(wrapper.html()).not.toMatch(/\btext-(xs|sm)\b/)
  wrapper.unmount()
})

it('shows a growth load failure as a warm notice, not a red error', async () => {
  const wrapper = await mountGrowth({ ok: false, status: 503 })

  const notice = wrapper.get('[data-testid="growth-error"]')
  expect(notice.text()).toContain('成长数据暂时不可用')
  expect(notice.classes()).toContain('notice-warm')
  expectReadable(notice.element)
  for (const name of notice.classes()) expect(name).not.toMatch(/red|error|wrong|danger/)
  expect(wrapper.text()).toContain('等待同步')
  wrapper.unmount()
})

const indicatorCodes = ['INDEPENDENT_SOLVING', 'UNDERSTANDING_AFTER_HELP', 'SELF_CORRECTION', 'TRANSFER_SUCCESS', 'DELAYED_REVIEW'] as const

function manyEvents(total: number) {
  return Array.from({ length: total }, (_, index) => ({
    source_kind: 'CLASSROOM_STAGE_EVIDENCE',
    source_id: `event-${index}`,
    subject: index % 2 ? 'PHYSICS' : 'CHINESE',
    knowledge_point: `知识点${index}`,
    occurred_at: '2026-08-26T10:05:00Z',
  }))
}

function fullGrowthResponse() {
  return {
    ...growthResponse(),
    buildings: { mastered_by_subject: { MATH: 2, ENGLISH: 1 }, cross_subject_insights: 0 },
    growth_evidence: {
      policy_version: 'growth-evidence-v1',
      indicators: [
        { code: 'INDEPENDENT_SOLVING', label: '独立解决', count: 1, events: [{ source_kind: 'CLASSROOM_STAGE_EVIDENCE', source_id: 'event-1', subject: 'MATH', knowledge_point: '一元一次方程', occurred_at: '2026-08-26T10:05:00Z' }], events_truncated: false },
        { code: 'UNDERSTANDING_AFTER_HELP', label: '帮助后理解', count: 0, events: [], events_truncated: false },
        { code: 'SELF_CORRECTION', label: '自我纠正', count: 25, events: manyEvents(20), events_truncated: true },
        { code: 'TRANSFER_SUCCESS', label: '迁移成功', count: 1, events: [{ source_kind: 'REVIEW_QUEUE', source_id: 'event-2', subject: 'CHEMISTRY', knowledge_point: '质量守恒', occurred_at: '2026-08-25T01:00:00Z' }], events_truncated: false },
        { code: 'DELAYED_REVIEW', label: '延迟复习', count: 1, events: [{ source_kind: 'REVIEW_QUEUE', source_id: 'event-3', subject: 'ENGLISH', knowledge_point: '一般过去时', occurred_at: '2026-08-24T12:30:00Z' }], events_truncated: false },
      ],
    },
  }
}

it('opens each evidence category to show the subject, knowledge point and time of every record', async () => {
  const wrapper = await mountGrowth({ ok: true, status: 200, body: fullGrowthResponse() })

  for (const code of indicatorCodes) {
    const button = wrapper.get(`[data-testid="growth-indicator-${code}"]`)
    expect(button.element.tagName).toBe('BUTTON')
    expect(button.attributes('aria-expanded')).toBe('false')
    expect(wrapper.find(`[data-testid="growth-events-${code}"]`).exists()).toBe(false)
  }

  const independent = wrapper.get('[data-testid="growth-indicator-INDEPENDENT_SOLVING"]')
  await independent.trigger('click')
  expect(independent.attributes('aria-expanded')).toBe('true')
  // 10:05 UTC is 18:05 in Shanghai, the child's learning day.
  expect(wrapper.get('[data-testid="growth-events-INDEPENDENT_SOLVING"]').text()).toBe('数学 · 一元一次方程 · 8月26日 18:05')

  await wrapper.get('[data-testid="growth-indicator-DELAYED_REVIEW"]').trigger('click')
  expect(wrapper.find('[data-testid="growth-events-INDEPENDENT_SOLVING"]').exists()).toBe(false)
  expect(wrapper.get('[data-testid="growth-events-DELAYED_REVIEW"]').text()).toBe('英语 · 一般过去时 · 8月24日 20:30')

  await wrapper.get('[data-testid="growth-indicator-TRANSFER_SUCCESS"]').trigger('click')
  expect(wrapper.get('[data-testid="growth-events-TRANSFER_SUCCESS"]').text()).toContain('化学 · 质量守恒')

  await wrapper.get('[data-testid="growth-indicator-UNDERSTANDING_AFTER_HELP"]').trigger('click')
  expect(wrapper.get('[data-testid="growth-events-UNDERSTANDING_AFTER_HELP"]').text()).toBe('等待新的学习证据')

  await wrapper.get('[data-testid="growth-indicator-UNDERSTANDING_AFTER_HELP"]').trigger('click')
  expect(wrapper.find('[data-testid="growth-events-UNDERSTANDING_AFTER_HELP"]').exists()).toBe(false)
  wrapper.unmount()
})

it('says there are earlier records when a category has more than the listed twenty', async () => {
  const wrapper = await mountGrowth({ ok: true, status: 200, body: fullGrowthResponse() })

  await wrapper.get('[data-testid="growth-indicator-SELF_CORRECTION"]').trigger('click')
  const events = wrapper.get('[data-testid="growth-events-SELF_CORRECTION"]')
  expect(events.findAll('li')).toHaveLength(20)
  expect(events.get('[data-testid="growth-events-truncated"]').text()).toBe('还有更早的记录没有列出')
  expect(wrapper.get('[data-testid="growth-indicator-SELF_CORRECTION"]').text()).toContain('25')

  await wrapper.get('[data-testid="growth-indicator-DELAYED_REVIEW"]').trigger('click')
  expect(wrapper.find('[data-testid="growth-events-truncated"]').exists()).toBe(false)
  wrapper.unmount()
})

it('always shows the five buildings and every subject, lighting only what was mastered', async () => {
  const wrapper = await mountGrowth({ ok: true, status: 200, body: fullGrowthResponse() })

  const buildings = {
    MATH_TOWER: ['数学塔', '2 个数学知识点已点亮', 'true'],
    LANGUAGE_HALL: ['语言馆', '1 个语言知识点已点亮', 'true'],
    WORLD_LIBRARY: ['世界图书馆', '0 个知识点已点亮', 'false'],
    SCIENCE_LAB: ['科学实验室', '0 个科学知识点已点亮 · 0 次跨学科发现', 'false'],
    EXPLORATION_STATION: ['探索站', '0 个知识点已点亮', 'false'],
  }
  for (const [key, [name, detail, lit]] of Object.entries(buildings)) {
    const building = wrapper.get(`[data-testid="growth-building-${key}"]`)
    expect(building.text()).toContain(name)
    expect(building.text()).toContain(detail)
    expect(building.attributes('data-lit')).toBe(lit)
    expect(building.find('.growth-lit').exists()).toBe(lit === 'true')
  }
  expect(wrapper.findAll('[data-testid^="growth-building-"]')).toHaveLength(5)

  const subjects = { MATH: ['数学', '2'], CHINESE: ['语文', '0'], ENGLISH: ['英语', '1'], PHYSICS: ['物理', '0'], CHEMISTRY: ['化学', '0'] }
  for (const [code, [name, total]] of Object.entries(subjects)) {
    const subject = wrapper.get(`[data-testid="growth-subject-${code}"]`)
    expect(subject.text()).toBe(`${name}${total}`)
    expect(subject.attributes('data-lit')).toBe(String(total !== '0'))
  }
  wrapper.unmount()
})

it('keeps the energy total as a footer compatibility record, with no score, ranking, badge or answer', async () => {
  const response = fullGrowthResponse()
  const wrapper = await mountGrowth({ ok: true, status: 200, body: response })
  for (const code of indicatorCodes) await wrapper.get(`[data-testid="growth-indicator-${code}"]`).trigger('click')

  const footer = wrapper.get('[data-testid="growth-compat-energy"]')
  expect(footer.text()).toBe('兼容能量记录：42')
  expect(wrapper.get('main').element.lastElementChild).toBe(footer.element)
  expect(wrapper.text().match(/42/g)).toHaveLength(1)

  const text = wrapper.text()
  for (const word of ['经验值', '积分', '分数', '得分', '排名', '排行', '徽章', '勋章', '等级', '升级', '宝箱', '抽奖', '错误', '答案', '解析']) {
    expect(text, word).not.toContain(word)
  }
  const html = wrapper.html().toLowerCase()
  for (const word of ['score', 'rank', 'badge', 'level-up', 'answer', 'solution', 'total_energy']) {
    expect(html, word).not.toContain(word)
  }
  wrapper.unmount()
})

it('lights things up with a short scale that reduced motion turns off', () => {
  const durations = [...mainCSS.matchAll(/\.growth-[\w-]+\s*\{[^}]*?(?:animation|transition):[^;]*?(\d+)ms/g)].map((match) => Number(match[1]))
  expect(durations.length).toBeGreaterThanOrEqual(3)
  for (const duration of durations) {
    expect(duration).toBeGreaterThanOrEqual(150)
    expect(duration).toBeLessThanOrEqual(400)
  }
  for (const keyframes of mainCSS.matchAll(/@keyframes growth-[\w-]+\s*\{([\s\S]*?)\n\}/g)) {
    expect(keyframes[1]).toMatch(/transform: scale/)
    expect(keyframes[1]).not.toMatch(/infinite|rotate/)
  }
  expect(mainCSS).not.toMatch(/\.growth-[\w-]+\s*\{[^}]*infinite/)

  const reduced = [...mainCSS.matchAll(/@media \(prefers-reduced-motion: reduce\) \{([\s\S]*?)\n\}/g)].map((match) => match[1]).join('\n')
  expect(reduced).toMatch(/\.growth-lit,\s*\.growth-events\s*\{\s*animation: none;/)
  expect(reduced).toMatch(/\.growth-indicator-chevron\s*\{\s*transition: none;/)
})
