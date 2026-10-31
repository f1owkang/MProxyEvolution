<template>
  <div class="flex flex-col gap-3">
    <SectionCard
      title="导入订阅"
      description="支持 Clash 订阅链接、本地配置文件与粘贴的完整 YAML。"
    >
      <div class="flex flex-col gap-3">
        <label class="form-control">
          <span class="text-base-content/60 mb-1 text-xs">订阅链接</span>
          <div class="flex flex-col gap-2 sm:flex-row">
            <input
              v-model="importUrl"
              type="url"
              class="input input-sm flex-1"
              placeholder="https://example.com/subscribe?token=…"
              spellcheck="false"
            />
            <button
              class="btn btn-primary btn-sm"
              :disabled="importing || !importUrl.trim()"
              @click="importFromUrl"
            >
              {{ importing ? '导入中…' : '导入链接' }}
            </button>
          </div>
        </label>

        <div class="flex flex-wrap items-center gap-2">
          <span class="text-base-content/60 text-xs">导入方式</span>
          <div class="join">
            <button
              class="join-item btn btn-xs"
              :class="mode === 'merge' ? 'btn-active' : ''"
              @click="mode = 'merge'"
            >
              合并
            </button>
            <button
              class="join-item btn btn-xs"
              :class="mode === 'replace' ? 'btn-active' : ''"
              @click="mode = 'replace'"
            >
              替换
            </button>
          </div>
          <span class="text-base-content/45 text-xs">
            {{ mode === 'merge' ? '保留现有节点与规则，追加订阅内容' : '用订阅内容覆盖节点、规则与 provider' }}
          </span>
        </div>

        <div class="flex flex-wrap gap-2">
          <button
            class="btn btn-sm"
            :disabled="importing"
            @click="fileInput?.click()"
          >
            从文件导入
          </button>
          <input
            ref="fileInput"
            type="file"
            class="hidden"
            accept=".yaml,.yml,.txt,.conf"
            @change="importFromFile"
          />
          <button
            class="btn btn-sm"
            :disabled="importing"
            @click="showPaste = !showPaste"
          >
            {{ showPaste ? '收起粘贴区' : '粘贴 YAML 导入' }}
          </button>
        </div>

        <div v-if="showPaste">
          <textarea
            v-model="pasteContent"
            class="textarea scrollbar-thin w-full font-mono text-xs"
            rows="8"
            placeholder="在此粘贴完整 Clash 配置…"
            spellcheck="false"
          />
          <div class="mt-2 flex justify-end">
            <button
              class="btn btn-primary btn-sm"
              :disabled="importing || !pasteContent.trim()"
              @click="importFromPaste"
            >
              导入粘贴内容
            </button>
          </div>
        </div>

        <div
          v-if="importMessage"
          class="alert py-2 text-xs"
          :class="importError ? 'alert-error' : 'alert-success'"
        >
          {{ importMessage }}
        </div>
      </div>
    </SectionCard>

    <SectionCard
      title="订阅 / 代理提供者"
      description="proxy-provider 列表，支持新增、删除与全部更新。"
    >
      <template #actions>
        <button
          class="btn btn-sm"
          :disabled="loadingProviders || updating"
          @click="updateAll"
        >
          {{ updating ? '更新中…' : '全部更新' }}
        </button>
      </template>

      <div
        v-if="providerError"
        class="alert alert-warning mb-2 py-2 text-xs"
      >
        {{ providerError }}
      </div>

      <div
        v-if="providers.length"
        class="divide-base-border divide-y"
      >
        <div
          v-for="provider in providers"
          :key="provider.name"
          class="flex items-center gap-2 py-2"
        >
          <div class="min-w-0 flex-1">
            <div class="truncate text-sm font-medium">
              {{ provider.name }}
            </div>
            <div class="text-base-content/50 text-xs">
              {{ provider.vehicleType || 'provider' }}
              <template v-if="provider.updatedAt">
                · 更新于 {{ formatTime(provider.updatedAt) }}
              </template>
            </div>
          </div>
          <button
            class="btn btn-ghost btn-xs text-error"
            :disabled="deleting === provider.name"
            @click="removeProvider(provider.name)"
          >
            {{ deleting === provider.name ? '删除中…' : '删除' }}
          </button>
        </div>
      </div>
      <div
        v-else-if="!loadingProviders"
        class="text-base-content/50 py-2 text-xs"
      >
        暂无 proxy-provider。可在下方新增，或通过订阅导入生成。
      </div>

      <div class="border-base-border mt-3 border-t pt-3">
        <div class="text-base-content/60 mb-2 text-xs">新增 proxy-provider</div>
        <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
          <input
            v-model="form.name"
            class="input input-sm"
            placeholder="名称（唯一）"
          />
          <input
            v-model="form.url"
            class="input input-sm"
            placeholder="订阅地址 URL"
            spellcheck="false"
          />
          <input
            v-model="form.interval"
            class="input input-sm"
            placeholder="更新间隔（秒，可选）"
            inputmode="numeric"
          />
          <input
            v-model="form.path"
            class="input input-sm"
            placeholder="缓存路径（可选）"
          />
        </div>
        <div class="mt-2 flex items-center justify-between gap-2">
          <span class="text-base-content/45 text-xs">写入 config.yaml 的 proxy-providers 并应用</span>
          <button
            class="btn btn-primary btn-sm"
            :disabled="adding || !form.name.trim() || !form.url.trim()"
            @click="addProvider"
          >
            {{ adding ? '添加中…' : '添加' }}
          </button>
        </div>
        <div
          v-if="providerMessage"
          class="alert mt-2 py-2 text-xs"
          :class="providerMessageType === 'error' ? 'alert-error' : 'alert-success'"
        >
          {{ providerMessage }}
        </div>
      </div>
    </SectionCard>
  </div>
</template>

<script setup lang="ts">
import {
  mproxyApi,
  type ImportMode,
  type MihomoProvider,
} from '@/api/mproxy'
import SectionCard from '@/components/mproxy/SectionCard.vue'
import { computed, onMounted, reactive, ref } from 'vue'

const emit = defineEmits<{
  changed: []
}>()

const mode = ref<ImportMode>('merge')
const importUrl = ref('')
const pasteContent = ref('')
const showPaste = ref(false)
const importing = ref(false)
const importMessage = ref('')
const importError = ref(false)

const providers = ref<MihomoProvider[]>([])
const loadingProviders = ref(false)
const providerError = ref('')
const updating = ref(false)
const deleting = ref('')
const adding = ref(false)
const providerMessage = ref('')
const providerMessageType = ref<'success' | 'error'>('success')

const form = reactive({
  name: '',
  url: '',
  interval: '',
  path: '',
})

const fileInput = ref<HTMLInputElement>()

const formatTime = (value: string) => {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

const setImportMessage = (text: string, isError = false) => {
  importMessage.value = text
  importError.value = isError
}

const setProviderMessage = (text: string, type: 'success' | 'error' = 'success') => {
  providerMessage.value = text
  providerMessageType.value = type
}

const summarizeImport = (result: Awaited<ReturnType<typeof mproxyApi.importConfig>>) => {
  const imported = result.imported
  return (
    `导入成功：节点 ${imported.proxies}、策略组 ${imported.groups}、` +
    `规则 ${imported.rules}、provider ${imported.providers}` +
    (result.warnings?.length ? `；${result.warnings.join('；')}` : '')
  )
}

const doImport = async (payload: { url?: string; content?: string }) => {
  importing.value = true
  importMessage.value = ''
  try {
    const result = await mproxyApi.importConfig({ ...payload, mode: mode.value, apply: 'auto' })
    setImportMessage(summarizeImport(result))
    await loadProviders()
    emit('changed')
  } catch (error) {
    setImportMessage(
      `导入失败：${error instanceof Error ? error.message : String(error)}`,
      true,
    )
  } finally {
    importing.value = false
  }
}

const importFromUrl = () => {
  const url = importUrl.value.trim()
  if (!url) return
  doImport({ url })
}

const importFromPaste = () => {
  const content = pasteContent.value.trim()
  if (!content) return
  doImport({ content })
}

const importFromFile = async (event: Event) => {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return

  try {
    const content = await file.text()
    await doImport({ content })
  } finally {
    input.value = ''
  }
}

const loadProviders = async () => {
  loadingProviders.value = true
  providerError.value = ''
  try {
    const response = await mproxyApi.getMihomoProviders()
    providers.value = Object.values(response.providers ?? {}).sort((a, b) =>
      a.name.localeCompare(b.name),
    )
  } catch (error) {
    providers.value = []
    providerError.value = `读取 provider 失败（内核可能未运行）：${
      error instanceof Error ? error.message : String(error)
    }`
  } finally {
    loadingProviders.value = false
  }
}

const updateAll = async () => {
  updating.value = true
  providerMessage.value = ''
  try {
    await mproxyApi.updateAllProviders()
    setProviderMessage('已触发全部 provider 更新')
    await loadProviders()
  } catch (error) {
    setProviderMessage(
      `更新失败：${error instanceof Error ? error.message : String(error)}`,
      'error',
    )
  } finally {
    updating.value = false
  }
}

const addProvider = async () => {
  adding.value = true
  providerMessage.value = ''
  try {
    await mproxyApi.addProvider({
      name: form.name.trim(),
      url: form.url.trim(),
      interval: form.interval ? Number(form.interval) : undefined,
      path: form.path.trim() || undefined,
    })
    setProviderMessage('已添加 proxy-provider')
    form.name = ''
    form.url = ''
    form.interval = ''
    form.path = ''
    await loadProviders()
    emit('changed')
  } catch (error) {
    setProviderMessage(
      `添加失败：${error instanceof Error ? error.message : String(error)}`,
      'error',
    )
  } finally {
    adding.value = false
  }
}

const removeProvider = async (name: string) => {
  if (!window.confirm(`确定要删除 proxy-provider「${name}」吗？`)) return
  deleting.value = name
  providerMessage.value = ''
  try {
    await mproxyApi.deleteProvider(name)
    setProviderMessage('已删除 provider')
    await loadProviders()
    emit('changed')
  } catch (error) {
    setProviderMessage(
      `删除失败：${error instanceof Error ? error.message : String(error)}`,
      'error',
    )
  } finally {
    deleting.value = ''
  }
}

onMounted(loadProviders)
</script>
