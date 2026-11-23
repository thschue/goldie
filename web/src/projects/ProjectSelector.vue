<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { listProjects, projectOf } from './api';

// ProjectSelector (handoff §2.1, §4): a combobox in the top bar, focus on it and
// options by aria-activedescendant. The choice is the `project` query parameter,
// never storage. The Project list is read when a Project is named or the list is
// first opened, so a page without one makes no call.
const route = useRoute();
const router = useRouter();
const current = computed(() => projectOf(route.query));
const projects = ref<string[]>([]);
const loaded = ref(false);
const open = ref(false);
const active = ref(0);
const narrow = ref(false);
const root = ref<HTMLElement>();
let media: MediaQueryList | undefined;
let loading = false;

const key = (id: string) => id.toLowerCase();
const registered = computed(() => projects.value.some((p) => key(p) === key(current.value)));
// Full id from 768 px, org/repo below.
const label = (id: string) => (narrow.value ? id.split('/').slice(-2).join('/') : id);

async function load() {
  if (loaded.value || loading) return;
  loading = true;
  try {
    projects.value = await listProjects();
    loaded.value = true;
  } catch {
    // The selector keeps working without the list: the options say so.
  } finally {
    loading = false;
  }
}

function show() {
  open.value = true;
  active.value = Math.max(projects.value.findIndex((p) => key(p) === key(current.value)), 0);
  void load();
}

const close = () => (open.value = false);

function choose(id: string) {
  close();
  void router.push({ query: { ...route.query, project: id } });
}

function move(by: number) {
  const n = projects.value.length;
  if (n) active.value = (active.value + by + n) % n;
}

function onKeydown(e: KeyboardEvent) {
  const k = e.key;
  if (!open.value) {
    if (k === 'Enter' || k === ' ' || k === 'ArrowDown' || k === 'ArrowUp') {
      e.preventDefault();
      show();
    }
    return;
  }
  if (k === 'Escape') {
    e.preventDefault();
    e.stopPropagation();
    close();
  } else if (k === 'ArrowDown' || k === 'ArrowUp') {
    e.preventDefault();
    move(k === 'ArrowDown' ? 1 : -1);
  } else if (k === 'Enter' || k === ' ') {
    e.preventDefault();
    const id = projects.value[active.value];
    if (id) choose(id);
    else close();
  } else if (k === 'Tab') {
    close();
  }
}

const onOutside = (e: MouseEvent) => {
  if (open.value && e.target instanceof Node && !root.value?.contains(e.target)) close();
};
const onMedia = (e: MediaQueryListEvent) => {
  narrow.value = e.matches;
};

onMounted(() => {
  media = window.matchMedia('(max-width: 767.98px)');
  narrow.value = media.matches;
  media.addEventListener('change', onMedia);
  document.addEventListener('click', onOutside);
  if (current.value) void load();
});
onBeforeUnmount(() => {
  media?.removeEventListener('change', onMedia);
  document.removeEventListener('click', onOutside);
});
watch(current, (id) => {
  if (id) void load();
});
</script>

<template>
  <div ref="root" class="selector">
    <span id="project-label" class="sr-only">Project</span>
    <button
      type="button"
      class="trigger"
      role="combobox"
      aria-labelledby="project-label project-value"
      aria-haspopup="listbox"
      aria-controls="project-listbox"
      :aria-expanded="open"
      :aria-activedescendant="open && projects.length ? `project-option-${active}` : undefined"
      @click="open ? close() : show()"
      @keydown="onKeydown"
      @keyup.space.prevent
    >
      <span v-if="current" id="project-value" class="id">{{ label(current) }}</span>
      <span v-else id="project-value" class="id">Choose a Project</span>
      <span v-if="current && loaded" class="chip" :class="registered ? 'yes' : 'no'">
        <svg viewBox="0 0 12 12" width="12" height="12" aria-hidden="true" focusable="false">
          <path v-if="registered" d="M2.5 6.5l2.5 2.5 4.5-5.5" />
          <circle v-else cx="6" cy="6" r="4" />
        </svg>
        {{ registered ? 'registered' : 'unregistered' }}
      </span>
      <svg class="caret" viewBox="0 0 12 12" width="12" height="12" aria-hidden="true" focusable="false">
        <path d="M2.5 4.5L6 8l3.5-3.5" />
      </svg>
    </button>
    <ul v-if="open" id="project-listbox" class="listbox" role="listbox" aria-label="Projects">
      <li
        v-for="(id, i) in projects"
        :id="`project-option-${i}`"
        :key="id"
        role="option"
        class="option"
        :class="{ active: i === active }"
        :aria-selected="key(id) === key(current)"
        @click="choose(id)"
      >
        <span class="id">{{ label(id) }}</span>
        <span class="chip yes">
          registered
        </span>
      </li>
      <li v-if="!projects.length" role="option" aria-disabled="true" aria-selected="false" class="option">
        {{ loaded ? 'No Projects registered yet.' : "Couldn't load Projects." }}
      </li>
    </ul>
  </div>
</template>

<style scoped>
.selector {
  position: relative;
  min-width: 0;
}
.trigger {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-width: 0;
  min-height: var(--target-min);
  padding: 0 var(--space-3);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--surface-1);
  color: var(--fg-default);
  font: inherit;
  text-align: left;
  cursor: pointer;
}
.id {
  min-width: 0;
  overflow: hidden;
  font-family: var(--font-mono);
  font-size: var(--text-md);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.chip {
  display: inline-flex;
  flex: none;
  align-items: center;
  gap: var(--space-1);
  padding: 2px var(--space-2);
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  color: var(--fg-secondary);
  font-size: var(--text-xs);
  font-weight: 600;
}
.chip.yes {
  background: var(--brand-tint);
  color: var(--brand-tint-ink);
}
.chip.no {
  border-style: dashed;
}
.chip svg,
.caret {
  flex: none;
  fill: none;
  stroke: currentColor;
  stroke-width: 1.8;
  stroke-linecap: round;
}
.listbox {
  position: absolute;
  top: calc(100% + var(--space-1));
  left: 0;
  z-index: 15;
  min-width: 100%;
  max-width: min(520px, calc(100vw - 2 * var(--space-4)));
  margin: 0;
  padding: var(--space-1);
  list-style: none;
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--surface-1);
  box-shadow: var(--shadow-lg);
}
.option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  min-height: var(--target-min);
  padding: 0 var(--space-3);
  border-radius: var(--radius-sm);
  cursor: pointer;
}
.option.active {
  background: var(--brand-tint);
}
</style>
