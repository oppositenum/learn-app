<script setup lang="ts">
import { ArrowDown, ArrowUp } from '@lucide/vue'
import { computed } from 'vue'

import type { StudentInteraction } from '../api/student'

const props = defineProps<{
  interaction: StudentInteraction
  modelValue: Record<string, unknown>
  disabled?: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: Record<string, unknown>]
}>()

const scene = computed(() => props.interaction.scene!)
const selected = computed(() => Array.isArray(props.modelValue.selected_option_ids) ? props.modelValue.selected_option_ids as string[] : [])
const ordered = computed(() => Array.isArray(props.modelValue.ordered_item_ids) ? props.modelValue.ordered_item_ids as string[] : (scene.value.items ?? []).map((item) => item.id))
const pairs = computed(() => Array.isArray(props.modelValue.pairs) ? props.modelValue.pairs as Array<{ left_id: string; right_id: string }> : [])
const placements = computed(() => Array.isArray(props.modelValue.placements) ? props.modelValue.placements as Array<{ item_id: string; group_id: string }> : [])
const values = computed(() => Array.isArray(props.modelValue.values) ? props.modelValue.values as Array<{ slot_id: string; value: string }> : [])

function toggleOption(id: string, checked: boolean) {
  if (props.interaction.renderer === 'SINGLE_CHOICE') {
    emit('update:modelValue', { selected_option_ids: checked ? [id] : [] })
    return
  }
  const next = selected.value.filter((candidate) => candidate !== id)
  if (checked) next.push(id)
  emit('update:modelValue', { selected_option_ids: next })
}

function moveItem(index: number, delta: -1 | 1) {
  const target = index + delta
  if (target < 0 || target >= ordered.value.length) return
  const next = [...ordered.value]
  ;[next[index], next[target]] = [next[target], next[index]]
  emit('update:modelValue', { ordered_item_ids: next })
}

function setPair(leftID: string, rightID: string) {
  const next = pairs.value.filter((pair) => pair.left_id !== leftID && pair.right_id !== rightID)
  if (rightID) next.push({ left_id: leftID, right_id: rightID })
  emit('update:modelValue', { pairs: next })
}

function setPlacement(itemID: string, groupID: string) {
  const next = placements.value.filter((placement) => placement.item_id !== itemID)
  if (groupID) next.push({ item_id: itemID, group_id: groupID })
  emit('update:modelValue', { placements: next })
}

function setNumber(raw: string) {
	if (!raw.trim()) {
		emit('update:modelValue', {})
		return
	}
  const value = Number(raw)
  if (Number.isFinite(value)) emit('update:modelValue', { value })
}

function setFill(slotID: string, value: string) {
  const next = values.value.filter((candidate) => candidate.slot_id !== slotID)
  next.push({ slot_id: slotID, value })
  emit('update:modelValue', { values: next })
}

function valueFor(key: 'left_id' | 'item_id' | 'slot_id', id: string, valueKey: 'right_id' | 'group_id' | 'value') {
  const source = (valueKey === 'right_id' ? pairs.value : valueKey === 'group_id' ? placements.value : values.value) as Array<Record<string, string>>
  const match = source.find((item) => item[key] === id)
  return match?.[valueKey] ?? ''
}

function labelForItem(id: string) {
  return scene.value.items?.find((item) => item.id === id)?.label ?? id
}
</script>

<template>
  <div
    class="student-interaction"
    :data-renderer="interaction.renderer"
  >
    <fieldset
      v-if="interaction.renderer === 'SINGLE_CHOICE' || interaction.renderer === 'MULTIPLE_CHOICE'"
      class="m-0 grid gap-2 border-0 p-0"
    >
      <legend class="sr-only">
        {{ scene.accessible_fallback }}
      </legend>
      <label
        v-for="option in scene.options"
        :key="option.id"
        class="interaction-option"
      >
        <input
          :type="interaction.renderer === 'SINGLE_CHOICE' ? 'radio' : 'checkbox'"
          name="student-interaction-choice"
          :value="option.id"
          :checked="selected.includes(option.id)"
          :disabled="disabled"
          @change="toggleOption(option.id, ($event.target as HTMLInputElement).checked)"
        >
        <span>{{ option.label }}</span>
      </label>
    </fieldset>

    <ol
      v-else-if="interaction.renderer === 'ORDERING'"
      class="grid gap-2"
      :aria-label="scene.accessible_fallback"
    >
      <li
        v-for="(id, index) in ordered"
        :key="id"
        class="interaction-row"
      >
        <span
          class="interaction-index"
          aria-hidden="true"
        >{{ index + 1 }}</span>
        <span class="min-w-0 flex-1">{{ labelForItem(id) }}</span>
        <button
          type="button"
          class="icon-button"
          :disabled="disabled || index === 0"
          :aria-label="`上移${labelForItem(id)}`"
          :title="`上移${labelForItem(id)}`"
          @click="moveItem(index, -1)"
        >
          <ArrowUp :size="17" />
        </button>
        <button
          type="button"
          class="icon-button"
          :disabled="disabled || index === ordered.length - 1"
          :aria-label="`下移${labelForItem(id)}`"
          :title="`下移${labelForItem(id)}`"
          @click="moveItem(index, 1)"
        >
          <ArrowDown :size="17" />
        </button>
      </li>
    </ol>

    <fieldset
      v-else-if="interaction.renderer === 'MATCHING'"
      class="m-0 grid gap-3 border-0 p-0"
    >
      <legend class="sr-only">
        {{ scene.accessible_fallback }}
      </legend>
      <label
        v-for="left in scene.left"
        :key="left.id"
        class="interaction-field"
      >
        <span>{{ left.label }}</span>
        <select
          :value="valueFor('left_id', left.id, 'right_id')"
          :disabled="disabled"
          @change="setPair(left.id, ($event.target as HTMLSelectElement).value)"
        >
          <option value="">请选择</option>
          <option
            v-for="right in scene.right"
            :key="right.id"
            :value="right.id"
          >{{ right.label }}</option>
        </select>
      </label>
    </fieldset>

    <fieldset
      v-else-if="interaction.renderer === 'GROUPING'"
      class="m-0 grid gap-3 border-0 p-0"
    >
      <legend class="sr-only">
        {{ scene.accessible_fallback }}
      </legend>
      <label
        v-for="item in scene.items"
        :key="item.id"
        class="interaction-field"
      >
        <span>{{ item.label }}</span>
        <select
          :value="valueFor('item_id', item.id, 'group_id')"
          :disabled="disabled"
          @change="setPlacement(item.id, ($event.target as HTMLSelectElement).value)"
        >
          <option value="">请选择分组</option>
          <option
            v-for="group in scene.groups"
            :key="group.id"
            :value="group.id"
          >{{ group.label }}</option>
        </select>
      </label>
    </fieldset>

    <fieldset
      v-else-if="interaction.renderer === 'NUMBER_LINE' && scene.number_line"
      class="m-0 grid gap-3 border-0 p-0"
    >
      <legend class="sr-only">
        {{ scene.accessible_fallback }}
      </legend>
      <label class="grid gap-2 text-base font-semibold">
        {{ scene.number_line.label }}
        <input
          type="range"
          :min="scene.number_line.min"
          :max="scene.number_line.max"
          :step="scene.number_line.step"
          :value="modelValue.value"
          :disabled="disabled"
          @input="setNumber(($event.target as HTMLInputElement).value)"
        >
      </label>
      <label class="interaction-field">
        <span>当前数值</span>
        <input
          type="number"
          :min="scene.number_line.min"
          :max="scene.number_line.max"
          :step="scene.number_line.step"
          :value="modelValue.value"
          :disabled="disabled"
          @input="setNumber(($event.target as HTMLInputElement).value)"
        >
      </label>
    </fieldset>

    <fieldset
      v-else-if="interaction.renderer === 'FILL_BLANKS'"
      class="m-0 grid gap-3 border-0 p-0"
    >
      <legend class="sr-only">
        {{ scene.accessible_fallback }}
      </legend>
      <label
        v-for="slot in scene.slots"
        :key="slot.id"
        class="interaction-field"
      >
        <span>{{ slot.label }}</span>
        <input
          type="text"
          maxlength="200"
          :value="valueFor('slot_id', slot.id, 'value')"
          :disabled="disabled"
          @input="setFill(slot.id, ($event.target as HTMLInputElement).value)"
        >
      </label>
    </fieldset>

    <details class="mt-4 border-t border-zinc-200 pt-3 text-base text-zinc-700">
      <summary class="cursor-pointer font-semibold">
        文字版任务
      </summary>
      <p class="mt-2 whitespace-pre-line leading-6">
        {{ interaction.accessible_fallback }}
      </p>
    </details>
  </div>
</template>

<style scoped>
.student-interaction {
  min-width: 0;
}

.interaction-option,
.interaction-row,
.interaction-field {
  display: flex;
  min-width: 0;
  min-height: 3rem;
  align-items: center;
  gap: 0.75rem;
  border: 1px solid #d4d4d8;
  background: #fff;
  padding: 0.75rem;
}

.interaction-option:has(input:checked) {
  border-color: #0f766e;
  background: #f0fdfa;
}

.interaction-option input {
  width: 1.125rem;
  height: 1.125rem;
  flex: none;
  accent-color: #0f766e;
}

.interaction-row .icon-button {
  width: 2.5rem;
  height: 2.5rem;
}

.interaction-index {
  display: grid;
  width: 1.75rem;
  height: 1.75rem;
  flex: none;
  place-items: center;
  background: #e4e4e7;
  font-size: 0.75rem;
  font-weight: 700;
}

.interaction-field {
  flex-wrap: wrap;
  justify-content: space-between;
}

.interaction-field > span {
  min-width: min(12rem, 100%);
  flex: 1;
  overflow-wrap: anywhere;
}

.interaction-field select,
.interaction-field input {
  min-height: 2.75rem;
  min-width: 0;
  flex: 1 1 12rem;
  border: 1px solid #a1a1aa;
  background: #fff;
  padding: 0.55rem 0.7rem;
  font-size: 1rem;
}

input[type='range'] {
  width: 100%;
  min-height: 2.75rem;
  accent-color: #0f766e;
}

@media (max-width: 360px) {
  .interaction-row {
    gap: 0.4rem;
    padding-inline: 0.5rem;
  }
}
</style>
