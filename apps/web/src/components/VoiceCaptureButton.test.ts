import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'

import VoiceCaptureButton from './VoiceCaptureButton.vue'
import { transcribeStudentAudio } from '../api/student'

vi.mock('../api/student', () => ({ transcribeStudentAudio: vi.fn(async () => '我先减去配送费') }))

class FakeMediaRecorder extends EventTarget {
	mimeType = 'audio/webm'
	state: RecordingState = 'inactive'
	start() { this.state = 'recording' }
	stop() {
		this.state = 'inactive'
		const dataEvent = new Event('dataavailable') as Event & { data: Blob }
		dataEvent.data = new Blob(['audio'])
		this.dispatchEvent(dataEvent)
		this.dispatchEvent(new Event('stop'))
	}
}

describe('VoiceCaptureButton', () => {
	it('places the transcript in editable text without submitting', async () => {
		const stop = vi.fn()
		Object.defineProperty(navigator, 'mediaDevices', { configurable: true, value: { getUserMedia: vi.fn(async () => ({ getTracks: () => [{ stop }] })) } })
		vi.stubGlobal('MediaRecorder', FakeMediaRecorder)
		const wrapper = mount(defineComponent({
			components: { VoiceCaptureButton },
			setup() { const answer = ref(''); const submits = ref(0); return { answer, submits } },
			template: '<form @submit.prevent="submits++"><VoiceCaptureButton session-id="session-1" @transcript="answer=$event"/><textarea v-model="answer"/><button type="submit">提交</button></form>',
		}))
		await wrapper.get('button[aria-label="使用语音回答"]').trigger('click')
		expect(wrapper.text()).toContain('正在录音')
		await wrapper.get('button[aria-label="停止录音"]').trigger('click')
		await flushPromises()
		expect(wrapper.get('textarea').element.value).toBe('我先减去配送费')
		expect((wrapper.vm as unknown as { submits: number }).submits).toBe(0)
		expect(transcribeStudentAudio).toHaveBeenCalledOnce()
		expect(stop).toHaveBeenCalledOnce()
	})
})
