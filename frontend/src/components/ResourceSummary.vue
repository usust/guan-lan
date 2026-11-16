<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { api } from '../api'
import type { SystemInfo } from '../types'
const info = ref<SystemInfo | null>(null)
const failed = ref(false)
let timer: ReturnType<typeof setInterval> | undefined
let active = true
let loading = false
function bytes(n: number) { return `${(n / 1024 ** 3).toFixed(1)} GiB` }
async function load() {
  if (loading) return
  loading = true
  try { const value = await api.system(); if (active) { info.value = value; failed.value = false } }
  catch { if (active) failed.value = true }
  finally { loading = false }
}
onMounted(() => { void load(); timer = setInterval(load, 30000) })
onUnmounted(() => { active = false; clearInterval(timer) })
</script>
<template>
  <section class="resource-summary" aria-label="资源使用情况">
    <div class="card-heading"><strong>资源概况</strong><button class="resource-refresh" aria-label="刷新资源" :disabled="loading" @click="load">刷新</button></div>
    <p v-if="failed" class="muted">资源获取失败，请重试</p>
    <template v-else>
      <p><span>容器总数</span><strong>{{ info?.resources?.available ? `${info.resources.containers} 个` : '—' }}</strong></p>
      <p><span>运行中</span><strong>{{ info?.resources?.available ? `${info.resources.running} 个` : '—' }}</strong></p>
      <p><span>容器内存</span><strong>{{ info?.resources?.available ? bytes(info.resources.memory_used) : '—' }}</strong></p>
      <small v-if="info?.resources?.available">Docker 总内存 {{ bytes(info.resources.memory_total) }}</small>
      <p><span>磁盘已用</span><strong>{{ info?.disk?.available ? bytes(info.disk.used) : '—' }}</strong></p>
      <el-progress v-if="info?.disk?.available && info.disk.total" :percentage="Math.min(100, Math.round(info.disk.used / info.disk.total * 100))" :show-text="false" :stroke-width="5" />
      <small v-if="info?.disk?.available">总容量 {{ bytes(info.disk.total) }} · 数据所在磁盘</small>
      <small v-if="info && !info.resources?.available">Docker 统计暂不可用</small>
      <small v-if="!info">正在获取资源…</small>
      <small v-else>每 30 秒更新 · Docker 全部容器</small>
    </template>
  </section>
</template>
