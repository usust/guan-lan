<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, errorMessage } from '../api'
import type { SystemInfo } from '../types'

const info = ref<SystemInfo | null>(null)
const loading = ref(false)
const error = ref('')

async function load(): Promise<void> {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    info.value = await api.system()
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <section>
    <div class="page-heading">
      <div><p class="eyebrow">管理空间</p><h1>管理首页</h1><p class="muted">查看平台资源与 Docker 状态，管理竞赛和题目。</p></div>
      <el-button :loading="loading" @click="load">刷新状态</el-button>
    </div>
    <p class="section-gap"><router-link to="/admin/sets"><el-button type="primary">竞赛管理</el-button></router-link> <router-link to="/admin/questions"><el-button>题目管理</el-button></router-link></p>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon class="section-gap" />
    <div v-loading="loading" class="system-content" :aria-busy="loading">
      <template v-if="info">
        <el-card shadow="never" class="section-gap">
          <template #header><h2>平台信息</h2></template>
          <el-descriptions :column="1" border>
            <el-descriptions-item label="平台标识">{{ info.platform_id || '未提供' }}</el-descriptions-item>
            <el-descriptions-item label="根目录"><code>{{ info.root || '未提供' }}</code></el-descriptions-item>
            <el-descriptions-item label="操作系统">{{ info.os || '未提供' }}</el-descriptions-item>
            <el-descriptions-item label="系统架构">{{ info.arch || '未提供' }}</el-descriptions-item>
          </el-descriptions>
        </el-card>
        <el-card shadow="never" class="section-gap">
          <template #header><h2>Docker 环境探测</h2></template>
          <el-descriptions :column="1" border>
            <el-descriptions-item label="连接状态"><el-tag :type="info.docker.available ? 'success' : 'warning'">{{ info.docker.available ? '可用' : '不可用' }}</el-tag></el-descriptions-item>
            <el-descriptions-item label="连接端点"><code>{{ info.docker.endpoint || '未提供' }}</code></el-descriptions-item>
            <el-descriptions-item label="Docker 版本">{{ info.docker.version || '未获取' }}</el-descriptions-item>
            <el-descriptions-item label="操作系统">{{ info.docker.os || '未获取' }}</el-descriptions-item>
            <el-descriptions-item label="架构">{{ info.docker.architecture || '未获取' }}</el-descriptions-item>
            <el-descriptions-item v-if="info.docker.error" label="探测说明">{{ info.docker.error }}</el-descriptions-item>
          </el-descriptions>
        </el-card>
      </template>
      <el-empty v-else-if="!loading" description="暂未获取平台信息，请点击刷新重试。" />
    </div>
  </section>
</template>
