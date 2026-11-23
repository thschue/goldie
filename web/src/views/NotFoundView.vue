<template>
  <h1>Page not found</h1>
  <p>There's no page at this address. Check the link, or start again from Home.</p>
  <RouterLink to="/" class="home">Go to Home</RouterLink>
</template>

<style scoped>
.home {
  display: inline-flex;
  align-items: center;
  min-height: var(--target-min);
  font-weight: 600;
}
</style>
