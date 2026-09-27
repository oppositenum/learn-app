import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'

import ParentLivePage from './ParentLivePage.vue'

// The live page reads everything from the supervision connection; each test
// sets the classroom it wants to see.
const question = 'Q-PROMPT-MARKER'
const answer = 'CORRECT-ANSWER-MARKER'
const preview = 'CHILD-PREVIEW-MARKER'
const tutorWords = 'TUTOR-TURN-MARKER'

function running() {
  return {
    studentID: 'student-1', sessionID: 'session-1', sessionActive: true, sessionStatus: 'ACTIVE',
    connected: true, reconnecting: false, connectionError: '', interventionStatus: '', elapsed: '02:05',
    children: [{ student_id: 'student-1', display_name: '小航', active_session_id: 'session-1' }],
    subject: '数学', knowledgePoint: '一元一次方程', questionPrompt: question, correctAnswer: answer,
    answerVisibility: 'SHORT_CURRENT', studentAnswerPreview: preview,
    misconception: 'FIXED_COST_IGNORED', tutorAction: 'PROBE', tutorReason: '先检查孩子当前思路。', socraticRound: 2,
    hintCount: 2, emotion: 'CALM', masteryState: 'LEARNING', masteryScore: 40,
    timeline: [
      { id: '1', actor: 'STUDENT', text: '孩子提交了一次回答', meta: 'ANSWER_SUBMITTED' },
      { id: '2', actor: 'TUTOR', text: tutorWords, meta: 'PROBE' },
    ],
  }
}

const learning = reactive({
  ...running(),
  selectChild: vi.fn(), intervene: vi.fn(), refreshSession: vi.fn(),
})
vi.mock('../../features/supervision/useParentLiveConnection', () => ({ useParentLiveConnection: () => learning }))

async function mountLive(state: Partial<ReturnType<typeof running>> = {}) {
  Object.assign(learning, running(), state)
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/parent/live', component: ParentLivePage },
    { path: '/parent', component: { template: '<div />' } },
  ] })
  await router.push('/parent/live')
  const wrapper = mount(ParentLivePage, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

enableAutoUnmount(afterEach)

afterEach(() => {
  vi.useRealTimers()
  learning.refreshSession.mockReset()
})

function expectNoAnswerHandOff(wrapper: Awaited<ReturnType<typeof mountLive>>) {
  const html = wrapper.html()
  for (const phrase of ['发答案', '发送答案', '替孩子', '替答', '代答', '错误', '红叉']) expect(html).not.toContain(phrase)
  expect(html).not.toMatch(/\btext-(xs|sm)\b|detail-label/)
}

it('shows the question, the answers, the tutor turn, hints, round and emotion while the child learns', async () => {
  const wrapper = await mountLive()

  expect(wrapper.get('[data-testid="parent-live-status"]').text()).toBe('正在学习')
  expect(wrapper.get('[data-testid="parent-live-question"]').text()).toBe(question)
  expect(wrapper.get('[data-testid="parent-live-correct-answer"]').text()).toBe(answer)
  expect(wrapper.get('[data-testid="parent-live-answer"]').attributes('data-visibility')).toBe('SHORT_CURRENT')
  expect(wrapper.get('[data-testid="parent-live-answer"]').text()).toBe(preview)
  expect(wrapper.get('[data-testid="parent-live-judgement"]').text()).toBe('FIXED_COST_IGNORED')
  expect(wrapper.get('[data-testid="parent-live-tutor-turn"]').text()).toBe(tutorWords)
  expect(wrapper.get('[data-testid="parent-live-action"]').text()).toBe('PROBE')
  expect(wrapper.get('[data-testid="parent-live-round"]').text()).toBe('第 2 轮 / 3')
  expect(wrapper.get('[data-testid="parent-live-hints"]').text()).toBe('提示 2 次')
  expect(wrapper.get('[data-testid="parent-live-emotion"]').text()).toBe('状态平稳')
  expect(wrapper.find('[data-testid="parent-live-paused"]').exists()).toBe(false)
  expect(wrapper.find('[data-testid="parent-live-waiting"]').exists()).toBe(false)

  // Only the two existing parent actions are offered.
  expect(wrapper.findAll('button').map((button) => button.text())).toEqual(['发送鼓励', '降低今天强度'])
  await wrapper.findAll('button')[0].trigger('click')
  expect(learning.intervene).toHaveBeenCalledWith('ENCOURAGEMENT')
  expectNoAnswerHandOff(wrapper)
})

it('names the child\'s state with one of three gentle labels', async () => {
  for (const [emotion, label] of [['CALM', '状态平稳'], ['BORED', '有点倦'], ['FRUSTRATED', '有点烦']] as const) {
    const wrapper = await mountLive({ emotion })
    const node = wrapper.get('[data-testid="parent-live-emotion"]')
    expect(node.attributes('data-emotion')).toBe(emotion)
    expect(node.text()).toBe(label)
    wrapper.unmount()
  }
})

it('hides a long or missing answer instead of showing it', async () => {
  let wrapper = await mountLive({ answerVisibility: 'WITHHELD_LONG', studentAnswerPreview: '' })
  expect(wrapper.get('[data-testid="parent-live-answer"]').text()).toBe('较长回答已隐藏')
  wrapper.unmount()
  wrapper = await mountLive({ answerVisibility: 'NONE', studentAnswerPreview: '' })
  expect(wrapper.get('[data-testid="parent-live-answer"]').text()).toBe('尚未提交简短回答')
  expect(wrapper.get('[data-testid="parent-live-hints"]').text()).toBe('提示 2 次')
})

it('shows a paused classroom without the question or any answer', async () => {
  // Even if old values were still held, the paused view must not render them.
  const wrapper = await mountLive({ sessionActive: false, sessionStatus: 'PAUSED', emotion: 'BORED', hintCount: 1 })

  expect(wrapper.get('[data-testid="parent-live-status"]').text()).toBe('已暂停')
  expect(wrapper.get('#paused-title').text()).toBe('已暂停')
  expect(wrapper.get('[data-testid="parent-live-round"]').text()).toBe('第 2 轮 / 3')
  expect(wrapper.get('[data-testid="parent-live-hints"]').text()).toBe('提示 1 次')
  expect(wrapper.get('[data-testid="parent-live-emotion"]').text()).toBe('有点倦')
  for (const id of ['parent-live-active', 'parent-live-question', 'parent-live-answer', 'parent-live-correct-answer', 'parent-live-tutor-turn', 'parent-live-waiting']) {
    expect(wrapper.find(`[data-testid="${id}"]`).exists(), id).toBe(false)
  }
  const html = wrapper.html()
  for (const marker of [question, answer, preview, tutorWords]) expect(html.includes(marker)).toBe(false)
  expect(wrapper.find('button').exists()).toBe(false)
  expectNoAnswerHandOff(wrapper)
})

it('waits for the child without showing the last question', async () => {
  const wrapper = await mountLive({ sessionActive: false, sessionStatus: 'COMPLETED' })

  expect(wrapper.get('[data-testid="parent-live-waiting"]').text()).toContain('等待孩子开始课堂')
  expect(wrapper.find('[data-testid="parent-live-paused"]').exists()).toBe(false)
  const html = wrapper.html()
  for (const marker of [question, answer, preview, tutorWords]) expect(html.includes(marker)).toBe(false)
  expectNoAnswerHandOff(wrapper)
})

it('reads a running classroom again so a quiet one turns paused', async () => {
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
  await mountLive()
  await vi.advanceTimersByTimeAsync(15000)
  expect(learning.refreshSession).toHaveBeenCalledWith('student-1', 'session-1')

  learning.refreshSession.mockReset()
  learning.sessionActive = false
  learning.sessionStatus = 'PAUSED'
  await vi.advanceTimersByTimeAsync(15000)
  expect(learning.refreshSession).not.toHaveBeenCalled()
})
