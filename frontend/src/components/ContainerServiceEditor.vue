<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import type { EnvironmentManifest } from '../types'
import ResourceLimitSlider from './ResourceLimitSlider.vue'
import { cpuOptions, memoryLimits, processLimits } from '../resourceLimits'
import { api } from '../api'
const hostCPUs = ref<number | null>(null)
const cpuLimits = computed(() => cpuOptions(hostCPUs.value ?? 16))
onMounted(async () => {
  try {
    const info = await api.system()
    if (info.resources.cpus && info.resources.cpus > 0) hostCPUs.value = info.resources.cpus
  } catch { /* Keep existing limits editable when Docker statistics are unavailable. */ }
})
const props = defineProps<{ environment: EnvironmentManifest; serviceIndex: number }>()
const emit = defineEmits<{ apply: [environment: EnvironmentManifest]; close: [] }>()
const draft = ref<EnvironmentManifest>(JSON.parse(JSON.stringify(props.environment)))
const service = draft.value.services[props.serviceIndex]!
service.ports ||= []
service.depends_on ||= []
service.name ??= service.key
service.cpu ??= 0
service.memory_mb ??= 0
service.pids_limit ??= 0
// Old package defaults are inactive; enabling limits starts from unlimited.
if (!service.resource_limits_enabled) {
  service.cpu = 0
  service.memory_mb = 0
  service.pids_limit = 0
}
const image = computed(() => draft.value.images.find(image => image.key === service.image))
function apply() {
  service.name = service.name?.trim()
  draft.value.version = draft.value.version.trim()
  if (!draft.value.version || draft.value.version.length > 64) { ElMessage.warning('请填写 1–64 字符的环境版本'); return }
  if (!service.name || [...service.name].length > 128) { ElMessage.warning('请填写 1–128 字符的系统名称'); return }
  const visiting = new Set<string>(), visited = new Set<string>()
  function visit(key: string): boolean {
    if (visiting.has(key)) return false
    if (visited.has(key)) return true
    const s = draft.value.services.find(s => s.key === key)
    if (!s) return false
    visiting.add(key)
    for (const dep of s.depends_on || []) if (!visit(dep)) return false
    visiting.delete(key); visited.add(key); return true
  }
  if (!draft.value.services.every(s => visit(s.key))) { ElMessage.warning('服务依赖不存在或形成循环'); return }
  const ports = new Set<string>()
  for (const p of service.ports || []) {
    const key = `${p.port}/${p.protocol}`
    if (!Number.isInteger(p.port) || p.port < 1 || p.port > 65535 || ports.has(key) || p.label.length > 128) { ElMessage.warning('端口须在 1–65535 之间，同协议端口不可重复，标签最长 128 字符'); return }
    ports.add(key)
  }
  if (service.resource_limits_enabled && (!Number.isFinite(service.cpu) || service.cpu < 0 || (service.cpu > 0 && service.cpu < 0.1) || service.cpu > 4096 || !Number.isInteger(service.memory_mb) || service.memory_mb < 0 || (service.memory_mb > 0 && service.memory_mb < 16) || service.memory_mb > 16384 || !Number.isInteger(service.pids_limit ?? 0) || (service.pids_limit ?? 0) < 0 || (service.pids_limit ?? 0) > 1048576)) {
    ElMessage.warning('CPU 为 0 或 0.1–4096，内存为 0 或 16–16384 MiB，进程数为 0–1048576；0 表示不限'); return
  }
  if (service.resource_limits_enabled && hostCPUs.value && service.cpu > hostCPUs.value) { ElMessage.warning(`CPU 配额不能超过 Docker 主机的 ${hostCPUs.value} 个逻辑核；可选择无限`); return }
  emit('apply', draft.value)
}
</script>

<template>
  <el-dialog :model-value="true" title="编辑系统" width="720px" :close-on-click-modal="false" @close="emit('close')">
    <el-form label-position="top" @submit.prevent="apply">
      <div class="two-columns">
        <el-form-item label="系统名称"><el-input v-model="service.name" maxlength="128" placeholder="例如：财务系统" /></el-form-item>
        <el-form-item label="上传文件"><el-input :model-value="image?.source_file || '原上传文件名未记录'" readonly /></el-form-item>
        <el-form-item label="环境版本（整道题共用）"><el-input v-model="draft.version" maxlength="64" /></el-form-item>
        <el-form-item label="镜像架构"><el-input :model-value="draft.architecture" disabled /></el-form-item>
      </div>
      <details class="system-technical-details">
        <summary>高级选项</summary>
        <el-checkbox v-model="service.resource_limits_enabled">启用资源限制</el-checkbox>
        <p class="muted">默认均为无限。拖动选择常用档位，最右端为无限；CPU 按整核设置，内存从 0.5 GB 起每 0.5 GB 一档。</p>
        <p v-if="service.resource_limits_enabled && hostCPUs" class="muted">Docker 主机可用 {{ hostCPUs }} 个逻辑核；CPU 配额为共享算力上限。</p>
        <div v-if="service.resource_limits_enabled" class="two-columns">
          <ResourceLimitSlider v-model="service.cpu" label="CPU" unit="核" :presets="cpuLimits" />
          <ResourceLimitSlider v-model="service.memory_mb" label="内存" unit="GB" :presets="memoryLimits" :divisor="1024" />
          <ResourceLimitSlider :model-value="service.pids_limit ?? 0" @update:model-value="service.pids_limit = $event" label="进程数" :presets="processLimits" />
        </div>
      </details>
      <el-form-item label="依赖服务"><el-select v-model="service.depends_on" multiple clearable><el-option v-for="s in draft.services.filter((_, i) => i !== serviceIndex)" :key="s.key" :label="`${s.name || s.key}（${draft.images.find(image => image.key === s.image)?.source_file || s.key}）`" :value="s.key" /></el-select></el-form-item>
      <el-form-item label="端口">
        <div class="service-port-editor">
          <div v-for="(port, i) in service.ports" :key="i" class="service-port-edit-row">
            <el-input-number v-model="port.port" aria-label="端口号" :min="1" :max="65535" :precision="0" controls-position="right" />
            <el-select v-model="port.protocol" aria-label="端口协议"><el-option label="TCP" value="tcp" /><el-option label="UDP" value="udp" /></el-select>
            <el-input v-model="port.label" aria-label="端口标签" placeholder="标签" maxlength="128" />
            <el-checkbox v-model="port.public">公开</el-checkbox>
            <el-button type="danger" text @click="service.ports!.splice(i, 1)">删除</el-button>
          </div>
          <el-button :disabled="service.ports!.length >= 32" @click="service.ports!.push({ port: 8080, protocol: 'tcp', public: false, label: '' })">添加端口</el-button>
        </div>
      </el-form-item>
      <details class="system-technical-details">
        <summary>技术详情</summary>
        <el-descriptions :column="1" border>
          <el-descriptions-item label="服务 ID">{{ service.id || '未分配' }}</el-descriptions-item>
          <el-descriptions-item label="内部服务标识">{{ service.key }}</el-descriptions-item>
          <el-descriptions-item label="镜像标识">{{ service.image }}</el-descriptions-item>
          <el-descriptions-item label="镜像归档路径">{{ image?.file }}</el-descriptions-item>
          <el-descriptions-item label="镜像 SHA256">{{ image?.sha256 }}</el-descriptions-item>
        </el-descriptions>
      </details>
      <p class="muted">系统已自动关联上传的 TAR 文件。应用修改后需重新测试。</p>
    </el-form>
    <template #footer><el-button @click="emit('close')">取消</el-button><el-button type="primary" @click="apply">应用修改</el-button></template>
  </el-dialog>
</template>

<style scoped>
.system-technical-details { margin: 16px 0; }
.system-technical-details summary { cursor: pointer; margin-bottom: 12px; }
.system-technical-details :deep(.el-descriptions__content) { overflow-wrap: anywhere; }
</style>
