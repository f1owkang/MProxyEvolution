<template>
  <div class="flex flex-col gap-3">
    <SectionCard
      :title="t('mproxyApps')"
      :description="t('mproxyAppsDesc')"
    >
      <template #actions>
        <button
          class="btn btn-primary btn-sm"
          :disabled="saving || !apps"
          @click="apply"
        >
          {{ saving ? t('mproxyApplying') : t('mproxyAppsApply') }}
        </button>
      </template>

      <div class="grid grid-cols-1 gap-2 sm:grid-cols-3">
        <button
          v-for="option in modeOptions"
          :key="option.value"
          type="button"
          class="rounded-xl border p-3 text-left transition"
          :class="
            currentMode === option.value
              ? 'border-primary/50 bg-primary/5 ring-primary/25 ring-1'
              : 'border-base-border bg-base-200/40 hover:bg-base-200/70'
          "
          @click="currentMode = option.value"
        >
          <div class="flex items-center justify-between gap-2">
            <span class="text-sm font-semibold">{{ option.label }}</span>
            <span
              v-if="currentMode === option.value"
              class="text-primary text-sm font-semibold"
              >✓</span
            >
          </div>
          <p class="text-base-content/55 mt-1 text-xs leading-4">
            {{ option.hint }}
          </p>
        </button>
      </div>

      <template v-if="currentMode !== 'all'">
        <div class="mt-3 flex flex-col gap-2 sm:flex-row sm:items-center">
          <label class="input input-sm flex flex-1 items-center gap-2">
            <span class="text-base-content/40 text-sm">⌕</span>
            <input
              v-model="search"
              class="grow bg-transparent text-sm outline-none"
              :placeholder="t('mproxyAppsSearch')"
              spellcheck="false"
            />
          </label>
          <div class="flex gap-2">
            <button
              class="btn btn-sm flex-1 sm:flex-none"
              :disabled="loading"
              @click="selectVisible"
            >
              {{ t('mproxySelectAll') }}
            </button>
            <button
              class="btn btn-sm flex-1 sm:flex-none"
              :disabled="loading || !selected.length"
              @click="clearSelection"
            >
              {{ t('mproxyClear') }}
            </button>
          </div>
        </div>

        <div class="mt-2 flex flex-wrap items-center justify-between gap-2">
          <span class="text-base-content/50 text-xs">
            {{ t('mproxyAppsSelected', { selected: selected.length, total: packages.length }) }}
            <span v-if="loading">{{ t('mproxyLoadingInline') }}</span>
          </span>
          <span
            v-if="selected.length"
            class="badge badge-primary badge-sm"
          >
            {{ selected.length }}
          </span>
        </div>

        <div
          v-if="error"
          class="alert alert-error mt-2 py-2 text-xs"
        >
          {{ error }}
        </div>

        <div
          class="mt-2 grid max-h-[24rem] scrollbar-thin grid-cols-1 gap-2 overflow-y-auto pr-1 sm:grid-cols-2"
        >
          <div
            v-if="!loading && !filteredPackages.length"
            class="text-base-content/50 col-span-full p-6 text-center text-xs"
          >
            {{ packages.length ? t('mproxyAppsNoMatch') : t('mproxyAppsEmpty') }}
          </div>
          <label
            v-for="pkg in filteredPackages"
            :key="pkg.packageName"
            class="flex cursor-pointer items-center gap-2.5 rounded-xl border p-2.5 transition"
            :class="
              selected.includes(pkg.packageName)
                ? 'border-primary/40 bg-primary/5'
                : 'border-base-border bg-base-200/40 hover:bg-base-200/70'
            "
          >
            <input
              type="checkbox"
              class="checkbox checkbox-sm"
              :checked="selected.includes(pkg.packageName)"
              @change="toggleApp(pkg.packageName, ($event.target as HTMLInputElement).checked)"
            />
            <img
              v-if="!iconFailed.has(pkg.packageName)"
              :src="iconUrl(pkg.packageName)"
              class="size-8 flex-none rounded-lg"
              alt=""
              loading="lazy"
              @error="markIconFailed(pkg.packageName)"
            />
            <span
              v-else
              class="bg-primary/15 text-primary flex size-8 flex-none items-center justify-center rounded-lg text-sm font-semibold"
            >
              {{ (pkg.label || pkg.packageName).slice(0, 1).toUpperCase() }}
            </span>
            <span class="min-w-0 flex-1">
              <span class="block truncate text-sm">{{ pkg.label || pkg.packageName }}</span>
              <span class="text-base-content/45 block truncate text-[11px]">{{
                pkg.packageName
              }}</span>
            </span>
          </label>
        </div>
      </template>

      <div
        v-if="message"
        class="alert mt-3 py-2 text-xs"
        :class="messageType === 'error' ? 'alert-error' : 'alert-success'"
      >
        {{ message }}
      </div>

      <button
        class="btn btn-primary mt-3 w-full sm:hidden"
        :disabled="saving || !apps"
        @click="apply"
      >
        {{ saving ? t('mproxyApplying') : t('mproxyAppsApply') }}
      </button>
    </SectionCard>
  </div>
</template>

<script setup lang="ts">
import { mproxyApi, type AppsMode, type MProxyAppsResponse, type PackageInfo } from '@/api/mproxy'
import SectionCard from '@/components/mproxy/SectionCard.vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

type AppEntry = PackageInfo & { label?: string }

type KsuPackageManager = {
  pm?: {
    list?: () =>
      | Promise<
          Array<{
            packageName?: string
            label?: string
            name?: string
            uid?: number
            system?: boolean
          }>
        >
      | Array<{
          packageName?: string
          label?: string
          name?: string
          uid?: number
          system?: boolean
        }>
  }
}

const { t } = useI18n()

const emit = defineEmits<{
  changed: []
}>()

const apps = ref<MProxyAppsResponse | null>(null)
const packages = ref<AppEntry[]>([])
const selected = ref<string[]>([])
const search = ref('')
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const message = ref('')
const messageType = ref<'success' | 'error'>('success')
const iconFailed = ref(new Set<string>())

const currentMode = ref<AppsMode>('all')

const modeOptions = computed<Array<{ value: AppsMode; label: string; hint: string }>>(() => [
  { value: 'all', label: t('mproxyAppsModeAll'), hint: t('mproxyAppsHintAll') },
  { value: 'whitelist', label: t('mproxyAppsModeWhitelist'), hint: t('mproxyAppsHintWhitelist') },
  { value: 'blacklist', label: t('mproxyAppsModeBlacklist'), hint: t('mproxyAppsHintBlacklist') },
])

const filteredPackages = computed(() => {
  const keyword = search.value.trim().toLowerCase()
  if (!keyword) return packages.value
  return packages.value.filter(
    (pkg) =>
      pkg.packageName.toLowerCase().includes(keyword) ||
      (pkg.label ?? '').toLowerCase().includes(keyword),
  )
})

const iconUrl = (packageName: string) => `ksu://icon/${packageName}`

const markIconFailed = (packageName: string) => {
  const next = new Set(iconFailed.value)
  next.add(packageName)
  iconFailed.value = next
}

const setMessage = (text: string, type: 'success' | 'error' = 'success') => {
  message.value = text
  messageType.value = type
}

const loadApps = async () => {
  error.value = ''
  try {
    const data = await mproxyApi.getApps()
    apps.value = data
    currentMode.value = data.mode ?? 'all'
    selected.value = [...(data.apps ?? [])]
  } catch (e) {
    error.value = t('mproxyAppsLoadError', {
      error: e instanceof Error ? e.message : String(e),
    })
  }
}

const loadKsuPackages = async (): Promise<AppEntry[]> => {
  const ksu = window.ksu as unknown as KsuPackageManager | undefined
  const list = ksu?.pm?.list
  if (!list) return []

  try {
    const result = await list()
    return result
      .filter((item) => item.packageName)
      .map((item) => ({
        packageName: item.packageName as string,
        uid: item.uid ?? 0,
        system: Boolean(item.system),
        label: item.label || item.name,
      }))
  } catch {
    return []
  }
}

const loadPackages = async () => {
  loading.value = true
  try {
    const response = await mproxyApi.getPackages('user')
    packages.value = (response.packages ?? []).map((pkg) => ({ ...pkg }))
  } catch {
    packages.value = await loadKsuPackages()
  } finally {
    loading.value = false
  }
}

const toggleApp = (packageName: string, checked: boolean) => {
  if (checked) {
    if (!selected.value.includes(packageName)) selected.value = [...selected.value, packageName]
  } else {
    selected.value = selected.value.filter((name) => name !== packageName)
  }
}

const selectVisible = () => {
  const next = new Set(selected.value)
  filteredPackages.value.forEach((pkg) => next.add(pkg.packageName))
  selected.value = [...next]
}

const clearSelection = () => {
  selected.value = []
}

const apply = async () => {
  if (saving.value) return
  saving.value = true
  message.value = ''
  try {
    const mode = currentMode.value
    const result = await mproxyApi.putApps(mode, mode === 'all' ? [] : selected.value)
    // PUT 响应不含 tunEnabled（重启后由 GET /api/mproxy/apps 提供），保留旧值即可。
    apps.value = {
      mode: result.mode,
      apps: result.apps,
      tunEnabled: apps.value?.tunEnabled ?? false,
    }
    setMessage(mode === 'all' ? t('mproxyAppsAppliedAll') : t('mproxyAppsSaved'))
    emit('changed')
  } catch (e) {
    setMessage(
      t('mproxyAppsSaveError', { error: e instanceof Error ? e.message : String(e) }),
      'error',
    )
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  loadApps()
  loadPackages()
})
</script>
