<script setup lang="ts">
import ApiStatus from '@/components/ApiStatus.vue'
</script>

<template>
  <main class="app">
    <h1>Campaign Composer</h1>
    <ApiStatus />
  </main>
</template>

<style scoped>
.app {
  max-width: 40rem;
  margin: 4rem auto;
  padding: 0 1rem;
  font-family: system-ui, sans-serif;
  color: #212529;
}
</style>
