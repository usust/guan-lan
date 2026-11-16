import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api, ApiError, errorMessage, setCsrfToken } from '../api'
import type { Session, User } from '../types'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const initialized = ref(false)
  const initializing = ref(false)
  const initializationError = ref('')
  const isAuthenticated = computed(() => user.value !== null)
  let pendingInitialization: Promise<void> | null = null

  function acceptSession(session: Session): void {
    user.value = session.user
    setCsrfToken(session.csrf_token)
    initialized.value = true
    initializationError.value = ''
  }

  function clearSession(): void {
    user.value = null
    setCsrfToken(null)
    initialized.value = true
  }

  async function initialize(force = false): Promise<void> {
    if (pendingInitialization) return pendingInitialization
    if (initialized.value && !force) return
    initializing.value = true
    initializationError.value = ''
    pendingInitialization = (async () => {
      try {
        acceptSession(await api.me())
      } catch (error) {
        clearSession()
        if (!(error instanceof ApiError && error.status === 401)) {
          initializationError.value = errorMessage(error)
        }
      } finally {
        initialized.value = true
        initializing.value = false
        pendingInitialization = null
      }
    })()
    return pendingInitialization
  }

  async function login(username: string, password: string): Promise<void> {
    acceptSession(await api.login(username, password))
  }

  async function logout(): Promise<void> {
    await api.logout()
    clearSession()
  }

  return { user, initialized, initializing, initializationError, isAuthenticated, initialize, login, logout, clearSession }
})
