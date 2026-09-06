import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter, type RouteRecordRaw } from 'vue-router'

import { router as applicationRouter } from '../routes'
import OwnerLayout from './OwnerLayout.vue'
import ParentLayout from './ParentLayout.vue'
import StudentLayout from './StudentLayout.vue'

const page = { template: '<div>page</div>' }

async function mountNavigation(layout: object, initialPath: string, routes: RouteRecordRaw[]) {
	const pinia = createPinia()
	setActivePinia(pinia)
	const router = createRouter({ history: createMemoryHistory(), routes })
	await router.push(initialPath)
	await router.isReady()
	const wrapper = mount(layout, { global: { plugins: [pinia, router] } })
	return { router, wrapper }
}

test('keeps student, parent, and owner navigation targets distinct and role-scoped', async () => {
	const student = await mountNavigation(StudentLayout, '/student', [
		{ path: '/student', component: page },
		{ path: '/student/growth', component: page },
		{ path: '/student/profile', component: page },
	])
	const studentLinks = student.wrapper.findAll('nav[aria-label="学生导航"] a')
	const studentTargets = studentLinks.map((link) => link.attributes('href'))
	expect(studentLinks.map((link) => link.text())).toEqual(['首页', '成长', '我的'])
	expect(studentTargets).toEqual(['/student', '/student/growth', '/student/profile'])
	expect(new Set(studentTargets).size).toBe(3)
	expect(studentLinks.filter((link) => link.classes('router-link-active'))).toHaveLength(1)
	student.wrapper.unmount()

	const parent = await mountNavigation(ParentLayout, '/parent', [
		{ path: '/parent', component: page },
		{ path: '/parent/ability', component: page },
		{ path: '/parent/report', component: page },
		{ path: '/parent/settings', component: page },
	])
	expect(parent.wrapper.findAll('nav[aria-label="家长导航"] a')).toHaveLength(4)
	parent.wrapper.unmount()

	const owner = await mountNavigation(OwnerLayout, '/admin/account', [
		{ path: '/admin/account', component: page },
		{ path: '/admin/accounts', component: page },
		{ path: '/admin/content', component: page },
		{ path: '/admin/trial', component: page },
		{ path: '/admin/cost', component: page },
	])
	expect(owner.wrapper.findAll('nav[aria-label="Owner 导航"] a')).toHaveLength(5)
	owner.wrapper.unmount()
})

test('removes dead top-level student redirects while retaining classroom subroutes', () => {
	const paths = new Set(applicationRouter.getRoutes().map((route) => route.path))
	expect(paths.has('/student/today')).toBe(false)
	expect(paths.has('/student/supply')).toBe(false)
	expect(paths.has('/student/voice')).toBe(false)
	expect(paths.has('/student/session/:id/supply')).toBe(true)
	expect(paths.has('/student/session/:id/voice')).toBe(true)
})
