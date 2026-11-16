<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import ResourceSummary from './components/ResourceSummary.vue'
import { errorMessage } from './api'
import { useAuthStore } from './stores/auth'

import { loadDifficulties } from './stores/difficulties'

import { platform, loadPlatform } from './stores/platform'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

onMounted(() => { void loadPlatform().catch(() => {}); void loadDifficulties().catch(() => {}) })
watch(() => [platform.name, platform.icon, route.meta.title], () => {
  document.title = `${String(route.meta.title ?? '首页')} · ${platform.name}`
  let icon = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (platform.icon) {
    if (!icon) { icon = document.createElement('link'); icon.rel = 'icon'; document.head.appendChild(icon) }
    icon.href = platform.icon
  } else { icon?.remove() }
}, { immediate: true })

const loggingOut = ref(false)
const menuOpen = ref(false)
const pageTitle = computed(() => String(route.meta.title ?? '首页'))
const navigation = computed(() => auth.user?.role === 'ADMIN'
  ? [{ to: '/admin', label: '管理首页' }, { to: '/admin/sets', label: '竞赛管理' }, { to: '/admin/questions', label: '题目管理' }, { to: '/admin/users', label: '用户管理' }]
  : [{ to: '/player', label: '竞赛与训练' }])

function isActive(path: string): boolean {
  return route.path === path || (path !== '/admin' && route.path.startsWith(`${path}/`))
}

watch(() => route.fullPath, () => { menuOpen.value = false })
watch(() => auth.user, () => { menuOpen.value = false })

async function logout(): Promise<void> {
  if (loggingOut.value) return
  loggingOut.value = true
  try {
    await auth.logout()
    await router.replace('/login')
    ElMessage.success('已安全退出')
  } catch (error) {
    ElMessage.error(errorMessage(error))
  } finally {
    loggingOut.value = false
  }
}
</script>

<template>
  <div v-if="!auth.initialized" class="boot-state" role="status" aria-live="polite">
    <p>{{ platform.name }}</p>
    <p class="muted">正在验证登录状态…</p>
  </div>
  <div v-else class="app-shell" :class="{ 'authenticated-shell': auth.user }">
    <template v-if="auth.user">
      <aside class="app-sidebar">
        <router-link :to="navigation[0]!.to" class="brand"><img v-if="platform.icon" :src="platform.icon" alt="" class="platform-brand-icon" />{{ platform.name }}</router-link>
        <nav aria-label="主导航" class="sidebar-nav">
          <router-link v-for="item in navigation" :key="item.to" :to="item.to" :class="{ active: isActive(item.to) }" :aria-current="isActive(item.to) ? 'page' : undefined">{{ item.label }}</router-link>
          <div v-if="auth.user.role === 'ADMIN'" class="sidebar-settings-group">
            <p class="sidebar-settings-title">系统设置</p>
            <router-link to="/admin/settings/platform" :class="{ active: isActive('/admin/settings/platform') }" :aria-current="isActive('/admin/settings/platform') ? 'page' : undefined">平台设置</router-link>
            <router-link to="/admin/question-types" :class="{ active: isActive('/admin/question-types') }" :aria-current="isActive('/admin/question-types') ? 'page' : undefined">题型管理</router-link>
          </div>
        </nav>
        <ResourceSummary v-if="auth.user.role === 'ADMIN'" />
        <p class="sidebar-caption">{{ platform.description }}</p>
      </aside>
      <el-drawer v-model="menuOpen" :title="platform.name" direction="ltr" size="260px" class="mobile-navigation">
        <nav aria-label="移动端主导航" class="sidebar-nav" @click="menuOpen = false">
          <router-link v-for="item in navigation" :key="item.to" :to="item.to" :class="{ active: isActive(item.to) }" :aria-current="isActive(item.to) ? 'page' : undefined">{{ item.label }}</router-link>
          <div v-if="auth.user.role === 'ADMIN'" class="sidebar-settings-group">
            <p class="sidebar-settings-title">系统设置</p>
            <router-link to="/admin/settings/platform" :class="{ active: isActive('/admin/settings/platform') }" :aria-current="isActive('/admin/settings/platform') ? 'page' : undefined">平台设置</router-link>
            <router-link to="/admin/question-types" :class="{ active: isActive('/admin/question-types') }" :aria-current="isActive('/admin/question-types') ? 'page' : undefined">题型管理</router-link>
          </div>
        </nav>
        <ResourceSummary v-if="auth.user.role === 'ADMIN'" />
      </el-drawer>
    </template>
    <div id="verification-rail"></div>
    <div class="app-workspace">
      <header v-if="auth.user" class="app-header">
        <el-button class="menu-toggle" :aria-expanded="menuOpen" aria-label="打开导航菜单" @click="menuOpen = true">菜单</el-button>
        <span v-if="!['/admin/question-types', '/admin/questions'].includes(route.path)" class="header-title">{{ pageTitle }}</span>
        <div class="account-actions">
          <span class="account-name">{{ auth.user.display_name || auth.user.username }}</span>
          <el-tag size="small" effect="plain">{{ auth.user.role === 'ADMIN' ? '管理员' : '用户' }}</el-tag>
          <el-button :loading="loggingOut" @click="logout">退出登录</el-button>
        </div>
      </header>
      <main :class="[auth.user ? 'main-content' : 'login-main', { 'main-content-wide': auth.user && route.path === '/admin/questions' }]">
        <router-view />
      </main>
    </div>
  </div>
</template>
