import { expect, test, type Locator, type Page } from '@playwright/test'

function jsonResponse(body: unknown, status = 200) {
  return { status, contentType: 'application/json', body: JSON.stringify(body) }
}

// One running classroom and three plan cards: one that still describes it
// (继续), one it has diverged from (当前课堂已回到…) and one unrelated (解锁).
const session = {
  id: 'session-home-320',
  version: 1,
  timing_version: 1,
  plan_block_id: 'block-diverged',
  subject_code: 'MATH',
  subject_name: '数学',
  knowledge_point: '一元一次方程',
  knowledge_point_id: 'kp-equation',
  difficulty: 'L1',
  question_id: 'question-home-320',
  prompt: '小明先付 8 元，每增加 1 本练习册再付 3 元。你会怎样找出总费用？',
  scene: {},
  input_schema: {},
  started_at: '2030-01-01T00:00:00Z',
  target_minutes: 20,
  status: 'PAUSED',
  active_seconds: 45,
  current_active_seconds: 0,
  timing_observed_at: '2030-01-01T00:00:45Z',
  state: 'ASK',
  socratic_round: 0,
  timeline: [],
}

const today = {
  learning_date: '2030-01-01',
  plans: [{
    id: 'plan-home-320',
    date: '2030-01-01',
    target_minutes: 40,
    blocks: [
      { id: 'block-current', session_id: session.id, sequence: 1, subject: 'MATH', knowledge_point_id: 'kp-equation', minutes: 20, mode: 'LEARN', reason: 'new', focus: '一元一次方程的实际问题', status: 'ACTIVE' },
      { id: 'block-diverged', sequence: 2, subject: 'CHINESE', knowledge_point_id: 'kp-info-extraction', minutes: 10, mode: 'MICRO_BACKTRACK', reason: 'cross_subject_prerequisite', focus: '信息提取', status: 'AVAILABLE' },
      { id: 'block-locked', sequence: 3, subject: 'ENGLISH', knowledge_point_id: 'kp-reading-detail', minutes: 10, mode: 'REVIEW', reason: 'spaced_review_due', focus: '阅读细节', status: 'AVAILABLE' },
    ],
  }],
}

async function routeHome(page: Page, todayStatus = 200) {
  await page.route('**/api/v1/auth/me', (route) => route.fulfill(jsonResponse({
    user_id: 'user-home-320', role: 'STUDENT', display_name: '测试学生', student_id: 'student-home-320',
  })))
  await page.route('**/api/v1/student/today', (route) => route.fulfill(todayStatus === 200
    ? jsonResponse(today)
    : jsonResponse({ error: { message: '今日计划暂时不可用' } }, todayStatus)))
  await page.route('**/api/v1/student/sessions/current', (route) => route.fulfill(jsonResponse(session)))
  await page.route('**/api/v1/student/growth', (route) => route.fulfill(jsonResponse({
    student_id: 'student-home-320', learning_date: '2030-01-01', total_energy: 6, streak_days: 3, buildings: {},
  })))
}

async function fontSize(locator: Locator) {
  return locator.evaluate((node) => Number.parseFloat(getComputedStyle(node).fontSize))
}

test.describe('student home at 320px', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 720 })
    await page.clock.setFixedTime(new Date('2030-01-01T04:00:00Z'))
  })

  test('keeps every word a child reads at 16px or larger and cards at least 48px tall', async ({ page }) => {
    await routeHome(page)
    await page.goto('/student')
    const plan = page.locator('section[aria-labelledby="plan-title"]')
    await expect(plan.getByRole('button')).toHaveCount(3)

    const texts = [
      page.getByText('你好，测试学生'),
      page.getByText('正在进行'),
      page.getByText('继续刚才的发现'),
      page.locator('a[href="/student/session/session-home-320"]').getByText('数学 · 一元一次方程'),
      page.getByText('3 天'),
      plan.getByText('40 分钟'),
      plan.getByText('一元一次方程的实际问题', { exact: false }),
      plan.getByText('继续', { exact: true }),
      plan.getByTestId('plan-block-diverged'),
      plan.getByText('完成当前探索后解锁'),
    ]
    for (const text of texts) {
      await expect(text).toBeVisible()
      expect(await fontSize(text), await text.textContent() ?? '').toBeGreaterThanOrEqual(16)
    }

    for (const card of await plan.getByRole('button').all()) {
      const box = await card.boundingBox()
      expect(box!.height).toBeGreaterThanOrEqual(48)
      // A wrapped status must stay inside the page, not push the card sideways.
      expect(box!.x + box!.width).toBeLessThanOrEqual(320)
    }
    const continueBox = await page.locator('a[href="/student/session/session-home-320"]').boundingBox()
    expect(continueBox!.height).toBeGreaterThanOrEqual(48)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320)
  })

  test('shows a failed plan as a warm notice with a 48px retry', async ({ page }) => {
    await routeHome(page, 503)
    await page.goto('/student')
    const notice = page.getByTestId('home-plan-error')
    await expect(notice).toBeVisible()
    expect(await fontSize(notice)).toBeGreaterThanOrEqual(16)
    const color = await notice.evaluate((node) => getComputedStyle(node).color)
    const [red, green, blue] = color.match(/\d+/g)!.map(Number)
    // #6b3c05 on the warm tint, not the old red-700.
    expect(red > green && green > blue && red < 160).toBe(true)

    const retry = page.getByRole('button', { name: '重试' })
    expect((await retry.boundingBox())!.height).toBeGreaterThanOrEqual(48)
    expect(await fontSize(retry)).toBeGreaterThanOrEqual(16)
  })
})
