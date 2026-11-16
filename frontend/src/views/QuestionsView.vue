<script setup lang="ts">
import { difficulties } from '../stores/difficulties'
import { computed, onMounted, ref } from 'vue'
import SetDetailView from './SetDetailView.vue'
import { useRouter } from 'vue-router'
import { api, errorMessage } from '../api'
import { questionTypeTree, typeDescendants, type QuestionTypeEntry, type QuestionTypeNode } from '../questionTypes'
import type { ChallengeSet } from '../types'
const router = useRouter()
const selectedQuestion = ref('')
const editorOpen = ref(false)
const questionEditor = ref<{ confirmLeave: () => Promise<boolean> }>()
function openQuestion(id: string) { selectedQuestion.value = id; editorOpen.value = true }
async function closeEditor(done: () => void) {
 if (await questionEditor.value?.confirmLeave() !== false) done()
}
async function questionDeleted() { editorOpen.value = false; await load() }
const items = ref<ChallengeSet[]>([])
const loading = ref(false)
const error = ref('')
const category = ref('all')
const knowledge = ref('')
const knowledgeTags = computed(() => [...new Set(items.value.flatMap(item => item.questions[0]?.tags || []))].sort((a, b) => a.localeCompare(b, 'zh-CN')))
function selectKnowledge(value: string) { knowledge.value = knowledge.value === value ? '' : value }
const types = ref<QuestionTypeEntry[]>([])
interface BrowserType { id: string; name: string; total_questions: number; children: BrowserType[] }
function browserNodes(nodes: QuestionTypeNode[]): BrowserType[] { return nodes.map(node => ({ id: `type:${node.name}`, name: node.name, total_questions: node.total_questions, children: browserNodes(node.children) })) }
const typeTree = computed(() => {
 const entries = types.value.map(type => ({ ...type, question_count: items.value.filter(i => i.questions[0]?.category === type.name).length }))
 const known = new Set(entries.map(i => i.name))
 for (const item of items.value) { const name = item.questions[0]?.category; if (name && !known.has(name)) { known.add(name); entries.push({ name, question_count: items.value.filter(i => i.questions[0]?.category === name).length, usage_count: 0 }) } }
 return [...browserNodes(questionTypeTree(entries)), { id: 'type:', name: '未分类', total_questions: items.value.filter(i => !i.questions[0]?.category).length, children: [] }]
})
const includedTypes = computed(() => typeDescendants(types.value, category.value === 'all' ? '' : category.value.slice(5)))
function selectType(node: { id: string }) { category.value = category.value === node.id ? 'all' : node.id }
const filtered = computed(() => items.value.filter(item =>
 (category.value === 'all' || includedTypes.value.has(item.questions[0]?.category || '')) &&
 (!knowledge.value || item.questions[0]?.tags?.includes(knowledge.value))
))
async function load() {
 loading.value = true; error.value = ''
 try { const [questions, taxonomy] = await Promise.all([api.questions(), api.questionTypes()]); items.value = questions.items; types.value = taxonomy.items; if (knowledge.value && !knowledgeTags.value.includes(knowledge.value)) knowledge.value = '';  if (category.value !== 'all' && category.value !== 'type:' && !types.value.some(t => t.name === category.value.slice(5))) category.value = 'all' } catch (e) { error.value = errorMessage(e) } finally { loading.value = false }
}
onMounted(load)
</script>
<template>
 <section>
  <el-alert v-if="error" :title="error" type="error" :closable="false" class="form-alert" />
  <div class="question-library-layout">
   <aside class="question-type-sidebar" aria-label="按题型浏览">
    <el-button class="all-questions-button" :class="{ 'is-active': category === 'all' }" :aria-pressed="category === 'all'" @click="category = 'all'"><span>全部题目</span><span class="question-type-count">{{ items.length }}</span></el-button>
    <el-tree :data="typeTree" :key="category" node-key="id" :props="{ label: 'name' }" default-expand-all :current-node-key="category" highlight-current :expand-on-click-node="false" @node-click="selectType">
     <template #default="{ data }"><span class="question-type-node"><span class="question-type-name"><span aria-hidden="true" class="question-type-folder">▱</span>{{ data.name || '未分类' }}</span><span class="question-type-count">{{ data.total_questions }}</span></span></template>
    </el-tree>
   </aside>
   <div class="question-library-main">
    <div class="question-library-toolbar">
     <div class="knowledge-filter" role="group" aria-label="按知识点筛选">
      <button type="button" class="knowledge-filter-tag" :class="{ 'is-active': !knowledge }" :aria-pressed="!knowledge" @click="knowledge = ''">全部</button>
      <button v-for="tag in knowledgeTags" :key="tag" type="button" class="knowledge-filter-tag" :class="{ 'is-active': knowledge === tag }" :aria-pressed="knowledge === tag" @click="selectKnowledge(tag)">{{ tag }}</button>
      <span v-if="!knowledgeTags.length" class="muted">暂无知识点标签</span>
     </div>
     <el-button type="primary" @click="router.push('/admin/questions/new')">创建题目</el-button>
    </div>
    <div v-loading="loading" class="question-cards" aria-label="题目列表" :aria-busy="loading">
     <article v-for="item in filtered" :key="item.id" class="library-question-card" tabindex="0" role="button" aria-haspopup="dialog" :aria-label="`编辑题目：${item.title}`" @click="openQuestion(item.id)" @keydown.enter.self.prevent="openQuestion(item.id)" @keydown.space.self.prevent="openQuestion(item.id)">
      <div class="question-card-meta"><button v-if="item.questions[0]?.category" type="button" class="knowledge-filter-tag" :aria-pressed="category === `type:${item.questions[0].category}`" @click.stop="selectType({ id: `type:${item.questions[0].category}` })" @keydown.stop>{{ item.questions[0].category }}</button><span v-else class="muted">未分类</span><span class="question-difficulty">{{ difficulties[item.questions[0]?.difficulty as keyof typeof difficulties] || '—' }}</span></div>
      <h2 class="question-card-title">{{ item.title }}</h2>
      <div class="question-card-knowledge">
       <div class="taxonomy-tags"><button v-for="value in item.questions[0]?.tags" :key="value" type="button" class="knowledge-filter-tag card-knowledge-tag" :class="{ 'is-active': knowledge === value }" :aria-pressed="knowledge === value" @click.stop="selectKnowledge(value)" @keydown.stop>{{ value }}</button><span v-if="!item.questions[0]?.tags?.length" class="muted">未设置</span></div>
      </div>
     </article>
     <el-empty v-if="!loading && !filtered.length" class="question-empty" :description="items.length ? '没有符合当前筛选条件的题目' : '暂无题目，点击创建题目开始'" />
    </div>
   </div>
  </div>
  <el-drawer v-model="editorOpen" title="编辑题目" direction="rtl" size="min(960px, 100vw)" destroy-on-close :before-close="closeEditor" class="question-editor-drawer">
   <SetDetailView v-if="editorOpen" :key="selectedQuestion" ref="questionEditor" :question-id="selectedQuestion" embedded @saved="load" @deleted="questionDeleted" />
  </el-drawer>

 </section>
</template>

<style scoped>
.question-library-layout { display: grid; grid-template-columns: 240px minmax(0, 1fr); gap: 24px; align-items: start; }
.question-type-sidebar { padding: 0 12px 0 0; align-self: stretch; border-right: 1px solid var(--el-border-color-light); }
.question-type-node { display: flex; justify-content: space-between; align-items: center; gap: 8px; width: 100%; min-width: 0; padding-right: 10px; }
.question-type-name { display: flex; align-items: center; gap: 8px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.question-type-folder { font-size: 20px; color: var(--el-color-primary); }
.question-type-count { flex-shrink: 0; font-size: 12px; padding: 1px 7px; border-radius: 10px; background: var(--el-fill-color-light); color: var(--el-text-color-secondary); }
.question-type-sidebar :deep(.el-tree-node__content) { height: 42px; border-radius: 6px; margin-bottom: 3px; }
.question-type-sidebar :deep(.el-tree-node.is-current > .el-tree-node__content) { color: var(--el-color-primary); background: var(--el-color-primary-light-9); font-weight: 600; }
.question-library-main { min-width: 0; }
.question-library-toolbar { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; margin-bottom: 16px; }
.knowledge-filter { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; min-width: 0; }
.knowledge-filter-tag { display: inline-flex; align-items: center; padding: 5px 12px; border: 1px solid var(--el-border-color-light); border-radius: 6px; background: var(--el-bg-color); color: var(--el-text-color-regular); font: inherit; font-size: 13px; line-height: 20px; cursor: pointer; overflow-wrap: anywhere; }
.knowledge-filter-tag:hover, .knowledge-filter-tag.is-active { color: var(--el-color-primary); border-color: var(--el-color-primary-light-5); background: var(--el-color-primary-light-9); }
.knowledge-filter-tag:focus-visible { outline: 2px solid var(--el-color-primary); outline-offset: 2px; }
.card-knowledge-tag { padding: 1px 8px; font-size: 12px; }
.question-library-toolbar > .el-button { flex-shrink: 0; }
.all-questions-button { width: 100%; height: 42px; margin-bottom: 10px; }
.all-questions-button :deep(> span) { display: flex; align-items: center; justify-content: space-between; width: 100%; gap: 8px; }
.all-questions-button.is-active { color: var(--el-color-primary); border-color: var(--el-color-primary-light-5); background: var(--el-color-primary-light-9); font-weight: 600; }
.question-cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 300px), 1fr)); gap: 18px; min-height: 160px; }
.library-question-card { display: flex; flex-direction: column; min-width: 0; padding: 22px; border: 1px solid var(--el-border-color-light); border-radius: 12px; background: var(--el-bg-color); cursor: pointer; transition: border-color .2s, box-shadow .2s; }
.library-question-card:hover, .library-question-card:focus-visible { border-color: var(--el-color-primary); box-shadow: 0 4px 16px rgb(34 105 83 / 8%); }
.library-question-card:focus-visible { outline: 2px solid var(--el-color-primary); outline-offset: 3px; }
.question-card-meta { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 10px; }
.question-difficulty { color: var(--el-text-color-secondary); font-size: 13px; }
.question-card-title { margin: 18px 0; font-size: 18px; line-height: 1.6; overflow-wrap: anywhere; }
.question-card-knowledge { margin-top: auto; }
.question-card-knowledge .taxonomy-tags { align-items: center; overflow-wrap: anywhere; }
.question-card-knowledge :deep(.el-tag) { max-width: 100%; height: auto; white-space: normal; }

.question-empty { grid-column: 1 / -1; }
@media (max-width: 900px) {
 .question-library-layout { grid-template-columns: minmax(0, 1fr); gap: 18px; }
 .question-type-sidebar { border-right: 0; padding-right: 0; }
 .question-library-toolbar { flex-wrap: wrap; }
 .question-type-sidebar :deep(.el-tree) { max-height: 240px; overflow: auto; }
}
</style>
