<template>
  <div>
    <div class="mb-2 flex flex-wrap items-center justify-between gap-2">
      <span class="text-base-content/50 text-xs">
        {{ t('mproxyLogsRecent', { lines }) }}
        <template v-if="updatedAt"> · {{ updatedAt }}</template>
      </span>
      <div class="flex items-center gap-1">
        <button
          class="btn btn-ghost btn-xs"
          :disabled="loading"
          @click="refresh"
        >
          {{ loading ? t('mproxyRefreshing') : t('mproxyRefresh') }}
        </button>
        <button
          v-if="clearable"
          class="btn btn-ghost btn-xs"
          :disabled="loading || !content"
          @click="clear"
        >
          {{ t('mproxyClear') }}
        </button>
        <button
          class="btn btn-ghost btn-xs"
          :disabled="!content"
          @click="copy"
        >
          {{ copied ? t('mproxyCopied') : t('mproxyCopy') }}
        </button>
      </div>
    </div>
    <pre
      class="bg-base-200 max-h-64 scrollbar-thin overflow-auto rounded-lg p-3 text-[11px] leading-4 whitespace-pre-wrap"
      >{{ content || t('mproxyLogsEmpty') }}</pre>
  </div>
</template>

<script setup lang="ts">
import { mproxyApi, normalizeLogLines } from '@/api/mproxy'
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const props = withDefaults(
  defineProps<{
    source?: 'core' | 'backend'
    lines?: number
    clearable?: boolean
    autoRefresh?: boolean
  }>(),
  {
    source: 'core',
    lines: 300,
    clearable: false,
    autoRefresh: true,
  },
)

const content = ref('')
const loading = ref(false)
const copied = ref(false)
const updatedAt = ref('')
let timer: ReturnType<typeof setInterval> | undefined

const refresh = async () => {
  loading.value = true
  try {
    const response =
      props.source === 'core'
        ? await mproxyApi.getCoreLogs(props.lines)
        : await mproxyApi.getBackendLogs(props.lines)
    content.value = normalizeLogLines(response).join('\n').trimEnd()
    updatedAt.value = new Date().toLocaleTimeString()
  } catch (error) {
    content.value = t('mproxyLogsReadError', {
      error: error instanceof Error ? error.message : String(error),
    })
  } finally {
    loading.value = false
  }
}

const clear = async () => {
  if (props.source !== 'core') return
  try {
    await mproxyApi.clearCoreLogs()
    content.value = ''
    updatedAt.value = new Date().toLocaleTimeString()
  } catch (error) {
    content.value = t('mproxyLogsClearError', {
      error: error instanceof Error ? error.message : String(error),
    })
  }
}

const copy = async () => {
  try {
    await navigator.clipboard.writeText(content.value)
    copied.value = true
    setTimeout(() => (copied.value = false), 1500)
  } catch {
    // ignore clipboard failures (non-secure contexts)
  }
}

onMounted(() => {
  refresh()
  if (props.autoRefresh) {
    timer = setInterval(refresh, 5000)
  }
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>
