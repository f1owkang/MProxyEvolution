<template>
  <div class="flex flex-col gap-3">
    <SectionCard
      :title="t('mproxyImportSubscription')"
      :description="t('mproxyImportDesc')"
    >
      <div class="flex flex-col gap-3">
        <label class="form-control">
          <span class="text-base-content/60 mb-1 text-xs">{{ t('mproxySubscriptionUrl') }}</span>
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
              {{ importing ? t('mproxyImporting') : t('mproxyImportLink') }}
            </button>
          </div>
        </label>

        <div class="flex flex-wrap items-center gap-2">
          <span class="text-base-content/60 text-xs">{{ t('mproxyImportMode') }}</span>
          <div class="join">
            <button
              class="join-item btn btn-xs"
              :class="mode === 'merge' ? 'btn-active' : ''"
              @click="mode = 'merge'"
            >
              {{ t('mproxyMerge') }}
            </button>
            <button
              class="join-item btn btn-xs"
              :class="mode === 'replace' ? 'btn-active' : ''"
              @click="mode = 'replace'"
            >
              {{ t('mproxyReplace') }}
            </button>
          </div>
          <span class="text-base-content/45 text-xs">
            {{ mode === 'merge' ? t('mproxyMergeHint') : t('mproxyReplaceHint') }}
          </span>
        </div>

        <div class="flex flex-wrap gap-2">
          <button
            class="btn btn-sm"
            :disabled="importing"
            @click="fileInput?.click()"
          >
            {{ t('mproxyImportFromFile') }}
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
            {{ showPaste ? t('mproxyPasteHide') : t('mproxyPasteShow') }}
          </button>
        </div>

        <div v-if="showPaste">
          <textarea
            v-model="pasteContent"
            class="textarea w-full scrollbar-thin font-mono text-xs"
            rows="8"
            :placeholder="t('mproxyPastePlaceholder')"
            spellcheck="false"
          />
          <div class="mt-2 flex justify-end">
            <button
              class="btn btn-primary btn-sm"
              :disabled="importing || !pasteContent.trim()"
              @click="importFromPaste"
            >
              {{ t('mproxyImportPaste') }}
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
      <div
        v-else-if="!loadingProviders"
        class="text-base-content/50 py-2 text-xs"
      >
        {{ t('mproxyProvidersEmpty') }}
      </div>

      <div class="border-base-border mt-3 border-t pt-3">
        <div class="text-base-content/60 mb-2 text-xs">{{ t('mproxyAddProvider') }}</div>
        <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
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
        <div class="mt-2 flex items-center justify-between gap-2">
          <span class="text-base-content/45 text-xs">{{ t('mproxyProviderHint') }}</span>
          <button
            class="btn btn-primary btn-sm"
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
      </div>
    </SectionCard>
  </div>
</template>

<script setup lang="ts">
import { mproxyApi, type ImportMode, type MihomoProvider } from '@/api/mproxy'
import SectionCard from '@/components/mproxy/SectionCard.vue'
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

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
