<script setup lang="ts">
import { GraduationCap, Link2, RefreshCw, ShieldCheck, UserPlus, UsersRound } from '@lucide/vue'
import { computed, onMounted, reactive, ref } from 'vue'

import {
  createParentAccount,
  createParentLink,
  createStudentAccount,
  getOwnerAccounts,
  type OwnerAccounts,
} from '../../api/owner'

const accounts = ref<OwnerAccounts>({ students: [], parents: [], links: [] })
const mode = ref<'STUDENT' | 'PARENT'>('STUDENT')
const loading = ref(false)
const acting = ref(false)
const error = ref('')
const success = ref('')
const studentForm = reactive({ display_name: '', email: '', password: '', grade_level: 7 })
const parentForm = reactive({ display_name: '', email: '', password: '', student_id: '' })
const linkForm = reactive({ parent_user_id: '', student_id: '' })

const activeLinks = computed(() => accounts.value.links.filter((link) => link.status === 'ACTIVE'))

function studentName(studentID: string) {
  return accounts.value.students.find((student) => student.student_id === studentID)?.display_name ?? '未知学生'
}

function parentName(parentUserID: string) {
  return accounts.value.parents.find((parent) => parent.user_id === parentUserID)?.display_name ?? '未知家长'
}

function linkedStudents(parentUserID: string) {
  return activeLinks.value.filter((link) => link.parent_user_id === parentUserID).map((link) => studentName(link.student_id))
}

function linkedParents(studentID: string) {
  return activeLinks.value.filter((link) => link.student_id === studentID).map((link) => parentName(link.parent_user_id))
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    accounts.value = await getOwnerAccounts()
    if (!parentForm.student_id && accounts.value.students[0]) parentForm.student_id = accounts.value.students[0].student_id
    if (!linkForm.parent_user_id && accounts.value.parents[0]) linkForm.parent_user_id = accounts.value.parents[0].user_id
    if (!linkForm.student_id && accounts.value.students[0]) linkForm.student_id = accounts.value.students[0].student_id
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '账号数据不可用'
  } finally {
    loading.value = false
  }
}

async function submitStudent() {
  acting.value = true
  error.value = ''
  success.value = ''
  try {
    const created = await createStudentAccount({ ...studentForm })
    success.value = `已创建学生 ${created.display_name}（${created.email}）`
    Object.assign(studentForm, { display_name: '', email: '', password: '', grade_level: 7 })
    await load()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '学生账号创建失败'
  } finally {
    acting.value = false
  }
}

async function submitParent() {
  acting.value = true
  error.value = ''
  success.value = ''
  try {
    const created = await createParentAccount({
      display_name: parentForm.display_name,
      email: parentForm.email,
      password: parentForm.password,
      student_ids: [parentForm.student_id],
    })
    success.value = `已创建家长 ${created.display_name}，并完成孩子绑定`
    Object.assign(parentForm, { display_name: '', email: '', password: '', student_id: accounts.value.students[0]?.student_id ?? '' })
    await load()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '家长账号创建失败'
  } finally {
    acting.value = false
  }
}

async function submitLink() {
  acting.value = true
  error.value = ''
  success.value = ''
  try {
    await createParentLink(linkForm.parent_user_id, linkForm.student_id)
    success.value = `已将 ${parentName(linkForm.parent_user_id)} 绑定到 ${studentName(linkForm.student_id)}`
    await load()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '家庭绑定失败'
  } finally {
    acting.value = false
  }
}

onMounted(load)
</script>

<template>
  <main class="page-wrap pb-28 pt-7">
    <header class="flex items-end justify-between gap-4">
      <div>
        <p class="text-sm text-zinc-500">
          身份与监督关系
        </p>
        <h1 class="mt-1 text-2xl font-semibold">
          账号与家庭
        </h1>
      </div>
      <button
        type="button"
        class="icon-button"
        aria-label="刷新账号列表"
        :disabled="loading"
        @click="load"
      >
        <RefreshCw :size="19" />
      </button>
    </header>

    <div class="mt-7 grid grid-cols-3 border-y border-zinc-300 py-5">
      <div><strong class="text-2xl tabular-nums">{{ accounts.students.length }}</strong><span class="mt-1 block text-xs text-zinc-500">学生</span></div>
      <div><strong class="text-2xl tabular-nums">{{ accounts.parents.length }}</strong><span class="mt-1 block text-xs text-zinc-500">家长</span></div>
      <div><strong class="text-2xl tabular-nums">{{ activeLinks.length }}</strong><span class="mt-1 block text-xs text-zinc-500">有效绑定</span></div>
    </div>

    <section
      class="mt-7"
      aria-labelledby="create-account-title"
    >
      <div class="flex items-center justify-between gap-4">
        <div>
          <h2
            id="create-account-title"
            class="text-lg font-semibold"
          >
            创建账号
          </h2>
          <p class="mt-1 text-sm text-zinc-500">
            密码只在创建时提交，后台不会再次显示。
          </p>
        </div>
        <UserPlus
          :size="20"
          class="shrink-0 text-teal-700"
          aria-hidden="true"
        />
      </div>

      <div
        class="mt-5 grid grid-cols-2 border border-zinc-300 bg-white p-1"
        aria-label="账号类型"
      >
        <button
          type="button"
          class="min-h-10 text-sm font-semibold"
          :class="mode === 'STUDENT' ? 'bg-teal-700 text-white' : 'text-zinc-600'"
          @click="mode = 'STUDENT'"
        >
          学生
        </button>
        <button
          type="button"
          class="min-h-10 text-sm font-semibold"
          :class="mode === 'PARENT' ? 'bg-teal-700 text-white' : 'text-zinc-600'"
          @click="mode = 'PARENT'"
        >
          家长
        </button>
      </div>

      <form
        v-if="mode === 'STUDENT'"
        class="mt-5 grid gap-4 sm:grid-cols-2"
        @submit.prevent="submitStudent"
      >
        <label class="text-sm font-semibold">学生姓名<input
          v-model.trim="studentForm.display_name"
          name="student-display-name"
          required
          maxlength="80"
          class="mt-2 h-12 w-full border border-zinc-300 bg-white px-3 font-normal"
          autocomplete="off"
        ></label>
        <label class="text-sm font-semibold">登录邮箱<input
          v-model.trim="studentForm.email"
          name="student-email"
          required
          type="email"
          maxlength="254"
          class="mt-2 h-12 w-full border border-zinc-300 bg-white px-3 font-normal"
          autocomplete="off"
        ></label>
        <label class="text-sm font-semibold">初始密码<input
          v-model="studentForm.password"
          name="student-password"
          required
          type="password"
          minlength="3"
          maxlength="256"
          class="mt-2 h-12 w-full border border-zinc-300 bg-white px-3 font-normal"
          autocomplete="new-password"
        ></label>
        <label class="text-sm font-semibold">年级<select
          v-model.number="studentForm.grade_level"
          name="student-grade"
          class="mt-2 h-12 w-full border border-zinc-300 bg-white px-3 font-normal"
        ><option
          v-for="grade in 9"
          :key="grade"
          :value="grade"
        >{{ grade <= 6 ? `小学 ${grade} 年级` : `初中 ${grade - 6} 年级` }}</option></select></label>
        <button
          type="submit"
          class="primary-button sm:col-span-2"
          :disabled="acting"
        >
          <GraduationCap :size="18" />创建学生账号
        </button>
      </form>

      <form
        v-else
        class="mt-5 grid gap-4 sm:grid-cols-2"
        @submit.prevent="submitParent"
      >
        <label class="text-sm font-semibold">家长姓名<input
          v-model.trim="parentForm.display_name"
          name="parent-display-name"
          required
          maxlength="80"
          class="mt-2 h-12 w-full border border-zinc-300 bg-white px-3 font-normal"
          autocomplete="off"
        ></label>
        <label class="text-sm font-semibold">登录邮箱<input
          v-model.trim="parentForm.email"
          name="parent-email"
          required
          type="email"
          maxlength="254"
          class="mt-2 h-12 w-full border border-zinc-300 bg-white px-3 font-normal"
          autocomplete="off"
        ></label>
        <label class="text-sm font-semibold">初始密码<input
          v-model="parentForm.password"
          name="parent-password"
          required
          type="password"
          minlength="3"
          maxlength="256"
          class="mt-2 h-12 w-full border border-zinc-300 bg-white px-3 font-normal"
          autocomplete="new-password"
        ></label>
        <label class="text-sm font-semibold">绑定学生<select
          v-model="parentForm.student_id"
          name="parent-student"
          required
          class="mt-2 h-12 w-full border border-zinc-300 bg-white px-3 font-normal"
        ><option
          disabled
          value=""
        >请选择学生</option><option
          v-for="student in accounts.students"
          :key="student.student_id"
          :value="student.student_id"
        >{{ student.display_name }} · {{ student.email }}</option></select></label>
        <p
          v-if="accounts.students.length === 0"
          class="text-sm text-amber-700 sm:col-span-2"
        >
          请先创建学生账号，再创建并绑定家长。
        </p>
        <button
          type="submit"
          class="primary-button sm:col-span-2"
          :disabled="acting || !parentForm.student_id"
        >
          <UsersRound :size="18" />创建家长并绑定
        </button>
      </form>

      <p
        v-if="success"
        class="mt-4 border-l-2 border-emerald-600 pl-3 text-sm text-emerald-700"
        role="status"
      >
        <ShieldCheck
          :size="16"
          class="mr-1 inline"
        />{{ success }}
      </p>
      <p
        v-if="error"
        class="mt-4 border-l-2 border-red-600 pl-3 text-sm text-red-700"
        role="alert"
      >
        {{ error }}
      </p>
    </section>

    <section
      class="mt-9 border-t border-zinc-300 pt-7"
      aria-labelledby="link-title"
    >
      <div class="flex items-center gap-2">
        <Link2
          :size="19"
          class="text-teal-700"
        /><h2
          id="link-title"
          class="text-lg font-semibold"
        >
          追加家庭绑定
        </h2>
      </div>
      <p class="mt-2 text-sm text-zinc-500">
        把已有家长账号绑定到另一位学生，重复绑定不会创建重复关系。
      </p>
      <form
        class="mt-5 grid gap-3 sm:grid-cols-[1fr_1fr_auto]"
        @submit.prevent="submitLink"
      >
        <label class="text-sm font-semibold">家长<select
          v-model="linkForm.parent_user_id"
          required
          class="mt-2 h-12 w-full border border-zinc-300 bg-white px-3 font-normal"
        ><option
          disabled
          value=""
        >请选择家长</option><option
          v-for="parent in accounts.parents"
          :key="parent.user_id"
          :value="parent.user_id"
        >{{ parent.display_name }}</option></select></label>
        <label class="text-sm font-semibold">学生<select
          v-model="linkForm.student_id"
          required
          class="mt-2 h-12 w-full border border-zinc-300 bg-white px-3 font-normal"
        ><option
          disabled
          value=""
        >请选择学生</option><option
          v-for="student in accounts.students"
          :key="student.student_id"
          :value="student.student_id"
        >{{ student.display_name }}</option></select></label>
        <button
          type="submit"
          class="primary-button self-end"
          :disabled="acting || !linkForm.parent_user_id || !linkForm.student_id"
        >
          <Link2 :size="17" />绑定
        </button>
      </form>
    </section>

    <section
      class="mt-9 border-t border-zinc-300 pt-7"
      aria-labelledby="students-title"
    >
      <h2
        id="students-title"
        class="text-lg font-semibold"
      >
        学生账号
      </h2>
      <div class="mt-4 divide-y divide-zinc-200 border-y border-zinc-300">
        <article
          v-for="student in accounts.students"
          :key="student.student_id"
          class="py-5"
        >
          <div class="flex items-start justify-between gap-4">
            <div class="min-w-0">
              <h3 class="font-semibold">
                {{ student.display_name }}
              </h3><p class="mt-1 break-all text-sm text-zinc-500">
                {{ student.email }}
              </p>
            </div><span class="shrink-0 text-sm font-semibold text-teal-800">{{ student.grade_level <= 6 ? `小${student.grade_level}` : `初${student.grade_level - 6}` }}</span>
          </div>
          <p class="mt-3 text-sm text-zinc-600">
            家长：{{ linkedParents(student.student_id).join('、') || '尚未绑定' }}
          </p>
        </article>
        <p
          v-if="!loading && accounts.students.length === 0"
          class="py-8 text-center text-sm text-zinc-500"
        >
          暂无学生账号
        </p>
      </div>
    </section>

    <section
      class="mt-9 border-t border-zinc-300 pt-7"
      aria-labelledby="parents-title"
    >
      <h2
        id="parents-title"
        class="text-lg font-semibold"
      >
        家长账号
      </h2>
      <div class="mt-4 divide-y divide-zinc-200 border-y border-zinc-300">
        <article
          v-for="parent in accounts.parents"
          :key="parent.user_id"
          class="py-5"
        >
          <div class="flex items-start justify-between gap-4">
            <div class="min-w-0">
              <h3 class="font-semibold">
                {{ parent.display_name }}
              </h3><p class="mt-1 break-all text-sm text-zinc-500">
                {{ parent.email }}
              </p>
            </div><span class="shrink-0 text-xs font-semibold text-zinc-500">PARENT</span>
          </div>
          <p class="mt-3 text-sm text-zinc-600">
            孩子：{{ linkedStudents(parent.user_id).join('、') || '尚未绑定' }}
          </p>
        </article>
        <p
          v-if="!loading && accounts.parents.length === 0"
          class="py-8 text-center text-sm text-zinc-500"
        >
          暂无家长账号
        </p>
      </div>
    </section>
  </main>
</template>
