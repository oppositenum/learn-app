<script setup lang="ts">
import { BookOpen, Home, Sparkles, UserRound } from '@lucide/vue'
</script>

<template>
  <div class="student-shell min-h-svh bg-[#f5f5f1] text-zinc-900">
    <RouterView />
    <nav
      class="bottom-nav"
      aria-label="学生导航"
    >
      <RouterLink
        to="/student"
        class="nav-item"
      >
        <Home
          :size="20"
          aria-hidden="true"
        />
        <span>首页</span>
      </RouterLink>
      <RouterLink
        to="/student"
        class="nav-item"
      >
        <BookOpen
          :size="20"
          aria-hidden="true"
        />
        <span>学习</span>
      </RouterLink>
      <RouterLink
        to="/student/growth"
        class="nav-item"
      >
        <Sparkles
          :size="20"
          aria-hidden="true"
        />
        <span>成长</span>
      </RouterLink>
      <RouterLink
        to="/student/profile"
        class="nav-item"
      >
        <UserRound
          :size="20"
          aria-hidden="true"
        />
        <span>我的</span>
      </RouterLink>
    </nav>
  </div>
</template>
