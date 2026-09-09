import { mount } from '@vue/test-utils'
import { expect, test } from 'vitest'

import type { StudentInteraction, StudentInteractionScene } from '../api/student'
import { structuredResponseComplete } from '../lib/studentInteraction'
import StudentInteractionRenderer from './StudentInteractionRenderer.vue'

const items = [{ id: 'a', label: '甲' }, { id: 'b', label: '乙' }]

function material(scene: StudentInteractionScene): StudentInteraction {
  return {
    version: 'student-interaction-v1', renderer: scene.renderer, scene,
    answer_schema: {}, accessible_fallback: scene.accessible_fallback, fallback: false,
  }
}

function latest(wrapper: ReturnType<typeof mount>) {
  const events = wrapper.emitted('update:modelValue') ?? []
  return events.at(-1)?.[0]
}

test('renders both choice contracts with native controls and structured IDs', async () => {
  for (const renderer of ['SINGLE_CHOICE', 'MULTIPLE_CHOICE'] as const) {
    const interaction = material({ version: 'student-interaction-v1', renderer, accessible_fallback: '按文字逐项选择。', options: items })
    const wrapper = mount(StudentInteractionRenderer, { props: { interaction, modelValue: { selected_option_ids: [] } } })
    const controls = wrapper.findAll<HTMLInputElement>('input')
    expect(controls).toHaveLength(2)
    expect(controls[0].attributes('type')).toBe(renderer === 'SINGLE_CHOICE' ? 'radio' : 'checkbox')
    expect(wrapper.get('details').text()).toContain('按文字逐项选择。')
    await controls[0].setValue(true)
    expect(latest(wrapper)).toEqual({ selected_option_ids: ['a'] })
    wrapper.unmount()
  }
})

test('renders keyboard ordering controls and emits a complete order', async () => {
  const interaction = material({ version: 'student-interaction-v1', renderer: 'ORDERING', accessible_fallback: '按先后顺序排列。', items })
  const wrapper = mount(StudentInteractionRenderer, { props: { interaction, modelValue: { ordered_item_ids: ['a', 'b'] } } })
  const moveDown = wrapper.get('button[aria-label="下移甲"]')
  await moveDown.trigger('click')
  expect(latest(wrapper)).toEqual({ ordered_item_ids: ['b', 'a'] })
})

test('renders matching and grouping as labelled native selects', async () => {
  const matching = material({ version: 'student-interaction-v1', renderer: 'MATCHING', accessible_fallback: '逐项配对。', left: items, right: [{ id: 'x', label: '一' }, { id: 'y', label: '二' }] })
  const matchWrapper = mount(StudentInteractionRenderer, { props: { interaction: matching, modelValue: { pairs: [] } } })
  await matchWrapper.findAll('select')[0].setValue('x')
  expect(latest(matchWrapper)).toEqual({ pairs: [{ left_id: 'a', right_id: 'x' }] })
  await matchWrapper.setProps({ modelValue: latest(matchWrapper) as Record<string, unknown> })
  await matchWrapper.findAll('select')[1].setValue('x')
  expect(latest(matchWrapper)).toEqual({ pairs: [{ left_id: 'b', right_id: 'x' }] })
  expect(structuredResponseComplete(matching, {
    pairs: [{ left_id: 'a', right_id: 'x' }, { left_id: 'b', right_id: 'x' }],
  })).toBe(false)

  const grouping = material({ version: 'student-interaction-v1', renderer: 'GROUPING', accessible_fallback: '逐项归类。', items, groups: [{ id: 'x', label: '第一组' }, { id: 'y', label: '第二组' }] })
  const groupWrapper = mount(StudentInteractionRenderer, { props: { interaction: grouping, modelValue: { placements: [] } } })
  await groupWrapper.findAll('select')[1].setValue('y')
  expect(latest(groupWrapper)).toEqual({ placements: [{ item_id: 'b', group_id: 'y' }] })
})

test('renders number-line and fill contracts with labelled native inputs', async () => {
  const numberLine = material({ version: 'student-interaction-v1', renderer: 'NUMBER_LINE', accessible_fallback: '输入数轴位置。', number_line: { label: '位置', min: -1, max: 1, step: 0.5 } })
  const numberWrapper = mount(StudentInteractionRenderer, { props: { interaction: numberLine, modelValue: { value: -1 } } })
  await numberWrapper.get('input[type="number"]').setValue('0.5')
  expect(latest(numberWrapper)).toEqual({ value: 0.5 })

  const fill = material({ version: 'student-interaction-v1', renderer: 'FILL_BLANKS', accessible_fallback: '填写两个空格。', slots: items })
  const fillWrapper = mount(StudentInteractionRenderer, { props: { interaction: fill, modelValue: { values: [] } } })
  await fillWrapper.findAll('input[type="text"]')[0].setValue('内容')
  expect(latest(fillWrapper)).toEqual({ values: [{ slot_id: 'a', value: '内容' }] })
})

test('keeps every renderer within a stable disabled control surface', () => {
  const scenes: StudentInteractionScene[] = [
    { version: 'student-interaction-v1', renderer: 'SINGLE_CHOICE', accessible_fallback: '文字', options: items },
    { version: 'student-interaction-v1', renderer: 'MULTIPLE_CHOICE', accessible_fallback: '文字', options: items },
    { version: 'student-interaction-v1', renderer: 'ORDERING', accessible_fallback: '文字', items },
    { version: 'student-interaction-v1', renderer: 'MATCHING', accessible_fallback: '文字', left: items, right: items },
    { version: 'student-interaction-v1', renderer: 'GROUPING', accessible_fallback: '文字', items, groups: items },
    { version: 'student-interaction-v1', renderer: 'NUMBER_LINE', accessible_fallback: '文字', number_line: { label: '数值', min: 0, max: 2, step: 1 } },
    { version: 'student-interaction-v1', renderer: 'FILL_BLANKS', accessible_fallback: '文字', slots: items },
  ]
  for (const scene of scenes) {
    const wrapper = mount(StudentInteractionRenderer, { props: { interaction: material(scene), modelValue: {}, disabled: true } })
    expect(wrapper.get('[data-renderer]').attributes('data-renderer')).toBe(scene.renderer)
    expect(wrapper.findAll('button,input,select').every((control) => control.attributes('disabled') !== undefined)).toBe(true)
    expect(wrapper.get('details').text()).toContain('文字')
    wrapper.unmount()
  }
})
