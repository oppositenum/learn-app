import { expect, test, type Page } from '@playwright/test'

// The parent live classroom at 320, 375 and 768: text at 16px or larger, every
// control at least 48px and no horizontal scroll, while the child is learning,
// paused or not in a classroom. Every API call is answered by a local fixture.

function jsonResponse(body: unknown) {
  return { status: 200, contentType: 'application/json', body: JSON.stringify(body) }
}

const question = 'Q-PROMPT-MARKER 小明买了三杯同价饮料和一份配送费，一共付了三十六元'
const answer = 'CORRECT-ANSWER-MARKER'
const preview = 'CHILD-PREVIEW-MARKER'
const tutorWords = 'TUTOR-TURN-MARKER 配送费是哪一部分？'

const active = {
  session_id: 'session-live', student_id: 'student-live', subject: '数学', knowledge_point: '一元一次方程的实际应用',
  started_at: '2030-01-01T01:00:00Z', status: 'ACTIVE', current_state: 'PROBE', socratic_round: 2, engagement: 'NORMAL',
  emotion: 'FRUSTRATED', hint_count: 2, question_prompt: question, correct_answer: { value: answer }, full_solution: 'FULL-SOLUTION-MARKER',
  detail_mode: 'LIVE', student_answer_visibility: 'SHORT_CURRENT', student_answer_preview: preview,
  answer_correct: false, error_type: 'FIXED_COST_IGNORED', misconceptions: ['EQ-WORD-FIXED-COST'],
  tutor_action: 'VOICE_EXPLAIN', tutor_reason: 'deescalate with a short voice explanation',
  target_minutes: 60, active_seconds: 725, mastery_state: 'LEARNING', mastery_score: 42,
  timeline: [
    { sequence: 1, actor: 'STUDENT', message: '孩子提交了一次回答', at: '2030-01-01T01:01:00Z' },
    { sequence: 2, actor: 'TUTOR', action: 'VOICE_EXPLAIN', message: tutorWords, at: '2030-01-01T01:02:00Z' },
  ],
}
const paused = {
  ...active, status: 'PAUSED', detail_mode: 'REPORT', question_prompt: '', correct_answer: null, full_solution: '',
  student_answer_visibility: 'WITHHELD_NOT_ACTIVE', student_answer_preview: undefined, emotion: 'BORED',
  timeline: [
    { sequence: 1, actor: 'STUDENT', message: '孩子提交了一次回答', at: '2030-01-01T01:01:00Z' },
    { sequence: 2, actor: 'TUTOR', action: 'VOICE_EXPLAIN', message: 'Tutor 完成了 VOICE_EXPLAIN 教学步骤', at: '2030-01-01T01:02:00Z' },
  ],
}

const states = { active, paused, waiting: null }

async function routeParent(page: Page, session: object | null) {
  // Registered first so the specific routes below take precedence.
  await page.route('**/api/v1/parent/**', (route) => route.fulfill(jsonResponse({})))
  await page.route('**/api/v1/auth/me', (route) => route.fulfill(jsonResponse({
    user_id: 'parent-live', role: 'PARENT', display_name: '测试家长', student_id: null,
  })))
  // The children list names only a running classroom; a paused one is opened
  // through the session in the address.
  await page.route('**/api/v1/parent/children', (route) => route.fulfill(jsonResponse({ children: [
    { student_id: 'student-live', display_name: '测试学生', grade_level: 9, active_session_id: session === active ? 'session-live' : null, subject: null, knowledge_point: null, started_at: null },
    { student_id: 'student-sibling', display_name: '测试学生二', grade_level: 7, active_session_id: null, subject: null, knowledge_point: null, started_at: null },
  ] })))
  await page.route('**/api/v1/parent/child/student-live/session/session-live', (route) => route.fulfill(jsonResponse(session)))
}

async function measure(page: Page) {
  return page.evaluate(() => {
    const visible = (element: Element) => {
      const style = getComputedStyle(element)
      if (style.visibility === 'hidden' || style.display === 'none') return false
      const rect = element.getBoundingClientRect()
      return rect.width > 1 && rect.height > 1
    }
    const texts: { text: string, fontSize: number }[] = []
    for (const element of document.body.querySelectorAll('*')) {
      if (['SCRIPT', 'STYLE', 'svg', 'path'].includes(element.tagName)) continue
      const own = [...element.childNodes].filter((node) => node.nodeType === Node.TEXT_NODE).map((node) => node.textContent ?? '').join('').trim()
      if (!own || !visible(element)) continue
      texts.push({ text: own.slice(0, 24), fontSize: Number.parseFloat(getComputedStyle(element).fontSize) })
    }
    const controls: { label: string, height: number }[] = []
    for (const element of document.body.querySelectorAll('button, a[href], select, [role="button"]')) {
      if (!visible(element)) continue
      const label = (element.getAttribute('aria-label') || (element as HTMLElement).innerText || element.tagName).trim().slice(0, 24)
      // Layout rounding reports 48px targets as 47.99997px.
      controls.push({ label, height: Math.round(element.getBoundingClientRect().height * 100) / 100 })
    }
    return { texts, controls, scrollWidth: document.documentElement.scrollWidth, bodyText: document.body.innerText }
  })
}

for (const viewport of [{ width: 320, height: 720 }, { width: 375, height: 812 }, { width: 768, height: 1024 }]) {
  for (const [state, session] of Object.entries(states)) {
    test(`parent live classroom is readable and tappable at ${viewport.width}px while ${state}`, async ({ page }) => {
      await page.setViewportSize(viewport)
      await routeParent(page, session)
      await page.goto(session ? '/parent/live?student=student-live&session=session-live' : '/parent/live?student=student-live')
      if (state === 'active') {
        await expect(page.getByTestId('parent-live-status')).toHaveText('正在学习')
        await expect(page.getByTestId('parent-live-question')).toHaveText(question)
        await expect(page.getByTestId('parent-live-correct-answer')).toHaveText(answer)
        await expect(page.getByTestId('parent-live-answer')).toHaveText(preview)
        await expect(page.getByTestId('parent-live-tutor-turn')).toHaveText(tutorWords)
        await expect(page.getByTestId('parent-live-hints')).toHaveText('提示 2 次')
        await expect(page.getByTestId('parent-live-round')).toHaveText('第 2 轮 / 3')
        await expect(page.getByTestId('parent-live-action')).toHaveText('VOICE_EXPLAIN')
        await expect(page.getByTestId('parent-live-emotion')).toHaveText('有点烦')
      } else if (state === 'paused') {
        await expect(page.getByTestId('parent-live-status')).toHaveText('已暂停')
        await expect(page.getByTestId('parent-live-paused')).toBeVisible()
        await expect(page.getByTestId('parent-live-emotion')).toHaveText('有点倦')
      } else {
        await expect(page.getByTestId('parent-live-waiting')).toBeVisible()
      }

      const result = await measure(page)
      test.info().annotations.push({ type: 'measure', description: JSON.stringify({ state, width: viewport.width, scrollWidth: result.scrollWidth }) })
      expect(result.texts.length).toBeGreaterThan(0)
      expect(result.texts.filter((item) => item.fontSize < 16), 'text under 16px').toEqual([])
      expect(result.controls.length).toBeGreaterThan(0)
      expect(result.controls.filter((item) => item.height < 48), 'controls under 48px').toEqual([])
      expect(result.scrollWidth, 'scroll width').toBeLessThanOrEqual(viewport.width)
      for (const phrase of ['FULL-SOLUTION-MARKER', '发答案', '发送答案', '替孩子', '替答', '错误', '红叉']) expect(result.bodyText.includes(phrase), phrase).toBe(false)
      if (state !== 'active') {
        for (const marker of ['Q-PROMPT-MARKER', answer, preview, 'TUTOR-TURN-MARKER']) expect(result.bodyText.includes(marker), 'private marker').toBe(false)
        expect(result.bodyText).not.toContain('正在学习')
        expect(result.bodyText).not.toContain('发送鼓励')
      } else {
        expect(result.controls.map((item) => item.label)).toEqual(expect.arrayContaining(['发送鼓励', '降低今天强度']))
      }
    })
  }
}
