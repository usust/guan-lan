<script setup lang="ts">
import { computed, nextTick, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import type { FormInstance, FormRules } from 'element-plus'
import { api, errorMessage } from '../api'
import { useAuthStore } from '../stores/auth'
import { homeFor } from '../router/access'

import { platform } from '../stores/platform'

const auth = useAuthStore()
const router = useRouter()
const formRef = ref<FormInstance>()
const registering = ref(false)
const form = reactive({ username: '', password: '', confirmPassword: '' })
const rules = computed<FormRules>(() => ({
  username: [
    { required: true, whitespace: true, message: '请输入用户名', trigger: 'blur' },
    ...(registering.value ? [{ validator: (_rule: unknown, value: string, callback: (error?: Error) => void) => {
      callback(/^[a-z0-9][a-z0-9_.-]{2,63}$/.test(value.trim().toLowerCase()) ? undefined : new Error('请输入3–64位字母、数字或 _ . -，以字母或数字开头'))
    }, trigger: 'blur' }] : []),
  ],
  password: [
    { required: true, message: '请输入密码', trigger: 'blur' },
    ...(registering.value ? [{ validator: (_rule: unknown, value: string, callback: (error?: Error) => void) => {
      const bytes = new TextEncoder().encode(value).length
      callback(bytes >= 12 && bytes <= 128 ? undefined : new Error('密码须为12–128字节（中文通常占3字节）'))
    }, trigger: 'blur' }] : []),
  ],
  confirmPassword: [{ validator: (_rule: unknown, value: string, callback: (error?: Error) => void) => {
    callback(value && value === form.password ? undefined : new Error('两次输入的密码不一致'))
  }, trigger: 'blur' }],
}))
const submitting = ref(false)
const error = ref('')
const success = ref('')

async function switchMode(): Promise<void> {
  if (submitting.value) return
  registering.value = !registering.value
  form.password = ''
  form.confirmPassword = ''
  error.value = ''
  success.value = ''
  await nextTick()
  formRef.value?.clearValidate()
}

async function submit(): Promise<void> {
  if (submitting.value) return
  submitting.value = true
  try {
    if (!(await formRef.value?.validate().catch(() => false))) return
    error.value = ''
    success.value = ''
    if (registering.value) {
      const { user } = await api.register(form.username.trim(), form.password)
      form.username = user.username
      form.password = ''
      form.confirmPassword = ''
      registering.value = false
      success.value = '注册成功，账号已保存到本地。请使用新账号登录。'
      await nextTick()
      formRef.value?.clearValidate()
    } else {
      await auth.login(form.username.trim(), form.password)
      form.password = ''
      await router.replace(homeFor(auth.user))
    }
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    submitting.value = false
  }
}

async function retrySession(): Promise<void> {
  await auth.initialize(true)
  if (auth.user) await router.replace(homeFor(auth.user))
}
</script>

<template>
  <section class="login-layout">
    <div class="login-intro">
      <p class="eyebrow"><img v-if="platform.icon" :src="platform.icon" alt="" class="platform-brand-icon" />{{ platform.name }}</p>
      <h1>从这里开始。</h1>
      <p>统一的账号入口，清晰的管理空间。</p>
      <p class="muted">账号与平台数据保存在运行后端的本机，不依赖云端存储。注册账号后即可参与竞赛与训练。</p>
    </div>
    <el-card class="login-card" shadow="never">
      <h2>{{ registering ? '注册账号' : '登录账号' }}</h2>
      <p class="muted">{{ registering ? '创建选手账号，开始竞赛与训练。' : '使用已有账号登录，或注册一个新账号。' }}</p>
      <el-alert v-if="auth.initializationError" :title="auth.initializationError" type="warning" :closable="false" show-icon>
        <el-button link type="primary" :loading="auth.initializing" @click="retrySession">重新验证登录状态</el-button>
      </el-alert>
      <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon class="form-alert" />
      <el-alert v-if="success" :title="success" type="success" :closable="false" show-icon class="form-alert" />
      <el-form ref="formRef" :model="form" :rules="rules" :validate-on-rule-change="false" label-position="top" @submit.prevent="submit">
        <el-form-item label="用户名" prop="username">
          <el-input v-model="form.username" name="username" autocomplete="username" placeholder="请输入用户名" :disabled="submitting" />
        </el-form-item>
        <el-form-item label="密码" prop="password">
          <el-input v-model="form.password" name="password" type="password" :autocomplete="registering ? 'new-password' : 'current-password'" :placeholder="registering ? '12–128字节，建议使用长密码' : '请输入密码'" show-password :disabled="submitting" />
        </el-form-item>
        <el-form-item v-if="registering" label="确认密码" prop="confirmPassword" required>
          <el-input v-model="form.confirmPassword" name="confirm-password" type="password" autocomplete="new-password" placeholder="请再次输入密码" show-password :disabled="submitting" />
        </el-form-item>
        <el-button class="full-width" type="primary" native-type="submit" :loading="submitting">{{ registering ? '注册账号' : '登录' }}</el-button>
      </el-form>
      <p class="auth-switch muted">
        {{ registering ? '已有账号？' : '还没有账号？' }}
        <el-button link type="primary" :disabled="submitting" @click="switchMode">{{ registering ? '返回登录' : '注册账号' }}</el-button>
      </p>
      <p v-if="registering" class="storage-note muted">账号存入本地数据库，密码以加盐哈希保存。</p>
    </el-card>
  </section>
</template>

<style scoped>
.auth-switch { margin: 20px 0 0; text-align: center; font-size: 14px; }
.storage-note { margin: 12px 0 0; text-align: center; font-size: 12px; }
</style>
