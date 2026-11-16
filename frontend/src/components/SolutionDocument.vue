<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'

const model = defineModel<{ name: string; data: string } | null>()
const props = defineProps<{ editable: boolean; disabled: boolean }>()
const url = ref('')
const reading = ref(false)
watch(model, doc => {
  if (url.value) URL.revokeObjectURL(url.value)
  url.value = ''
  if (doc) {
    const bytes = Uint8Array.from(atob(doc.data), c => c.charCodeAt(0))
    url.value = URL.createObjectURL(new Blob([bytes], { type: 'application/pdf' }))
  }
}, { immediate: true })
onUnmounted(() => { if (url.value) URL.revokeObjectURL(url.value) })
async function upload(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || props.disabled || reading.value) return
  if (!file.name.toLowerCase().endsWith('.pdf') || !file.size || file.size > 20 * 1024 * 1024) {
    ElMessage.warning('请选择不超过 20 MiB 的 PDF 文件')
    return
  }
  reading.value = true
  try {
    if (await file.slice(0, 5).text() !== '%PDF-') throw new Error('文件内容不是有效的 PDF')
    const data = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => resolve(String(reader.result).split(',')[1]!)
      reader.onerror = () => reject(new Error('PDF 读取失败，请重试'))
      reader.readAsDataURL(file)
    })
    model.value = { name: file.name, data }
    ElMessage.success('解题文档已添加，请保存修改')
  } catch (error) { ElMessage.error(error instanceof Error ? error.message : 'PDF 读取失败') }
  finally { reading.value = false }
}
</script>

<template>
  <section class="solution-document" aria-label="解题文档">
    <div class="document-heading">
      <div><h3>解题文档</h3><p class="muted">仅管理员可见 · PDF 格式，最大 20 MiB</p></div>
      <div v-if="editable" class="button-row">
        <label class="el-button el-button--primary" :class="{ 'is-disabled': disabled || reading }">
          {{ reading ? '正在读取…' : model ? '替换 PDF' : '上传 PDF' }}
          <input type="file" accept=".pdf,application/pdf" :disabled="disabled || reading" hidden @change="upload" />
        </label>
        <el-button v-if="model" type="danger" plain :disabled="disabled || reading" @click="model = null">移除</el-button>
      </div>
    </div>
    <template v-if="model && url">
      <p class="document-name">{{ model.name }} <a :href="url" target="_blank" rel="noopener">新窗口打开</a></p>
      <object :key="url" :data="url" type="application/pdf" class="pdf-preview" :aria-label="`解题文档：${model.name}`">
        <p>浏览器无法内嵌预览 PDF，<a :href="url" target="_blank" rel="noopener">点击打开文档</a>。</p>
      </object>
    </template>
    <el-empty v-else description="暂无解题文档" />
  </section>
</template>

<style scoped>
.solution-document { width: 100%; min-width: 0; margin-bottom: 24px; }
.document-heading { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 16px; }
h3 { margin: 0; }
.muted { margin: 8px 0; font-size: 13px; }
.document-name { overflow-wrap: anywhere; }
.document-name a { margin-left: 16px; }
.pdf-preview { display: block; width: 100%; height: 75vh; min-height: 480px; border: 1px solid #dfe6e2; border-radius: 8px; }
</style>
