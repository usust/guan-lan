<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { errorMessage } from '../api'
import { uploadFileResumable, formatUploadBytes, formatUploadTime, type UploadProgress } from '../packageUpload'
import { useAuthStore } from '../stores/auth'
import type { Attachment } from '../types'
const props = defineProps<{ disabled?: boolean }>()
const emit = defineEmits<{ added: [attachment: Attachment]; busy: [value: boolean] }>()
const auth = useAuthStore()
const progress = ref<UploadProgress | null>(null)
const status = ref<'uploading' | 'paused' | 'error' | 'done'>('done')
const error = ref('')
const name = ref('')
let queue: File[] = []
let controller: AbortController | null = null
const percentage = computed(() => progress.value ? Math.min(100, Math.floor(progress.value.loaded / progress.value.total * 100)) : 0)
const label = computed(() => {
  if (status.value === 'paused') return '已暂停'
  if (status.value === 'error') return '上传中断'
  return { preparing: '正在识别文件', checking: '正在核对已上传分片', uploading: '正在上传', validating: '正在完成上传' }[progress.value?.phase || 'preparing']
})
async function run() {
  if (!queue.length || controller || props.disabled) return
  controller = new AbortController()
  const current = controller
  status.value = 'uploading'; error.value = ''; emit('busy', true)
  try {
    while (queue.length) {
      const file = queue[0]!
      name.value = file.name
      const attachment = await uploadFileResumable<Attachment>(file, String(auth.user?.id), current.signal,
        value => { progress.value = value }, '/admin/attachment-uploads')
      emit('added', attachment)
      queue.shift()
    }
    status.value = 'done'; progress.value = null
    ElMessage.success('附件上传完成，请保存修改')
  } catch (e) {
    status.value = current.signal.aborted ? 'paused' : 'error'
    if (!current.signal.aborted) error.value = errorMessage(e)
  } finally { controller = null; emit('busy', false) }
}
function select(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files || []); input.value = ''
  if (!files.length || controller || props.disabled) return
  if (files.some(file => !file.size)) { ElMessage.warning('请选择非空附件'); return }
  queue = files
  void run()
}
function pause() { controller?.abort() }
onUnmounted(pause)
</script>
<template>
  <div class="attachment-uploader">
    <label class="attachment-upload" :class="{ disabled: disabled || status === 'uploading' }">上传<input type="file" multiple aria-label="上传附件" :disabled="disabled || status === 'uploading'" @change="select" /></label>
    <div v-if="progress" class="tar-upload-progress" role="status" aria-live="polite">
      <div class="card-heading"><strong>{{ label }}：{{ name }}</strong><button v-if="status === 'uploading'" type="button" class="container-configure" @click="pause">暂停</button><button v-else type="button" class="container-configure" :disabled="disabled" @click="run">继续上传</button></div>
      <el-progress :percentage="percentage" :status="status === 'error' ? 'exception' : undefined" />
      <p>{{ formatUploadBytes(progress.loaded) }} / {{ formatUploadBytes(progress.total) }}<template v-if="status === 'uploading' && progress.phase === 'uploading'"> · {{ formatUploadBytes(progress.speed) }}/s · 预计剩余 {{ formatUploadTime(progress.remaining) }}</template></p>
      <p v-if="error" class="tar-upload-error">{{ error }}</p>
      <p class="muted">支持大文件和断点续传；中断或刷新后重新选择同一文件可继续。上传完成后请保存修改。</p>
    </div>
  </div>
</template>
<style scoped>
.attachment-uploader { width: 100%; }
.attachment-upload { float: right; }
.tar-upload-progress { clear: both; }
</style>
