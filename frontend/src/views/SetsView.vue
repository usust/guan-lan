<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { api, errorMessage } from '../api'
import { useAuthStore } from '../stores/auth'
import type { ChallengeSet } from '../types'
const auth = useAuthStore()
const admin = computed(() => auth.user?.role === 'ADMIN')
const router = useRouter()
const route = useRoute()
const createPage = computed(() => route.path === '/admin/competitions/create')
const draft = ref({ title: '', description: '', starts_at: Date.now(), ends_at: Date.now() + 7 * 86400000 })
watch(createPage, () => { error.value = '' })
const items = ref<ChallengeSet[]>([])
const loading = ref(false)
const error = ref('')
const creating = ref(false)
async function load() {
  loading.value = true; error.value = ''
  try { items.value = (await api.sets()).items } catch (e) { error.value = errorMessage(e) } finally { loading.value = false }
}
async function create() {
  if (creating.value) return
  creating.value = true
  try {
    if (!draft.value.title.trim()) { ElMessage.warning('请输入竞赛名称'); return }
    if (draft.value.ends_at <= draft.value.starts_at) { ElMessage.warning('结束时间必须晚于开始时间'); return }
    const result = await api.saveSet({ id: '', ...draft.value, title: draft.value.title.trim(), status: 'DRAFT', version: 0, questions: [] })
    await router.push(`/admin/sets/${result.set.id}?choose=1`)
  } catch (e) { ElMessage.error(errorMessage(e)) } finally { creating.value = false }
}
function label(item: ChallengeSet) {
  if (item.status === 'DRAFT') return '草稿'
  if (item.status === 'CLOSED') return '已下架'
  if (Date.now() >= item.ends_at) return '已结束'
  return Date.now() < item.starts_at ? '即将开放' : '进行中'
}
function open(item: ChallengeSet) { router.push(`/${admin.value ? 'admin' : 'player'}/sets/${item.id}`) }
onMounted(load)
</script>
<template>
  <section v-if="createPage">
    <div class="page-heading"><div><p class="eyebrow">竞赛管理</p><h1>创建竞赛</h1><p class="muted">先设置竞赛信息，创建后选择题目并发布。</p></div></div>
    <el-card shadow="never"><el-form label-position="top" :disabled="creating" @submit.prevent="create">
      <el-form-item label="竞赛名称" required><el-input v-model="draft.title" maxlength="100" placeholder="请输入竞赛名称" /></el-form-item>
      <el-form-item label="竞赛介绍"><el-input v-model="draft.description" type="textarea" :rows="3" maxlength="5000" /></el-form-item>
      <div class="two-columns"><el-form-item label="开始时间" required><el-date-picker v-model="draft.starts_at" type="datetime" value-format="x" :clearable="false" @change="draft.starts_at = Number(draft.starts_at)" /></el-form-item><el-form-item label="结束时间" required><el-date-picker v-model="draft.ends_at" type="datetime" value-format="x" :clearable="false" @change="draft.ends_at = Number(draft.ends_at)" /></el-form-item></div>
      <el-button type="primary" native-type="submit" :loading="creating">创建并选择题目</el-button><el-button @click="router.push('/admin/sets')">返回列表</el-button>
    </el-form></el-card>
  </section>
  <section v-else>
    <div class="page-heading">
      <div><p class="eyebrow">{{ admin ? '内容管理' : '选手空间' }}</p><h1>{{ admin ? '竞赛管理' : '竞赛与训练' }}</h1><p class="muted">{{ admin ? '编排多阶段题目，发布后固定内容与分值。' : '选择竞赛开始解题，每个小题独立提交和计分。' }}</p></div>
      <div class="button-row"><el-button :loading="loading" @click="load">刷新</el-button><el-button v-if="admin" type="primary" :loading="creating" @click="router.push('/admin/competitions/create')">创建竞赛</el-button></div>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <div v-loading="loading" class="set-grid" :aria-busy="loading">
      <el-card v-for="item in items" :key="item.id" shadow="hover" class="set-card">
        <el-tag :type="item.status === 'DRAFT' ? 'info' : label(item) === '进行中' ? 'success' : 'warning'">{{ label(item) }}</el-tag>
        <h2 class="section-gap">{{ item.title }}</h2><p class="muted description-text">{{ item.description || '暂无介绍' }}</p>
        <p class="table-note">{{ new Date(item.starts_at).toLocaleString() }} — {{ new Date(item.ends_at).toLocaleString() }}</p>
        <el-button class="section-gap" type="primary" plain @click="open(item)">{{ admin ? '管理内容' : '查看题目' }}</el-button>
      </el-card>
    </div>
    <el-empty v-if="!loading && !items.length && !error" :description="admin ? '还没有竞赛，点击创建开始编排。' : '暂时没有已发布的竞赛。'" />
  </section>
</template>
