import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, expect, test } from 'vitest'

import { useAuthSession } from '../stores/auth'
import AccountPage from './AccountPage.vue'

async function mountAccount(path: string, role: 'STUDENT' | 'PARENT') {
  const pinia = createPinia()
  setActivePinia(pinia)
  useAuthSession().user.value = { user_id: 'user-1', role, display_name: '测试用户', student_id: role === 'STUDENT' ? 'student-1' : null }
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/student/profile', component: AccountPage },
      { path: '/parent/account', component: AccountPage },
      { path: '/login', component: { template: '<div>登录</div>' } },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(AccountPage, { global: { plugins: [pinia, router] } })
  await flushPromises()
  return wrapper
}

afterEach(() => {
  useAuthSession().resetForTests()
})

test('the student profile raises the role line to 16px and sign-out to 48px', async () => {
  const wrapper = await mountAccount('/student/profile', 'STUDENT')

  const role = wrapper.get('[data-testid="account-role"]')
  expect(role.classes()).toContain('text-base')
  expect(role.classes()).not.toContain('text-sm')
  const signOut = wrapper.get('[data-testid="account-sign-out"]')
  expect(signOut.text()).toContain('退出当前账户')
  expect(signOut.classes()).toContain('home-action')
  wrapper.unmount()
})

test('the parent account page keeps its existing role line and button size', async () => {
  const wrapper = await mountAccount('/parent/account', 'PARENT')

  const role = wrapper.get('[data-testid="account-role"]')
  expect(role.classes()).toContain('text-sm')
  expect(role.classes()).not.toContain('text-base')
  const signOut = wrapper.get('[data-testid="account-sign-out"]')
  expect(signOut.classes()).toContain('secondary-button')
  expect(signOut.classes()).not.toContain('home-action')
  wrapper.unmount()
})
