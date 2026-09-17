<script setup lang="ts">
import { ArrowRight, LockKeyhole } from '@lucide/vue'
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { roleHome, useAuthSession } from '../stores/auth'

const auth = useAuthSession()
const route = useRoute()
const router = useRouter()
const email = ref('')
const password = ref('')
const busy = ref(false)
const error = ref('')

async function submit() {
  busy.value = true
  error.value = ''
  try {
    const user = await auth.login(email.value.trim(), password.value)
    const requested = typeof route.query.redirect === 'string' ? route.query.redirect : ''
    await router.replace(requested.startsWith(roleHome(user.role)) ? requested : roleHome(user.role))
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '登录暂时不可用'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="grid min-h-svh place-items-center bg-[var(--surface)] px-4 py-10">
    <section
      class="w-full max-w-sm"
      aria-labelledby="login-title"
    >
      <div class="flex size-11 items-center justify-center rounded-[var(--radius)] bg-[var(--accent)] text-white shadow-[var(--lift-sm)]">
        <LockKeyhole
          :size="21"
          aria-hidden="true"
        />
      </div>
      <p class="mt-7 text-sm font-semibold text-teal-800">
        AI Learning Tutor
      </p>
      <h1
        id="login-title"
        class="mt-1 text-3xl font-semibold"
      >
        进入学习空间
      </h1>
      <form
        class="mt-8"
        @submit.prevent="submit"
      >
        <p
          v-if="error"
          class="mb-4 rounded-[var(--radius-sm)] bg-red-50 px-3 py-2.5 text-sm text-red-700 shadow-[inset_3px_0_0_#dc2626]"
          role="alert"
        >
          {{ error }}
        </p>
        <label
          for="login-email"
          class="detail-label"
        >邮箱</label>
        <input
          id="login-email"
          v-model="email"
          type="email"
          autocomplete="username"
          required
          class="answer-field mt-2 h-12 px-3.5 py-0"
        >
        <label
          for="login-password"
          class="detail-label mt-5 block"
        >密码</label>
        <input
          id="login-password"
          v-model="password"
          type="password"
          autocomplete="current-password"
          required
          minlength="3"
          class="answer-field mt-2 h-12 px-3.5 py-0"
        >
        <button
          type="submit"
          class="primary-button mt-7 w-full"
          :disabled="busy"
        >
          {{ busy ? '正在验证' : '登录' }}<ArrowRight
            :size="18"
            aria-hidden="true"
          />
        </button>
      </form>
    </section>
  </main>
</template>
