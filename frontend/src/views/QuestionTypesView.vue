<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import DifficultyMaintenance from '../components/DifficultyMaintenance.vue'
import TaxonomyActionIcon from '../components/TaxonomyActionIcon.vue'
import { api, errorMessage } from '../api'
import type { KnowledgePoint } from '../knowledgePoints'
import { questionTypeTree, typeDescendants, type QuestionTypeEntry as Entry } from '../questionTypes'
const activeTab = ref('types')
const items = ref<Entry[]>([])
const points = ref<KnowledgePoint[]>([])
const tagsByType = computed(() => {
 const grouped = new Map<string, KnowledgePoint[]>()
 for (const point of points.value) {
  if (!point.owner_type) continue
  const tags = grouped.get(point.owner_type) ?? []
  tags.push(point); grouped.set(point.owner_type, tags)
 }
 return grouped
})
const selectedName = ref<string | null>(null)
const selected = computed(() => items.value.find(item => item.name === selectedName.value))
const includeChildren = ref(true)
const tagName = ref('')
const editingTag = ref<string | null>(null)
const addingTag = ref(false)
const tagInput = ref<{ focus: () => void }>()
function cancelTagEdit() { editingTag.value = null; addingTag.value = false; tagName.value = '' }
async function editTag(point?: KnowledgePoint) {
 cancelTagEdit()
 editingTag.value = point?.name ?? null; addingTag.value = !point; tagName.value = point?.name ?? ''
 await nextTick(); tagInput.value?.focus()
}
const selectedBranch = computed(() => selected.value ? typeDescendants(items.value, selected.value.name) : new Set(items.value.map(item => item.name)))
const branchTags = computed(() => points.value.filter(point => selectedName.value === 'root' || (includeChildren.value ? selectedBranch.value.has(point.owner_type ?? '') : point.owner_type === selected.value?.name)))
function selectType(node: DirectoryNode) {
 if (busy.value) return
 selectedName.value = node.root ? 'root' : node.name
 cancelTagEdit()
}
async function saveTag() {
 const target = tagName.value.trim()
 if (busy.value || !target || (!editingTag.value && !selected.value)) return
 if (target === editingTag.value) { cancelTagEdit(); return }
 busy.value = true
 try {
  if (editingTag.value) await api.changeTaxonomy('tag', { source: editingTag.value, target, mode: 'rename' })
  else await api.createKnowledgePoint(target, '', selected.value!.name)
  cancelTagEdit(); ElMessage.success('标签已保存'); await load()
 } catch (e) { ElMessage.error(errorMessage(e)) } finally { busy.value = false }
}
const creating = ref(false)
const name = ref('')
const parent = ref('')
const nameInput = ref<{ focus: () => void }>()
const moving = ref<{ name: string; parent: string } | null>(null)
const tree = computed(() => questionTypeTree(items.value))
const moveOptions = computed(() => { const excluded = typeDescendants(items.value, moving.value?.name ?? ''); return questionTypeTree(items.value.filter(i => !excluded.has(i.name))) })
async function move() {
 if (!moving.value) return
 busy.value = true
 try { await api.moveQuestionType(moving.value.name, moving.value.parent); moving.value = null; ElMessage.success('上级题型已更新'); await load() } catch (e) { ElMessage.error(errorMessage(e)) } finally { busy.value = false }
}
function addChild(item?: Entry) { parent.value = item?.name ?? ''; name.value = ''; creating.value = true }
const loading = ref(false)
const busy = ref(false)
const error = ref('')
type DirectoryNode = Entry & { key: string; root?: boolean; total_questions: number; total_usage: number; children: DirectoryNode[] }
const directory = computed(() => {
 const decorate = (nodes: ReturnType<typeof questionTypeTree>): DirectoryNode[] => nodes.map(node => ({ ...node, key: `type:${node.name}`, children: decorate(node.children) }))
 return [{ key: 'root', name: '全部题型', root: true, question_count: 0, usage_count: 0, total_questions: tree.value.reduce((sum, node) => sum + node.total_questions, 0), total_usage: tree.value.reduce((sum, node) => sum + node.total_usage, 0), children: decorate(tree.value) }]
})
const change = ref<{ source: string; target: string; mode: 'rename' | 'merge' | 'delete'; kind: 'type' | 'tag'; count: number } | null>(null)
const targetOptions = computed(() => items.value.filter(i => !typeDescendants(items.value, change.value?.source ?? '').has(i.name)))
const operation = computed(() => ({ rename: '重命名', merge: '合并', delete: '移除' })[change.value?.mode ?? 'rename'])
async function load() {
 loading.value = true; error.value = ''
 try { const [data, knowledge] = await Promise.all([api.questionTypes(), api.knowledgePoints()]); items.value = data.items; points.value = knowledge.items; if (selectedName.value !== 'root' && !items.value.some(item => item.name === selectedName.value)) selectedName.value = null } catch (e) { error.value = errorMessage(e) } finally { loading.value = false }
}
async function create() {
 if (!name.value.trim()) return
 busy.value = true
 try { await api.createQuestionType(name.value.trim(), parent.value); name.value = ''; creating.value = false; ElMessage.success('题型已创建'); await load() } catch (e) { ElMessage.error(errorMessage(e)) } finally { busy.value = false }
}
function openChange(item: Entry, mode: 'rename' | 'merge' | 'delete') {
 change.value = { source: item.name, target: mode === 'rename' ? item.name : '', mode, kind: 'type', count: item.usage_count }
}
async function applyChange() {
 if (!change.value) return
 busy.value = true
 try { const previous = change.value; const { updated } = await api.changeTaxonomy(change.value.kind, { source: change.value.source, target: change.value.target, mode: change.value.mode }); ElMessage.success(`整理完成，已更新 ${updated} 道题目（含竞赛副本）`); if (previous.kind === 'type' && selectedName.value === previous.source) selectedName.value = previous.target; change.value = null; await load() } catch (e) { ElMessage.error(errorMessage(e)) } finally { busy.value = false }
}
async function remove(item: Entry) {
 try { await ElMessageBox.confirm(`删除未使用的题型“${item.name}”？`, '删除题型', { type: 'warning' }) } catch { return }
 busy.value = true
 try { await api.deleteQuestionType(item.name); ElMessage.success('题型已删除'); await load() } catch (e) { ElMessage.error(errorMessage(e)) } finally { busy.value = false }
}
onMounted(load)
</script>
<template>
 <section>
  <el-tabs v-model="activeTab" class="maintenance-tabs">
   <el-tab-pane label="类型维护" name="types" />
   <el-tab-pane label="难度维护" name="difficulty" />
  </el-tabs>
  <DifficultyMaintenance v-if="activeTab === 'difficulty'" />
  <el-alert v-if="error" :title="error" type="error" :closable="false" class="form-alert" />
  <div v-show="activeTab === 'types'" v-loading="loading" class="taxonomy-layout" :class="{ 'has-details': selected || selectedName === 'root' }">
  <div class="taxonomy-directory">
   <el-tree :data="directory" node-key="key" :props="{ label: 'name' }" :indent="24" default-expand-all :expand-on-click-node="false" highlight-current @node-click="selectType" empty-text="暂无匹配的题型" class="taxonomy-tree">
    <template #default="{ data }">
     <div class="taxonomy-node">
      <div class="taxonomy-node-label"><span class="taxonomy-folder" aria-hidden="true"></span><strong>{{ data.name }}</strong><el-tag class="taxonomy-question-count" type="info" size="small" :aria-label="`${data.name}共${data.total_questions}题（含子题型）`">{{ data.total_questions }} 题</el-tag></div>
      <div v-if="!data.root && tagsByType.get(data.name)?.length" class="taxonomy-inline-tags" :aria-label="`${data.name}的知识点标签`">
       <el-tag v-for="point in tagsByType.get(data.name)" :key="point.name" size="small">{{ point.name }}</el-tag>
      </div>
      <div class="taxonomy-node-actions" @click.stop @keydown.stop>
       <el-tooltip :content="data.root ? '新增题型' : '新增子级'" placement="top" :show-after="2000"><el-button text type="primary" class="taxonomy-icon-button" :disabled="busy" :aria-label="data.root ? '新增题型' : `在${data.name}下新增子级`" @click="addChild(data.root ? undefined : data)"><TaxonomyActionIcon name="add" /></el-button></el-tooltip>
       <template v-if="!data.root">
        <el-tooltip content="调整上级" placement="top" :show-after="2000"><el-button text class="taxonomy-icon-button" :disabled="busy" :aria-label="`调整${data.name}的上级`" @click="moving = { name: data.name, parent: data.parent_name || '' }"><TaxonomyActionIcon name="move" /></el-button></el-tooltip>
        <el-tooltip content="重命名" placement="top" :show-after="2000"><el-button text class="taxonomy-icon-button" :disabled="busy" :aria-label="`重命名${data.name}`" @click="openChange(data, 'rename')"><TaxonomyActionIcon name="rename" /></el-button></el-tooltip>
        <el-tooltip content="合并到其他题型" placement="top" :show-after="2000"><span><el-button text class="taxonomy-icon-button" :disabled="busy || !items.some(item => !typeDescendants(items, data.name).has(item.name))" :aria-label="`合并${data.name}`" @click="openChange(data, 'merge')"><TaxonomyActionIcon name="merge" /></el-button></span></el-tooltip>
        <el-tooltip :content="data.usage_count || data.knowledge_count || data.children?.length ? '有题目、知识点引用或子级，暂不可删除' : '删除'" placement="top" :show-after="2000"><span><el-button text type="danger" class="taxonomy-icon-button" :disabled="busy || !!data.usage_count || !!data.knowledge_count || !!data.children?.length" :aria-label="`删除${data.name}`" @click="remove(data)"><TaxonomyActionIcon name="delete" /></el-button></span></el-tooltip>
       </template>
      </div>
     </div>
    </template>
   </el-tree>
  </div>
  <aside v-if="selected || selectedName === 'root'" class="type-details" aria-label="知识点标签">
   <template v-if="selected || selectedName === 'root'">
    <div v-if="selected" class="tags-options"><el-checkbox v-model="includeChildren">包含子题型</el-checkbox></div>
    <div class="tag-list">
     <template v-for="point in branchTags" :key="point.name">
      <form v-if="editingTag === point.name" class="tag-editor" @submit.prevent="saveTag">
       <el-input :ref="(el: any) => { if (el) tagInput = el }" v-model="tagName" maxlength="32" :disabled="busy" aria-label="编辑知识点标签" @keydown.esc="!busy && cancelTagEdit()" />
       <el-button native-type="submit" text type="primary" :disabled="!tagName.trim()" :loading="busy">保存</el-button><el-button text :disabled="busy" @click="cancelTagEdit">取消</el-button>
      </form>
      <div v-else class="tag-item">
       <el-tag>{{ point.name }}</el-tag>
       <button type="button" class="tag-edit-button" :aria-label="`编辑标签${point.name}`" :disabled="busy" @click="editTag(point)"><TaxonomyActionIcon name="rename" /></button>
      </div>
     </template>
     <form v-if="addingTag" class="tag-editor" @submit.prevent="saveTag">
      <el-input :ref="(el: any) => { if (el) tagInput = el }" v-model="tagName" maxlength="32" :disabled="busy" placeholder="知识点名称" aria-label="新增知识点标签" @keydown.esc="!busy && cancelTagEdit()" />
      <el-button native-type="submit" text type="primary" :disabled="!tagName.trim()" :loading="busy">添加</el-button><el-button text :disabled="busy" @click="cancelTagEdit">取消</el-button>
     </form>
     <el-button v-else-if="selected" class="tag-add-button" :disabled="busy" @click="editTag()">＋ 添加知识点</el-button>
    </div>
   </template>
   <p v-else class="no-tags">点击左侧题型，查看知识点标签</p>
  </aside>
  </div>
  <el-dialog v-model="creating" :title="parent ? '新增子题型' : '新增题型'" width="520px" :close-on-click-modal="!busy" :close-on-press-escape="!busy" :show-close="!busy" @opened="nameInput?.focus()">
   <el-form label-position="top" :disabled="busy" @submit.prevent="create">
    <el-form-item label="上级题型"><el-tree-select v-model="parent" :data="tree" node-key="name" :props="{ label: 'name', value: 'name' }" check-strictly clearable filterable placeholder="全部题型（顶层）" style="width: 100%" /></el-form-item>
    <el-form-item label="题型名称"><el-input ref="nameInput" v-model="name" maxlength="32" show-word-limit placeholder="请输入题型名称" /></el-form-item>
   </el-form>
   <template #footer><el-button :disabled="busy" @click="creating = false">取消</el-button><el-button type="primary" :loading="busy" :disabled="!name.trim()" @click="create">添加题型</el-button></template>
  </el-dialog>
  <el-dialog :model-value="!!moving" title="调整上级题型" width="520px" :close-on-click-modal="!busy" :show-close="!busy" :close-on-press-escape="!busy" @close="moving = null"><template v-if="moving"><p>当前题型：{{ moving.name }}</p><el-tree-select v-model="moving.parent" :data="moveOptions" node-key="name" :props="{ label: 'name', value: 'name' }" check-strictly clearable filterable placeholder="无上级（顶层题型）" :disabled="busy" style="width: 100%" /></template><template #footer><el-button :disabled="busy" @click="moving = null">取消</el-button><el-button type="primary" :loading="busy" @click="move">保存</el-button></template></el-dialog>
  <el-dialog :model-value="!!change" :title="`${operation}${change?.kind === 'type' ? '题型' : '知识点'}`" width="520px" :close-on-click-modal="!busy" :close-on-press-escape="!busy" :show-close="!busy" @close="change = null">
   <template v-if="change">
    <p>当前{{ change.kind === 'type' ? '题型' : '知识点' }}：<strong>{{ change.source }}</strong></p>
    <el-form label-position="top" @submit.prevent="applyChange">
     <el-form-item v-if="change.mode !== 'delete'" :label="change.mode === 'merge' ? '合并到已有名称' : '新名称'">
      <el-select v-if="change.mode === 'merge'" v-model="change.target" filterable :disabled="busy" placeholder="请选择合并目标"><el-option v-for="option in targetOptions" :key="option.name" :label="option.name" :value="option.name" /></el-select>
      <el-input v-else v-model="change.target" maxlength="32" show-word-limit :disabled="busy" />
     </el-form-item>
    </el-form>
    <el-alert type="warning" :closable="false" show-icon :title="`将影响 ${change.count} 处题目引用（含竞赛副本）`" :description="change.mode === 'delete' ? '删除此知识点，并从所有关联题目中移除，题目仍会保留。' : change.mode === 'merge' ? '关联题目将改用目标名称；源题型的直接子级移到目标下，考点重复项自动去重。' : '关联题目将统一使用新名称。'" />
   </template>
   <template #footer><el-button :disabled="busy" @click="change = null">取消</el-button><el-button :type="change?.mode === 'delete' ? 'danger' : 'primary'" :loading="busy" :disabled="!change || (change.mode !== 'delete' && (!change.target.trim() || change.target.trim() === change.source))" @click="applyChange">确认{{ operation }}</el-button></template>
  </el-dialog>
 </section>
</template>

<style scoped>
.maintenance-tabs { margin-bottom: 20px; }

.taxonomy-layout { display: grid; grid-template-columns: minmax(0, 1fr); gap: 28px; align-items: start; }
.taxonomy-layout.has-details { grid-template-columns: minmax(0, 1fr) minmax(340px, 390px); }
.taxonomy-directory { padding: 0; min-width: 0; overflow-x: auto; }
.type-details { min-width: 0; border-left: 1px solid var(--el-border-color-light); padding: 24px; min-height: 560px; }
.tags-options { display: flex; justify-content: flex-start; margin-bottom: 12px; }
.tag-list { display: flex; flex-wrap: wrap; align-items: center; gap: 14px 12px; padding-top: 8px; }
.tag-item { position: relative; max-width: 100%; }
.tag-item :deep(.el-tag) { height: auto; min-height: 30px; padding: 5px 20px 5px 10px; white-space: normal; overflow-wrap: anywhere; }
.tag-edit-button { position: absolute; top: -5px; right: -4px; display: grid; place-items: center; width: 18px; height: 18px; padding: 3px; border: 1px solid var(--el-color-primary-light-7); border-radius: 50%; background: var(--el-bg-color); color: var(--el-color-primary); cursor: pointer; }
.tag-edit-button:disabled { cursor: default; opacity: .5; }
.tag-edit-button:focus-visible { outline: 2px solid var(--el-color-primary); outline-offset: 2px; }
.tag-edit-button :deep(svg) { width: 11px; height: 11px; }
.tag-add-button { margin: 0; height: 30px; border-style: dashed; color: var(--el-color-primary); }
.tag-editor { display: flex; align-items: center; gap: 4px; max-width: 100%; }
.tag-editor :deep(.el-input) { width: 150px; }
.tag-editor .el-button + .el-button { margin-left: 0; }
.no-tags { color: var(--el-text-color-secondary); font-size: 13px; }
@media (max-width: 1100px) { .taxonomy-layout, .taxonomy-layout.has-details { grid-template-columns: minmax(0, 1fr); } .type-details { border-left: 0; border-top: 1px solid var(--el-border-color-light); padding: 20px 0; min-height: 0; } }
.taxonomy-tree { --el-tree-node-hover-bg-color: var(--el-color-primary-light-9); }
.taxonomy-tree :deep(.el-tree-node:focus > .el-tree-node__content),
.taxonomy-tree :deep(.el-tree-node.is-current > .el-tree-node__content) { background-color: transparent; }
.taxonomy-tree :deep(.el-tree-node__content:hover),
.taxonomy-tree :deep(.el-tree-node.is-current > .el-tree-node__content) { background-color: var(--el-color-primary-light-9); }
.taxonomy-tree :deep(.el-tree-node:focus-visible > .el-tree-node__content) { outline: 1px solid var(--el-color-primary); outline-offset: -1px; }
.taxonomy-tree :deep(.el-tree-node__content) { height: auto; min-height: 56px; border-radius: 8px; padding-right: 12px; }
.taxonomy-tree :deep(.el-tree-node__expand-icon) { padding: 10px; }
.taxonomy-node { position: relative; box-sizing: border-box; min-height: 40px; display: flex; align-items: center; justify-content: flex-start; flex: 1; min-width: 0; gap: 16px; flex-wrap: wrap; padding: 8px 0; }
.taxonomy-node-label { flex-shrink: 0; max-width: 100%; display: flex; align-items: center; gap: 12px; min-width: 0; flex-wrap: wrap; }
.taxonomy-node-label strong { font-size: 15px; overflow-wrap: anywhere; white-space: normal; }
.taxonomy-question-count { flex-shrink: 0; font-variant-numeric: tabular-nums; }
.taxonomy-folder { position: relative; display: inline-block; flex-shrink: 0; width: 18px; height: 13px; border: 1.5px solid var(--el-color-primary); border-radius: 3px; background: var(--el-color-primary-light-9); }
.taxonomy-folder::before { content: ''; position: absolute; left: 0; top: -5px; width: 8px; height: 4px; border: 1.5px solid var(--el-color-primary); border-bottom: 0; border-radius: 3px 3px 0 0; background: var(--el-color-primary-light-9); }
.taxonomy-inline-tags { display: flex; flex: 1 1 160px; min-width: 0; flex-wrap: wrap; align-items: center; gap: 6px; }
.taxonomy-inline-tags :deep(.el-tag) { max-width: 100%; height: auto; min-height: 24px; white-space: normal; overflow-wrap: anywhere; }
.taxonomy-node-actions { position: absolute; right: 0; top: 50%; transform: translateY(-50%); z-index: 1; display: flex; align-items: center; justify-content: flex-end; gap: 4px; padding-left: 8px; border-radius: 4px; background: var(--el-bg-color); opacity: 0; pointer-events: none; }
.taxonomy-tree :deep(.el-tree-node__content:hover) .taxonomy-node-actions,
.taxonomy-tree :deep(.el-tree-node.is-current > .el-tree-node__content) .taxonomy-node-actions { background: var(--el-color-primary-light-9); }
.taxonomy-tree :deep(.el-tree-node__content:hover) .taxonomy-node-actions,
.taxonomy-node:has(:focus-visible) .taxonomy-node-actions { opacity: 1; pointer-events: auto; }
.taxonomy-tree :deep(.el-tree-node:focus-visible > .el-tree-node__content) .taxonomy-node-actions { opacity: 1; pointer-events: auto; }
@media (hover: none) {
 .taxonomy-node-actions { opacity: 1; pointer-events: auto; }
}
.taxonomy-node-actions .el-button + .el-button { margin-left: 0; }
.taxonomy-icon-button { width: 32px; height: 32px; padding: 6px; }
@media (max-width: 700px) {
 .taxonomy-directory { padding: 0; }
 .taxonomy-node { flex-wrap: wrap; gap: 8px; }
 .taxonomy-node-actions { margin-left: 0; }
 .taxonomy-node-label { gap: 8px; }
}
</style>
