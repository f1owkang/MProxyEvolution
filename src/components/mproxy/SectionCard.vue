<template>
  <section class="base-container p-4">
    <header class="flex flex-wrap items-start justify-between gap-2">
      <div class="min-w-0">
        <h2 class="text-[15px] leading-5 font-semibold">
          {{ title }}
        </h2>
        <p
          v-if="description"
          class="text-base-content/55 mt-0.5 text-xs leading-4"
        >
          {{ description }}
        </p>
      </div>
      <div
        v-if="$slots.actions"
        class="flex flex-wrap items-center gap-2"
      >
        <slot name="actions" />
      </div>
    </header>
    <div
      v-if="$slots.default"
      class="mt-3"
    >
      <slot />
    </div>
  </section>
</template>

<script setup lang="ts">
defineProps<{
  title: string
  description?: string
}>()
</script>
