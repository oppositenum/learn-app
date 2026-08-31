import { ref } from 'vue'

import { getSessionUser, login as loginRequest, logout as logoutRequest, type SessionUser, type UserRole } from '../api/auth'

const user = ref<SessionUser | null>(null)
let loaded = false
let loading: Promise<SessionUser | null> | null = null

export function roleHome(role: UserRole): string {
  if (role === 'PARENT') return '/parent'
  if (role === 'OWNER') return '/admin/content'
  return '/student'
}

export function useAuthSession() {
  async function ensure(): Promise<SessionUser | null> {
    if (loaded) return user.value
    if (!loading) {
      loading = getSessionUser().then((result) => {
        user.value = result
        loaded = true
        return result
      }).finally(() => { loading = null })
    }
    return loading
  }

  async function login(email: string, password: string): Promise<SessionUser> {
    const result = await loginRequest(email, password)
    user.value = result
    loaded = true
    return result
  }

  async function logout(): Promise<void> {
    await logoutRequest()
    user.value = null
    loaded = true
  }

  function resetForTests() {
    user.value = null
    loaded = false
    loading = null
  }

  return { user, ensure, login, logout, resetForTests }
}
