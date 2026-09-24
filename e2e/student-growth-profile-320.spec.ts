import { expect, test, type Locator, type Page } from '@playwright/test'

function jsonResponse(body: unknown, status = 200) {
  return { status, contentType: 'application/json', body: JSON.stringify(body) }
}

// Long knowledge-point names make the evidence and building lines wrap at 320px.
const growth = {
  student_id: 'student-growth-320',
  learning_date: '2030-01-01',
  total_energy: 42,
  streak_days: 7,
  buildings: {
    mastered_by_subject: { MATH: 2, CHINESE: 1, ENGLISH: 1, PHYSICS: 1, CHEMISTRY: 1 },
    cross_subject_insights: 3,
  },
  growth_evidence: {
    policy_version: 'growth-evidence-v1',
    indicators: [
      { code: 'INDEPENDENT_SOLVING', label: '独立解决', count: 8, events: [{ source_kind: 'CLASSROOM_STAGE_EVIDENCE', source_id: 'event-1', subject: 'MATH', knowledge_point: '一元一次方程在实际购物问题中的应用', occurred_at: '2030-01-01T02:00:00Z' }], events_truncated: false },
      { code: 'UNDERSTANDING_AFTER_HELP', label: '帮助后理解', count: 2, events: [], events_truncated: false },
      { code: 'SELF_CORRECTION', label: '自我纠正', count: 3, events: [], events_truncated: false },
      { code: 'TRANSFER_SUCCESS', label: '迁移成功', count: 4, events: [], events_truncated: false },
      { code: 'DELAYED_REVIEW', label: '延迟复习', count: 1, events: [], events_truncated: false },
    ],
  },
}

async function routeStudent(page: Page, growthStatus = 200) {
  await page.route('**/api/v1/auth/me', (route) => route.fulfill(jsonResponse({
    user_id: 'user-growth-320', role: 'STUDENT', display_name: '测试学生', student_id: 'student-growth-320',
  })))
  await page.route('**/api/v1/student/growth', (route) => route.fulfill(growthStatus === 200
    ? jsonResponse(growth)
    : jsonResponse({ error: { message: 'unavailable' } }, growthStatus)))
  await page.route('**/api/v1/student/sessions/current', (route) => route.fulfill({ status: 204 }))
}

async function fontSize(locator: Locator) {
  return locator.evaluate((node) => Number.parseFloat(getComputedStyle(node).fontSize))
}

async function expectNoHorizontalScroll(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320)
}

test.describe('student growth and profile at 320px', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 720 })
    await page.clock.setFixedTime(new Date('2030-01-01T04:00:00Z'))
  })

  test('growth text stays at 16px or larger while wrapping inside the page', async ({ page }) => {
    await routeStudent(page)
    await page.goto('/student/growth')
    await expect(page.getByText('连续 7 天')).toBeVisible()

    const texts = [
      page.getByText('本周成长'),
      page.getByText('学习证据', { exact: true }),
      page.getByText('连续 7 天'),
      page.getByText('一元一次方程在实际购物问题中的应用', { exact: false }),
      page.getByText('等待新的学习证据').first(),
      page.getByText('2 个数学知识点已点亮'),
      page.getByText('次跨学科发现', { exact: false }),
      page.getByText('兼容能量记录：42'),
    ]
    for (const text of texts) {
      await expect(text).toBeVisible()
      expect(await fontSize(text), await text.textContent() ?? '').toBeGreaterThanOrEqual(16)
    }
    await expectNoHorizontalScroll(page)
  })

  test('a growth failure shows a warm 16px notice', async ({ page }) => {
    await routeStudent(page, 503)
    await page.goto('/student/growth')
    const notice = page.getByTestId('growth-error')
    await expect(notice).toBeVisible()
    expect(await fontSize(notice)).toBeGreaterThanOrEqual(16)
    const [red, green, blue] = (await notice.evaluate((node) => getComputedStyle(node).color)).match(/\d+/g)!.map(Number)
    // #6b3c05 on the warm tint, not the old red-700.
    expect(red > green && green > blue && red < 160).toBe(true)
    await expectNoHorizontalScroll(page)
  })

  test('the student profile has a 16px role line and a 48px sign-out', async ({ page }) => {
    await routeStudent(page)
    await page.goto('/student/profile')
    const role = page.getByTestId('account-role')
    await expect(role).toBeVisible()
    expect(await fontSize(role)).toBeGreaterThanOrEqual(16)
    const signOut = page.getByTestId('account-sign-out')
    expect((await signOut.boundingBox())!.height).toBeGreaterThanOrEqual(48)
    expect(await fontSize(signOut)).toBeGreaterThanOrEqual(16)
    await expectNoHorizontalScroll(page)
  })
})
