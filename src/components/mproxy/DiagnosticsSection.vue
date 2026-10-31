<template>
  <div class="flex flex-col gap-3">
    <SectionCard
      title="诊断"
      description="快速定位后端与内核状态，必要时下载诊断包反馈问题。"
    >
      <div class="grid grid-cols-1 gap-2 text-sm">
        <div class="bg-base-200 flex items-center justify-between gap-2 rounded-lg px-3 py-2.5">
          <div class="min-w-0">
            <div class="text-base-content/50 text-[11px]">面板地址</div>
            <div class="truncate font-mono text-xs">{{ panelUrl || '未获取' }}</div>
          </div>
          <button
            class="btn btn-ghost btn-xs"
            :disabled="!panelUrl"
            @click="copy(panelUrl)"
          >
            复制
          </button>
        </div>

        <div class="grid grid-cols-2 gap-2 sm:grid-cols-3">
          <div class="bg-base-200 rounded-lg p-3">
            <div class="text-base-content/50 text-[11px]">后端版本</div>
            <div class="mt-0.5 truncate font-medium">{{ status?.backend.version || '-' }}</div>
          </div>
          <div class="bg-base-200 rounded-lg p-3">
            <div class="text-base-content/50 text-[11px]">设备</div>
            <div class="mt-0.5 truncate font-medium">
              {{ status?.system.model || '-' }}
            </div>
          </div>
          <div class="bg-base-200 rounded-lg p-3">
            <div class="text-base-content/50 text-[11px]">Android / 内核</div>
            <div class="mt-0.5 truncate font-medium">
              {{ status?.system.android || '-' }}
            </div>
          </div>
          <div class="bg-base-200 rounded-lg p-3">
            <div class="text-base-content/50 text-[11px]">CPU 架构</div>
            <div class="mt-0.5 truncate font-medium">{{ status?.system.arch || '-' }}</div>
          </div>
          <div class="bg-base-200 rounded-lg p-3">
            <div class="text-base-content/50 text-[11px]">内存</div>
            <div class="mt-0.5 truncate font-medium">
              {{ formatBytes(status?.system.memFree) }} / {{ formatBytes(status?.system.memTotal) }}
            </div>
          </div>
          <div class="bg-base-200 rounded-lg p-3">
            <div class="text-base-content/50 text-[11px]">数据目录</div>
            <div class="mt-0.5 truncate font-medium">{{ status?.backend.dataDir || '-' }}</div>
          </div>
        </div>

        <div class="mt-1 flex flex-wrap gap-2">
          <button
            class="btn btn-primary btn-sm"
            :disabled="downloading"
            @click="downloadDiagnostics"
          >
            {{ downloading ? '打包中…' : '下载诊断包' }}
          </button>
          <button
            class="btn btn-sm"
            :disabled="loading"
            @click="loadDiagnostics"
          >
            {{ loading ? '读取中…' : '刷新诊断信息' }}
          </button>
        </div>

        <div
          v-if="message"
          class="alert mt-1 py-2 text-xs"
          :class="messageType === 'error' ? 'alert-error' : 'alert-success'"
        >
          {{ message }}
        </div>

        <details
          v-if="diagnostics"
          class="bg-base-200 rounded-lg p-3"
        >
          <summary class="cursor-pointer text-xs font-medium">诊断详情</summary>
          <pre class="scrollbar-thin mt-2 max-h-64 overflow-auto text-[11px] whitespace-pre-wrap">{{ diagnosticsText }}</pre>
        </details>
      </div>
    </SectionCard>

    <SectionCard
      title="文档与帮助"
      description="MihomoProxy Evolution 只维护一份 config.yaml，所有设置最终都会写回该文件。"
    >
      <ul class="list-disc space-y-1.5 pl-5 text-sm">
        <li>
          <a
            class="link link-primary"
            href="https://wiki.metacubex.one/"
            target="_blank"
            rel="noreferrer"
          >mihomo 配置文档</a>
        </li>
        <li>
          <a
            class="link link-primary"
            href="https://kernelsu.org/"
            target="_blank"
            rel="noreferrer"
          >KernelSU 官方文档</a>
        </li>
        <li>
          <a
            class="link link-primary"
            href="https://github.com/MetaCubeX/mihomo/releases"
            target="_blank"
            rel="noreferrer"
          >mihomo 版本发布</a>
        </li>
      </ul>
    </SectionCard>
  </div>
</template>

<script setup lang="ts">
import { mproxyApi, type DiagnosticsResponse, type MProxyStatus } from '@/api/mproxy'
import SectionCard from '@/components/mproxy/SectionCard.vue'
import { computed, onMounted, ref } from 'vue'

const props = defineProps<{
  status: MProxyStatus | null
}>()

const metaUrl = ref('')
const diagnostics = ref<DiagnosticsResponse | null>(null)
const loading = ref(false)
const downloading = ref(false)
const message = ref('')
const messageType = ref<'success' | 'error'>('success')

const panelUrl = computed(() => metaUrl.value)

const diagnosticsText = computed(() =>
  diagnostics.value ? JSON.stringify(diagnostics.value, null, 2) : '',
)

const setMessage = (text: string, type: 'success' | 'error' = 'success') => {
  message.value = text
  messageType.value = type
}

const formatBytes = (bytes?: number) => {
  if (!bytes || !Number.isFinite(bytes)) return '-'
  const units = ['B', 'KiB', 'MiB', 'GiB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`
}

const copy = async (text: string) => {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    setMessage('已复制到剪贴板')
  } catch {
    setMessage('复制失败，请长按手动复制', 'error')
  }
}

const loadMeta = async () => {
  try {
    const meta = await mproxyApi.meta()
    metaUrl.value = meta.url || `${window.location.origin}/`
  } catch {
    metaUrl.value = `${window.location.origin}/`
  }
}

const loadDiagnostics = async () => {
  loading.value = true
  try {
    diagnostics.value = await mproxyApi.getDiagnostics()
  } catch (error) {
    setMessage(
      `读取诊断信息失败：${error instanceof Error ? error.message : String(error)}`,
      'error',
    )
  } finally {
    loading.value = false
  }
}

const downloadDiagnostics = async () => {
  downloading.value = true
  try {
    await mproxyApi.downloadDiagnostics()
    setMessage('诊断包已开始下载')
  } catch (error) {
    setMessage(
      `下载失败：${error instanceof Error ? error.message : String(error)}`,
      'error',
    )
  } finally {
    downloading.value = false
  }
}

onMounted(() => {
  loadMeta()
  loadDiagnostics()
})
</script>
