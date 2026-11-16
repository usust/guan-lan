<script setup lang="ts">
defineProps<{ name: 'add' | 'move' | 'rename' | 'merge' | 'delete' | 'link' }>()
</script>
<template>
 <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">
  <path v-if="name === 'add'" d="M12 5v14M5 12h14" />
  <template v-else-if="name === 'move'"><path d="M12 3v18M3 12h18M9 6l3-3 3 3M9 18l3 3 3-3M6 9l-3 3 3 3M18 9l3 3-3 3" /></template>
  <path v-else-if="name === 'rename'" d="m15 5 4 4M4 20l5-1L20 8a2.8 2.8 0 0 0-4-4L5 15l-1 5Z" />
  <template v-else-if="name === 'merge'"><circle cx="6" cy="5" r="2" /><circle cx="18" cy="5" r="2" /><circle cx="12" cy="19" r="2" /><path d="M6 7v2c0 4 6 3 6 8M18 7v2c0 4-6 3-6 8" /></template>
  <path v-else-if="name === 'link'" d="m10 13 4-4M8 16l-1 1a4 4 0 0 1-6-6l5-5a4 4 0 0 1 6 0M16 8l1-1a4 4 0 0 1 6 6l-5 5a4 4 0 0 1-6 0" />
  <path v-else d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7" />
 </svg>
</template>
