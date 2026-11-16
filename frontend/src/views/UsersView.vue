<script setup lang="ts">
import { nextTick, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance, FormRules } from 'element-plus'
import { api, errorMessage } from '../api'
import { useAuthStore } from '../stores/auth'
import type { CreateUserInput, User } from '../types'

const auth = useAuthStore()
const users = ref<User[]>([])
const loading = ref(false)
const error = ref('')
const changingId = ref<User['id'] | null>(null)
const dialogOpen = ref(false)
const creating = ref(false)
const createError = ref('')
const formRef = ref<FormInstance>()
const form = reactive<CreateUserInput>({ username: '', display_name: '', password: '', role: 'PLAYER' })
const rules: FormRules = {
  username: [{ required: true, whitespace: true, message: '请输入用户名', trigger: 'blur' }],
  display_name: [{ required: true, whitespace: true, message: '请输入显示名称', trigger: 'blur' }],
  password: [{ required: true, message: '请输入初始密码', trigger: 'blur' }],
  role: [{ required: true, message: '请选择角色', trigger: 'change' }],
}

async function load(): Promise<void> {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    users.value = (await api.users()).items
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    loading.value = false
  }
}

async function openCreate(): Promise<void> {
  Object.assign(form, { username: '', display_name: '', password: '', role: 'PLAYER' })
  createError.value = ''
  dialogOpen.value = true
  await nextTick()
  formRef.value?.clearValidate()
}

async function create(): Promise<void> {
  if (creating.value || !(await formRef.value?.validate().catch(() => false))) return
  creating.value = true
  createError.value = ''
  try {
    await api.createUser({ ...form, username: form.username.trim(), display_name: form.display_name.trim() })
    form.password = ''
    dialogOpen.value = false
    ElMessage.success('用户已创建')
    await load()
  } catch (cause) {
    createError.value = errorMessage(cause)
  } finally {
    creating.value = false
  }
}

function beforeClose(done: () => void): void {
  if (!creating.value) done()
}

async function changeStatus(user: User): Promise<void> {
  if (changingId.value !== null) return
  const status = user.status === 'ACTIVE' ? 'DISABLED' : 'ACTIVE'
  const action = status === 'ACTIVE' ? '启用' : '禁用'
  try {
    await ElMessageBox.confirm(
      `确定${action}用户“${user.display_name || user.username}”（${user.username}）吗？${status === 'DISABLED' ? '禁用后该用户将无法登录。' : ''}`,
      `${action}用户`,
      { confirmButtonText: `确认${action}`, cancelButtonText: '取消', type: 'warning' },
    )
  } catch {
    return
  }
  changingId.value = user.id
  error.value = ''
  try {
    await api.updateUserStatus(user.id, status)
    ElMessage.success(`用户已${action}`)
    await load()
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    changingId.value = null
  }
}

onMounted(load)
</script>

<template>
  <section>
    <div class="page-heading">
      <div><p class="eyebrow">账号与权限</p><h1>用户管理</h1><p class="muted">创建账号，并管理用户的启用状态。</p></div>
      <div class="button-row"><el-button :loading="loading" :disabled="creating || changingId !== null" @click="load">刷新列表</el-button><el-button type="primary" :disabled="loading || changingId !== null" @click="openCreate">新建用户</el-button></div>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon class="form-alert" />
    <el-card shadow="never">
      <el-table v-loading="loading" :data="users" row-key="id" :aria-busy="loading" empty-text="暂无用户" table-layout="auto">
        <el-table-column prop="username" label="用户名" min-width="150" />
        <el-table-column prop="display_name" label="显示名称" min-width="150" />
        <el-table-column label="角色" min-width="100"><template #default="{ row }"><el-tag :type="row.role === 'ADMIN' ? 'primary' : 'info'" effect="plain">{{ row.role === 'ADMIN' ? '管理员' : '用户' }}</el-tag></template></el-table-column>
        <el-table-column label="状态" min-width="100"><template #default="{ row }"><el-tag :type="row.status === 'ACTIVE' ? 'success' : 'danger'">{{ row.status === 'ACTIVE' ? '已启用' : '已禁用' }}</el-tag></template></el-table-column>
        <el-table-column label="操作" min-width="140"><template #default="{ row }"><el-button :type="row.status === 'ACTIVE' ? 'danger' : 'primary'" link :loading="changingId === row.id" :disabled="row.id === auth.user?.id || loading || (changingId !== null && changingId !== row.id)" @click="changeStatus(row)">{{ row.status === 'ACTIVE' ? '禁用' : '启用' }}</el-button><span v-if="row.id === auth.user?.id" class="muted self-label">当前账号</span></template></el-table-column>
      </el-table>
      <p class="table-note muted">为避免误操作，当前登录账号不能在此处禁用自己。</p>
    </el-card>
    <el-dialog v-model="dialogOpen" title="新建用户" width="480px" class="user-dialog" :close-on-click-modal="false" :close-on-press-escape="!creating" :show-close="!creating" :before-close="beforeClose" @closed="form.password = ''">
      <el-alert v-if="createError" :title="createError" type="error" :closable="false" show-icon class="form-alert" />
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top" :disabled="creating" @submit.prevent="create">
        <el-form-item label="用户名" prop="username"><el-input v-model="form.username" autocomplete="off" placeholder="请输入用户名" /></el-form-item>
        <el-form-item label="显示名称" prop="display_name"><el-input v-model="form.display_name" autocomplete="off" placeholder="请输入显示名称" /></el-form-item>
        <el-form-item label="初始密码" prop="password"><el-input v-model="form.password" type="password" show-password autocomplete="new-password" placeholder="请设置初始密码" /></el-form-item>
        <el-form-item label="角色" prop="role"><el-select v-model="form.role" class="full-width"><el-option label="用户" value="PLAYER" /><el-option label="管理员" value="ADMIN" /></el-select></el-form-item>
      </el-form>
      <template #footer><el-button :disabled="creating" @click="dialogOpen = false">取消</el-button><el-button type="primary" :loading="creating" @click="create">创建用户</el-button></template>
    </el-dialog>
  </section>
</template>
