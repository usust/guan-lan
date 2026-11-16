import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { platform } from '../stores/platform'
import { routeRedirect } from './access'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: () => import('../views/LoginView.vue'), meta: { title: '登录' } },
    { path: '/', component: () => import('../views/WelcomeView.vue') },
    { path: '/admin', component: () => import('../views/AdminHomeView.vue'), meta: { title: '管理首页' } },
    { path: '/admin/question-types', component: () => import('../views/QuestionTypesView.vue'), meta: { title: '题型管理' } },
    { path: '/admin/questions', component: () => import('../views/QuestionsView.vue'), meta: { title: '题目管理' } },
    { path: '/admin/questions/:id', component: () => import('../views/SetDetailView.vue'), meta: { title: '编辑题目' } },
    { path: '/admin/settings', redirect: '/admin/settings/platform' },
    { path: '/admin/settings/platform', component: () => import('../views/PlatformSettingsView.vue'), meta: { title: '平台设置' } },
    { path: '/admin/users', component: () => import('../views/UsersView.vue'), meta: { title: '用户管理' } },
    { path: '/player', component: () => import('../views/SetsView.vue'), meta: { title: '竞赛与训练' } },
    { path: '/player/sets/:id', component: () => import('../views/SetDetailView.vue'), meta: { title: '题目详情' } },
    { path: '/admin/competitions/create', component: () => import('../views/SetsView.vue'), meta: { title: '创建竞赛' } },
    { path: '/admin/sets', component: () => import('../views/SetsView.vue'), meta: { title: '竞赛管理' } },
    { path: '/admin/sets/:id', component: () => import('../views/SetDetailView.vue'), meta: { title: '竞赛详情' } },
    { path: '/:pathMatch(.*)*', component: () => import('../views/WelcomeView.vue') },
  ],
})

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  await auth.initialize()
  return routeRedirect(to.path, auth.user)
})

router.afterEach((to) => {
  document.title = `${String(to.meta.title ?? '首页')} · ${platform.name}`
})
