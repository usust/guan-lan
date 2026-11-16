import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { api, ApiError } from '../src/api'
import { useAuthStore } from '../src/stores/auth'
import type { Session } from '../src/types'

const session: Session = {
  user: { id: '1', username: 'admin', display_name: '管理员', role: 'ADMIN', status: 'ACTIVE' },
  csrf_token: 'test-csrf',
}

beforeEach(() => {
  vi.restoreAllMocks()
  setActivePinia(createPinia())
})

describe('authentication store', () => {
  it('restores a cookie session once, including simultaneous initialization', async () => {
    const me = vi.spyOn(api, 'me').mockResolvedValue(session)
    const auth = useAuthStore()
    await Promise.all([auth.initialize(), auth.initialize()])
    await auth.initialize()
    expect(me).toHaveBeenCalledOnce()
    expect(auth.user).toEqual(session.user)
    expect(auth.isAuthenticated).toBe(true)
    expect(auth.initializing).toBe(false)
  })

  it('treats an expired session as signed out without an initialization error', async () => {
    vi.spyOn(api, 'me').mockRejectedValue(new ApiError('请登录', 401, 'UNAUTHORIZED'))
    const auth = useAuthStore()
    await auth.initialize()
    expect(auth.user).toBeNull()
    expect(auth.initialized).toBe(true)
    expect(auth.initializationError).toBe('')
  })

  it('shows a server failure and allows an explicit retry', async () => {
    const me = vi.spyOn(api, 'me').mockRejectedValueOnce(new ApiError('无法连接', 0, 'NETWORK_ERROR')).mockResolvedValueOnce(session)
    const auth = useAuthStore()
    await auth.initialize()
    expect(auth.initializationError).toBe('无法连接')
    await auth.initialize(true)
    expect(me).toHaveBeenCalledTimes(2)
    expect(auth.initializationError).toBe('')
    expect(auth.user).toEqual(session.user)
  })

  it('logs in and only clears the session after successful logout', async () => {
    vi.spyOn(api, 'login').mockResolvedValue(session)
    const logout = vi.spyOn(api, 'logout').mockRejectedValueOnce(new Error('网络中断')).mockResolvedValueOnce({})
    const auth = useAuthStore()
    await auth.login('admin', 'password')
    await expect(auth.logout()).rejects.toThrow('网络中断')
    expect(auth.isAuthenticated).toBe(true)
    await auth.logout()
    expect(logout).toHaveBeenCalledTimes(2)
    expect(auth.user).toBeNull()
  })
})
