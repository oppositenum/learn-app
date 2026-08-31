<script setup lang="ts">
import { CalendarDays, CircleDollarSign, Library, UserRound, UsersRound } from '@lucide/vue'
</script>
<template>
  <div class="owner-shell min-h-svh bg-[#f4f4f2] text-zinc-900">
    <RouterView /><nav
      class="bottom-nav"
      aria-label="Owner 导航"
    >
      <RouterLink
        to="/admin/account"
        class="nav-item"
      >
        <UserRound :size="20" /><span>账户</span>
      </RouterLink><RouterLink
        to="/admin/content"
        class="nav-item"
      >
        <Library :size="20" /><span>内容</span>
      </RouterLink><RouterLink
        to="/admin/accounts"
        class="nav-item"
      >
        <UsersRound :size="20" /><span>账号</span>
      </RouterLink><RouterLink
        to="/admin/trial"
        class="nav-item"
      >
        <CalendarDays :size="20" /><span>试用</span>
      </RouterLink><RouterLink
        to="/admin/cost"
        class="nav-item"
      >
        <CircleDollarSign :size="20" /><span>成本</span>
      </RouterLink>
    </nav>
  </div>
</template>
