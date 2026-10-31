<template>
  <section class="base-container border-primary/30 border-2 p-4">
    <div class="flex items-start justify-between gap-2">
      <div>
        <h2 class="text-base font-semibold">欢迎使用 MihomoProxy Evolution</h2>
        <p class="text-base-content/55 mt-0.5 text-xs">
          {{ needsCore ? '还没有安装 mihomo 内核，先完成安装。' : '还没有可用节点，先导入订阅或配置。' }}
        </p>
      </div>
      <button
        class="btn btn-ghost btn-xs"
        @click="$emit('dismiss')"
      >
        跳过
      </button>
    </div>

    <ol class="mt-3 space-y-2">
      <li
        v-for="(step, index) in steps"
        :key="step.key"
        class="flex items-start gap-3 rounded-lg bg-base-200 px-3 py-2.5"
        :class="step.current ? 'ring-primary/40 ring-1' : ''"
      >
        <span
          class="flex size-6 flex-none items-center justify-center rounded-full text-xs font-semibold"
          :class="step.current ? 'bg-primary text-primary-content' : 'bg-base-content/10 text-base-content/60'"
        >
          {{ index + 1 }}
        </span>
        <div class="min-w-0 flex-1">
          <div class="text-sm font-medium">{{ step.title }}</div>
          <div class="text-base-content/55 text-xs">{{ step.description }}</div>
        </div>
        <button
          v-if="step.target"
          class="btn btn-primary btn-xs flex-none"
          @click="$emit('navigate', step.target)"
        >
          {{ step.action }}
        </button>
      </li>
    </ol>

    <div
      v-if="needsCore"
      class="text-base-content/45 mt-2 text-xs"
    >
      安装完成后本向导会自动进入下一步。
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  needsCore: boolean
  needsNodes: boolean
}>()

defineEmits<{
  navigate: [tab: 'core' | 'subscription']
  dismiss: []
}>()

const steps = computed(() => {
  const result: Array<{
    key: string
    title: string
    description: string
    target?: 'core' | 'subscription'
    action: string
    current: boolean
  }> = []

  if (props.needsCore) {
    result.push({
      key: 'core',
      title: '安装内核',
      description: '下载与当前设备 ABI 匹配的 mihomo 内核。',
      target: 'core' as const,
      action: '去安装',
      current: true,
    })
  } else {
    result.push({
      key: 'subscription',
      title: '导入节点 / 订阅',
      description: '支持订阅链接、本地 YAML 文件或粘贴完整配置。',
      target: 'subscription' as const,
      action: '去导入',
      current: true,
    })
  }

  result.push({
    key: 'apps',
    title: '按需配置应用分流',
    description: '默认全部应用走代理，可在应用分区分流。',
    target: undefined as 'core' | 'subscription' | undefined,
    action: '',
    current: false,
  })

  return result
})
</script>
