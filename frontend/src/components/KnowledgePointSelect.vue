<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { type KnowledgePoint } from '../knowledgePoints'
import { questionTypeTree, type QuestionTypeEntry, type QuestionTypeNode } from '../questionTypes'
const props = defineProps<{ modelValue?: string[]; points: KnowledgePoint[]; types: QuestionTypeEntry[]; category: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()
const points = computed(() => props.points)
const choosing = ref(false)
interface ChoiceNode extends QuestionTypeNode { key: string; kind: 'type' | 'point' | 'root'; owner: string; children: ChoiceNode[] }
const directory = computed(() => {
 const pointNodes = (nodes: QuestionTypeNode[], owner: string): ChoiceNode[] => nodes.map(node => ({ ...node, key: `point:${node.name}`, kind: 'point', owner, children: pointNodes(node.children, owner) }))
 const typeNodes = (nodes: QuestionTypeNode[]): ChoiceNode[] => nodes.map(node => ({ ...node, key: `type:${node.name}`, kind: 'type', owner: node.name, children: [...typeNodes(node.children), ...pointNodes(questionTypeTree(points.value.filter(p => p.owner_type === node.name)), node.name)] }))
 const nodes = typeNodes(questionTypeTree(props.types))
 const pending = points.value.filter(p => !p.owner_type)
 for (const name of props.modelValue ?? []) if (!points.value.some(p => p.name === name)) pending.push({ name, types: [], question_count: 0, usage_count: 0 })
 if (pending.length) nodes.push({ name: '待归属知识点', key: 'pending', kind: 'root', owner: '', question_count: 0, usage_count: 0, total_questions: 0, total_usage: 0, children: pointNodes(questionTypeTree(pending), '') })
 return nodes
})
const expanded = computed(() => {
 const keys = new Set<string>()
 let category = props.category
 while (category && !keys.has(`type:${category}`)) { keys.add(`type:${category}`); category = props.types.find(t => t.name === category)?.parent_name ?? '' }
 for (const name of props.modelValue ?? []) {
  let point = points.value.find(p => p.name === name)
  if (point?.owner_type) { let type: string | undefined = point.owner_type; while (type && !keys.has(`type:${type}`)) { keys.add(`type:${type}`); type = props.types.find(t => t.name === type)?.parent_name } }
  else keys.add('pending')
  const seen = new Set<string>()
  while (point?.parent_name && !seen.has(point.parent_name)) { seen.add(point.parent_name); keys.add(`point:${point.parent_name}`); point = points.value.find(p => p.name === point?.parent_name) }
 }
 return [...keys]
})
function toggle(point: string, checked: boolean) {
 const selected = props.modelValue ?? []
 if (checked && selected.length >= 20) { ElMessage.warning('每道题最多选择 20 个知识点'); return }
 emit('update:modelValue', checked ? [...new Set([...selected, point])] : selected.filter(p => p !== point))
}
</script>
<template>
 <div class="knowledge-select">
  <div class="knowledge-selected">
   <el-tag v-for="name in modelValue ?? []" :key="name" closable @close="toggle(name, false)">{{ name }}</el-tag>
   <span v-if="!modelValue?.length" class="knowledge-empty">未选择知识点</span>
   <el-button text type="primary" :aria-expanded="choosing" @click="choosing = !choosing">{{ choosing ? '完成选择' : '选择知识点' }}</el-button>
  </div>
  <div v-if="choosing" class="knowledge-picker">
  <div class="knowledge-choice-tree">
   <el-tree :key="category" :data="directory" node-key="key" :props="{ label: 'name' }" :indent="20" :default-expanded-keys="expanded" :expand-on-click-node="true" empty-text="暂无可选知识点">
    <template #default="{ data }">
     <div class="knowledge-choice-node">
      <template v-if="data.kind === 'point'">
       <el-checkbox :model-value="(modelValue ?? []).includes(data.name)" :aria-label="`选择知识点${data.name}`" @click.stop @keydown.stop @change="(value: unknown) => toggle(data.name, !!value)">{{ data.name }}</el-checkbox>
      </template>
      <strong v-else>{{ data.name }}</strong>
     </div>
    </template>
   </el-tree>
  </div>
  </div>
 </div>

</template>
<style scoped>
.knowledge-select { width: 100%; }
.knowledge-selected { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; min-height: 32px; }
.knowledge-selected :deep(.el-tag) { max-width: 100%; height: auto; white-space: normal; overflow-wrap: anywhere; }
.knowledge-empty { color: var(--el-text-color-secondary); font-size: 14px; }
.knowledge-picker { margin-top: 8px; padding: 12px; border: 1px solid var(--el-border-color-light); border-radius: 6px; }
.knowledge-choice-tree { max-height: 420px; overflow: auto; }
.knowledge-choice-tree :deep(.el-tree) { --el-tree-node-hover-bg-color: transparent; }
.knowledge-choice-tree :deep(.el-tree-node__content) { min-height: 40px; height: auto; background: transparent; }
.knowledge-choice-tree :deep(.el-tree-node:focus > .el-tree-node__content) { background: transparent; }
.knowledge-choice-node { display: flex; align-items: center; gap: 10px; min-width: 0; flex: 1; }
.knowledge-choice-node strong { font-size: 14px; font-weight: 600; }
.knowledge-choice-node :deep(.el-checkbox__label) { white-space: normal; overflow-wrap: anywhere; }
</style>
