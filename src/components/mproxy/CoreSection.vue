<template>
  <div class="flex flex-col gap-3">
    <SectionCard
      title="内核"
      description="安装 / 更新 mihomo 内核，启动或停止代理。"
    >
      <template #actions>
        <button
          class="btn btn-sm"
          :disabled="checking"
          @click="checkUpdate"
        >
          {{ checking ? '检查中…' : '检查更新' }}
        </button>
        <button
          v-if="!status?.core.installed"
          class="btn btn-primary btn-sm"
          :disabled="busy"
          @click="installOrUpdate"
        >
          安装内核
        </button>
        <button
          v-else-if="updateInfo?.available"
          class="btn btn-primary btn-sm"
          :disabled="busy"
          @click="installOrUpdate"
        >
          更新到 {{ updateInfo?.latest }}
        </button>
      </template>

      <div class="grid grid-cols-2 gap-2 text-sm sm:grid-cols-3">
        <div class="bg-base-200 rounded-lg p-3">
          <div class="text-base-content/50 text-[11px]">状态</div>
          <div class="mt-0.5 flex items-center gap-1.5 font-medium">
            <span
              :class="[
                'inline-block size-2 rounded-full',
                status?.core.running ? 'bg-success' : 'bg-base-content/30',
              ]"
            />
            {{ status ? (status.core.running ? '运行中' : '已停止') : '未知' }}
          </div>
        </div>
        <div class="bg-base-200 rounded-lg p-3">
          <div class="text-base-content/50 text-[11px]">内核版本</div>
          <div class="mt-0.5 truncate font-medium">
            {{ settings?.coreVersion || status?.core.version || '未安装' }}
          </div>
        </div>
        <div class="bg-base-200 rounded-lg p-3">
          <div class="text-base-content/50 text-[11px]">架构 / ABI</div>
          <div class="mt-0.5 truncate font-medium">
            {{ status?.system.arch || '-' }} / {{ settings?.abi || status?.system.abi || '-' }}
          </div>
        </div>
        <div class="bg-base-200 rounded-lg p-3">
          <div class="text-base-content/50 text-[11px]">控制器</div>
          <div class="mt-0.5 truncate font-medium">
            {{ status ? status.core.controller.host + ':' + status.core.controller.port : '-' }}
          </div>
        </div>
        <div class="bg-base-200 rounded-lg p-3">
          <div class="text-base-content/50 text-[11px]">运行时长</div>
          <div class="mt-0.5 truncate font-medium">
            {{ formatUptime(status?.core.uptimeSec) }}
          </div>
        </div>
        <div class="bg-base-200 rounded-lg p-3">
          <div class="text-base-content/50 text-[11px]">自动重启</div>
          <div class="mt-0.5 truncate font-medium">{{ status?.core.restarts ?? 0 }} 次</div>
        </div>
        <div class="bg-base-200 rounded-lg p-3">
          <div class="text-base-content/50 text-[11px]">更新通道</div>
          <div class="mt-0.5 flex items-center gap-2 font-medium">
            <select
              class="select select-xs"
              :value="settings?.x.updateChannel ?? 'stable'"
              @change="changeChannel(($event.target as HTMLSelectElement).value)"
            >
              <option value="stable">稳定版</option>
              <option value="alpha">预览版</option>
            </select>
          </div>
        </div>
      </div>

      <div
        v-if="status?.core.lastExit"
        class="bg-base-200 mt-2 rounded-lg px-3 py-2.5"
      >
        <div class="text-base-content/50 text-[11px]">上次内核退出</div>
        <div class="mt-0.5 font-mono text-xs break-all">{{ status.core.lastExit }}</div>
      </div>

      <label
        class="bg-base-200 mt-3 flex items-center justify-between gap-3 rounded-lg px-3 py-2.5"
      >
        <span class="text-sm">
          开机自启
          <span class="text-base-content/50 block text-xs"
            >设备启动后自动拉起 MihomoProxy Evolution 与内核</span
          >
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
        v-if="job"
        class="mt-3"
      >
        <div class="mb-1 flex items-center justify-between text-xs">
          <span>{{ job.message || job.type || '任务进行中' }}</span>
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

      <div class="mt-3 flex flex-wrap gap-2">
        <button
          class="btn btn-sm"
          :disabled="busy || !status?.core.installed || status?.core.running"
          @click="runAction('start')"
        >
          启动
        </button>
        <button
          class="btn btn-sm"
          :disabled="busy || !status?.core.running"
          @click="runAction('stop')"
        >
          停止
        </button>
        <button
          class="btn btn-sm"
          :disabled="busy || !status?.core.installed"
          @click="runAction('restart')"
        >
          重启内核
        </button>
        <button
          class="btn btn-sm"
          :disabled="busy || !status?.core.running"
          @click="runAction('reload')"
        >
          重载配置
        </button>
      </div>
    </SectionCard>

    <SectionCard
      title="内核日志"
      description="mihomo 运行日志尾部，排查启动失败时优先查看。"
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
  if (day > 0) return `${day} 天 ${hour} 小时`
  if (hour > 0) return `${hour} 小时 ${minute} 分`
  return `${minute} 分`
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
    message: '准备中…',
    error: '',
    startedAt: new Date().toISOString(),
    finishedAt: '',
  }
  const finished = await waitForJob(jobId, (value) => {
    job.value = value
  })
  job.value = finished
  if (finished.state === 'error') {
    throw new Error(finished.error || '任务失败')
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
</script>
