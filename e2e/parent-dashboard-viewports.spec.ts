import { expect, test, type Page } from '@playwright/test'

// The parent home page at 320, 375 and 768: text at 16px or larger, every
// control at least 48px and no horizontal scroll, while the child is learning,
// paused or not in a classroom. Every API call is answered by a local fixture.

function jsonResponse(body: unknown) {
  return { status: 200, contentType: 'application/json', body: JSON.stringify(body) }
}

const session = {
  session_id: 'session-dashboard', status: 'ACTIVE', subject: '数学', knowledge_point: '一元一次方程的实际应用',
  active_seconds: 3725, target_minutes: 60, socratic_round: 3, tutor_action: 'VOICE_EXPLAIN',
  error_type: 'FIXED_COST_IGNORED', misconceptions: ['EQ-WORD-FIXED-COST'],
}
const plan = {
  plan_id: 'plan-dashboard', date: '2030-01-01', target_minutes: 60,
  blocks: [
    { id: 'block-1', sequence: 1, subject: '化学', knowledge_point: '物理变化与化学变化', minutes: 15, status: 'COMPLETED', progress: 'COMPLETED', session_id: 'session-0' },
    { id: 'block-2', sequence: 2, subject: '数学', knowledge_point: '一元一次方程的实际应用', minutes: 30, status: 'ACTIVE', progress: 'IN_PROGRESS', session_id: 'session-dashboard' },
    { id: 'block-3', sequence: 3, subject: '物理', knowledge_point: '', minutes: 15, status: 'AVAILABLE', progress: 'NOT_STARTED', session_id: null },
  ],
}

const states = {
  active: { session, today_plan: plan },
  paused: { session: { ...session, status: 'PAUSED' }, today_plan: { ...plan, blocks: plan.blocks.map((block) => block.id === 'block-2' ? { ...block, status: 'AVAILABLE', progress: 'PAUSED' } : block) } },
  waiting: { session: null, today_plan: null },
}

async function routeParent(page: Page, overview: object) {
  // Registered first so the specific routes below take precedence.
  await page.route('**/api/v1/parent/**', (route) => route.fulfill(jsonResponse({})))
  await page.route('**/api/v1/auth/me', (route) => route.fulfill(jsonResponse({
    user_id: 'parent-dashboard', role: 'PARENT', display_name: '测试家长', student_id: null,
  })))
  await page.route('**/api/v1/parent/children', (route) => route.fulfill(jsonResponse({ children: [
    { student_id: 'student-dashboard', display_name: '测试学生', grade_level: 9, active_session_id: null, subject: null, knowledge_point: null, started_at: null },
    { student_id: 'student-sibling', display_name: '测试学生二', grade_level: 7, active_session_id: null, subject: null, knowledge_point: null, started_at: null },
  ] })))
  await page.route('**/api/v1/parent/child/student-dashboard/overview', (route) => route.fulfill(jsonResponse({
    student_id: 'student-dashboard', learning_date: '2030-01-01', ...overview,
  })))
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
  for (const [state, overview] of Object.entries(states)) {
    test(`parent home is readable and tappable at ${viewport.width}px while ${state}`, async ({ page }) => {
      await page.setViewportSize(viewport)
      await routeParent(page, overview)
      await page.goto('/parent')
      if (state === 'waiting') await expect(page.getByTestId('parent-waiting')).toBeVisible()
      else await expect(page.getByTestId('parent-session-card')).toHaveAttribute('data-status', state === 'active' ? 'ACTIVE' : 'PAUSED')
      await expect(page.getByTestId('parent-session-status')).toHaveText(state === 'active' ? '正在学习' : state === 'paused' ? '已暂停' : /实时已连接|正在重连|等待连接/)
      if (state === 'waiting') await expect(page.getByTestId('parent-plan-empty')).toBeVisible()
      else await expect(page.getByTestId('parent-plan-block')).toHaveCount(3)

      const result = await measure(page)
      test.info().annotations.push({ type: 'measure', description: JSON.stringify({ state, width: viewport.width, scrollWidth: result.scrollWidth }) })
      expect(result.texts.length).toBeGreaterThan(0)
      expect(result.texts.filter((item) => item.fontSize < 16), 'text under 16px').toEqual([])
      expect(result.controls.length).toBeGreaterThan(0)
      expect(result.controls.filter((item) => item.height < 48), 'controls under 48px').toEqual([])
      expect(result.scrollWidth, 'scroll width').toBeLessThanOrEqual(viewport.width)
      for (const phrase of ['孩子刚才回答', '标准答案', '发送鼓励']) expect(result.bodyText).not.toContain(phrase)
      if (state === 'paused') expect(result.bodyText).not.toContain('正在学习')
    })
  }
}
