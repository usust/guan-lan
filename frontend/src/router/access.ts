import type { User } from '../types'

export function homeFor(user: User | null): string {
  return user?.role === 'ADMIN' ? '/admin' : user ? '/player' : '/login'
}

export function routeRedirect(path: string, user: User | null): string | undefined {
  if (path === '/login') return user ? homeFor(user) : undefined
  if (!user) return '/login'
  const known = ['/admin/question-types', '/admin/settings', '/admin/settings/platform', '/admin', '/admin/users', '/player', '/admin/sets', '/admin/competitions/create', '/admin/questions'].includes(path)
    || /^\/(admin|player)\/sets\/[^/]+$/.test(path)
    || /^\/admin\/questions\/[^/]+$/.test(path)
  if (path === '/' || !known) return homeFor(user)
  if (path.startsWith('/admin') && user.role !== 'ADMIN') return '/player'
  if (path.startsWith('/player') && user.role === 'ADMIN') return '/admin'
  return undefined
}
