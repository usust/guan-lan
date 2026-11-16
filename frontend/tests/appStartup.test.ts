import { afterEach, expect, it, vi } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { createPinia } from 'pinia'
import App from '../src/App.vue'

vi.mock('vue-router', () => ({
  useRoute: () => ({ meta: { title: '登录' }, fullPath: '/login', path: '/login' }),
  useRouter: () => ({ replace: vi.fn() }),
}))

afterEach(() => vi.unstubAllGlobals())

it('renders the app on startup with immediate platform title watching enabled', async () => {
  const pageDocument = { title: '', querySelector: vi.fn(() => null) }
  vi.stubGlobal('document', pageDocument)
  const html = await renderToString(createSSRApp(App).use(createPinia()))
  expect(html).toContain('正在验证登录状态')
  expect(html).toContain('华山平台')
  expect(pageDocument.title).toBe('登录 · 华山平台')
})
