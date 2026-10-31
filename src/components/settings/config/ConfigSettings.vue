<template>
  <div class="flex flex-col gap-4">
    <ConfigSection
      :status="status"
      @changed="reload"
    />
    <SubscriptionSection @changed="reload" />
  </div>
</template>

<script setup lang="ts">
import { mproxyApi, type MProxyStatus } from '@/api/mproxy'
import ConfigSection from '@/components/mproxy/ConfigSection.vue'
import SubscriptionSection from '@/components/mproxy/SubscriptionSection.vue'
import { onMounted, onUnmounted, ref } from 'vue'

const status = ref<MProxyStatus | null>(null)
const error = ref('')

const load = async () => {
  try {
    status.value = await mproxyApi.status()
    error.value = ''
  } catch (e) {
    error.value = `无法连接 MihomoProxy Evolution 后端：${e instanceof Error ? e.message : String(e)}`
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
