<template>
  <div class="flex flex-col gap-3">
    <SectionCard
      title="配置"
      description="唯一配置文件 config.yaml。修改后先校验，再保存并应用。"
    >
      <template #actions>
        <button
          class="btn btn-sm"
          :disabled="loading"
          @click="load"
        >
          {{ loading ? '加载中…' : '重新加载' }}
        </button>
        <button
          class="btn btn-sm"
          :disabled="saving || !yamlText"
          @click="validate"
        >
          校验
        </button>
        <button
          class="btn btn-primary btn-sm"
          :disabled="saving || !yamlText"
          @click="saveAndApply"
        >
          {{ saving ? '应用中…' : '保存并应用' }}
        </button>
      </template>

      <div class="meta-row text-base-content/55 mb-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
        <span>路径：{{ config?.path || status?.config.path || '-' }}</span>
        <span>
          有效性：
          <span :class="config?.valid ? 'text-success' : 'text-error'">
            {{ config?.valid ? '通过' : '未通过' }}
          </span>
        </span>
        <span>大小：{{ prettySize(status?.config.size) }}</span>
        <span v-if="config?.modifiedAt">更新于 {{ formatTime(config.modifiedAt) }}</span>
      </div>

      <textarea
        v-model="yamlText"
        class="textarea scrollbar-thin w-full font-mono text-xs leading-5"
        rows="18"
        spellcheck="false"
        placeholder="正在加载配置…"
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
          {{ validation.ok ? '校验通过' : '校验失败' }}
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
          导出配置
        </button>
        <button
          class="btn btn-sm"
          :disabled="busy"
          @click="backupConfig"
        >
          下载备份
        </button>
        <button
          class="btn btn-sm"
          :disabled="busy"
          @click="restoreConfig"
        >
          恢复备份
        </button>
        <button
          class="btn btn-error btn-outline btn-sm"
          :disabled="busy"
          @click="resetConfig"
        >
          重置配置
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
    setMessage(`读取配置失败：${error instanceof Error ? error.message : String(error)}`, 'error')
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
      setMessage('配置校验未通过，请检查输出', 'error')
    }
  } catch (error) {
    setMessage(`校验失败：${error instanceof Error ? error.message : String(error)}`, 'error')
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
        ? '已保存，内核已重启并应用新配置'
        : result.applied
          ? '已保存并热重载配置'
          : '已保存配置',
    )
    emit('changed')
  } catch (error) {
    setMessage(`保存失败：${error instanceof Error ? error.message : String(error)}`, 'error')
  } finally {
    saving.value = false
  }
}

const runDownload = async (fn: () => Promise<void>, label: string) => {
  busy.value = true
  try {
    await fn()
    setMessage(`${label}已开始下载`)
  } catch (error) {
    setMessage(`${label}失败：${error instanceof Error ? error.message : String(error)}`, 'error')
  } finally {
    busy.value = false
  }
}

const exportConfig = () => runDownload(mproxyApi.exportConfig, '导出')
const backupConfig = () => runDownload(mproxyApi.backupConfig, '备份')

const restoreConfig = async () => {
  if (!window.confirm('确定要恢复上一份备份吗？当前配置会被覆盖。')) return
  busy.value = true
  try {
    await mproxyApi.restoreConfig()
    await load()
    setMessage('已恢复备份')
    emit('changed')
  } catch (error) {
    setMessage(`恢复失败：${error instanceof Error ? error.message : String(error)}`, 'error')
  } finally {
    busy.value = false
  }
}

const resetConfig = async () => {
  if (!window.confirm('确定要重置为默认配置吗？自定义节点、规则与订阅都会丢失。')) return
  busy.value = true
  try {
    await mproxyApi.resetConfig()
    await load()
    setMessage('已重置为默认配置')
    emit('changed')
  } catch (error) {
    setMessage(`重置失败：${error instanceof Error ? error.message : String(error)}`, 'error')
  } finally {
    busy.value = false
  }
}

onMounted(load)
</script>
