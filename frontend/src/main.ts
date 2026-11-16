import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'
import './styles.css'
import App from './App.vue'
import { router } from './router'
import { setUnauthorizedHandler } from './api'
import { useAuthStore } from './stores/auth'

const app = createApp(App)
const pinia = createPinia()
app.use(pinia)
setUnauthorizedHandler(() => {
  const auth = useAuthStore(pinia)
  auth.clearSession()
  // During initial /auth/me, the router guard handles the redirect itself.
  if (router.currentRoute.value.path !== '/login' && router.currentRoute.value.matched.length) {
    void router.replace('/login')
  }
})
app.use(router)
app.use(ElementPlus, { locale: zhCn })
app.mount('#app')
