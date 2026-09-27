import { expect, test, type Page } from '@playwright/test'

// The parent settings page at 320, 375 and 768: text at 16px or larger, every
// control at least 48px and no horizontal scroll, whether today's plan is
// updated at once or kept because a classroom is open. Every API call is
// answered by a local fixture.

function jsonResponse(body: unknown) {
  return { status: 200, contentType: 'application/json', body: JSON.stringify(body) }
}

const preferences = {
  daily_minutes: 30, priority_subject_codes: ['MATH'], review_only: false, reduce_intensity: false,
  enabled_subject_codes: ['MATH', 'ENGLISH', 'PHYSICS'], priority_domain_id: null, configured: true,
  domain_options: [
    { id: 'domain-math-equation', subject_code: 'MATH', name: '方程与不等式' },
    { id: 'domain-math-function', subject_code: 'MATH', name: '函数' },
    { id: 'domain-english-grammar', subject_code: 'ENGLISH', name: '语法' },
    { id: 'domain-physics-motion', subject_code: 'PHYSICS', name: '机械运动与力' },
    { id: 'domain-chemistry-matter', subject_code: 'CHEMISTRY', name: '物质的变化' },
  ],
}

const updates = {
  updated: { saved: true, plan_updated: true, today_preserved: false, applies_from: '2030-01-01', answer_controls_available: false },
  preserved: { saved: true, plan_updated: true, today_preserved: true, applies_from: '2030-01-02', answer_controls_available: false },
}

async function routeParent(page: Page, update: object, sent: Record<string, unknown>[]) {
  // Registered first so the specific routes below take precedence.
  await page.route('**/api/v1/parent/**', (route) => route.fulfill(jsonResponse({})))
  await page.route('**/api/v1/auth/me', (route) => route.fulfill(jsonResponse({
    user_id: 'parent-settings', role: 'PARENT', display_name: '测试家长', student_id: null,
  })))
  await page.route('**/api/v1/parent/children', (route) => route.fulfill(jsonResponse({ children: [
    { student_id: 'student-settings', display_name: '测试学生', grade_level: 9, active_session_id: null, subject: null, knowledge_point: null, started_at: null },
  ] })))
  await page.route('**/api/v1/parent/child/student-settings/preferences', (route) => {
    if (route.request().method() === 'PUT') {
      sent.push(route.request().postDataJSON() as Record<string, unknown>)
      return route.fulfill(jsonResponse(update))
    }
    return route.fulfill(jsonResponse(preferences))
  })
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
      if (['SCRIPT', 'STYLE', 'svg', 'path', 'OPTION'].includes(element.tagName)) continue
      const own = [...element.childNodes].filter((node) => node.nodeType === Node.TEXT_NODE).map((node) => node.textContent ?? '').join('').trim()
      if (!own || !visible(element)) continue
      texts.push({ text: own.slice(0, 24), fontSize: Number.parseFloat(getComputedStyle(element).fontSize) })
    }
    // A select shows its chosen option in its own box, so its font counts.
    for (const element of document.body.querySelectorAll('select')) {
      if (visible(element)) texts.push({ text: 'select', fontSize: Number.parseFloat(getComputedStyle(element).fontSize) })
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
  for (const [state, update] of Object.entries(updates)) {
    test(`parent settings is readable and tappable at ${viewport.width}px when today is ${state}`, async ({ page }) => {
      const sent: Record<string, unknown>[] = []
      await page.setViewportSize(viewport)
      await routeParent(page, update, sent)
      await page.goto('/parent/settings?student=student-settings')
      const domain = page.getByTestId('parent-priority-domain')
      await expect(domain.locator('option')).toHaveCount(5)
      await domain.selectOption('domain-physics-motion')
      await page.getByTestId('parent-state-not-good').check()
      await expect(page.getByTestId('parent-review-only')).toBeChecked()
      await expect(page.getByTestId('parent-reduce-intensity')).toBeChecked()
      await page.getByRole('button', { name: '保存计划偏好' }).click()
      await expect(page.getByTestId('parent-settings-status')).toHaveText(state === 'updated'
        ? '已保存，今日计划已更新，生效日期是 2030-01-01'
        : '已保存，今日计划保持不变，将从 2030-01-02 生效')
      expect(sent.at(-1)).toMatchObject({ priority_domain_id: 'domain-physics-motion', state_not_good: true, review_only: true, reduce_intensity: true })

      const result = await measure(page)
      test.info().annotations.push({ type: 'measure', description: JSON.stringify({ state, width: viewport.width, scrollWidth: result.scrollWidth }) })
      expect(result.texts.length).toBeGreaterThan(0)
      expect(result.texts.filter((item) => item.fontSize < 16), 'text under 16px').toEqual([])
      expect(result.controls.length).toBeGreaterThan(0)
      expect(result.controls.filter((item) => item.height < 48), 'controls under 48px').toEqual([])
      expect(result.scrollWidth, 'scroll width').toBeLessThanOrEqual(viewport.width)
      for (const phrase of ['发答案', '发送答案', '替孩子', '替答', '标准答案', '发送鼓励']) expect(result.bodyText, phrase).not.toContain(phrase)
    })
  }
}
