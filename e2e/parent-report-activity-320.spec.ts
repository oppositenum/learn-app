import { expect, test, type Page } from '@playwright/test'

function jsonResponse(body: unknown) {
  return { status: 200, contentType: 'application/json', body: JSON.stringify(body) }
}

// Fourteen days with the widest minute count a day can reach, so a cell that
// fits these fits every real day.
const days = Array.from({ length: 14 }, (_, index) => ({
  date: `2030-01-${String(index + 10)}`, completed_sessions: 1, active_seconds: index === 0 ? 86400 : 6000,
}))

async function routeParent(page: Page) {
  // Registered first so the specific routes below take precedence.
  await page.route('**/api/v1/parent/**', (route) => route.fulfill(jsonResponse({ events: [] })))
  await page.route('**/api/v1/auth/me', (route) => route.fulfill(jsonResponse({
    user_id: 'parent-activity-320', role: 'PARENT', display_name: '测试家长', student_id: null,
  })))
  await page.route('**/api/v1/parent/children', (route) => route.fulfill(jsonResponse({ children: [{
    student_id: 'student-activity-320', display_name: '测试学生', grade_level: 7, active_session_id: null, subject: null, knowledge_point: null, started_at: null,
  }] })))
  await page.route('**/api/v1/parent/child/student-activity-320/report', (route) => route.fulfill(jsonResponse({
    student_id: 'student-activity-320',
    summary: { completed_sessions: 14, active_seconds: 164400, total_energy: 0, streak_days: 14, reward_events: 0 },
    activity_days: days,
    recent_sessions: [],
    growth_evidence: { policy_version: 'growth-evidence-v1', indicators: [] },
  })))
}

for (const viewport of [{ width: 320, height: 720, maxColumns: 4 }, { width: 768, height: 1024, maxColumns: 7 }]) {
  test(`parent activity days fit their cells at ${viewport.width}px`, async ({ page }) => {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await routeParent(page)
    await page.goto('/parent/report?student=student-activity-320')
    const cells = page.getByTestId('activity-day')
    await expect(cells).toHaveCount(14)

    const layout = await cells.evaluateAll((nodes) => nodes.map((cell) => {
      const box = cell.getBoundingClientRect()
      const parts = [...cell.children].map((part) => {
        const range = document.createRange()
        range.selectNodeContents(part)
        const text = range.getBoundingClientRect()
        return {
          text: part.textContent,
          lines: range.getClientRects().length,
          fontSize: Number.parseFloat(getComputedStyle(part).fontSize),
          inside: text.left >= box.left && text.right <= box.right && text.top >= box.top && text.bottom <= box.bottom,
        }
      })
      return { left: Math.round(box.left), overflow: cell.scrollWidth > cell.clientWidth, parts }
    }))

    expect(layout.map((cell) => cell.parts[0].text)).toEqual(days.map((day) => day.date.slice(5)).reverse())
    expect(new Set(layout.map((cell) => cell.left)).size).toBeLessThanOrEqual(viewport.maxColumns)
    for (const cell of layout) {
      expect(cell.overflow, cell.parts[0].text ?? '').toBe(false)
      for (const part of cell.parts) {
        expect(part.lines, part.text ?? '').toBe(1)
        expect(part.inside, part.text ?? '').toBe(true)
        expect(part.fontSize, part.text ?? '').toBeGreaterThanOrEqual(16)
      }
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(viewport.width)
  })
}
