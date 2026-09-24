<script setup lang="ts">
import { Check } from '@lucide/vue'
import { computed } from 'vue'

import { classroomStageLabels, classroomStages } from '../lib/studentInteraction'

const props = defineProps<{ stage: string }>()

// Only the server's four stages are drawn. An unknown stage leaves every node
// hollow rather than guessing how far the child has come.
const activeIndex = computed(() => classroomStages.indexOf(props.stage as (typeof classroomStages)[number]))

function nodeState(index: number) {
	if (activeIndex.value < 0 || index > activeIndex.value) return 'upcoming'
	return index === activeIndex.value ? 'current' : 'done'
}
</script>

<template>
  <ol
    class="stage-track"
    aria-label="课堂阶段"
  >
    <li
      v-for="(node, index) in classroomStages"
      :key="node"
      class="stage-track-node"
      :data-stage-state="nodeState(index)"
      :aria-current="node === props.stage ? 'step' : undefined"
    >
      <span
        class="stage-track-mark"
        aria-hidden="true"
      >
        <Check
          v-if="nodeState(index) === 'done'"
          :size="16"
          :stroke-width="3"
        />
      </span>
      <span class="stage-track-label">{{ classroomStageLabels[node] }}</span>
    </li>
  </ol>
</template>
