<script setup lang="ts">
import { difficulties, difficultyLevels, loadDifficulties } from '../stores/difficulties'
import KnowledgePointSelect from '../components/KnowledgePointSelect.vue'
import type { KnowledgePoint } from '../knowledgePoints'
import { questionTypeTree, typeDescendants, type QuestionTypeEntry } from '../questionTypes'
import { memoryGB } from '../resourceLimits'
import { legacyAttachmentBlob } from '../attachmentDownload'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import AttachmentUploader from '../components/AttachmentUploader.vue'
import { copyEditorValue, equalEditorValue } from '../editorSnapshot'
import { api, errorMessage, request, type StagedPackage } from '../api'
import { useAuthStore } from '../stores/auth'
import { uploadPackageResumable, formatUploadBytes, formatUploadTime, type UploadProgress } from '../packageUpload'
import { startupStage, startupStages } from '../startupProgress'
import { VerificationConnectionError } from '../runtimeVerification'
import { verificationChecklist, updateChecklist, finishChecklist, type CheckItem } from '../verificationChecklist'
import ContainerServiceEditor from '../components/ContainerServiceEditor.vue'
import SubtaskEditor from '../components/SubtaskEditor.vue'
import SolutionDocument from '../components/SolutionDocument.vue'
import AttachmentList from '../components/AttachmentList.vue'
import type { Attachment, Challenge, ChallengeSet, Ranking, Submission, RuntimeInstance, EnvironmentManifest, Subtask } from '../types'

const props = defineProps<{ questionId?: string; embedded?: boolean }>()
const emit = defineEmits<{ saved: []; deleted: [] }>()
const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const library = computed(() => !!set.value?.library)
const admin = computed(() => auth.user?.role === 'ADMIN')
const set = ref<ChallengeSet | null>(null)
const original = ref<ChallengeSet | null>(null)
const dirty = computed(() => set.value !== null && !equalEditorValue(set.value, original.value))
const editing = computed(() => admin.value && (set.value?.status === 'DRAFT' || (!library.value && set.value?.status === 'CLOSED')))
const loading = ref(false)
const busy = ref(false)
const attachmentUploading = ref(false)
const runtimeBusy = ref('')
const startup = ref<{ id: string; stage: number; elapsed: number; message: string; state: 'running' | 'success' | 'error' } | null>(null)
let startupTimer: ReturnType<typeof setInterval> | undefined
onUnmounted(() => clearInterval(startupTimer))
const serviceEditor = ref<{ question: Challenge; index: number } | null>(null)
function applyServiceEdit(environment: EnvironmentManifest) {
  const q = serviceEditor.value!.question
  q.environment = environment
  q.runtime_ready = false
  if (verification.value?.question === q.id) verification.value = null
  serviceEditor.value = null
  testPanelOpen.value = false
  ElMessage.success('容器配置已修改，请保存或点击测试')
}
const testPanelOpen = ref(false)
const verification = ref<{ question: string; state: 'running' | 'success' | 'error'; started: number; items: CheckItem[]; messages: { time: string; text: string }[] } | null>(null)
const instances = ref<Record<string, RuntimeInstance>>({})
function runtimeRemaining(id: string) {
  const instance = instances.value[id]
  return instance?.state === 'RUNNING' ? Math.max(0, (instance.expires_at || 0) - now.value) : 0
}
function runtimeRunning(id: string) { return runtimeRemaining(id) > 0 }
function runtimePercent(id: string) { return Math.min(100, runtimeRemaining(id) / 3600000 * 100) }
function runtimeTime(id: string) {
  const seconds = Math.ceil(runtimeRemaining(id) / 1000)
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}

const pendingRuntime = computed(() => set.value?.questions.filter(q => q.environment && !q.runtime_ready) ?? [])
const error = ref('')
const ranks = ref<Ranking[]>([])
const history = ref<Submission[]>([])
const flags = ref<Record<string, string>>({})
const activeTab = ref('competition')
const editorTab = ref('basic')
const questionSearch = ref('')
const questionCategory = ref('')
const taxonomyTypes = ref<QuestionTypeEntry[]>([])
const taxonomyTree = computed(() => questionTypeTree(taxonomyTypes.value))
const pickerTypes = computed(() => typeDescendants(taxonomyTypes.value, pickerCategory.value))
const knowledgePoints = ref<KnowledgePoint[]>([])
const knowledgeTree = computed(() => questionTypeTree(knowledgePoints.value))
const pickerKnowledge = computed(() => typeDescendants(knowledgePoints.value, pickerTag.value))
const pickerSearch = ref('')
const pickerCategory = ref('')
const pickerTag = ref('')
const filteredCandidates = computed(() => candidates.value.filter(({ question: q }) => (!pickerCategory.value || pickerTypes.value.has(q.category)) && (!pickerTag.value || q.tags?.some(tag => pickerKnowledge.value.has(tag))) && `${q.title} ${q.category} ${(q.tags ?? []).join(' ')}`.toLowerCase().includes(pickerSearch.value.trim().toLowerCase())))
const questionStatus = ref('all')
const selectedQuestion = ref('')
const questionEntries = computed(() => (set.value?.questions ?? []).map((question, index) => ({
  question, key: question.id || `question-${index}`, number: index + 1,
  points: question.subtasks.reduce((sum, task) => sum + task.points, 0),
  solved: question.subtasks.filter(task => task.solved).length,
  complete: question.subtasks.length > 0 && question.subtasks.every(task => task.solved),
})))
const questionCategories = computed(() => [...new Set((set.value?.questions ?? []).map(q => q.category))])
const filteredQuestions = computed(() => {
  const keyword = questionSearch.value.trim().toLocaleLowerCase()
  return questionEntries.value.filter(({ question, complete }) =>
    (!keyword || `${question.title} ${(question.tags ?? []).join(' ')} ${question.description} ${question.subtasks.map(t => `${t.title} ${t.content}`).join(' ')}`.toLocaleLowerCase().includes(keyword)) &&
    (!questionCategory.value || question.category === questionCategory.value) &&
    (questionStatus.value === 'all' || (questionStatus.value === 'solved' ? complete : !complete)))
})
const currentQuestion = computed(() => questionEntries.value.find(entry => entry.key === selectedQuestion.value))
const currentQuestionIndex = computed(() => filteredQuestions.value.findIndex(entry => entry.key === selectedQuestion.value))
const displayedQuestions = computed(() => admin.value ? set.value?.questions ?? [] : currentQuestion.value ? [currentQuestion.value.question] : [])
watch(questionEntries, () => {
  // Preserve the current question after submitting an answer, even if its status changes.
  if (!questionEntries.value.some(entry => entry.key === selectedQuestion.value)) selectedQuestion.value = filteredQuestions.value[0]?.key ?? ''
}, { immediate: true })
watch([questionSearch, questionCategory, questionStatus], () => {
  if (!filteredQuestions.value.some(entry => entry.key === selectedQuestion.value)) selectedQuestion.value = filteredQuestions.value[0]?.key ?? ''
})
watch(() => route.params.id, () => { selectedQuestion.value = ''; questionSearch.value = ''; questionCategory.value = ''; questionStatus.value = 'all' })
function resetQuestionFilters() { questionSearch.value = ''; questionCategory.value = ''; questionStatus.value = 'all' }
const questionDetail = ref<HTMLElement | null>(null)
function selectQuestion(key: string) {
  selectedQuestion.value = key
  questionDetail.value?.scrollTo({ top: 0 })
}
function navigateQuestion(step: number) {
  const entry = filteredQuestions.value[currentQuestionIndex.value + step]
  if (entry) selectQuestion(entry.key)
}
function locateSubtask(index: number) {
  questionDetail.value?.querySelectorAll<HTMLElement>('.subtask-box')[index]?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

const libraryEditing = computed(() => editing.value && library.value)
const now = ref(Date.now())
const timer = setInterval(() => { now.value = Date.now() }, 1000)
onUnmounted(() => clearInterval(timer))
const accepting = computed(() => set.value?.status === 'PUBLISHED' && now.value >= set.value.starts_at && now.value < set.value.ends_at)
const total = computed(() => set.value?.questions.flatMap(q => q.subtasks).reduce((n, t) => n + t.points, 0) ?? 0)
function competitionDate(value: number) {
  const date = new Date(value)
  return `${date.getFullYear()}.${String(date.getMonth() + 1).padStart(2, '0')}.${String(date.getDate()).padStart(2, '0')}`
}
function competitionTime(value: number) {
  return new Date(value).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })
}
const solved = computed(() => set.value?.questions.flatMap(q => q.subtasks).filter(t => t.solved).length ?? 0)
const windowLabel = computed(() => set.value?.status === 'DRAFT' ? '草稿' : set.value?.status === 'CLOSED' ? '已下架' : (set.value && now.value >= set.value.ends_at) ? '已结束' : accepting.value ? '进行中' : '尚未开放')
function accept(value: ChallengeSet) { if (value.library && activeTab.value === 'competition') activeTab.value = 'questions'; set.value = value; original.value = copyEditorValue(value) }
async function syncRuntimeInstances() {
  const targets = set.value?.questions.flatMap(q => q.subtasks).filter(t => t.service_keys?.length) ?? []
  const states = await Promise.all(targets.map(async t => [t.id, await api.runtime(t.id)] as const))
  instances.value = Object.fromEntries(states)
}
async function load() {
  tarController?.abort(); tarTarget.value = null; tarProgress.value = null; tarStatus.value = 'done'; tarFile = null
  subtaskEditor.value = null
  loading.value = true; error.value = ''; editorTab.value = 'basic'
  if (route.path === '/admin/questions/new') {
    accept({ id: '', library: true, title: '新题目', description: '', status: 'DRAFT', starts_at: 0, ends_at: 253402300799000, version: 0, questions: [{ id: '', title: '新题目', description: '', category: 'WEB', tags: [], difficulty: difficulties.MEDIUM ? 'MEDIUM' : (difficultyLevels.value[0] ?? ''), environment: null, package_validated: false, subtasks: [] }] }); loading.value = false; return
  }
  try {
    accept((await api.set(props.questionId ?? String(route.params.id))).set)
    instances.value = {}
    if (!admin.value) await syncRuntimeInstances()
  } catch (e) { error.value = errorMessage(e) } finally { loading.value = false }
}
async function refresh() {
  if (attachmentUploading.value) return
  if (dirty.value || tarStatus.value === 'paused' || tarStatus.value === 'error') {
    try { await ElMessageBox.confirm('刷新将丢弃尚未保存的修改。上传分片将保留，重新选择同一文件可继续。', '刷新内容', { type: 'warning' }) } catch { return }
  }
  await load()
}
const saveProgress = ref<number | null>(null)
async function save() {
  if (!set.value || busy.value || attachmentUploading.value) return
  busy.value = true
  saveProgress.value = 0
  try { if (library.value) set.value.title = set.value.questions[0]?.title || set.value.title; accept((await (editing.value ? api.saveSet(set.value, (loaded, total) => { saveProgress.value = Math.min(100, Math.floor(loaded / total * 100)) }) : api.saveCompetitionInfo(set.value))).set); ElMessage.success('内容已保存'); emit('saved'); if (library.value && route.params.id === 'new') await router.replace(`/admin/questions/${set.value.id}`) } catch (e) { ElMessage.error(errorMessage(e)) } finally { busy.value = false; saveProgress.value = null }
}
async function transition(status: 'PUBLISHED' | 'CLOSED') {
  if (!set.value || busy.value || attachmentUploading.value) return
  if (dirty.value) { ElMessage.warning('请先保存修改，再进行发布。'); return }
  if (status === 'PUBLISHED' && pendingRuntime.value.length) { ElMessage.warning('请先对所有容器题执行运行验收。'); return }
  try { await ElMessageBox.confirm(status === 'PUBLISHED' ? '发布后将固定题目、分值和 Flag，所有选手均可查看并在开放时间内提交。' : '下架后其他用户无法查看或提交，已有成绩保留。', status === 'PUBLISHED' ? '发布竞赛' : '下架竞赛', { type: 'warning' }) } catch { return }
  busy.value = true
  try { accept((await api.setStatus(set.value.id, status, set.value.version)).set); ElMessage.success('状态已更新') } catch (e) { ElMessage.error(errorMessage(e)) } finally { busy.value = false }
}
async function remove() {
  if (!set.value || busy.value || attachmentUploading.value) return
  try { await ElMessageBox.confirm(library.value ? '删除该题目及其小题和环境包？已加入竞赛的副本不受影响。' : '删除该草稿及其全部题目和环境包？', library.value ? '删除题目' : '删除竞赛', { type: 'warning' }) } catch { return }
  busy.value = true
  try { await api.deleteSet(set.value.id, set.value.version); original.value = copyEditorValue(set.value); if (props.embedded) emit('deleted'); else await router.push(library.value ? '/admin/questions' : '/admin/sets') } catch (e) { ElMessage.error(errorMessage(e)) } finally { busy.value = false }
}

const pickerOpen = ref(false)
const candidates = ref<{ question: Challenge; competition: string }[]>([])
const selected = ref<string[]>([])
const pickerLoading = ref(false)
const pickerError = ref('')
async function chooseQuestions() {
  activeTab.value = 'questions'
  pickerOpen.value = true; pickerLoading.value = true; pickerError.value = ''; selected.value = []
  try {
    const lists = (await api.questions()).items.filter(item => item.id !== set.value?.id)
    const details = await Promise.all(lists.map(item => api.set(item.id)))
    candidates.value = details.flatMap(item => item.set.questions.map(question => ({ question, competition: item.set.title })))
  } catch (e) { pickerError.value = errorMessage(e) }
  finally { pickerLoading.value = false }
}
function importQuestions() {
  if (!set.value) return
  const chosen = candidates.value.filter(item => selected.value.includes(item.question.id))
  if (set.value.questions.length + chosen.length > 50) { ElMessage.warning('每场竞赛最多 50 道题目'); return }
  for (const item of chosen) {
    const q: Challenge = copyEditorValue(item.question)
    q.source_id = q.id; q.id = ''; q.runtime_ready = false
    q.subtasks.forEach(t => { t.id = ''; t.flag = ''; t.solved = false; t.attempts = 0 })
    set.value.questions.push(q)
  }
  pickerOpen.value = false
  ElMessage.success('题目已加入，请保存修改；容器题需重新运行验收')
}

const subtaskEditor = ref<{ question: Challenge; index: number; task: Subtask } | null>(null)
function addSubtask(q: Challenge) {
  subtaskEditor.value = { question: q, index: -1, task: { id: '', title: `小题 ${q.subtasks.length + 1}`, content: '', points: 100, service_key: '', service_keys: [], attachments: [], flag: '', flag_configured: false, solved: false, attempts: 0 } }
  testPanelOpen.value = false
}
function editSubtask(q: Challenge, index: number) {
  subtaskEditor.value = { question: q, index, task: q.subtasks[index]! }
  testPanelOpen.value = false
}
function applySubtask(task: Subtask) {
  const editor = subtaskEditor.value!
  if (editor.index < 0) editor.question.subtasks.push(task)
  else editor.question.subtasks[editor.index] = task
  subtaskEditor.value = null
}
function removeSubtask(q: Challenge, index: number) { q.subtasks.splice(index, 1) }
function move(index: number, step: number) {
  if (!set.value) return
  const items = set.value.questions
  const target = index + step
  if (target < 0 || target >= items.length) return
  ;[items[index], items[target]] = [items[target]!, items[index]!]
}
async function removeQuestion(index: number) {
  try { await ElMessageBox.confirm('移除此题及其全部小题？保存后生效。', '移除题目', { type: 'warning' }); set.value?.questions.splice(index, 1) } catch { /* canceled */ }
}
const tarProgress = ref<UploadProgress | null>(null)
const tarStatus = ref<'uploading' | 'paused' | 'error' | 'done'>('done')
const tarError = ref('')
const tarFileName = ref('')
const tarTarget = ref<Challenge | null>(null)
let tarFile: File | null = null
let tarController: AbortController | null = null
const tarPercent = computed(() => tarProgress.value ? Math.min(100, Math.floor(tarProgress.value.loaded / tarProgress.value.total * 100)) : 0)
const tarPhaseLabel = computed(() => {
  if (tarStatus.value === 'paused') return '已暂停'
  if (tarStatus.value === 'error') return '上传中断'
  return { preparing: '正在识别文件', checking: '正在核对已上传分片', uploading: '正在上传', validating: '上传完成，正在校验 TAR 和镜像摘要' }[tarProgress.value?.phase || 'preparing']
})
function pauseTar() { tarController?.abort() }
async function continueTar() {
  if (!tarFile || !tarTarget.value || busy.value) return
  const q = tarTarget.value
  const controller = new AbortController()
  tarController = controller; tarStatus.value = 'uploading'; tarError.value = ''; busy.value = true
  try {
    let staged = await uploadPackageResumable(tarFile, String(auth.user?.id), controller.signal, value => { tarProgress.value = value })
    for (const image of staged.environment.images) image.source_file ||= tarFile.name
    if (q.environment) staged = await request<StagedPackage>('/admin/packages/merge', {
      method: 'POST', signal: controller.signal,
      body: JSON.stringify({ base_token: q.package_token || '', question_id: q.id, package_token: staged.package_token, environment: q.environment }),
    })
    q.environment = staged.environment
    q.package_token = staged.package_token
    q.package_sha256 = staged.package_sha256
    q.package_validated = true
    q.runtime_ready = false
    q.has_environment = true
    tarStatus.value = 'done'; tarFile = null; tarProgress.value = null
    ElMessage.success(`环境包校验通过，当前共 ${q.environment.services.length} 个服务，请保存修改。`)
  } catch (e) {
    tarStatus.value = controller.signal.aborted ? 'paused' : 'error'
    if (!controller.signal.aborted) tarError.value = errorMessage(e)
  } finally { busy.value = false; tarController = null }
}
// Validate after selection: native macOS pickers can disable .tar.gz files
// when an accept filter contains compound extensions.
async function upload(event: Event, q: Challenge) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]; input.value = ''
  if (!file || !set.value || busy.value) return
  if (!/\.(tar|tar\.gz|tgz)$/i.test(file.name) || !file.size) { ElMessage.warning('请选择非空的 TAR、TAR.GZ 或 TGZ 包。'); return }
  tarTarget.value = q; tarFile = file; tarFileName.value = file.name
  await continueTar()
}
onUnmounted(() => tarController?.abort())
async function downloadAttachment(a: Attachment) {
  if (a.file_id) {
    const link = document.createElement('a')
    link.href = `/api/v1/attachments/${encodeURIComponent(a.file_id)}`
    link.download = a.name
    link.click()
    return
  }
  let blob: Blob
  try { blob = await legacyAttachmentBlob(a.data) }
  catch { ElMessage.error('附件数据损坏，请刷新页面后重试'); return }
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a'); link.href = url; link.download = a.name; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000)
}
function removeService(q: Challenge, index: number) {
  if (!q.environment) return
  if (q.environment.services.length === 1) { clearEnvironment(q); return }
  const key = q.environment.services[index]!.key
  q.environment.services.splice(index, 1)
  for (const service of q.environment.services) service.depends_on = (service.depends_on || []).filter(dep => dep !== key)
  for (const task of q.subtasks) task.service_keys = (task.service_keys || []).filter(value => value !== key)
  q.runtime_ready = false
}
function clearEnvironment(q: Challenge) { q.subtasks.forEach(t => { t.service_keys = [] }); q.environment = null; q.package_validated = false; q.runtime_ready = false; q.has_environment = false; delete q.package_sha256; delete q.package_token }
function hasTestResult(q: Challenge) {
  return !!q.runtime_ready || (verification.value?.question === q.id && verification.value.state !== 'running')
}
function testAction(q: Challenge) {
  if (testPanelOpen.value) { testPanelOpen.value = false; return }
  testPanelOpen.value = true
  if (!hasTestResult(q) && verification.value?.state !== 'running') void verifyRuntime(q)
}
function testItems(q: Challenge) {
  if (verification.value?.question === q.id) return verification.value.items
  const items = verificationChecklist(q.environment!)
  if (q.runtime_ready) finishChecklist(items, true)
  return items
}
async function verifyRuntime(q: Challenge) {
  if (!set.value || !q.environment || busy.value) return
  testPanelOpen.value = true
  const questionIndex = set.value.questions.indexOf(q)
  const newQuestion = !set.value.id
  busy.value = true; runtimeBusy.value = q.id; error.value = ''
  verification.value = { question: q.id, state: 'running', started: Date.now(), items: verificationChecklist(q.environment), messages: [] }
  try {
    if (dirty.value || !q.id) {
      verification.value.messages.push({ time: new Date().toLocaleTimeString(), text: '正在保存修改，保存成功后开始验收' })
      if (library.value) set.value.title = set.value.questions[0]?.title || set.value.title
      accept((await api.saveSet(set.value)).set)
      q = set.value!.questions[questionIndex]!
      verification.value.question = q.id
    }
    await api.verifyRuntime(set.value.id, q.id, set.value.version, event => {
      verification.value!.messages.push({ time: new Date().toLocaleTimeString(), text: event.message })
      updateChecklist(verification.value!.items, event.message)
    })
    verification.value.state = 'success'
    finishChecklist(verification.value.items, true)
    ElMessage.success('运行验收通过，测试资源已清理。')
  }
  catch (e) {
    error.value = errorMessage(e)
    verification.value!.state = 'error'
    finishChecklist(verification.value!.items, false, e instanceof VerificationConnectionError)
    if (verification.value!.messages.at(-1)?.text !== error.value) verification.value!.messages.push({ time: new Date().toLocaleTimeString(), text: error.value })
  }
  finally { busy.value = false; runtimeBusy.value = '' }
  if (newQuestion && set.value?.id) await router.replace(`/admin/questions/${set.value.id}`)
  else if (!dirty.value && set.value?.id) void loadAfterFailedVerification()
}
async function loadAfterFailedVerification() {
  try { if (set.value) accept((await api.set(set.value.id)).set) } catch { /* keep the original verification error */ }
}
async function changeRuntime(q: Subtask, start: boolean) {
  if (busy.value) return
  busy.value = true; runtimeBusy.value = q.id; error.value = ''
  try {
    if (start) {
      startup.value = { id: q.id, stage: 0, elapsed: 0, message: '正在检查运行条件', state: 'running' }
      const startedAt = Date.now()
      startupTimer = setInterval(() => { if (startup.value) startup.value.elapsed = Math.floor((Date.now() - startedAt) / 1000) }, 1000)
      instances.value[q.id] = await api.startRuntime(q.id, event => {
        if (!startup.value || event.type !== 'progress') return
        const stage = startupStage(event.message)
        if (stage !== undefined) startup.value.stage = Math.max(startup.value.stage, stage)
        startup.value.message = event.message.startsWith('等待系统') ? event.message : startupStages[startup.value.stage]!
      })
      startup.value.stage = 5; startup.value.state = 'success'; startup.value.message = '系统已就绪，可以访问'
    }
    else { await api.stopRuntime(q.id); instances.value[q.id] = { state: 'STOPPED', endpoints: [] }; if (startup.value?.id === q.id) startup.value = null }
    await syncRuntimeInstances()
  } catch (e) {
    error.value = errorMessage(e)
    if (start && startup.value) { startup.value.state = 'error'; startup.value.message = error.value }
  }
  finally { clearInterval(startupTimer); busy.value = false; runtimeBusy.value = '' }
}
async function submit(id: string) {
  if (busy.value || !flags.value[id]) return
  busy.value = true
  try {
    const result = await api.submitFlag(id, flags.value[id]!)
    if (!result.correct) ElMessage.warning('答案不正确')
    else { ElMessage.success(result.first_solve ? `解题成功，获得 ${result.awarded_points} 分` : '该小题已经解出，不重复计分'); flags.value[id] = '' }
    await load()
  } catch (e) { ElMessage.error(errorMessage(e)) } finally { busy.value = false }
}
async function loadResults() {
  if (!set.value) return
  error.value = ''
  try {
    if (activeTab.value === 'leaderboard') ranks.value = (await api.leaderboard(set.value.id)).items
    if (activeTab.value === 'history') history.value = (await api.submissions(set.value.id)).items
  } catch (e) { error.value = errorMessage(e) }
}
async function confirmLeave() {
  if (busy.value || runtimeBusy.value) { ElMessage.warning('操作进行中，请稍后再关闭'); return false }
  if (!dirty.value && !attachmentUploading.value && tarStatus.value !== 'uploading' && tarStatus.value !== 'paused' && tarStatus.value !== 'error') return true
  try { await ElMessageBox.confirm('离开将丢弃尚未保存的修改并暂停上传。已接收的分片会保留，重新选择同一文件可继续。', '离开页面', { type: 'warning' }); return true } catch { return false }
}
defineExpose({ confirmLeave })
onBeforeRouteLeave(confirmLeave)
onBeforeRouteUpdate(confirmLeave)
watch(() => route.params.id, () => { activeTab.value = 'competition'; ranks.value = []; history.value = []; flags.value = {}; void load() })
function beforeUnload(e: BeforeUnloadEvent) { if (dirty.value || attachmentUploading.value || tarStatus.value === 'uploading') { e.preventDefault(); e.returnValue = '' } }
window.addEventListener('beforeunload', beforeUnload)
onUnmounted(() => window.removeEventListener('beforeunload', beforeUnload))
onMounted(async () => { try { await loadDifficulties() } catch (e) { ElMessage.error(errorMessage(e)) } if (admin.value) { try { const [taxonomy, knowledge] = await Promise.all([api.questionTypes(), api.knowledgePoints()]); taxonomyTypes.value = taxonomy.items; knowledgePoints.value = knowledge.items } catch (e) { ElMessage.error(errorMessage(e)) } } await load(); if (editing.value && route.query.choose === '1') await chooseQuestions() })
</script>

<template>
  <section v-loading="loading" :aria-busy="loading" class="set-detail">
    <nav v-if="!embedded" class="detail-navigation" aria-label="返回导航">
      <router-link :to="library ? '/admin/questions' : admin ? '/admin/sets' : '/player'" class="detail-back"><span class="detail-back-arrow" aria-hidden="true">←</span>返回{{ library ? '题目管理' : '竞赛列表' }}</router-link>
    </nav>
    <div class="page-heading">
      <div class="detail-heading-main">
        <div class="detail-title-row"><h1>{{ set?.title || '竞赛详情' }}</h1><span v-if="set" class="detail-status" :class="{ 'is-live': accepting }">{{ windowLabel }}</span></div>
        <div v-if="set" class="detail-metrics"><span><strong>{{ total }}</strong>总分</span><span v-if="!library"><strong>{{ set.questions.length }}</strong>道题目</span><span v-if="!admin"><strong>{{ solved }}</strong>已解小题</span><span v-if="dirty">有未保存修改</span></div>
      </div>
      <div class="button-row wrapping">
        <el-button :disabled="busy || attachmentUploading" @click="refresh">刷新</el-button>
        <el-button v-if="admin && !library && !editing" type="primary" :disabled="busy || attachmentUploading" @click="save">保存修改</el-button>
        <template v-if="editing"><el-button type="primary" :disabled="busy || attachmentUploading" @click="save">保存修改</el-button><el-button v-if="set?.status === 'DRAFT'" type="danger" plain :disabled="busy || attachmentUploading" @click="remove">{{ library ? '删除题目' : '删除草稿' }}</el-button></template>
        <el-button v-if="admin && !library && set?.status !== 'PUBLISHED'" type="primary" :disabled="busy || attachmentUploading || dirty || pendingRuntime.length > 0" @click="transition('PUBLISHED')">发布</el-button>
        <el-button v-if="admin && !library && set?.status === 'PUBLISHED'" type="warning" :disabled="busy || attachmentUploading || dirty" @click="transition('CLOSED')">下架</el-button>
      </div>
    </div>
    <div v-if="saveProgress !== null" class="tar-upload-progress" role="status" aria-live="polite">
      <strong>{{ saveProgress === 100 ? '数据已发送，正在保存…' : '正在上传附件和题目内容…' }}</strong>
      <el-progress :percentage="saveProgress" />
      <p class="muted">保存完成前请勿关闭页面；失败后可保留当前内容并重试。</p>
    </div>
    <SubtaskEditor v-if="subtaskEditor" :question="subtaskEditor.question" :task="subtaskEditor.task" :creating="subtaskEditor.index < 0" :busy="busy" @apply="applySubtask" @close="subtaskEditor = null" @download="downloadAttachment" />
    <ContainerServiceEditor v-if="serviceEditor?.question.environment" :environment="serviceEditor.question.environment" :service-index="serviceEditor.index" @apply="applyServiceEdit" @close="serviceEditor = null" />
    <el-alert v-if="error" :title="error" type="error" :closable="false" class="form-alert" />
    <el-alert v-if="editing && !library && pendingRuntime.length" title="容器题需要运行验收后才能发布" :description="`待验收：${pendingRuntime.map(q => q.title).join('、')}。请先保存修改，再到「选择题目」点击「验收」。验收最长约 90 秒，失败原因会显示在此处。`" type="warning" :closable="false" show-icon class="form-alert" />
    <template v-if="set">

      <el-dialog v-model="pickerOpen" title="选择题目" width="720px" class="user-dialog">
        <p class="muted">从题目管理复制题目、分值、Flag 和环境配置，保存后独立管理。</p>
        <el-alert v-if="pickerError" :title="pickerError" type="error" :closable="false" />
        <div class="taxonomy-filters form-alert"><el-input v-model="pickerSearch" clearable placeholder="搜索题目或知识点" aria-label="搜索题库" /><el-tree-select v-model="pickerCategory" :data="taxonomyTree" node-key="name" :props="{ label: 'name', value: 'name' }" check-strictly clearable filterable placeholder="全部题型（含下级）" /><el-tree-select v-model="pickerTag" :data="knowledgeTree" node-key="name" :props="{ label: 'name', value: 'name' }" check-strictly clearable filterable placeholder="全部知识点（含下级）" /></div>
        <div v-loading="pickerLoading"><el-checkbox-group v-model="selected"><div v-for="item in filteredCandidates" :key="item.question.id" class="question-choice"><el-checkbox :value="item.question.id">{{ item.question.title }}</el-checkbox><p class="muted">{{ item.competition }} · {{ item.question.category }} · {{ item.question.subtasks.length }} 个小题</p><div class="taxonomy-tags"><el-tag v-for="tag in item.question.tags" :key="tag" size="small" type="info">{{ tag }}</el-tag></div></div></el-checkbox-group><el-empty v-if="!pickerLoading && !filteredCandidates.length && !pickerError" :description="candidates.length ? '没有匹配的题目，请调整筛选条件' : '题库暂无题目，请先前往题目管理创建'" /></div>
        <template #footer><el-button @click="pickerOpen = false">取消</el-button><el-button type="primary" :disabled="pickerLoading || !selected.length" @click="importQuestions">加入 {{ selected.length }} 道题目</el-button></template>
      </el-dialog>
      <el-tabs v-if="libraryEditing" v-model="editorTab" class="question-editor-tabs">
        <el-tab-pane label="基础信息" name="basic" />
        <el-tab-pane label="题目信息" name="details" />
        <el-tab-pane label="解题文档" name="solution" />
      </el-tabs>
      <el-tabs v-model="activeTab" class="detail-tabs" :class="{ 'question-editor-content': libraryEditing }" @tab-change="loadResults">
        <el-tab-pane v-if="!library" label="竞赛信息" name="competition">
      <el-card v-if="admin && !library" shadow="never" class="form-alert">
        <el-form label-position="top" :disabled="busy">
          <el-form-item label="竞赛名称"><el-input v-model="set.title" maxlength="100" /></el-form-item>
          <el-form-item label="介绍"><el-input v-model="set.description" type="textarea" :rows="3" maxlength="5000" /></el-form-item>
          <div class="two-columns"><el-form-item label="开放时间"><el-date-picker v-model="set.starts_at" type="datetime" :clearable="false" value-format="x" @change="set.starts_at = Number(set.starts_at)" /></el-form-item><el-form-item label="结束时间"><el-date-picker v-model="set.ends_at" type="datetime" :clearable="false" value-format="x" @change="set.ends_at = Number(set.ends_at)" /></el-form-item></div>
        </el-form>

      </el-card>
      <div v-else-if="!library" class="competition-overview">
        <section v-if="set.description" class="competition-introduction"><h2>竞赛介绍</h2><p class="description-text">{{ set.description }}</p></section>
        <section class="competition-schedule" aria-label="竞赛开放时间">
          <div class="schedule-range">
            <div class="schedule-endpoint"><span>开始时间</span><time :datetime="new Date(set.starts_at).toISOString()">{{ competitionDate(set.starts_at) }}<small>{{ competitionTime(set.starts_at) }}</small></time></div>
            <span class="schedule-connector" aria-hidden="true">→</span>
            <div class="schedule-endpoint"><span>结束时间</span><time :datetime="new Date(set.ends_at).toISOString()">{{ competitionDate(set.ends_at) }}<small>{{ competitionTime(set.ends_at) }}</small></time></div>
          </div>
        </section>
      </div>
        </el-tab-pane>
        <el-tab-pane :label="!library && admin ? '选择题目' : '题目信息'" name="questions">
      <div v-if="editing && !library" class="button-row wrapping section-gap"><el-button type="primary" :disabled="busy" @click="chooseQuestions">从题库选择题目</el-button><el-button @click="router.push('/admin/questions')">管理题库</el-button><span class="muted">已选 {{ set.questions.length }} / 50 题</span></div>
          <el-table v-if="!library && admin" :data="set.questions" empty-text="暂无题目" class="section-gap">
            <el-table-column type="index" label="序号" width="70" />
            <el-table-column prop="title" label="题目名称" min-width="180" />
            <el-table-column prop="category" label="题型" width="100" />
            <el-table-column label="小题" width="80"><template #default="scope">{{ scope.row.subtasks.length }}</template></el-table-column>
            <el-table-column label="分值" width="100"><template #default="scope">{{ scope.row.subtasks.reduce((sum: number, t: Subtask) => sum + t.points, 0) }}</template></el-table-column>
            <el-table-column label="环境" min-width="160"><template #default="scope">{{ !scope.row.environment ? '无' : scope.row.runtime_ready ? '验收通过' : '待验收' }}</template></el-table-column>
            <el-table-column v-if="editing" label="操作" width="280"><template #default="scope"><div class="subtask-row-actions"><el-button text :disabled="busy || scope.$index === 0" @click="move(scope.$index, -1)">上移</el-button><el-button text :disabled="busy || scope.$index === set.questions.length - 1" @click="move(scope.$index, 1)">下移</el-button><el-button v-if="scope.row.environment" text type="primary" :disabled="busy || !scope.row.package_validated" @click="verifyRuntime(scope.row)">验收</el-button><el-button text type="danger" :disabled="busy" @click="removeQuestion(scope.$index)">移除</el-button></div></template></el-table-column>
          </el-table>
          <div v-if="!library && admin && verification" class="verification-progress" role="status"><strong>{{ { running: '正在验收', success: '验收通过', error: '验收失败' }[verification.state] }}</strong><ol><li v-for="(message, i) in verification.messages" :key="i">{{ message.time }} {{ message.text }}</li></ol></div>
          <template v-if="library || !admin">

          <div :class="{ 'question-workspace': !admin }">
          <aside v-if="!admin" class="question-navigator" aria-label="题目目录">
            <div class="navigator-heading"><strong>题目目录</strong><span>{{ questionEntries.length }} 道大题</span></div>
            <el-input v-model="questionSearch" clearable placeholder="搜索大题或小题关键词" aria-label="搜索题目" />
            <div class="navigator-filters">
              <el-select v-model="questionCategory" placeholder="全部题型" clearable aria-label="题目题型"><el-option v-for="category in questionCategories" :key="category" :label="category" :value="category" /></el-select>
              <el-select v-model="questionStatus" aria-label="解题状态"><el-option label="全部状态" value="all" /><el-option label="未完成" value="unsolved" /><el-option label="已完成" value="solved" /></el-select>
            </div>
            <div class="navigator-count">共 {{ filteredQuestions.length }} 道大题</div>
            <nav class="navigator-list" aria-label="选择题目">
              <button v-for="entry in filteredQuestions" :key="entry.key" type="button" class="navigator-item" :class="{ active: selectedQuestion === entry.key }" :aria-current="selectedQuestion === entry.key ? 'true' : undefined" @click="selectQuestion(entry.key)">
                <span class="navigator-number">{{ String(entry.number).padStart(2, '0') }}</span>
                <span class="navigator-label"><strong>{{ entry.question.title }}</strong><span class="navigator-meta">{{ entry.question.category }} · {{ entry.points }} 分 · {{ entry.question.subtasks.length }} 小题</span><small :class="{ 'is-complete': entry.complete }">{{ entry.complete ? '已完成' : '解题进度' }} {{ entry.solved }}/{{ entry.question.subtasks.length }}</small></span>
              </button>
              <div v-if="!filteredQuestions.length" class="navigator-empty"><p>没有匹配的题目</p><el-button text type="primary" @click="resetQuestionFilters">清除筛选</el-button></div>
            </nav>
          </aside>
          <div ref="questionDetail" class="question-detail" :class="{ 'participant-detail': !admin }">
          <div v-if="!admin && currentQuestion" class="question-detail-toolbar"><span>第 {{ currentQuestion.number }} 道大题 · 已解出 {{ currentQuestion.solved }}/{{ currentQuestion.question.subtasks.length }} 小题</span><div><el-button size="small" :disabled="currentQuestionIndex <= 0" @click="navigateQuestion(-1)">上一大题</el-button><el-button size="small" :disabled="currentQuestionIndex < 0 || currentQuestionIndex >= filteredQuestions.length - 1" @click="navigateQuestion(1)">下一大题</el-button></div></div>
          <el-card v-for="(q, index) in displayedQuestions" :key="q.id || `new-${index}`" shadow="never" class="question-card">
            <template v-if="!libraryEditing" #header><div class="card-heading"><h2>{{ q.title }}</h2><div v-if="editing && !library" class="button-row"><el-button :disabled="busy || index === 0" size="small" @click="move(index, -1)">上移</el-button><el-button :disabled="busy || index === set.questions.length - 1" size="small" @click="move(index, 1)">下移</el-button><el-button type="danger" plain size="small" :disabled="busy" @click="removeQuestion(index)">移除</el-button></div><el-tag v-else>{{ q.category }} · {{ difficulties[q.difficulty] }}</el-tag></div></template>
            <div v-if="!libraryEditing && q.tags?.length" class="taxonomy-tags section-gap"><el-tag v-for="tag in q.tags" :key="tag" size="small" type="info">{{ tag }}</el-tag></div>
            <div :class="{ 'question-editor-layout': libraryEditing }">
            <div class="question-editor-main">
            <SolutionDocument v-if="admin && (!libraryEditing || editorTab === 'solution')" v-model="q.solution_document" :editable="editing" :disabled="busy" />
            <el-form v-if="editing && (!libraryEditing || editorTab === 'basic')" label-position="top" :disabled="busy">
              <el-form-item label="题目名称"><el-input v-model="q.title" maxlength="100" /></el-form-item>
              <div class="two-columns"><el-form-item label="题型"><el-tree-select v-model="q.category" :data="taxonomyTree" node-key="name" :props="{ label: 'name', value: 'name' }" check-strictly filterable placeholder="请选择题型" /></el-form-item><el-form-item label="难度"><el-select v-model="q.difficulty"><el-option v-for="level in difficultyLevels" :key="level" :label="difficulties[level]" :value="level" /></el-select></el-form-item></div>
              <el-form-item label="知识点"><KnowledgePointSelect v-model="q.tags" :points="knowledgePoints" :types="taxonomyTypes" :category="q.category" /></el-form-item>
              <el-form-item label="题目描述"><el-input v-model="q.description" type="textarea" :rows="3" maxlength="10000" /></el-form-item>
              <template v-if="libraryEditing">
                <el-form-item label="附件">
                  <AttachmentList :attachments="q.attachments" removable :disabled="busy" @download="downloadAttachment" @remove="q.attachments?.splice($event, 1)">
                    <template #action>
                      <AttachmentUploader :disabled="busy" @busy="attachmentUploading = $event" @added="(q.attachments ||= []).push($event)" />
                    </template>
                  </AttachmentList>

                </el-form-item>
                <el-form-item label="容器">
                  <div class="container-list">
                    <div class="container-list-header">
                      <div class="container-summary">
                        <span class="container-count">{{ q.environment ? `${q.environment.services.length} 个服务` : '未配置' }}</span>
                        <span v-if="q.environment" class="container-status">{{ q.runtime_ready ? '运行验收通过' : q.package_validated ? '包校验通过 · 待运行验收' : '请上传环境包' }}</span>
                      </div>
                      <label class="attachment-upload" :class="{ disabled: busy }">{{ q.environment ? '追加' : '上传' }}<input type="file" aria-label="上传 TAR / TAR.GZ 包" :disabled="busy" @change="upload($event, q)" /></label>
                    </div>
<div v-if="tarTarget === q && tarProgress" class="tar-upload-progress" role="status" aria-live="polite">
                    <div class="card-heading"><strong>{{ tarPhaseLabel }}</strong><button v-if="tarStatus === 'uploading'" type="button" class="container-configure" :disabled="tarProgress.phase === 'validating'" @click="pauseTar">暂停</button><button v-else type="button" class="container-configure" :disabled="busy" @click="continueTar">继续上传</button></div>
                    <p>{{ tarFileName }}</p>
                    <el-progress :percentage="tarPercent" :status="tarStatus === 'error' ? 'exception' : undefined" />
                    <p>{{ formatUploadBytes(tarProgress.loaded) }} / {{ formatUploadBytes(tarProgress.total) }}<template v-if="tarStatus === 'uploading' && tarProgress.phase === 'uploading'"> · {{ formatUploadBytes(tarProgress.speed) }}/s · 预计剩余 {{ formatUploadTime(tarProgress.remaining) }}</template></p>
                    <p v-if="tarError" class="tar-upload-error">{{ tarError }}</p>
                    <p class="muted">支持断点续传；刷新或网络中断后，重新选择同一文件即可继续。</p>
                  </div>
                    <template v-if="q.environment">
                      <div class="container-services-scroll">
                        <table class="container-services" aria-label="容器服务列表">
                          <thead><tr><th scope="col">系统名称</th><th scope="col">上传文件</th><th scope="col">版本</th><th scope="col">架构</th><th scope="col">CPU</th><th scope="col">内存</th><th scope="col">端口</th><th scope="col">依赖</th><th scope="col">操作</th></tr></thead>
                          <tbody>
                            <tr v-for="(service, serviceIndex) in q.environment.services" :key="service.key">
                              <th scope="row" class="container-service-name">{{ service.name || service.key }}</th>
                              <td>{{ q.environment.images.find(image => image.key === service.image)?.source_file || '原上传文件名未记录' }}</td>
                              <td>{{ q.environment.version }}</td>
                              <td>{{ q.environment.architecture }}</td>
                              <td>{{ service.resource_limits_enabled && service.cpu ? `${service.cpu} CPU` : '不限' }}</td>
                              <td>{{ service.resource_limits_enabled && service.memory_mb ? memoryGB(service.memory_mb) : '不限' }}</td>
                              <td>{{ service.ports?.map(p => `${p.port}/${p.protocol}${p.public ? '（入口）' : '（内部）'}${p.label ? ` ${p.label}` : ''}`).join('、') || '无端口' }}</td>
                              <td>{{ service.depends_on?.join('、') || '—' }}</td>
                              <td><div class="container-service-actions"><button type="button" class="container-configure" :disabled="busy" @click="serviceEditor = { question: q, index: serviceIndex }">编辑</button><button type="button" class="container-configure" :disabled="!q.package_validated" :aria-expanded="testPanelOpen" @click="testAction(q)">测试</button><el-button type="danger" text :disabled="busy" @click="removeService(q, serviceIndex)">移除</el-button></div></td>
                            </tr>
                          </tbody>
                        </table>
                      </div>
                    </template>

                  </div>
                </el-form-item>
              </template>
            </el-form>
            <section v-else-if="!editing" class="question-overview" aria-label="大题信息">
              <nav v-if="!admin && q.subtasks.length" class="subtask-shortcuts" aria-label="本题小题定位"><span>本题小题</span><button v-for="(task, taskIndex) in q.subtasks" :key="task.id || taskIndex" type="button" :class="{ solved: task.solved }" @click="locateSubtask(taskIndex)">{{ taskIndex + 1 }}. {{ task.title }}<span v-if="task.solved"> ✓</span></button></nav>
              <p v-if="q.description" class="description-text">{{ q.description }}</p>
              <AttachmentList v-if="q.attachments?.length" :attachments="q.attachments" @download="downloadAttachment" />
            </section>
            <div v-if="editing && (!libraryEditing || editorTab === 'details')">
              <div class="card-heading"><h3>题目信息</h3><el-button type="primary" :disabled="busy || q.subtasks.length >= 50" @click="addSubtask(q)">添加题目</el-button></div>
              <el-table :data="q.subtasks" empty-text="暂无题目，请点击添加题目" class="section-gap">
                <el-table-column type="index" label="序号" width="70" />
                <el-table-column prop="title" label="题目名称" min-width="160" />
                <el-table-column prop="points" label="分值" width="90" />
                <el-table-column label="附件" width="90"><template #default="scope">{{ scope.row.attachments?.length || 0 }} 个</template></el-table-column>
                <el-table-column label="Docker" min-width="150"><template #default="scope">{{ scope.row.service_keys?.join('、') || '未绑定' }}</template></el-table-column>
                <el-table-column label="操作" width="170"><template #default="scope"><div class="subtask-row-actions"><el-button text type="primary" :disabled="busy" @click="editSubtask(q, scope.$index)">编辑</el-button><el-button text type="danger" :disabled="busy" @click="removeSubtask(q, scope.$index)">移除</el-button></div></template></el-table-column>
              </el-table>
            </div>
            <template v-if="!editing"><div v-for="(t, ti) in q.subtasks" :key="t.id || `new-${ti}`" class="subtask-box" v-show="!libraryEditing || editorTab === 'details'">
              <div class="card-heading"><h3>{{ t.title }}</h3><el-tag :type="t.solved ? 'success' : 'info'">{{ t.solved ? '已解出' : `${t.points} 分` }}</el-tag><el-button v-if="editing" type="danger" text :disabled="busy" @click="removeSubtask(q, ti)">移除小题</el-button></div>
              <div><p class="description-text">{{ t.content }}</p><AttachmentList v-if="t.attachments?.length" :attachments="t.attachments" @download="downloadAttachment" /><p v-if="admin && t.service_keys?.length" class="muted">绑定 Docker：{{ t.service_keys.join('、') }}</p><div v-if="!admin" class="flag-row"><el-input v-model="flags[t.id]" :aria-label="`${t.title} 的答案`" placeholder="提交答案" :maxlength="512" :disabled="busy || !accepting || t.solved" @keyup.enter="submit(t.id)" /><el-button type="primary" :disabled="busy || !accepting || t.solved || !flags[t.id]" @click="submit(t.id)">{{ t.solved ? '已解出' : '提交' }}</el-button></div>            <div v-if="!admin && q.has_environment && t.service_keys?.length" class="subtask-environment">
              <div v-if="runtimeRunning(t.id)" class="subtask-environment-timer" role="timer" :aria-label="`环境剩余时间 ${runtimeTime(t.id)}`">
                <div class="subtask-environment-summary">
                  <span>剩余 {{ runtimeTime(t.id) }}</span>
                  <code v-for="endpoint in (instances[t.id]?.endpoints || []).filter(endpoint => t.service_keys?.includes(endpoint.service))" :key="`${endpoint.service}-${endpoint.port}-${endpoint.protocol}`">{{ endpoint.address }}</code>
                </div>
                <el-progress :percentage="runtimePercent(t.id)" :show-text="false" :stroke-width="4" :color="runtimeRemaining(t.id) <= 300000 ? '#e6a23c' : '#226953'" />
              </div>
              <div class="subtask-environment-controls button-row runtime-toolbar">
                <el-button type="primary" :disabled="busy || !accepting || runtimeRunning(t.id)" :loading="runtimeBusy === t.id" @click="changeRuntime(t, true)">启动</el-button>
                <el-button :disabled="busy || !instances[t.id]?.id" @click="changeRuntime(t, false)">停止</el-button>
                <div v-if="startup?.id === t.id" class="runtime-startup-progress" :class="`is-${startup.state}`" role="status" aria-live="polite">
                  <div class="runtime-startup-line">
                    <span class="runtime-startup-label">{{ startup.state === 'success' ? '已就绪' : startup.state === 'error' ? '启动未完成' : startupStages[startup.stage] }}</span>
                    <span class="runtime-startup-time">{{ startup.state === 'running' ? `阶段 ${startup.stage + 1}/${startupStages.length} · 已等待 ${startup.elapsed} 秒` : `耗时 ${startup.elapsed} 秒` }}</span>
                  </div>
                  <el-progress v-if="startup.state === 'running'" :percentage="Math.round(startup.stage / (startupStages.length - 1) * 100)" :show-text="false" :stroke-width="3" color="#226953" />
                  <p v-if="startup.state === 'error'" class="runtime-startup-error">{{ startup.message }}</p>
                </div>
              </div>
            </div>
</div>
            </div>
            </template>
            <div v-if="admin && !libraryEditing" class="question-inline-settings">
            <div v-if="editing || q.environment" class="editor-detail-section">
              <h3>多容器环境</h3>
              <div class="editor-detail-content">
              <el-tag :type="q.runtime_ready ? 'success' : 'info'">{{ q.runtime_ready ? '运行验收通过' : q.package_validated ? '包校验通过 · 待运行验收' : q.environment ? '配置已保存 · 请上传环境包' : '未配置' }}</el-tag>
              <p class="muted">运行验收将导入镜像，在独立网络试运行并检查健康状态和安全策略，完成后清理测试容器。公开端口直接映射至宿主机所有 IPv4 网卡，不创建网关；外网访问需配置公网地址和防火墙，知道地址即可连接。</p>
              <template v-if="q.environment"><p>版本 {{ q.environment.version }} · {{ q.environment.architecture }} · {{ q.environment.services.length }} 个服务</p><div v-for="service in q.environment.services" :key="service.key" class="service-row"><strong>{{ service.name || service.key }}</strong><span>CPU：{{ service.resource_limits_enabled && service.cpu ? `${service.cpu} 核` : '不限' }} · 内存：{{ service.resource_limits_enabled && service.memory_mb ? memoryGB(service.memory_mb) : '不限' }} · 进程数：{{ service.resource_limits_enabled && service.pids_limit ? service.pids_limit : '不限' }}</span><span>{{ service.ports?.map(p => `${p.port}/${p.protocol}${p.public ? '（入口）' : '（内部）'}`).join('、') || '无端口' }}</span><span v-if="service.depends_on?.length">依赖：{{ service.depends_on.join('、') }}</span></div></template>
              <div v-if="editing" class="button-row wrapping section-gap"><label class="upload-label" :class="{ disabled: busy }">上传 TAR / TAR.GZ 包<input type="file" :disabled="busy" @change="upload($event, q)" /></label><el-button v-if="q.environment" :disabled="busy" @click="clearEnvironment(q)">移除环境配置</el-button></div>
<div v-if="tarTarget === q && tarProgress" class="tar-upload-progress" role="status" aria-live="polite">
                    <div class="card-heading"><strong>{{ tarPhaseLabel }}</strong><button v-if="tarStatus === 'uploading'" type="button" class="container-configure" :disabled="tarProgress.phase === 'validating'" @click="pauseTar">暂停</button><button v-else type="button" class="container-configure" :disabled="busy" @click="continueTar">继续上传</button></div>
                    <p>{{ tarFileName }}</p>
                    <el-progress :percentage="tarPercent" :status="tarStatus === 'error' ? 'exception' : undefined" />
                    <p>{{ formatUploadBytes(tarProgress.loaded) }} / {{ formatUploadBytes(tarProgress.total) }}<template v-if="tarStatus === 'uploading' && tarProgress.phase === 'uploading'"> · {{ formatUploadBytes(tarProgress.speed) }}/s · 预计剩余 {{ formatUploadTime(tarProgress.remaining) }}</template></p>
                    <p v-if="tarError" class="tar-upload-error">{{ tarError }}</p>
                    <p class="muted">支持断点续传；刷新或网络中断后，重新选择同一文件即可继续。</p>
                  </div>
              <el-button v-if="editing && q.environment" class="section-gap" type="primary" :disabled="busy || !q.package_validated" :loading="runtimeBusy === q.id" @click="verifyRuntime(q)">运行验收</el-button>
              <div v-if="verification?.question === q.id" class="verification-progress" role="status" aria-live="polite">
                <strong>{{ verification.state === 'running' ? '正在运行验收' : verification.state === 'success' ? '验收通过' : '验收失败' }}</strong>
                <span v-if="verification.state === 'running'" class="muted"> · 已用 {{ Math.max(0, Math.floor((now - verification.started) / 1000)) }} 秒</span>
                <ol><li v-for="(item, index) in verification.messages" :key="index"><time>{{ item.time }}</time> {{ item.text }}</li></ol>
              </div>
              <p v-if="editing" class="table-note muted">支持 docker save 导出的单镜像 TAR，或 manifest.yaml + images/*.tar 环境包，也支持 gzip 压缩（.tar.gz / .tgz）；不限制包大小，支持断点续传。上传后请保存修改，再运行验收。</p>
              </div>
            </div>
            </div>
            </div>
            <Teleport v-if="libraryEditing && q.environment && testPanelOpen" :disabled="embedded" to="#verification-rail">
            <aside class="verification-sidebar" aria-label="运行验收检查清单">
              <div class="test-sidebar-close"><el-button text aria-label="关闭测试侧边栏" @click="testPanelOpen = false">关闭 ×</el-button></div>
              <ul class="verification-checklist" aria-live="polite">
                <li v-for="item in testItems(q)" :key="item.trigger" :data-state="item.state">
                  <span class="check-state">{{ { pending: '待测试', running: '测试中', passed: '通过', failed: '失败', skipped: '未执行', interrupted: '连接中断，结果待确认' }[item.state] }}</span>
                  <span>{{ item.label }}</span>
                </li>
              </ul>
              <div class="test-sidebar-footer">
                <p v-if="verification?.question === q.id && verification.state === 'error'" class="tar-upload-error">{{ verification.messages.at(-1)?.text }}</p>
                <div class="test-sidebar-actions">
                  <div class="test-result" role="status" aria-live="polite" :data-state="verification?.question === q.id ? verification.state : q.runtime_ready ? 'success' : 'pending'">{{ verification?.question === q.id ? ({ running: '测试中…', success: '测试通过', error: '测试失败' }[verification.state]) : q.runtime_ready ? '测试通过' : '待测试' }}</div>
                  <el-button :disabled="busy || !q.package_validated" @click="verifyRuntime(q)">重试</el-button>
                </div>
              </div>
            </aside>
            </Teleport>
            </div>
          </el-card>
          <el-empty v-if="!set.questions.length" description="暂无题目" />
          <el-empty v-else-if="!admin && !currentQuestion" description="请选择或搜索一道题目" />
          </div>
          </div>
          </template>

        </el-tab-pane>
        <el-tab-pane v-if="!library" label="排行榜" name="leaderboard"><p class="muted">按总分降序，同分按达成时间升序。显示前 500 名。</p><el-table :data="ranks" empty-text="暂无解题成绩"><el-table-column type="index" label="排名" width="80" /><el-table-column prop="display_name" label="选手" /><el-table-column prop="points" label="得分" /><el-table-column prop="solved" label="解题数" /><el-table-column label="最后解题时间"><template #default="scope">{{ new Date(scope.row.last_solve).toLocaleString() }}</template></el-table-column></el-table></el-tab-pane>
        <el-tab-pane v-if="!admin" label="我的提交" name="history"><p class="muted">最近 100 次提交，不保存 Flag 原文。</p><el-table :data="history" empty-text="暂无提交"><el-table-column prop="title" label="小题" /><el-table-column label="结果"><template #default="scope"><el-tag :type="scope.row.correct ? 'success' : 'danger'">{{ scope.row.correct ? '正确' : '错误' }}</el-tag></template></el-table-column><el-table-column label="提交时间"><template #default="scope">{{ new Date(scope.row.created_at).toLocaleString() }}</template></el-table-column></el-table></el-tab-pane>
      </el-tabs>
    </template>
  </section>
</template>

<style scoped>
.question-workspace { display: grid; grid-template-columns: 300px minmax(0, 1fr); gap: 22px; align-items: start; margin-top: 18px; }
.question-navigator { position: sticky; top: 20px; padding: 0 18px 0 0; }
.navigator-heading { display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; gap: 8px; }
.navigator-heading strong { font-size: 16px; color: #226953; }
.navigator-heading span, .navigator-count { color: #748279; font-size: 12px; }
.navigator-filters { display: flex; gap: 8px; margin-top: 10px; }
.navigator-filters > * { flex: 1; min-width: 0; }
.navigator-count { margin: 16px 0 8px; }
.navigator-list { display: flex; flex-direction: column; gap: 6px; max-height: calc(100vh - 300px); min-height: 120px; overflow-y: auto; }
.navigator-item { display: flex; gap: 10px; width: 100%; text-align: left; background: transparent; border: 1px solid transparent; border-radius: 8px; padding: 12px 10px; cursor: pointer; color: #34463d; font: inherit; }
.navigator-item:hover { background: #f4f7f5; }
.navigator-item.active { background: #edf5f0; border-color: #bed8ca; }
.navigator-item:focus-visible { outline: 2px solid #226953; outline-offset: -2px; }
.navigator-number { color: #7d8b82; font-size: 12px; padding-top: 2px; }
.navigator-label { display: flex; flex-direction: column; gap: 6px; min-width: 0; overflow-wrap: anywhere; }
.navigator-label strong { font-size: 14px; line-height: 1.5; }
.navigator-label small.is-complete { color: #226953; }
.navigator-label small { color: #7c8881; font-size: 12px; }
.navigator-meta { font-size: 12px; color: #78877d; }
.navigator-meta b { color: #226953; margin-left: 6px; font-weight: 500; }
.navigator-empty { text-align: center; color: #78877d; padding: 20px 0; }
.question-detail { min-width: 0; }
.participant-detail { max-height: calc(100dvh - 48px); overflow-y: auto; padding-right: 6px; scrollbar-gutter: stable; }
.participant-detail .subtask-box { scroll-margin-top: 16px; }
.subtask-shortcuts { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; padding-bottom: 16px; margin-bottom: 16px; border-bottom: 1px solid #e6ece8; }
.subtask-shortcuts > span { font-size: 12px; color: #78877d; margin-right: 4px; }
.subtask-shortcuts button { font: inherit; font-size: 12px; line-height: 1.5; color: #52685c; background: #f5f8f6; border: 1px solid #dfe6e2; padding: 5px 10px; border-radius: 6px; cursor: pointer; text-align: left; }
.subtask-shortcuts button:hover, .subtask-shortcuts button:focus-visible { color: #226953; border-color: #80ad97; }
.subtask-shortcuts button.solved { color: #226953; background: #edf5f0; }

.question-detail-toolbar { display: flex; justify-content: space-between; align-items: center; gap: 12px; margin-bottom: 12px; color: #748279; font-size: 13px; }
.participant-detail > .question-card { margin-top: 0; }
@media (max-width: 900px) { .question-workspace { grid-template-columns: 240px minmax(0, 1fr); gap: 14px; } .question-navigator { padding: 12px; } }
@media (max-width: 640px) { .question-workspace { grid-template-columns: minmax(0, 1fr); } .question-navigator { position: static; } .participant-detail { max-height: none; overflow: visible; padding-right: 0; } .navigator-list { max-height: 230px; } .question-detail-toolbar { flex-wrap: wrap; } }

.runtime-toolbar.subtask-environment-controls { align-self: stretch; align-items: center; flex-wrap: wrap; gap: 10px; }
.runtime-startup-progress { min-width: 220px; max-width: 480px; flex: 1; margin-left: 6px; font-size: 12px; line-height: 1.5; }
.runtime-startup-line { display: flex; align-items: baseline; justify-content: space-between; flex-wrap: wrap; gap: 4px 12px; }
.runtime-startup-label { color: #456456; font-weight: 500; }
.runtime-startup-time { color: #78877e; font-variant-numeric: tabular-nums; white-space: nowrap; }
.runtime-startup-progress :deep(.el-progress) { margin-top: 5px; }
.runtime-startup-progress.is-success { flex: 0 1 auto; min-width: 0; }
.is-success .runtime-startup-label { color: #226953; }
.is-success .runtime-startup-label::before { content: '✓'; margin-right: 5px; }
.is-error .runtime-startup-label, .runtime-startup-error { color: var(--el-color-danger); }
.runtime-startup-error { margin: 4px 0 0; overflow-wrap: anywhere; }
@media (max-width: 600px) {
  .runtime-startup-progress.is-running, .runtime-startup-progress.is-error { flex-basis: 100%; max-width: none; margin-left: 0; }
}
</style>
