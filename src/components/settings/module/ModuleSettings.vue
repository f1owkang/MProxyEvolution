<template>
  <div class="flex flex-col gap-4">
    <div
      v-if="error"
      class="alert alert-error py-2 text-xs"
    >
      <span class="min-w-0 flex-1">{{ error }}</span>
      <button
        class="btn btn-ghost btn-xs"
        @click="reload"
      >
        重试
      </button>
    </div>

    <CoreSection
      :status="status"
      :settings="settings"
      @changed="reload"
    />
    <DiagnosticsSection :status="status" />
  </div>
</template>

<script setup lang="ts">
import { mproxyApi, type MProxySettings, type MProxyStatus } from '@/api/mproxy'
import CoreSection from '@/components/mproxy/CoreSection.vue'
import DiagnosticsSection from '@/components/mproxy/DiagnosticsSection.vue'
import { onMounted, onUnmounted, ref } from 'vue'

const status = ref<MProxyStatus | null>(null)
const settings = ref<MProxySettings | null>(null)
const error = ref('')

const load = async () => {
  try {
    status.value = await mproxyApi.status()
    error.value = ''
  } catch (e) {
    error.value = `无法连接 MProxy 后端：${e instanceof Error ? e.message : String(e)}`
    return
  }
  try {
    settings.value = await mproxyApi.getSettings()
  } catch {
    // 设置接口失败不阻塞其它区域
  }
}

const reload = () => {
  void load()
}

let timer: ReturnType<typeof setInterval> | undefined

onMounted(() => {
  void load()
  timer = setInterval(() => {
    if (document.visibilityState === 'visible') void load()
  }, 10_000)
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>
