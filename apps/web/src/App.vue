<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { router } from './routes'
import { authChangeStorageKey, roleHome, useAuthSession } from './stores/auth'

const auth = useAuthSession()
const user = auth.user
let externalSync: Promise<void> | null = null
let externalSyncPending = false
const authSyncing = ref(false)

function requiredRole() {
  return router.currentRoute.value.matched.map((record) => record.meta.role).find(Boolean)
}

function reconcileExternalAuth() {
	externalSyncPending = true
  authSyncing.value = true
  if (!externalSync) {
    externalSync = (async () => {
      while (externalSyncPending) {
        externalSyncPending = false
        const previousUserID = user.value?.user_id
        let current
        try {
          current = await auth.refresh()
        } catch {
          await router.replace('/login')
          continue
        }
        if (!current) {
          if (router.currentRoute.value.path !== '/login') {
            await router.replace({ path: '/login', query: { redirect: router.currentRoute.value.fullPath } })
          }
          continue
        }
        if (previousUserID !== current.user_id || requiredRole() !== current.role || router.currentRoute.value.path === '/login') {
          await router.replace(roleHome(current.role))
        }
      }
    })().finally(() => {
      externalSync = null
      if (externalSyncPending) void reconcileExternalAuth()
      else authSyncing.value = false
    })
  }
  return externalSync
}

function handleAuthStorage(event: StorageEvent) {
  if (event.key === authChangeStorageKey && event.newValue) {
    auth.invalidateExternalChange()
    void reconcileExternalAuth()
  }
}

onMounted(() => window.addEventListener('storage', handleAuthStorage))
onBeforeUnmount(() => window.removeEventListener('storage', handleAuthStorage))
</script>

<template>
  <RouterView
    v-if="!authSyncing"
    v-slot="{ Component }"
  >
    <Transition
      name="page"
      mode="out-in"
    >
      <component
        :is="Component"
        :key="user?.user_id ?? 'anonymous'"
      />
    </Transition>
  </RouterView>
</template>
