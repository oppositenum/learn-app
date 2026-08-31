export type UserRole = 'STUDENT' | 'PARENT' | 'OWNER'

export interface SessionUser {
  user_id: string
  role: UserRole
  display_name: string
  student_id: string | null
}

export async function login(email: string, password: string): Promise<SessionUser> {
  const response = await fetch('/api/v1/auth/login', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  })
  if (!response.ok) throw new Error(response.status === 401 ? '邮箱或密码不正确' : `登录暂时不可用（${response.status}）`)
  return response.json() as Promise<SessionUser>
}

export async function getSessionUser(): Promise<SessionUser | null> {
  const response = await fetch('/api/v1/auth/me', { credentials: 'same-origin' })
  if (response.status === 401) return null
  if (!response.ok) throw new Error(`账户状态暂时不可用（${response.status}）`)
  return response.json() as Promise<SessionUser>
}

export async function logout(): Promise<void> {
  const response = await fetch('/api/v1/auth/logout', { method: 'POST', credentials: 'same-origin' })
  if (!response.ok && response.status !== 401) throw new Error(`退出暂时不可用（${response.status}）`)
}
