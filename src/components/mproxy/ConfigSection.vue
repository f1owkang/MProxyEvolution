<template>
  <div class="flex flex-col gap-3">
    <SectionCard
      :title="t('mproxyConfig')"
      :description="t('mproxyConfigDesc')"
    >
      <template #actions>
        <button
          class="btn btn-sm"
          :disabled="loading"
          @click="load"
        >
          {{ loading ? t('mproxyLoading') : t('mproxyReloadConfig') }}
        </button>
        <button
          class="btn btn-sm"
          :disabled="saving || !yamlText"
          @click="validate"
        >
          {{ t('mproxyValidate') }}
        </button>
        <button
          class="btn btn-primary btn-sm"
          :disabled="saving || !yamlText"
          @click="saveAndApply"
        >
          {{ saving ? t('mproxyApplying') : t('mproxySaveApply') }}
        </button>
      </template>

      <div
        class="meta-row text-base-content/55 mb-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs"
      >
        <span>{{ t('mproxyLabelPath') }}：{{ config?.path || status?.config.path || '-' }}</span>
        <span>
          {{ t('mproxyLabelValid') }}：
          <span :class="config?.valid ? 'text-success' : 'text-error'">
            {{ config?.valid ? t('mproxyValidPass') : t('mproxyValidFail') }}
          </span>
        </span>
        <span>{{ t('mproxyLabelSize') }}：{{ prettySize(status?.config.size) }}</span>
        <span v-if="config?.modifiedAt">{{
          t('mproxyUpdatedAt', { time: formatTime(config.modifiedAt) })
        }}</span>
      </div>

      <textarea
        v-model="yamlText"
        class="textarea w-full scrollbar-thin font-mono text-xs leading-5"
        rows="18"
        spellcheck="false"
        :placeholder="t('mproxyConfigLoadingPlaceholder')"
        :disabled="loading"
      />

      <div
        v-if="validation"
        class="mt-2"
      >
        <div
          class="text-xs font-medium"
          :class="validation.ok ? 'text-success' : 'text-error'"
        >
          {{ validation.ok ? t('mproxyValidationOk') : t('mproxyValidationFail') }}
        </div>
        <pre
          v-if="validation.output"
          class="bg-base-200 mt-1 max-h-40 overflow-auto rounded-lg p-2 text-[11px] whitespace-pre-wrap"
          >{{ validation.output }}</pre>
      </div>

      <div
        v-if="message"
        class="alert mt-2 py-2 text-xs"
        :class="messageType === 'error' ? 'alert-error' : 'alert-success'"
      >
        {{ message }}
      </div>

      <div class="mt-3 flex flex-wrap gap-2">
        <button
          class="btn btn-sm"
          :disabled="busy"
          @click="exportConfig"
        >
          {{ t('mproxyExportConfig') }}
        </button>
        <button
          class="btn btn-sm"
          :disabled="busy"
          @click="backupConfig"
        >
          {{ t('mproxyDownloadBackup') }}
        </button>
        <button
          class="btn btn-sm"
          :disabled="busy"
          @click="restoreConfig"
        >
          {{ t('mproxyRestoreBackup') }}
        </button>
        <button
          class="btn btn-error btn-outline btn-sm"
          :disabled="busy"
          @click="resetConfig"
        >
          {{ t('mproxyResetConfig') }}
        </button>
      </div>
    </SectionCard>
  </div>
</template>

<script setup lang="ts">
import {
  mproxyApi,
  type ConfigResponse,
  type MProxyStatus,
  type ValidateConfigResponse,
} from '@/api/mproxy'
import SectionCard from '@/components/mproxy/SectionCard.vue'
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

defineProps<{
  status: MProxyStatus | null
}>()

const emit = defineEmits<{
  changed: []
}>()

const config = ref<ConfigResponse | null>(null)
const yamlText = ref('')
const validation = ref<ValidateConfigResponse | null>(null)
const loading = ref(false)
const saving = ref(false)
const busy = ref(false)
const message = ref('')
const messageType = ref<'success' | 'error'>('success')

const prettySize = (size?: number) => {
  if (!size || !Number.isFinite(size)) return '-'
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KiB`
  return `${(size / 1024 / 1024).toFixed(2)} MiB`
}

const formatTime = (value: string) => {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

const setMessage = (text: string, type: 'success' | 'error' = 'success') => {
  message.value = text
  messageType.value = type
}

const load = async () => {
  loading.value = true
  message.value = ''
  try {
    const data = await mproxyApi.getConfig()
    config.value = data
    yamlText.value = data.yaml
    validation.value = null
  } catch (error) {
    setMessage(
      t('mproxyConfigLoadError', {
        error: error instanceof Error ? error.message : String(error),
      }),
      'error',
    )
  } finally {
    loading.value = false
  }
}

const validate = async () => {
  saving.value = true
  message.value = ''
  try {
    validation.value = await mproxyApi.validateConfig(yamlText.value)
    if (!validation.value.ok) {
      setMessage(t('mproxyConfigValidationMessage'), 'error')
    }
  } catch (error) {
    setMessage(
      t('mproxyConfigValidateError', {
        error: error instanceof Error ? error.message : String(error),
      }),
      'error',
    )
  } finally {
    saving.value = false
  }
}

const saveAndApply = async () => {
  saving.value = true
  message.value = ''
  try {
    const result = await mproxyApi.saveConfig(yamlText.value, 'auto')
    validation.value = result.validationOutput
      ? { ok: result.ok, output: result.validationOutput }
      : null
    config.value = await mproxyApi.getConfig()
    yamlText.value = config.value.yaml
    setMessage(
      result.restarted
        ? t('mproxyConfigSavedRestarted')
        : result.applied
          ? t('mproxyConfigSavedReloaded')
          : t('mproxyConfigSaved'),
    )
    emit('changed')
  } catch (error) {
    setMessage(
      t('mproxyConfigSaveError', {
        error: error instanceof Error ? error.message : String(error),
      }),
      'error',
    )
  } finally {
    saving.value = false
  }
}

const runDownload = async (fn: () => Promise<void>, label: string) => {
  busy.value = true
  try {
    await fn()
    setMessage(t('mproxyDownloadStarted', { label }))
  } catch (error) {
    setMessage(
      t('mproxyDownloadFailed', {
        label,
        error: error instanceof Error ? error.message : String(error),
      }),
      'error',
    )
  } finally {
    busy.value = false
  }
}

const exportConfig = () => runDownload(mproxyApi.exportConfig, t('mproxyLabelExport'))
const backupConfig = () => runDownload(mproxyApi.backupConfig, t('mproxyLabelBackup'))

const restoreConfig = async () => {
  if (!window.confirm(t('mproxyRestoreConfirm'))) return
  busy.value = true
  try {
    await mproxyApi.restoreConfig()
    await load()
    setMessage(t('mproxyRestored'))
    emit('changed')
  } catch (error) {
    setMessage(
      t('mproxyRestoreError', {
        error: error instanceof Error ? error.message : String(error),
      }),
      'error',
    )
  } finally {
    busy.value = false
  }
}

const resetConfig = async () => {
  if (!window.confirm(t('mproxyResetConfirm'))) return
  busy.value = true
  try {
    await mproxyApi.resetConfig()
    await load()
    setMessage(t('mproxyResetDone'))
    emit('changed')
  } catch (error) {
    setMessage(
      t('mproxyResetError', {
        error: error instanceof Error ? error.message : String(error),
      }),
      'error',
    )
  } finally {
    busy.value = false
  }
}

onMounted(load)
</script>
