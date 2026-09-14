import { expect, test, type Locator, type Page } from '@playwright/test'

declare global {
  interface Window {
    __setSoftKeyboardOpen?: (open: boolean) => void
  }
}

const sessionID = 'session-320'
const privateAnswer = 'PRIVATE_STANDARD_ANSWER_MUST_NOT_RENDER'
const privateSolution = 'PRIVATE_FULL_SOLUTION_MUST_NOT_RENDER'
const parentOnly = 'PARENT_SCORE_MUST_NOT_RENDER'
const peerOnly = 'PEER_COMPARISON_MUST_NOT_RENDER'

const session = {
  id: sessionID,
  version: 1,
  timing_version: 1,
  plan_block_id: 'block-320',
  subject_code: 'MATH',
  subject_name: '数学',
  knowledge_point: '一元一次方程',
  difficulty: 'L1',
  question_id: 'question-320',
  prompt: '小明先付 8 元，每增加 1 本练习册再付 3 元。你会怎样找出总费用？',
  scene: { prompt: '小明先付 8 元，每增加 1 本练习册再付 3 元。' },
  input_schema: { type: 'string' },
  started_at: '2030-01-01T00:00:00Z',
  target_minutes: 20,
  status: 'ACTIVE',
  active_seconds: 45,
  current_active_seconds: 12,
  timing_observed_at: '2030-01-01T00:00:45Z',
  state: 'ASK',
  socratic_round: 0,
  timeline: [{
    sequence: 1,
    actor: 'TUTOR',
    action: 'ASK',
    message: '先观察固定起点和每增加一本的变化。',
    at: '2030-01-01T00:00:10Z',
  }],
}

function jsonResponse(body: unknown, status = 200) {
  return { status, contentType: 'application/json', body: JSON.stringify(body) }
}

async function installKeyboardViewportShim(page: Page) {
  await page.addInitScript(() => {
    const state = { open: false }
    const viewport = new EventTarget()
    Object.defineProperties(viewport, {
      width: { get: () => window.innerWidth },
      height: { get: () => state.open ? 360 : window.innerHeight },
      offsetLeft: { get: () => 0 },
      offsetTop: { get: () => 0 },
      pageLeft: { get: () => window.scrollX },
      pageTop: { get: () => window.scrollY },
      scale: { get: () => 1 },
    })
    Object.defineProperty(window, 'visualViewport', { configurable: true, value: viewport })
    Object.defineProperty(window, '__setSoftKeyboardOpen', {
      configurable: true,
      value: (open: boolean) => {
        state.open = open
        viewport.dispatchEvent(new Event('resize'))
        viewport.dispatchEvent(new Event('scroll'))
      },
    })
  })
}

async function assertReachableAboveKeyboard(page: Page, locator: Locator) {
  await locator.scrollIntoViewIfNeeded()
  await expect.poll(async () => locator.evaluate((element) => {
    const rect = element.getBoundingClientRect()
    const viewport = window.visualViewport
    if (!viewport) return false
    return rect.left >= -1
      && rect.right <= viewport.width + 1
      && rect.top >= -1
      && rect.bottom <= viewport.height + 1
  })).toBe(true)
}

test.describe('student classroom at 320px with a soft-keyboard viewport', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 720 })
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await installKeyboardViewportShim(page)

    await page.route('**/api/v1/auth/me', (route) => route.fulfill(jsonResponse({
      user_id: 'user-320', role: 'STUDENT', display_name: '测试学生', student_id: 'student-320',
    })))
    await page.route(`**/api/v1/student/sessions/${sessionID}`, (route) => route.fulfill(jsonResponse(session)))
    await page.route(`**/api/v1/student/sessions/${sessionID}/answers`, async (route) => {
      await route.fulfill(jsonResponse({
        session_id: sessionID,
        version: 2,
        timing_version: 1,
        action: 'HINT',
        message: '继续说说你先找到的固定起点。',
        status: 'ACTIVE',
        active_seconds: 50,
        current_active_seconds: 17,
        timing_observed_at: '2030-01-01T00:00:50Z',
      }))
    })
    await page.route(`**/api/v1/student/sessions/${sessionID}/support`, async (route) => {
      await route.fulfill(jsonResponse({
        session_id: sessionID,
        version: 3,
        timing_version: 1,
        action: 'HINT',
        message: '看看没有买练习册时要先付多少。',
        status: 'ACTIVE',
        active_seconds: 52,
        current_active_seconds: 19,
        timing_observed_at: '2030-01-01T00:00:52Z',
      }))
    })
  })

  test('keeps task and controls reachable while keyboard is open, with reduced motion', async ({ page }) => {
    await page.goto(`/student/session/${sessionID}`)
    await expect(page.getByRole('heading', { name: session.prompt })).toBeVisible()
    await expect(page.locator('#student-answer')).toBeVisible()

    expect(await page.evaluate(() => window.matchMedia('(prefers-reduced-motion: reduce)').matches)).toBe(true)
    await page.setViewportSize({ width: 320, height: 360 })
    await page.evaluate(() => window.__setSoftKeyboardOpen?.(true))

    const answer = page.locator('#student-answer')
    const submit = page.getByRole('button', { name: '提交想法' })
    const hint = page.getByTestId('hint-support')
    const explain = page.getByTestId('explain-support')
    const voice = page.getByRole('button', { name: '使用语音回答' })

    await assertReachableAboveKeyboard(page, page.getByRole('heading', { name: session.prompt }))
    await answer.focus()
    await assertReachableAboveKeyboard(page, answer)
    await answer.fill('我先找固定的 8 元，再看每本增加 3 元。')

    for (const control of [submit, hint, explain, voice]) {
      await assertReachableAboveKeyboard(page, control)
      await expect(control).toBeEnabled()
    }

    expect(await page.evaluate(() => {
      const viewport = window.visualViewport!
      return document.documentElement.scrollWidth <= viewport.width + 1
    })).toBe(true)

    await submit.click()
    await expect(page.getByText('继续说说你先找到的固定起点。')).toBeVisible()

    await hint.click()
    await expect(page.getByText('看看没有买练习册时要先付多少。')).toBeVisible()

    await page.setViewportSize({ width: 320, height: 720 })
    await page.evaluate(() => window.__setSoftKeyboardOpen?.(false))
    expect(await page.evaluate(() => window.visualViewport?.height === window.innerHeight)).toBe(true)
    await answer.scrollIntoViewIfNeeded()
    expect(await answer.evaluate((element) => {
      const rect = element.getBoundingClientRect()
      return rect.top >= -1 && rect.bottom <= window.innerHeight + 1
    })).toBe(true)

    await page.setViewportSize({ width: 320, height: 360 })
    await page.evaluate(() => window.__setSoftKeyboardOpen?.(true))
    const explainRequest = page.waitForRequest((request) => {
      if (!request.url().endsWith(`/api/v1/student/sessions/${sessionID}/support`)) return false
      try { return JSON.parse(request.postData() || '{}').type === 'EXPLAIN' } catch { return false }
    })
    await explain.click()
    await explainRequest
    await page.setViewportSize({ width: 320, height: 720 })
    await page.evaluate(() => window.__setSoftKeyboardOpen?.(false))

    const bodyText = await page.locator('body').innerText()
    for (const forbidden of [privateAnswer, privateSolution, parentOnly, peerOnly, '标准答案', '完整解析', '家长评分', '同龄人比较']) {
      expect(bodyText).not.toContain(forbidden)
    }
  })
})
