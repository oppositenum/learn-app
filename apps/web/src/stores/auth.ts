import { getActivePinia } from 'pinia'
import { ref } from 'vue'

import { getSessionUser, login as loginRequest, logout as logoutRequest, type SessionUser, type UserRole } from '../api/auth'
import { useGrowthStore } from './growth'
import { useLearningStore } from './learning'
import { useSupervisionStore } from './supervision'

const user = ref<SessionUser | null>(null)
let loaded = false
let generation = 0
let loading: { generation: number; promise: Promise<SessionUser | null> } | null = null
let refreshing: { generation: number; promise: Promise<SessionUser | null> } | null = null

export const authChangeStorageKey = 'ai-learning-tutor:auth-change'

function broadcastAuthChange() {
  try {
    localStorage.setItem(authChangeStorageKey, `${Date.now()}:${Math.random()}`)
  } catch {
    // Account changes still work when storage is unavailable.
  }
}

function resetStudentDomain() {
  const pinia = getActivePinia()
  if (!pinia) return
  useLearningStore(pinia).reset()
  useGrowthStore(pinia).reset()
  useSupervisionStore(pinia).reset()
}

function applyUser(result: SessionUser | null) {
  if (user.value?.user_id !== result?.user_id || user.value?.student_id !== result?.student_id) resetStudentDomain()
  user.value = result
  loaded = true
  return result
}

export function roleHome(role: UserRole): string {
  if (role === 'PARENT') return '/parent'
  if (role === 'OWNER') return '/admin/content'
  return '/student'
}

export function useAuthSession() {
  async function ensure(): Promise<SessionUser | null> {
    if (loaded) return user.value
    const requestedGeneration = generation
    if (!loading || loading.generation !== requestedGeneration) {
      const promise = getSessionUser().then((result) => generation === requestedGeneration ? applyUser(result) : user.value)
      loading = { generation: requestedGeneration, promise }
      void promise.then(
        () => { if (loading?.promise === promise) loading = null },
        () => { if (loading?.promise === promise) loading = null },
      )
    }
    return loading.promise
  }

  async function login(email: string, password: string): Promise<SessionUser> {
		if (user.value) {
			try {
				await logout()
			} catch {
				throw new Error('无法退出当前账号，尚未切换；请稍后重试')
			}
		}
    const requestedGeneration = ++generation
    loading = null
    refreshing = null
    const result = await loginRequest(email, password)
    if (generation !== requestedGeneration) {
      if (user.value) return user.value
      throw new Error('登录状态已发生变化，请重试')
    }
    applyUser(result)
    broadcastAuthChange()
    return result
  }

  async function refresh(): Promise<SessionUser | null> {
    const requestedGeneration = generation
    if (!refreshing || refreshing.generation !== requestedGeneration) {
      const promise = getSessionUser().then((result) => generation === requestedGeneration ? applyUser(result) : user.value)
      refreshing = { generation: requestedGeneration, promise }
      void promise.then(
        () => { if (refreshing?.promise === promise) refreshing = null },
        () => { if (refreshing?.promise === promise) refreshing = null },
      )
    }
    return refreshing.promise
  }

  function invalidateExternalChange() {
    generation++
    loading = null
    refreshing = null
    applyUser(null)
  }

  async function logout(): Promise<void> {
    const requestedGeneration = ++generation
    loading = null
    refreshing = null
    await logoutRequest()
    if (generation !== requestedGeneration) return
    applyUser(null)
    broadcastAuthChange()
  }

  function resetForTests() {
    generation++
    user.value = null
    loaded = false
    loading = null
    refreshing = null
  }

  return { user, ensure, refresh, invalidateExternalChange, login, logout, resetForTests }
}
