<template>
  <div class="flex flex-col gap-3">
    <SectionCard
      :title="t('mproxyDiagnostics')"
      :description="t('mproxyDiagnosticsDesc')"
    >
      <div class="grid grid-cols-1 gap-2 text-sm">
        <div class="bg-base-200 flex items-center justify-between gap-2 rounded-lg px-3 py-2.5">
          <div class="min-w-0">
            <div class="text-base-content/50 text-[11px]">{{ t('mproxyLabelPanelUrl') }}</div>
            <div class="truncate font-mono text-xs">
              {{ panelUrl || t('mproxyNotObtained') }}
            </div>
          </div>
          <button
            class="btn btn-ghost btn-xs"
            :disabled="!panelUrl"
            @click="copy(panelUrl)"
          >
            {{ t('mproxyCopy') }}
          </button>
        </div>

        <div class="grid grid-cols-2 gap-2 sm:grid-cols-3">
          <div class="bg-base-200 rounded-lg p-3">
            <div class="text-base-content/50 text-[11px]">
              {{ t('mproxyLabelBackendVersion') }}
            </div>
            <div class="mt-0.5 truncate font-medium">{{ status?.backend.version || '-' }}</div>
          </div>
          <div class="bg-base-200 rounded-lg p-3">
            <div class="text-base-content/50 text-[11px]">{{ t('mproxyLabelDevice') }}</div>
            <div class="mt-0.5 truncate font-medium">
              {{ status?.system.model || '-' }}
            </div>
          </div>
          <div class="bg-base-200 rounded-lg p-3">
            <div class="text-base-content/50 text-[11px]">
              {{ t('mproxyLabelAndroidKernel') }}
            </div>
            <div class="mt-0.5 truncate font-medium">
              {{ status?.system.android || '-' }}
            </div>
          </div>
          <div class="bg-base-200 rounded-lg p-3">
            <div class="text-base-content/50 text-[11px]">{{ t('mproxyLabelCpuArch') }}</div>
            <div class="mt-0.5 truncate font-medium">{{ status?.system.arch || '-' }}</div>
          </div>
          <div class="bg-base-200 rounded-lg p-3">
            <div class="text-base-content/50 text-[11px]">{{ t('mproxyLabelMemory') }}</div>
            <div class="mt-0.5 truncate font-medium">
              {{ formatBytes(status?.system.memFree) }} / {{ formatBytes(status?.system.memTotal) }}
            </div>
          </div>
          <div class="bg-base-200 rounded-lg p-3">
            <div class="text-base-content/50 text-[11px]">{{ t('mproxyLabelDataDir') }}</div>
            <div class="mt-0.5 truncate font-medium">{{ status?.backend.dataDir || '-' }}</div>
          </div>
        </div>

        <div class="mt-1 flex flex-wrap gap-2">
          <button
            class="btn btn-primary btn-sm"
            :disabled="downloading"
            @click="downloadDiagnostics"
          >
            {{ downloading ? t('mproxyDiagnosticsPacking') : t('mproxyDownloadDiagnostics') }}
          </button>
          <button
            class="btn btn-sm"
            :disabled="loading"
            @click="loadDiagnostics"
          >
            {{ loading ? t('mproxyDiagnosticsReading') : t('mproxyRefreshDiagnostics') }}
          </button>
        </div>

        <div
          v-if="message"
          class="alert mt-1 py-2 text-xs"
          :class="messageType === 'error' ? 'alert-error' : 'alert-success'"
        >
          {{ message }}
        </div>

        <div
          v-if="diagnostics?.checks?.length"
          class="mt-1 grid grid-cols-1 gap-2 sm:grid-cols-2"
        >
          <div
            v-for="check in diagnostics.checks"
            :key="check.name"
            class="bg-base-200 rounded-lg px-3 py-2.5"
          >
            <div class="flex items-center gap-1.5 text-xs font-medium">
              <span
                class="inline-block size-2 shrink-0 rounded-full"
                :class="check.ok ? 'bg-success' : 'bg-error'"
              />
              {{ check.name }}
            </div>
            <div class="text-base-content/60 mt-1 font-mono text-[11px] break-all">
              {{ check.detail }}
            </div>
          </div>
        </div>

        <details
          v-if="diagnostics"
          class="bg-base-200 rounded-lg p-3"
        >
          <summary class="cursor-pointer text-xs font-medium">
            {{ t('mproxyDiagnosticsDetails') }}
          </summary>
          <pre class="mt-2 max-h-64 scrollbar-thin overflow-auto text-[11px] whitespace-pre-wrap">{{
            diagnosticsText
          }}</pre>
        </details>
      </div>
    </SectionCard>

    <SectionCard
      :title="t('mproxyDocsTitle')"
      :description="t('mproxyDocsDesc')"
    >
      <ul class="list-disc space-y-1.5 pl-5 text-sm">
        <li>
          <a
            class="link link-primary"
            href="https://wiki.metacubex.one/"
            target="_blank"
            rel="noreferrer"
            >{{ t('mproxyDocsMihomoConfig') }}</a
          >
        </li>
        <li>
          <a
            class="link link-primary"
            href="https://kernelsu.org/"
            target="_blank"
            rel="noreferrer"
            >{{ t('mproxyDocsKernelSU') }}</a
          >
        </li>
        <li>
          <a
            class="link link-primary"
            href="https://github.com/MetaCubeX/mihomo/releases"
            target="_blank"
            rel="noreferrer"
            >{{ t('mproxyDocsMihomoReleases') }}</a
          >
        </li>
      </ul>
    </SectionCard>
  </div>
</template>

<script setup lang="ts">
import { mproxyApi, type DiagnosticsResponse, type MProxyStatus } from '@/api/mproxy'
import SectionCard from '@/components/mproxy/SectionCard.vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

defineProps<{
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
    setMessage(t('mproxyDiagnosticsCopied'))
  } catch {
    setMessage(t('mproxyCopyFailed'), 'error')
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
      t('mproxyDiagnosticsReadError', {
        error: error instanceof Error ? error.message : String(error),
      }),
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
    setMessage(t('mproxyDiagnosticsDownloadStarted'))
  } catch (error) {
    setMessage(
      t('mproxyDiagnosticsDownloadError', {
        error: error instanceof Error ? error.message : String(error),
      }),
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
