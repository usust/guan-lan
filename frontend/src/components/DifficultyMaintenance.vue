<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { request, errorMessage } from '../api'
import { difficulties, difficultyLevels, setDifficulties, type DifficultySettings } from '../stores/difficulties'
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const editor = ref<{ code: string; name: string; creating: boolean } | null>(null)
const rows = computed(() => difficultyLevels.value.map((code, index) => ({ code, name: difficulties[code], rank: index + 1 })))
async function load() {
 loading.value = true; error.value = ''
 try { setDifficulties(await request<DifficultySettings>('/admin/difficulty-settings')) }
 catch (e) { error.value = errorMessage(e) }
 finally { loading.value = false }
}
async function persist(settings: DifficultySettings): Promise<boolean> {
 saving.value = true
 try {
  setDifficulties(await request<DifficultySettings>('/admin/difficulty-settings', { method: 'PUT', body: JSON.stringify(settings) }))
  ElMessage.success('难度已保存'); return true
 } catch (e) { ElMessage.error(errorMessage(e)); return false }
 finally { saving.value = false }
}
async function save() {
 if (!editor.value || saving.value) return
 const { code, creating } = editor.value
 const name = editor.value.name.trim()
 if (!name || [...name].length > 16) { ElMessage.error('请输入1至16个字符的难度名称'); return }
 if (Object.entries(difficulties).some(([key, value]) => key !== code && value.toLowerCase() === name.toLowerCase())) { ElMessage.error('难度名称不能重复'); return }
 if (creating && difficulties[code]) { ElMessage.error('难度已存在，请重新添加'); return }
 if (await persist({ ...difficulties, [code]: name })) editor.value = null
}
async function remove(code: string) {
 if (saving.value || rows.value.length <= 1) return
 try { await ElMessageBox.confirm(`删除难度“${difficulties[code]}”？仍被题目使用的难度无法删除。`, '删除难度', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }) } catch { return }
 const settings = { ...difficulties }; delete settings[code]
 await persist(settings)
}
onMounted(load)
</script>
<template>
 <div v-loading="loading" class="difficulty-maintenance">
  <el-alert v-if="error" :title="error" type="error" :closable="false" class="form-alert" />
  <div class="difficulty-toolbar">
   <el-button type="primary" :disabled="saving || loading || !!error || rows.length >= 50" @click="editor = { code: `CUSTOM_${Date.now()}_${Math.random().toString(36).slice(2, 8).toUpperCase()}`, name: '', creating: true }">新增难度</el-button>
   <el-button :disabled="saving || loading" @click="load">刷新</el-button>
  </div>
  <el-table :data="rows" class="difficulty-table">
   <el-table-column prop="rank" label="序号" width="100" />
   <el-table-column prop="name" label="难度名称" min-width="180" />
   <el-table-column label="操作" width="180">
    <template #default="{ row }">
     <el-button text type="primary" :disabled="saving || loading || !!error" @click="editor = { code: row.code, name: row.name, creating: false }">修改</el-button>
     <el-button text type="danger" :disabled="saving || loading || !!error || rows.length <= 1" @click="remove(row.code)">删除</el-button>
    </template>
   </el-table-column>
  </el-table>
  <el-dialog :model-value="!!editor" :title="editor?.creating ? '新增难度' : '修改难度'" width="440px" :close-on-click-modal="!saving" :close-on-press-escape="!saving" :show-close="!saving" @close="editor = null">
   <el-form v-if="editor" label-position="top" :disabled="saving" @submit.prevent="save">
    <el-form-item label="难度名称"><el-input v-model="editor.name" maxlength="16" show-word-limit placeholder="请输入难度名称" /></el-form-item>
   </el-form>
   <template #footer><el-button :disabled="saving" @click="editor = null">取消</el-button><el-button type="primary" :loading="saving" :disabled="!editor?.name.trim()" @click="save">保存</el-button></template>
  </el-dialog>
 </div>
</template>
<style scoped>
.difficulty-maintenance { max-width: 800px; padding: 12px 0 24px; }
.difficulty-toolbar { display: flex; justify-content: space-between; margin-bottom: 20px; }
.difficulty-table :deep(.el-table__cell) { padding: 16px 12px; }
</style>
