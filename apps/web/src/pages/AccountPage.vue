<script setup lang="ts">
import { LogOut, UserRound } from '@lucide/vue'
import { useRouter } from 'vue-router'

import { useAuthSession } from '../stores/auth'

const auth = useAuthSession()
const user = auth.user
const router = useRouter()

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
    <p class="mt-1 text-sm text-zinc-500">
      {{ user?.role }}
    </p>
    <div class="mt-8 border-t border-zinc-300 pt-6">
      <button
        type="button"
        class="secondary-button w-full"
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
