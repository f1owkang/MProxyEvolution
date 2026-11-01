<template>
  <div class="flex flex-col gap-3">
    <SectionCard
      :title="t('mproxyImportSubscription')"
      :description="t('mproxyImportDesc')"
    >
      <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <button
          v-for="item in modeOptions"
          :key="item.value"
          type="button"
          class="rounded-xl border p-3 text-left transition"
          :class="
            mode === item.value
              ? 'border-primary/50 bg-primary/5 ring-primary/25 ring-1'
              : 'border-base-border bg-base-200/40 hover:bg-base-200/70'
          "
          @click="mode = item.value"
        >
          <div class="flex items-center justify-between gap-2">
            <span class="text-sm font-semibold">{{ item.label }}</span>
            <span
              v-if="mode === item.value"
              class="text-primary text-sm font-semibold"
              >✓</span
            >
          </div>
          <p class="text-base-content/55 mt-1 text-xs leading-4">
            {{ item.hint }}
          </p>
        </button>
      </div>

      <div class="mt-3 grid grid-cols-3 gap-2">
        <button
          v-for="item in sourceOptions"
          :key="item.value"
          type="button"
          class="rounded-lg border px-2 py-2 text-xs font-medium transition"
          :class="
            source === item.value
              ? 'border-primary/50 bg-primary/10 text-primary'
              : 'border-base-border bg-base-200/40 text-base-content/70 hover:bg-base-200/70'
          "
          @click="source = item.value"
        >
          {{ item.label }}
        </button>
      </div>

      <div class="border-base-border bg-base-200/40 mt-3 rounded-xl border p-3">
        <template v-if="source === 'url'">
          <label class="form-control">
            <span class="text-base-content/60 mb-1 text-xs">{{ t('mproxySubscriptionUrl') }}</span>
            <input
              v-model="importUrl"
              type="url"
              class="input input-sm w-full"
              placeholder="https://example.com/subscribe?token=…"
              spellcheck="false"
            />
          </label>
          <button
            class="btn btn-primary btn-sm mt-2 w-full"
            :disabled="importing || !importUrl.trim()"
            @click="importFromUrl"
          >
            {{ importing ? t('mproxyImporting') : t('mproxyImportLink') }}
          </button>
        </template>

        <template v-else-if="source === 'file'">
          <p class="text-base-content/60 text-xs leading-4">
            {{ t('mproxyImportDesc') }}
          </p>
          <button
            class="btn btn-primary btn-sm mt-2 w-full"
            :disabled="importing"
            @click="fileInput?.click()"
          >
            {{ importing ? t('mproxyImporting') : t('mproxyImportFromFile') }}
          </button>
          <input
            ref="fileInput"
            type="file"
            class="hidden"
            accept=".yaml,.yml,.txt,.conf"
            @change="importFromFile"
          />
        </template>

        <template v-else>
          <textarea
            v-model="pasteContent"
            class="textarea w-full scrollbar-thin font-mono text-xs"
            rows="8"
            :placeholder="t('mproxyPastePlaceholder')"
            spellcheck="false"
          />
          <button
            class="btn btn-primary btn-sm mt-2 w-full"
            :disabled="importing || !pasteContent.trim()"
            @click="importFromPaste"
          >
            {{ importing ? t('mproxyImporting') : t('mproxyImportPaste') }}
          </button>
        </template>
      </div>

      <div
        v-if="importMessage"
        class="alert mt-3 py-2 text-xs"
        :class="importError ? 'alert-error' : 'alert-success'"
      >
        {{ importMessage }}
      </div>
    </SectionCard>

    <SectionCard
      :title="t('mproxyProviders')"
      :description="t('mproxyProvidersDesc')"
    >
      <template #actions>
        <button
          class="btn btn-sm"
          :disabled="loadingProviders || updating"
          @click="updateAll"
        >
          {{ updating ? t('mproxyUpdating') : t('mproxyUpdateAll') }}
        </button>
      </template>

      <div
        v-if="providerError"
        class="alert alert-warning mb-3 py-2 text-xs"
      >
        {{ providerError }}
      </div>

      <div
        v-if="providers.length"
        class="grid grid-cols-1 gap-2 sm:grid-cols-2"
      >
        <div
          v-for="provider in providers"
          :key="provider.name"
          class="border-base-border bg-base-200/40 rounded-xl border p-3"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <div class="truncate text-sm font-medium">
                {{ provider.name }}
              </div>
              <div class="text-base-content/50 mt-0.5 text-xs">
                {{ provider.vehicleType || 'provider' }}
                <template v-if="provider.updatedAt">
                  · {{ t('mproxyUpdatedAt', { time: formatTime(provider.updatedAt) }) }}
                </template>
              </div>
            </div>
            <button
              class="btn btn-ghost btn-xs text-error"
              :disabled="deleting === provider.name"
              @click="removeProvider(provider.name)"
            >
              {{ deleting === provider.name ? t('mproxyDeleting') : t('mproxyDelete') }}
            </button>
          </div>
        </div>
      </div>
      <div
        v-else-if="!loadingProviders"
        class="text-base-content/50 rounded-xl border border-dashed p-4 text-center text-xs"
      >
        {{ t('mproxyProvidersEmpty') }}
      </div>

      <details class="border-base-border mt-3 rounded-xl border p-3">
        <summary class="cursor-pointer text-xs font-medium">
          {{ t('mproxyAddProvider') }}
        </summary>
        <div class="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-2">
          <input
            v-model="form.name"
            class="input input-sm"
            :placeholder="t('mproxyProviderName')"
          />
          <input
            v-model="form.url"
            class="input input-sm"
            :placeholder="t('mproxyProviderUrl')"
            spellcheck="false"
          />
          <input
            v-model="form.interval"
            class="input input-sm"
            :placeholder="t('mproxyProviderInterval')"
            inputmode="numeric"
          />
          <input
            v-model="form.path"
            class="input input-sm"
            :placeholder="t('mproxyProviderPath')"
          />
        </div>
        <div class="mt-2 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
          <span class="text-base-content/45 text-xs">{{ t('mproxyProviderHint') }}</span>
          <button
            class="btn btn-primary btn-sm w-full sm:w-auto"
            :disabled="adding || !form.name.trim() || !form.url.trim()"
            @click="addProvider"
          >
            {{ adding ? t('mproxyAdding') : t('mproxyAdd') }}
          </button>
        </div>
        <div
          v-if="providerMessage"
          class="alert mt-2 py-2 text-xs"
          :class="providerMessageType === 'error' ? 'alert-error' : 'alert-success'"
        >
          {{ providerMessage }}
        </div>
      </details>
    </SectionCard>

    <ConfigSection
      :status="status"
      @changed="emit('changed')"
    />
  </div>
</template>
<script setup lang="ts">
import { mproxyApi, type ImportMode, type MihomoProvider, type MProxyStatus } from '@/api/mproxy'
import ConfigSection from '@/components/mproxy/ConfigSection.vue'
import SectionCard from '@/components/mproxy/SectionCard.vue'
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const props = withDefaults(
  defineProps<{
    status?: MProxyStatus | null
  }>(),
  { status: null },
)

const emit = defineEmits<{
  changed: []
}>()

// status 供底部的 ConfigSection 使用；用 void 避免未使用变量告警。
void props

const mode = ref<ImportMode>('merge')
const modeOptions = computed(() => [
  { value: 'merge' as ImportMode, label: t('mproxyMerge'), hint: t('mproxyMergeHint') },
  { value: 'replace' as ImportMode, label: t('mproxyReplace'), hint: t('mproxyReplaceHint') },
])
const sourceOptions = computed(() => [
  { value: 'url' as const, label: t('mproxyImportLink') },
  { value: 'file' as const, label: t('mproxyImportFromFile') },
  { value: 'paste' as const, label: t('mproxyImportPaste') },
])
const importUrl = ref('')
const pasteContent = ref('')
const source = ref<'url' | 'file' | 'paste'>('url')
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
  const summary = t('mproxyImportSuccess', {
    proxies: imported.proxies,
    groups: imported.groups,
    rules: imported.rules,
    providers: imported.providers,
  })
  return (
    summary +
    (result.warnings?.length
      ? t('mproxyImportWarnings', { warnings: result.warnings.join('; ') })
      : '')
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
      t('mproxyImportError', {
        error: error instanceof Error ? error.message : String(error),
      }),
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
    providerError.value = t('mproxyProvidersLoadError', {
      error: error instanceof Error ? error.message : String(error),
    })
  } finally {
    loadingProviders.value = false
  }
}

const updateAll = async () => {
  updating.value = true
  providerMessage.value = ''
  try {
    await mproxyApi.updateAllProviders()
    setProviderMessage(t('mproxyProvidersUpdated'))
    await loadProviders()
  } catch (error) {
    setProviderMessage(
      t('mproxyProvidersUpdateError', {
        error: error instanceof Error ? error.message : String(error),
      }),
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
    setProviderMessage(t('mproxyProviderAdded'))
    form.name = ''
    form.url = ''
    form.interval = ''
    form.path = ''
    await loadProviders()
    emit('changed')
  } catch (error) {
    setProviderMessage(
      t('mproxyProviderAddError', {
        error: error instanceof Error ? error.message : String(error),
      }),
      'error',
    )
  } finally {
    adding.value = false
  }
}

const removeProvider = async (name: string) => {
  if (!window.confirm(t('mproxyProviderDeleteConfirm', { name }))) return
  deleting.value = name
  providerMessage.value = ''
  try {
    await mproxyApi.deleteProvider(name)
    setProviderMessage(t('mproxyProviderDeleted'))
    await loadProviders()
    emit('changed')
  } catch (error) {
    setProviderMessage(
      t('mproxyProviderDeleteError', {
        error: error instanceof Error ? error.message : String(error),
      }),
      'error',
    )
  } finally {
    deleting.value = ''
  }
}

onMounted(loadProviders)
</script>
