<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { request, errorMessage } from '../api'
import { platform, type PlatformBranding, type PlatformSettings } from '../stores/platform'

const settings = ref<PlatformSettings>()
const form = reactive<PlatformBranding>({ name: '', description: '', icon: '' })
const loading = ref(false)
const saving = ref(false)
const resetting = ref(false)
const resetGroups = [
  { value: 'content', label: '题目与比赛（含附件、环境包及临时上传）' },
  { value: 'taxonomy', label: '题型类型、知识点、难度等级' },
  { value: 'results', label: '提交记录与成绩' },
  { value: 'users', label: '其他用户（保留当前管理员）' },
  { value: 'runtime', label: '运行环境与验收记录' },
  { value: 'branding', label: '平台名称、简介与图标' },
  { value: 'logs', label: '系统日志与操作记录' },
  { value: 'backups', label: '备份文件' },
]
const selectedResetGroups = ref<string[]>([])
const resetDependencies = computed(() => selectedResetGroups.value.some(value => ['content', 'users'].includes(value)))
function updateResetGroups() {
  if (resetDependencies.value) selectedResetGroups.value = [...new Set([...selectedResetGroups.value, 'results', 'runtime'])]
}
const error = ref('')
const iconReading = ref(false)
const paths: Record<string, string> = { settings: '设置目录', database: '数据库文件', packages: '环境包目录', attachments: '附件目录', images: '镜像归档目录', templates: '题目模板目录', runtime: '运行实例目录', logs: '日志目录', temp: '临时文件目录', backups: '备份目录' }
async function load() {
  loading.value = true
  error.value = ''
  try {
    settings.value = await request<PlatformSettings>('/admin/platform-settings')
    Object.assign(form, settings.value.branding)
  } catch (e) { error.value = errorMessage(e) }
  finally { loading.value = false }
}
async function chooseIcon(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (!['image/png', 'image/jpeg'].includes(file.type) || file.size > 256 * 1024) {
    ElMessage.error('请选择不超过256 KB的 PNG 或 JPEG 图标'); return
  }
  iconReading.value = true
  try {
    form.icon = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => resolve(String(reader.result))
      reader.onerror = () => reject(new Error('图标读取失败'))
      reader.readAsDataURL(file)
    })
  } catch (e) { ElMessage.error(errorMessage(e)) }
  finally { iconReading.value = false }
}
async function save() {
  if (!form.name.trim()) { ElMessage.error('请输入平台名称'); return }
  saving.value = true
  try {
    const result = await request<PlatformBranding>('/admin/platform-settings', { method: 'PUT', body: JSON.stringify(form) })
    Object.assign(form, result)
    Object.assign(platform, result)
    if (settings.value) settings.value.branding = { ...result }
    ElMessage.success('平台设置已保存并生效')
  } catch (e) { ElMessage.error(errorMessage(e)) }
  finally { saving.value = false }
}
async function resetSystem() {
  if (!selectedResetGroups.value.length) { ElMessage.warning('请至少选择一组需要删除的数据'); return }
  const groups = [...selectedResetGroups.value]
  const labels = resetGroups.filter(group => groups.includes(group.value)).map(group => group.label).join('；')
  try {
    await ElMessageBox.prompt(`此操作不可恢复，将删除：${labels}。当前管理员账号和服务器启动配置始终保留。请输入“重置系统”确认。`, '重置系统', {
      type: 'warning', confirmButtonText: '永久删除并重置', cancelButtonText: '取消',
      inputValidator: value => value === '重置系统' || '请输入“重置系统”',
      closeOnClickModal: false, closeOnPressEscape: false,
    })
  } catch { return }
  resetting.value = true
  try {
    const result = await request<{ logged_out: boolean }>('/admin/system/reset', { method: 'POST', body: JSON.stringify({ confirmation: '重置系统', groups }) })
    ElMessage.success(result.logged_out ? '所选数据已清空，请重新登录' : '所选数据已清空')
    if (result.logged_out) window.location.assign('/login')
    else window.location.reload()
  } catch (e) { ElMessage.error(errorMessage(e)) }
  finally { resetting.value = false }
}
onMounted(load)
</script>

<template>
  <section v-loading="loading || resetting" element-loading-text="正在处理，请勿关闭页面">
    <p class="eyebrow">系统设置</p>
    <h1>平台设置</h1>
    <p class="muted">集中管理平台品牌信息，查看配置、存储及运行参数。</p>
    <el-alert v-if="error" :title="error" type="error" :closable="false" class="section-gap" />
    <el-button v-if="error" class="section-gap" @click="load">重新加载</el-button>
    <template v-if="settings">
      <el-card shadow="never" class="section-gap">
        <h2>基本信息</h2>
        <el-form label-position="top" :disabled="saving" @submit.prevent="save">
          <el-form-item label="平台名称" required><el-input v-model="form.name" maxlength="64" show-word-limit /></el-form-item>
          <el-form-item label="平台简介"><el-input v-model="form.description" maxlength="200" show-word-limit placeholder="用于侧栏说明与页脚" /></el-form-item>
          <el-form-item label="平台图标">
            <div class="platform-icon-editor">
              <img v-if="form.icon" :src="form.icon" alt="平台图标预览" class="platform-icon-preview" />
              <input type="file" accept="image/png,image/jpeg" aria-label="选择平台图标" :disabled="saving || iconReading" @change="chooseIcon" />
              <el-button v-if="form.icon" @click="form.icon = ''">移除图标</el-button>
            </div>
          </el-form-item>
          <p class="muted">支持 PNG、JPEG，最大256 KB、2048×2048像素；保存后同步更新导航、登录页和浏览器图标。</p>
          <el-button type="primary" native-type="submit" :loading="saving" :disabled="iconReading">保存设置</el-button>
        </el-form>
      </el-card>
      <el-card shadow="never" class="section-gap">
        <h2>配置与存储</h2>
        <el-alert title="以下为服务器当前生效的路径" description="目录由启动配置 root（或 HUASHAN_ROOT）统一派生，不能在线切换。迁移时请停止服务、备份并完整迁移数据根目录，调整启动配置后重启；单独改路径不会迁移已有数据。" type="info" :closable="false" show-icon />
        <el-descriptions :column="1" border class="section-gap">
          <el-descriptions-item label="平台标识"><span class="platform-path">{{ settings.platform_id }}</span></el-descriptions-item>
          <el-descriptions-item label="启动配置文件"><span class="platform-path">{{ settings.config_file || '由部署程序提供' }}</span></el-descriptions-item>
          <el-descriptions-item label="存储根目录"><span class="platform-path">{{ settings.root }}</span></el-descriptions-item>
          <el-descriptions-item v-for="(label, key) in paths" :key="key" :label="label"><span class="platform-path">{{ settings.paths[key] }}</span></el-descriptions-item>
          <el-descriptions-item label="平台设置文件"><span class="platform-path">{{ settings.paths.settings }}/platform.json</span></el-descriptions-item>
        </el-descriptions>
      </el-card>
      <el-card shadow="never" class="section-gap">
        <h2>服务与运行环境</h2>
        <p class="muted">以下参数在启动配置文件中调整，重启服务后生效。管理员账号请在“用户管理”中维护。</p>
        <el-descriptions :column="1" border>
          <el-descriptions-item label="监听地址（listen）">{{ settings.listen || '由部署程序提供' }}</el-descriptions-item>
          <el-descriptions-item label="允许的来源（allowed_origin）">{{ settings.allowed_origin || '未设置' }}</el-descriptions-item>
          <el-descriptions-item label="安全 Cookie（secure_cookie）">{{ settings.secure_cookie ? '已启用（仅 HTTPS）' : '未启用' }}</el-descriptions-item>
          <el-descriptions-item label="Docker 地址（docker_endpoint）"><span class="platform-path">{{ settings.docker_endpoint || '运行时默认地址' }}</span></el-descriptions-item>
          <el-descriptions-item label="环境访问主机（runtime_public_host）">{{ settings.runtime_public_host }}</el-descriptions-item>
          <el-descriptions-item label="最低保留磁盘空间（min_free_bytes）">{{ (settings.min_free_bytes / 1024 / 1024).toLocaleString() }} MiB</el-descriptions-item>
        </el-descriptions>
      </el-card>
      <el-card shadow="never" class="section-gap">
        <h2>重置系统</h2>
        <el-alert title="永久删除，无法撤销" description="仅删除勾选的数据。当前管理员账号与服务器启动配置始终保留；删除其他用户后需要重新登录。" type="error" :closable="false" show-icon />
        <el-checkbox-group v-model="selectedResetGroups" class="reset-options section-gap" :disabled="resetting" @change="updateResetGroups">
          <el-checkbox v-for="group in resetGroups" :key="group.value" :value="group.value" :disabled="resetDependencies && ['results', 'runtime'].includes(group.value)">{{ group.label }}</el-checkbox>
        </el-checkbox-group>
        <p class="muted">删除题目比赛或用户时，须同时清空提交成绩和运行环境，已自动勾选。单独删除题型、知识点和难度时，会清除保留题目上的对应标记。</p>
        <el-button type="danger" class="section-gap" :loading="resetting" :disabled="saving || !selectedResetGroups.length" @click="resetSystem">删除所选数据</el-button>
      </el-card>
    </template>
  </section>
</template>

<style scoped>
.reset-options { display: flex; flex-direction: column; align-items: flex-start; }
.reset-options :deep(.el-checkbox) { height: auto; min-height: 32px; white-space: normal; margin-right: 0; }
</style>
