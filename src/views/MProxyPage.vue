<template>
  <div
    class="h-full overflow-x-hidden overflow-y-auto"
    :style="pageStyle"
  >
    <div class="flex flex-col gap-3 p-3">
      <header class="base-container p-4">
        <div class="flex flex-wrap items-start justify-between gap-2">
          <div class="min-w-0">
            <div class="flex items-center gap-2">
              <h1 class="text-base leading-6 font-semibold">
                {{ t('mproxy') }}
              </h1>
              <span
                class="badge badge-sm"
                :class="statusBadgeClass"
              >
                {{ statusText }}
              </span>
            </div>
            <p class="text-base-content/55 mt-1 text-xs leading-4">
              {{ t('mproxyPageSubtitle') }}
            </p>
          </div>
          <button
            class="btn btn-ghost btn-sm"
            :disabled="loading"
            @click="loadAll"
          >
            {{ loading ? t('mproxyRefreshing') : t('mproxyRefresh') }}
          </button>
        </div>

        <SegmentedControl
          v-model="tab"
          :options="tabs"
          block
          class="mt-3"
        />
      </header>

      <div
        v-if="!hasToken"
        class="alert alert-warning py-2 text-xs"
      >
        {{ t('mproxyNoToken') }}
      </div>

      <div
        v-if="error"
        class="alert alert-error py-2 text-xs"
      >
        <span class="min-w-0 flex-1">{{ error }}</span>
        <button
          class="btn btn-ghost btn-xs"
          @click="loadAll"
        >
          {{ t('mproxyRetry') }}
        </button>
      </div>

      <OnboardingCard
        v-if="showOnboarding"
        :needs-core="needsCore"
        :needs-nodes="needsNodes"
        @navigate="tab = $event"
        @dismiss="dismissOnboarding"
      />

      <CoreSection
        v-if="tab === 'core'"
        :status="status"
        :settings="settings"
        @changed="reload"
      />

      <template v-else-if="tab === 'subscription'">
        <SubscriptionSection @changed="reload" />
        <ConfigSection
          :status="status"
          @changed="reload"
        />
      </template>
      <AppsSection
        v-else-if="tab === 'apps'"
        @changed="reload"
      />
      <DiagnosticsSection
        v-else-if="tab === 'diagnostics'"
        :status="status"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { hasMProxyToken, mproxyApi, type MProxySettings, type MProxyStatus } from '@/api/mproxy'
import SegmentedControl from '@/components/common/SegmentedControl.vue'
import AppsSection from '@/components/mproxy/AppsSection.vue'
import ConfigSection from '@/components/mproxy/ConfigSection.vue'
import CoreSection from '@/components/mproxy/CoreSection.vue'
import DiagnosticsSection from '@/components/mproxy/DiagnosticsSection.vue'
import OnboardingCard from '@/components/mproxy/OnboardingCard.vue'
import SubscriptionSection from '@/components/mproxy/SubscriptionSection.vue'
import { usePaddingForViews } from '@/composables/use-padding-for-views'
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
const { padding, paddingTop, paddingBottom } = usePaddingForViews({
  offsetTop: 0,
  offsetBottom: 0,
})
// Edge-to-edge（KernelSU）下页面顶部会压到状态栏：叠加系统安全区，底部同样避让。
// 参考 CRecordEvolution：新版 KSU 注入 --safe-area-inset-*，旧版 insets.css 只提供
// --window-inset-*，浏览器再兜底 env()，最后 0。
const pageStyle = computed(() => ({
  ...padding.value,
  paddingTop: `calc(var(--safe-area-inset-top, var(--window-inset-top, env(safe-area-inset-top, 0px))) + ${paddingTop.value}px)`,
  paddingBottom: `calc(var(--safe-area-inset-bottom, var(--window-inset-bottom, env(safe-area-inset-bottom, 0px))) + ${paddingBottom.value}px)`,
}))

const tab = ref('core')
const status = ref<MProxyStatus | null>(null)
const settings = ref<MProxySettings | null>(null)
const loading = ref(false)
const error = ref('')
const hasToken = ref(hasMProxyToken())

const tabs = computed(() => [
  { value: 'core', label: t('mproxyCore') },
  { value: 'subscription', label: t('mproxySubscription') },
  { value: 'apps', label: t('mproxyApps') },
  { value: 'diagnostics', label: t('mproxyDiagnostics') },
])

const statusText = computed(() => {
  if (!status.value) return t('mproxyStatusOffline')
  if (!status.value.core.installed) return t('mproxyStatusNotInstalled')
  return status.value.core.running ? t('mproxyStatusRunning') : t('mproxyStatusStopped')
})

const statusBadgeClass = computed(() => {
  if (!status.value) return 'badge-ghost'
  if (!status.value.core.installed) return 'badge-warning'
  return status.value.core.running ? 'badge-success' : 'badge-ghost'
})

const needsCore = computed(() => Boolean(status.value && !status.value.core.installed))
const needsNodes = computed(() => {
  const summary = status.value?.config.summary
  if (!status.value?.core.installed || !summary) return false
  return summary.proxyCount === 0 && summary.providerCount === 0
})

const showOnboarding = computed(() =>
  Boolean(
    status.value &&
    settings.value &&
    !settings.value.x.onboarded &&
    (needsCore.value || needsNodes.value),
  ),
)

const describeError = (value: unknown) => {
  if (value instanceof Error) return value.message
  return String(value)
}

const loadStatus = async () => {
  try {
    status.value = await mproxyApi.status()
    error.value = ''
    hasToken.value = hasMProxyToken()
  } catch (e) {
    error.value = t('mproxyBackendConnectError', { error: describeError(e) })
  }
}

const loadSettings = async () => {
  try {
    settings.value = await mproxyApi.getSettings()
  } catch {
    // settings are optional for rendering the shell
  }
}

const loadAll = async () => {
  loading.value = true
  await Promise.all([loadStatus(), loadSettings()])
  loading.value = false
}

const reload = async () => {
  await Promise.all([loadStatus(), loadSettings()])
}

const dismissOnboarding = async () => {
  try {
    await mproxyApi.patchSettings({ onboarded: true })
  } finally {
    await loadSettings()
  }
}

let timer: ReturnType<typeof setInterval> | undefined

onMounted(() => {
  loadAll()
  timer = setInterval(() => {
    if (document.visibilityState === 'visible') loadStatus()
  }, 10_000)
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>
