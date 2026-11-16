<script setup lang="ts">
import { computed } from 'vue'
import { limitOptions } from '../resourceLimits'
const props = defineProps<{ modelValue: number; label: string; unit?: string; presets: number[]; divisor?: number }>()
const emit = defineEmits<{ 'update:modelValue': [value: number] }>()
// Retain a saved custom limit as a stop until the editor is closed.
const initialValue = props.modelValue
const options = computed(() => limitOptions(props.presets, initialValue))
const end = computed(() => options.value.length - 1)
const position = computed({
  get: () => options.value.indexOf(props.modelValue),
  set: (index: number) => emit('update:modelValue', options.value[index] ?? 0),
})
function display(value: number) {
  return value === 0 ? '无限' : `${Number((value / (props.divisor ?? 1)).toFixed(4))}${props.unit ? ` ${props.unit}` : ''}`
}
function format(index: number) { return display(options.value[index] ?? 0) }
</script>

<template>
  <div class="resource-limit-slider">
    <div class="resource-limit-heading"><span>{{ label }}</span><strong>{{ display(modelValue) }}</strong></div>
    <el-slider v-model="position" :min="0" :max="end" :step="1" show-stops :format-tooltip="format" :aria-label="label" />
    <div class="resource-limit-scale"><span>{{ display(options[0]!) }}</span><span>无限</span></div>
  </div>
</template>

<style scoped>
.resource-limit-slider { min-width: 0; padding: 4px 12px 12px; }
.resource-limit-heading, .resource-limit-scale { display: flex; justify-content: space-between; gap: 12px; }
.resource-limit-heading { color: var(--el-text-color-regular); }
.resource-limit-heading strong { color: var(--el-color-primary); }
.resource-limit-scale { font-size: 12px; color: var(--el-text-color-secondary); }
</style>
