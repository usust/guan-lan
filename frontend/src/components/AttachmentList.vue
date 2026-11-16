<script setup lang="ts">
import { computed } from 'vue'
import type { Challenge } from '../types'

type Attachment = NonNullable<Challenge['attachments']>[number]
const props = withDefaults(defineProps<{
  attachments?: Attachment[]
  removable?: boolean
  disabled?: boolean
  compact?: boolean
}>(), { attachments: () => [] })
const emit = defineEmits<{
  download: [attachment: Attachment]
  remove: [index: number]
}>()

const fileGroups = [
  { extensions: ['tar.gz', 'tar.bz2', 'tar.xz', 'zip', 'rar', '7z', 'tar', 'gz', 'bz2', 'xz'], label: '压缩包', tone: 'archive' },
  { extensions: ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico', 'avif'], label: '图片', tone: 'image' },
  { extensions: ['pdf'], label: 'PDF 文档', tone: 'document' },
  { extensions: ['txt', 'md', 'log', 'csv'], label: '文本文件', tone: 'document' },
  { extensions: ['doc', 'docx', 'odt'], label: '文档', tone: 'document' },
  { extensions: ['xls', 'xlsx', 'ods'], label: '电子表格', tone: 'document' },
  { extensions: ['ppt', 'pptx', 'odp'], label: '演示文稿', tone: 'document' },
  { extensions: ['py', 'js', 'ts', 'json', 'yaml', 'yml', 'xml', 'html', 'css', 'sh', 'sql', 'c', 'cpp', 'h', 'go', 'java', 'rs', 'php'], label: '代码文件', tone: 'code' },
  { extensions: ['pcap', 'pcapng', 'cap'], label: '流量文件', tone: 'code' },
]

function fileInfo(name: string) {
  const lower = name.toLowerCase()
  for (const group of fileGroups) {
    const extension = group.extensions.find(ext => lower.endsWith(`.${ext}`))
    if (extension) return { extension: extension.toUpperCase(), label: group.label, tone: group.tone }
  }
  const dot = name.lastIndexOf('.')
  const extension = dot > 0 && dot < name.length - 1 ? name.slice(dot + 1).toUpperCase() : ''
  return { extension: extension || 'FILE', label: extension ? `${extension} 文件` : '文件', tone: 'default' }
}

function byteLength(data: string) {
  // Base64 length gives the original size without decoding a second copy of the file.
  const encoded = /[\r\n]/.test(data) ? data.replace(/[\r\n]/g, '') : data
  const padding = encoded.endsWith('==') ? 2 : encoded.endsWith('=') ? 1 : 0
  return Math.max(0, Math.floor(encoded.length * 3 / 4) - padding)
}
function formatSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++ }
  return `${Number(value.toFixed(1))} ${units[unit]}`
}
const files = computed(() => props.attachments.map(attachment => ({
  attachment,
  ...fileInfo(attachment.name),
  size: attachment.file_id ? (attachment.size || 0) : byteLength(attachment.data),
})))
const totalSize = computed(() => formatSize(files.value.reduce((total, file) => total + file.size, 0)))
</script>

<template>
  <div class="attachment-list" :class="{ 'is-compact': compact }">
    <div class="attachment-list-header">
      <div class="attachment-summary">
        <span class="attachment-count">{{ files.length ? `${files.length} 个附件` : '暂无附件' }}</span>
        <span v-if="files.length" class="attachment-total">共 {{ totalSize }}</span>
      </div>
      <slot name="action" />
    </div>
    <ul v-if="files.length" class="attachment-items" aria-label="附件列表">
      <li v-for="(file, index) in files" :key="index" class="attachment-item">
        <span class="attachment-file-icon" :class="`file-tone-${file.tone}`" aria-hidden="true">
          <svg viewBox="0 0 32 38" fill="none"><path d="M7 1.5h12l10 10v22a3 3 0 0 1-3 3H7a3 3 0 0 1-3-3v-29a3 3 0 0 1 3-3Z" fill="currentColor" fill-opacity=".07" stroke="currentColor" stroke-width="1.5" /><path d="M19 2v10h10" stroke="currentColor" stroke-width="1.5" /></svg>
          <span>{{ file.extension.length <= 4 ? file.extension : file.extension.includes('.') ? file.extension.split('.').pop() : 'FILE' }}</span>
        </span>
        <div class="attachment-file-info">
          <button type="button" class="attachment-name" :title="file.attachment.name" :aria-label="`下载 ${file.attachment.name}`" @click="emit('download', file.attachment)">{{ file.attachment.name }}</button>
          <div class="attachment-meta"><span class="attachment-type">{{ file.label }}</span><span class="attachment-size">{{ formatSize(file.size) }}</span></div>
        </div>
        <div class="attachment-actions">
          <button type="button" class="attachment-download" :aria-label="`下载 ${file.attachment.name}`" :title="`下载 ${file.attachment.name}`" @click="emit('download', file.attachment)">
            <svg viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="M10 2.5v10m-3.5-3.5L10 12.5l3.5-3.5M3.5 13v3.5h13V13" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg>
            <span v-if="!compact">下载</span>
          </button>
          <button v-if="removable" type="button" class="attachment-remove" :disabled="disabled" :aria-label="`移除 ${file.attachment.name}`" @click="emit('remove', index)">移除</button>
        </div>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.attachment-list { width: 100%; min-width: 0; overflow: hidden; background: transparent; font-size: 14px; line-height: 1.5; }
.attachment-list-header { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px 16px; min-height: 46px; padding: 10px 14px; background: #f3f7f5; }
.attachment-summary { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 12px; }
.attachment-count { color: #20322d; font-weight: 600; }
.attachment-total { color: #65766e; font-size: 12px; font-variant-numeric: tabular-nums; }
.attachment-items { list-style: none; margin: 0; padding: 0; }
.attachment-item { display: flex; align-items: center; gap: 12px; padding: 14px; border-top: 1px solid #e5ebe8; }
.attachment-item:hover, .attachment-item:focus-within { background: #f8faf9; }
.attachment-file-icon { position: relative; flex: 0 0 34px; width: 34px; height: 40px; color: #65766e; }
.attachment-file-icon > svg { width: 100%; height: 100%; }
.attachment-file-icon > span { position: absolute; left: 0; right: 0; top: 19px; text-align: center; font-size: 9px; font-weight: 700; line-height: 14px; }
.file-tone-document { color: #226953; }
.file-tone-archive { color: #94651c; }
.file-tone-image, .file-tone-code { color: #386b94; }
.attachment-file-info { flex: 1; min-width: 0; }
.attachment-name { display: block; width: 100%; padding: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; border: 0; background: transparent; color: #20322d; font: inherit; font-weight: 500; text-align: left; cursor: pointer; }
.attachment-name:hover { color: #226953; text-decoration: underline; text-underline-offset: 3px; }
.attachment-meta { display: flex; flex-wrap: wrap; gap: 2px 12px; margin-top: 4px; color: #65766e; font-size: 12px; line-height: 1.5; }
.attachment-type { min-width: 0; overflow-wrap: anywhere; }
.attachment-size { white-space: nowrap; font-variant-numeric: tabular-nums; }
.attachment-actions { display: flex; flex-shrink: 0; align-items: center; gap: 4px; }
.attachment-download, .attachment-remove { display: inline-flex; align-items: center; justify-content: center; gap: 5px; min-width: 32px; min-height: 32px; padding: 6px 8px; border: 0; border-radius: 5px; background: transparent; color: #226953; font: inherit; font-size: 12px; line-height: 20px; cursor: pointer; }
.attachment-download svg { width: 17px; height: 17px; }
.attachment-download:hover { background: #edf4f0; }
.attachment-remove { color: #b0443d; }
.attachment-remove:hover:not(:disabled) { background: #fdf0ef; }
.attachment-remove:disabled { opacity: .5; cursor: not-allowed; }
.attachment-list button:focus-visible { outline: 2px solid #226953; outline-offset: 2px; }
.is-compact .attachment-item { gap: 8px; padding: 12px 10px; }
.is-compact .attachment-list-header { padding: 10px; }
.is-compact .attachment-actions { gap: 0; }
.is-compact .attachment-download, .is-compact .attachment-remove { padding: 6px; }
@media (max-width: 540px) {
  .attachment-item { gap: 8px; padding: 12px 10px; }
  .attachment-download > span { display: none; }
}
</style>
