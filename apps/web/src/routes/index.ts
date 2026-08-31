import { createRouter, createWebHistory } from 'vue-router'

import ParentLayout from '../layouts/ParentLayout.vue'
import OwnerLayout from '../layouts/OwnerLayout.vue'
import StudentLayout from '../layouts/StudentLayout.vue'
import StudentGrowthPage from '../pages/student/StudentGrowthPage.vue'
import StudentHomePage from '../pages/student/StudentHomePage.vue'
import StudentSessionPage from '../pages/student/StudentSessionPage.vue'
import StudentSupplyPage from '../pages/student/StudentSupplyPage.vue'
import StudentVoicePage from '../pages/student/StudentVoicePage.vue'
import LoginPage from '../pages/LoginPage.vue'
import AccountPage from '../pages/AccountPage.vue'
import { roleHome, useAuthSession } from '../stores/auth'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
		{ path: '/login', component: LoginPage },
    { path: '/', redirect: '/student' },
    {
      path: '/student',
      component: StudentLayout,
			meta: { role: 'STUDENT' },
      children: [
        { path: '', component: StudentHomePage },
        { path: 'today', redirect: '/student' },
        { path: 'session/:id/voice', component: StudentVoicePage },
        { path: 'session/:id/supply', component: StudentSupplyPage },
        { path: 'session/:id', component: StudentSessionPage },
        { path: 'supply', redirect: '/student' },
        { path: 'voice', redirect: '/student' },
        { path: 'growth', component: StudentGrowthPage },
				{ path: 'profile', component: AccountPage },
      ],
    },
    {
      path: '/parent',
      component: ParentLayout,
			meta: { role: 'PARENT' },
      children: [
        { path: '', component: () => import('../pages/parent/ParentDashboardPage.vue') },
        { path: 'live', component: () => import('../pages/parent/ParentLivePage.vue') },
        { path: 'ability', component: () => import('../pages/parent/ParentAbilityPage.vue') },
        { path: 'report/session/:id', component: () => import('../pages/parent/ParentSessionReportPage.vue') },
        { path: 'report', component: () => import('../pages/parent/ParentReportPage.vue') },
				{ path: 'settings', component: () => import('../pages/parent/ParentSettingsPage.vue') },
				{ path: 'account', component: AccountPage },
      ],
    },
		{
				path: '/admin', component: OwnerLayout, meta: { role: 'OWNER' }, children: [
					{ path: '', redirect: '/admin/content' },
					{ path: 'accounts', component: () => import('../pages/admin/AdminAccountsPage.vue') },
					{ path: 'content', component: () => import('../pages/admin/AdminContentPage.vue') },
				{ path: 'quality', redirect: '/admin/content' },
				{ path: 'trial', component: () => import('../pages/admin/AdminTrialPage.vue') },
				{ path: 'cost', component: () => import('../pages/admin/AdminCostPage.vue') },
				{ path: 'account', component: AccountPage },
			],
		},
  ],
})

router.beforeEach(async (to) => {
	const auth = useAuthSession()
	let user
	try { user = await auth.ensure() } catch { user = null }
	if (to.path === '/login') return user ? roleHome(user.role) : true
	if (!user) return { path: '/login', query: { redirect: to.fullPath } }
	const requiredRole = to.matched.map((record) => record.meta.role).find(Boolean)
	if (requiredRole && requiredRole !== user.role) return roleHome(user.role)
	return true
})
