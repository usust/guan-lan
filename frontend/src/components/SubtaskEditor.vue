<script setup lang="ts">
import AttachmentUploader from './AttachmentUploader.vue'
import { copyEditorValue } from '../editorSnapshot'
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import AttachmentList from './AttachmentList.vue'
import type { Attachment, Challenge, Subtask } from '../types'
const props = defineProps<{ question: Challenge; task: Subtask; busy: boolean; creating: boolean }>()
const emit = defineEmits<{ apply: [task: Subtask]; close: []; download: [attachment: Attachment] }>()
const draft = ref<Subtask>(copyEditorValue(props.task))
const reading = ref(false)
function apply() {
 if (!draft.value.title.trim()) { ElMessage.warning('请填写题目名称'); return }
 if (!Number.isInteger(draft.value.points) || draft.value.points < 1 || draft.value.points > 100000) { ElMessage.warning('分值请输入 1–100000 的整数'); return }
 emit('apply', draft.value)
}
</script>
<template>
 <el-drawer :model-value="true" :title="creating ? '添加题目' : '编辑题目'" direction="rtl" size="min(520px, 100vw)" :close-on-click-modal="false" :before-close="() => emit('close')">
              <el-form label-position="top" :disabled="busy">
                <el-form-item label="题目名称"><el-input v-model="draft.title" maxlength="100" /></el-form-item>
                <el-form-item label="题目内容"><el-input v-model="draft.content" type="textarea" :rows="2" maxlength="5000" /></el-form-item>
                <el-form-item label="附件">
                  <AttachmentList :attachments="draft.attachments" removable :disabled="busy || reading" @download="emit('download', $event)" @remove="draft.attachments?.splice($event, 1)">
                    <template #action><AttachmentUploader :disabled="busy" @busy="reading = $event" @added="(draft.attachments ||= []).push($event)" /></template>
                  </AttachmentList>
                </el-form-item>
                <el-form-item label="绑定 Docker">
                  <el-select v-model="draft.service_keys" multiple clearable placeholder=" " :disabled="!question.environment" style="width: 100%">
                    <el-option v-for="service in question.environment?.services || []" :key="service.key" :label="`${service.name || service.key}（${question.environment?.images.find(image => image.key === service.image)?.source_file || service.key}）`" :value="service.key" />
                  </el-select>
                </el-form-item>
                <el-form-item label="分值"><el-input v-model.number="draft.points" inputmode="numeric" /></el-form-item>
                <el-form-item label="Flag"><el-input v-model="draft.flag" type="text" autocomplete="off" :maxlength="512" /></el-form-item>
              </el-form>

 <template #footer><el-button :disabled="busy || reading" @click="emit('close')">取消</el-button><el-button type="primary" :disabled="busy || reading" @click="apply">{{ creating ? '添加' : '完成' }}</el-button></template>
 </el-drawer>
</template>
