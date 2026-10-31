<template>
  <section class="base-container border-primary/30 border-2 p-4">
    <div class="flex items-start justify-between gap-2">
      <div>
        <h2 class="text-base font-semibold">{{ t('mproxyOnboardingTitle') }}</h2>
        <p class="text-base-content/55 mt-0.5 text-xs">
          {{ needsCore ? t('mproxyOnboardingNeedCore') : t('mproxyOnboardingNeedNodes') }}
        </p>
      </div>
      <button
        class="btn btn-ghost btn-xs"
        @click="$emit('dismiss')"
      >
        {{ t('skip') }}
      </button>
    </div>

    <ol class="mt-3 space-y-2">
      <li
        v-for="(step, index) in steps"
        :key="step.key"
        class="bg-base-200 flex items-start gap-3 rounded-lg px-3 py-2.5"
        :class="step.current ? 'ring-primary/40 ring-1' : ''"
      >
        <span
          class="flex size-6 flex-none items-center justify-center rounded-full text-xs font-semibold"
          :class="
            step.current
              ? 'bg-primary text-primary-content'
              : 'bg-base-content/10 text-base-content/60'
          "
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
      {{ t('mproxyOnboardAutoNext') }}
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

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
      title: t('mproxyOnboardStepCore'),
      description: t('mproxyOnboardStepCoreDesc'),
      target: 'core' as const,
      action: t('mproxyOnboardStepCoreAction'),
      current: true,
    })
  } else {
    result.push({
      key: 'subscription',
      title: t('mproxyOnboardStepImport'),
      description: t('mproxyOnboardStepImportDesc'),
      target: 'subscription' as const,
      action: t('mproxyOnboardStepImportAction'),
      current: true,
    })
  }

  result.push({
    key: 'apps',
    title: t('mproxyOnboardStepApps'),
    description: t('mproxyOnboardStepAppsDesc'),
    target: undefined as 'core' | 'subscription' | undefined,
    action: '',
    current: false,
  })

  return result
})
</script>
