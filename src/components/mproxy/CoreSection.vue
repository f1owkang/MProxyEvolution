<template>
  <div class="flex flex-col gap-3">
    <SectionCard
      :title="t('mproxyCore')"
      :description="t('mproxyCoreDesc')"
    >
      <template #actions>
        <button
          class="btn btn-sm"
          :disabled="checking"
          @click="checkUpdate"
        >
          {{ checking ? t('mproxyChecking') : t('mproxyCheckUpdate') }}
        </button>
        <button
          v-if="!status?.core.installed"
          class="btn btn-primary btn-sm"
          :disabled="busy"
          @click="installOrUpdate"
        >
          {{ t('mproxyCoreInstall') }}
        </button>
        <button
          v-else-if="updateInfo?.available"
          class="btn btn-primary btn-sm"
          :disabled="busy"
          @click="installOrUpdate"
        >
          {{ t('mproxyCoreUpdateTo', { version: updateInfo?.latest }) }}
        </button>
      </template>

      <div class="grid grid-cols-2 gap-2 text-sm sm:grid-cols-3">
        <div class="bg-base-200/40 border-base-border rounded-xl border p-3">
          <div class="text-base-content/50 text-[11px]">{{ t('mproxyLabelStatus') }}</div>
          <div class="mt-0.5 flex items-center gap-1.5 font-medium">
            <span
              :class="[
                'inline-block size-2 rounded-full',
                status?.core.running ? 'bg-success' : 'bg-base-content/30',
              ]"
            />
            {{
              status
                ? status.core.running
                  ? t('mproxyStatusRunning')
                  : t('mproxyStatusStopped')
                : t('mproxyUnknown')
            }}
          </div>
        </div>
        <div class="bg-base-200/40 border-base-border rounded-xl border p-3">
          <div class="text-base-content/50 text-[11px]">{{ t('mproxyLabelCoreVersion') }}</div>
          <div class="mt-0.5 truncate font-medium">
            {{ settings?.coreVersion || status?.core.version || t('mproxyNotInstalled') }}
          </div>
        </div>
        <div class="bg-base-200/40 border-base-border rounded-xl border p-3">
          <div class="text-base-content/50 text-[11px]">{{ t('mproxyLabelArch') }}</div>
          <div class="mt-0.5 truncate font-medium">
            {{ status?.system.arch || '-' }} / {{ settings?.abi || status?.system.abi || '-' }}
          </div>
        </div>
        <div class="bg-base-200/40 border-base-border rounded-xl border p-3">
          <div class="text-base-content/50 text-[11px]">{{ t('mproxyLabelController') }}</div>
          <div class="mt-0.5 truncate font-medium">
            {{ status ? status.core.controller.host + ':' + status.core.controller.port : '-' }}
          </div>
        </div>
        <div class="bg-base-200/40 border-base-border rounded-xl border p-3">
          <div class="text-base-content/50 text-[11px]">{{ t('mproxyLabelUptime') }}</div>
          <div class="mt-0.5 truncate font-medium">
            {{ formatUptime(status?.core.uptimeSec) }}
          </div>
        </div>
        <div class="bg-base-200/40 border-base-border rounded-xl border p-3">
          <div class="text-base-content/50 text-[11px]">{{ t('mproxyLabelRestarts') }}</div>
          <div class="mt-0.5 truncate font-medium">
            {{ t('mproxyTimes', { n: status?.core.restarts ?? 0 }) }}
          </div>
        </div>
        <div class="bg-base-200/40 border-base-border rounded-xl border p-3">
          <div class="text-base-content/50 text-[11px]">{{ t('mproxyLabelChannel') }}</div>
          <div class="mt-0.5 flex items-center gap-2 font-medium">
            <select
              class="select select-sm"
              :value="settings?.x.updateChannel ?? 'stable'"
              @change="changeChannel(($event.target as HTMLSelectElement).value)"
            >
              <option value="stable">{{ t('mproxyChannelStable') }}</option>
              <option value="alpha">{{ t('mproxyChannelAlpha') }}</option>
            </select>
          </div>
        </div>
      </div>

      <div
        v-if="status?.core.lastExit"
        class="bg-base-200/40 border-base-border mt-2 rounded-xl border px-3 py-2.5"
      >
        <div class="text-base-content/50 text-[11px]">{{ t('mproxyLabelLastExit') }}</div>
        <div class="mt-0.5 font-mono text-xs break-all">{{ status.core.lastExit }}</div>
      </div>

      <label
        class="bg-base-200/40 border-base-border mt-3 flex items-center justify-between gap-3 rounded-xl border p-3"
      >
        <span class="text-sm">
          {{ t('mproxyAutoStart') }}
          <span class="text-base-content/50 block text-xs">{{ t('mproxyAutoStartDesc') }}</span>
        </span>
        <input
          type="checkbox"
          class="toggle toggle-sm"
          :checked="settings?.x.autoStart ?? false"
          :disabled="busy || !settings"
          @change="toggleAutoStart(($event.target as HTMLInputElement).checked)"
        />
      </label>

      <div
        class="bg-base-200/40 border-base-border mt-2 flex items-center justify-between gap-3 rounded-xl border p-3"
      >
        <span class="text-sm">
          {{ t('mproxyPrivateDNS') }}
          <span class="text-base-content/50 block text-xs">{{ t('mproxyPrivateDNSDesc') }}</span>
        </span>
        <select
          class="select select-sm"
          :value="settings?.x.privateDNS ?? 'auto'"
          :disabled="busy || !settings"
          @change="changePrivateDNS(($event.target as HTMLSelectElement).value)"
        >
          <option value="auto">{{ t('mproxyPrivateDNSAuto') }}</option>
          <option value="keep">{{ t('mproxyPrivateDNSKeep') }}</option>
        </select>
      </div>

      <label
        class="bg-base-200/40 border-base-border mt-2 flex items-center justify-between gap-3 rounded-xl border p-3"
      >
        <span class="text-sm">
          {{ t('mproxyIPv6') }}
          <span class="text-base-content/50 block text-xs">{{ t('mproxyIPv6Desc') }}</span>
        </span>
        <input
          type="checkbox"
          class="toggle toggle-sm"
          :checked="settings?.x.ipv6 ?? false"
          :disabled="busy || !settings"
          @change="toggleIPv6(($event.target as HTMLInputElement).checked)"
        />
      </label>

      <div
        v-if="job"
        class="mt-3"
      >
        <div class="mb-1 flex items-center justify-between text-xs">
          <span>{{ job.message || job.type || t('mproxyJobRunning') }}</span>
          <span>{{ Math.round(job.progress || 0) }}%</span>
        </div>
        <progress
          class="progress progress-primary w-full"
          :value="job.progress || 0"
          max="100"
        />
      </div>

      <div
        v-if="error"
        class="alert alert-error mt-3 py-2 text-xs"
      >
        {{ error }}
      </div>

      <div class="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-4">
        <button
          class="btn btn-sm w-full"
          :disabled="busy || !status?.core.installed || status?.core.running"
          @click="runAction('start')"
        >
          {{ t('mproxyStart') }}
        </button>
        <button
          class="btn btn-sm w-full"
          :disabled="busy || !status?.core.running"
          @click="runAction('stop')"
        >
          {{ t('mproxyStop') }}
        </button>
        <button
          class="btn btn-sm w-full"
          :disabled="busy || !status?.core.installed"
          @click="runAction('restart')"
        >
          {{ t('mproxyRestart') }}
        </button>
        <button
          class="btn btn-sm w-full"
          :disabled="busy || !status?.core.running"
          @click="runAction('reload')"
        >
          {{ t('mproxyReload') }}
        </button>
      </div>
    </SectionCard>

    <SectionCard
      :title="t('mproxyCoreLogs')"
      :description="t('mproxyCoreLogsDesc')"
    >
      <LogTail
        source="core"
        :lines="200"
        clearable
      />
    </SectionCard>
  </div>
</template>

<script setup lang="ts">
import {
  mproxyApi,
  waitForJob,
  type CoreUpdateCheckResponse,
  type JobResponse,
  type MProxySettings,
  type MProxyStatus,
  type UpdateChannel,
} from '@/api/mproxy'
import LogTail from '@/components/mproxy/LogTail.vue'
import SectionCard from '@/components/mproxy/SectionCard.vue'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const props = defineProps<{
  status: MProxyStatus | null
  settings: MProxySettings | null
}>()

const emit = defineEmits<{
  changed: []
}>()

const busy = ref(false)
const checking = ref(false)
const job = ref<JobResponse | null>(null)
const updateInfo = ref<CoreUpdateCheckResponse | null>(null)
const error = ref('')

const formatUptime = (seconds?: number) => {
  if (!seconds || !Number.isFinite(seconds)) return '-'
  const total = Math.floor(seconds)
  const day = Math.floor(total / 86400)
  const hour = Math.floor((total % 86400) / 3600)
  const minute = Math.floor((total % 3600) / 60)
  if (day > 0) return t('mproxyUptimeDays', { days: day, hours: hour })
  if (hour > 0) return t('mproxyUptimeHours', { hours: hour, minutes: minute })
  return t('mproxyUptimeMinutes', { minutes: minute })
}

const channel = (): UpdateChannel => props.settings?.x.updateChannel ?? 'stable'

const checkUpdate = async () => {
  checking.value = true
  error.value = ''
  try {
    updateInfo.value = await mproxyApi.checkCoreUpdate(channel())
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    checking.value = false
  }
}

const runJob = async (jobId: string) => {
  job.value = {
    id: jobId,
    type: 'core-update',
    state: 'running',
    progress: 0,
    message: t('mproxyJobPreparing'),
    error: '',
    startedAt: new Date().toISOString(),
    finishedAt: '',
  }
  const finished = await waitForJob(jobId, (value) => {
    job.value = value
  })
  job.value = finished
  if (finished.state === 'error') {
    throw new Error(finished.error || t('mproxyJobFailed'))
  }
  updateInfo.value = null
}

const installOrUpdate = async () => {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    const { jobId } = await mproxyApi.updateCore(channel())
    await runJob(jobId)
    emit('changed')
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    busy.value = false
  }
}

const runAction = async (action: 'start' | 'stop' | 'restart' | 'reload') => {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    const map = {
      start: mproxyApi.startCore,
      stop: mproxyApi.stopCore,
      restart: mproxyApi.restartCore,
      reload: mproxyApi.reloadCore,
    }
    await map[action]()
    emit('changed')
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    busy.value = false
  }
}

const changeChannel = async (value: string) => {
  try {
    await mproxyApi.patchSettings({ updateChannel: value as UpdateChannel })
    emit('changed')
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}

const toggleAutoStart = async (value: boolean) => {
  try {
    await mproxyApi.patchSettings({ autoStart: value })
    emit('changed')
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}

const changePrivateDNS = async (value: string) => {
  try {
    await mproxyApi.patchSettings({ privateDNS: value === 'keep' ? 'keep' : 'auto' })
    emit('changed')
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}

const toggleIPv6 = async (value: boolean) => {
  try {
    await mproxyApi.patchSettings({ ipv6: value })
    emit('changed')
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}
</script>
