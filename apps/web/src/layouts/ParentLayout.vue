<script setup lang="ts">
import { ChartNoAxesCombined, ClipboardList, Home, SlidersHorizontal } from '@lucide/vue'
</script>

<template>
  <div class="parent-shell min-h-svh bg-[#f3f4f2] text-zinc-900">
    <RouterView />
    <nav
      class="bottom-nav"
      aria-label="家长导航"
    >
      <RouterLink
        to="/parent"
        class="nav-item"
      >
        <Home
          :size="20"
          aria-hidden="true"
        />
        <span>首页</span>
      </RouterLink>
      <RouterLink
        to="/parent/ability"
        class="nav-item"
      >
        <ChartNoAxesCombined
          :size="20"
          aria-hidden="true"
        />
        <span>能力</span>
      </RouterLink>
      <RouterLink
        to="/parent/report"
        class="nav-item"
      >
        <ClipboardList
          :size="20"
          aria-hidden="true"
        />
        <span>报告</span>
      </RouterLink>
      <RouterLink
        to="/parent/settings"
        class="nav-item"
      >
        <SlidersHorizontal
          :size="20"
          aria-hidden="true"
        />
        <span>设置</span>
      </RouterLink>
    </nav>
  </div>
</template>
