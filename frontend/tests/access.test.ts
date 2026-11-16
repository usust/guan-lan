import { describe, expect, it } from 'vitest'
import { homeFor, routeRedirect } from '../src/router/access'
import type { User } from '../src/types'

const player: User = { id: '1', username: 'player', display_name: '玩家', role: 'PLAYER', status: 'ACTIVE' }
const admin: User = { ...player, id: '2', role: 'ADMIN' }

describe('route access', () => {
  it('requires a session for every page except login', () => {
    expect(routeRedirect('/login', null)).toBeUndefined()
    for (const path of ['/', '/admin', '/admin/users', '/player', '/unknown']) {
      expect(routeRedirect(path, null)).toBe('/login')
    }
  })
  it('prevents players from opening administrator routes', () => {
    expect(routeRedirect('/admin', player)).toBe('/player')
    expect(routeRedirect('/admin/users', player)).toBe('/player')
    expect(routeRedirect('/player', player)).toBeUndefined()
  })
  it('keeps administrators in their own home and management routes', () => {
    expect(routeRedirect('/admin', admin)).toBeUndefined()
    expect(routeRedirect('/admin/users', admin)).toBeUndefined()
    expect(routeRedirect('/player', admin)).toBe('/admin')
  })
  it('redirects login, root and unknown routes to a role-aware home', () => {
    for (const path of ['/login', '/', '/unknown']) {
      expect(routeRedirect(path, admin)).toBe('/admin')
      expect(routeRedirect(path, player)).toBe('/player')
    }
    expect(homeFor(null)).toBe('/login')
  })
})

it('protects competition editing and allows the correct detail pages', () => {
  expect(routeRedirect('/admin/sets', player)).toBe('/player')
  expect(routeRedirect('/admin/sets/set-1', player)).toBe('/player')
  expect(routeRedirect('/admin/sets/set-1', admin)).toBeUndefined()
  expect(routeRedirect('/player/sets/set-1', player)).toBeUndefined()
  expect(routeRedirect('/player/sets/set-1', admin)).toBe('/admin')
  expect(routeRedirect('/player/sets/set-1', null)).toBe('/login')
})


it.each(['/admin/question-types', '/admin/questions', '/admin/questions/new', '/admin/questions/question-1'])(
  'allows only administrators to access question management: %s', (path) => {
    expect(routeRedirect(path, admin)).toBeUndefined()
    expect(routeRedirect(path, player)).toBe('/player')
    expect(routeRedirect(path, null)).toBe('/login')
  },
)

it('rejects unknown nested question management routes', () => {
  expect(routeRedirect('/admin/questions/question-1/unknown', admin)).toBe('/admin')
})

it('allows only administrators to create competitions', () => {
  expect(routeRedirect('/admin/competitions/create', admin)).toBeUndefined()
  expect(routeRedirect('/admin/competitions/create', player)).toBe('/player')
  expect(routeRedirect('/admin/competitions/create', null)).toBe('/login')
})

it.each(['/admin/settings', '/admin/settings/platform'])(
  'restricts platform settings to administrators: %s', path => {
    expect(routeRedirect(path, admin)).toBeUndefined()
    expect(routeRedirect(path, player)).toBe('/player')
    expect(routeRedirect(path, null)).toBe('/login')
  },
)
