<script setup lang="ts">
import { LogOut, UserRound } from '@lucide/vue'
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useAuthSession } from '../stores/auth'

const auth = useAuthSession()
const user = auth.user
const router = useRouter()
const route = useRoute()
// Parents and the Owner share this page. Only the student view is raised to the
// 16px text and 48px tap size used across the student screens.
const studentView = computed(() => route.path.startsWith('/student'))

async function signOut() {
  await auth.logout()
  await router.replace('/login')
}
</script>

<template>
  <main class="page-wrap pb-28 pt-8">
    <UserRound
      :size="30"
      class="text-teal-700"
      aria-hidden="true"
    />
    <h1 class="mt-4 text-2xl font-semibold">
      {{ user?.display_name }}
    </h1>
    <p
      data-testid="account-role"
      class="mt-1 text-zinc-500"
      :class="studentView ? 'text-base' : 'text-sm'"
    >
      {{ user?.role }}
    </p>
    <div class="mt-8 border-t border-zinc-300 pt-6">
      <button
        type="button"
        data-testid="account-sign-out"
        class="secondary-button w-full"
        :class="{ 'home-action': studentView }"
        @click="signOut"
      >
        <LogOut
          :size="18"
          aria-hidden="true"
        />退出当前账户
      </button>
    </div>
  </main>
</template>
