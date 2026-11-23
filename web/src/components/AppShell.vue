<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import RoleMenu from '../auth/RoleMenu.vue';
import ProjectSelector from '../projects/ProjectSelector.vue';
import GoldieMark from './GoldieMark.vue';
import SideNav from './SideNav.vue';
import ThemeToggle from './ThemeToggle.vue';

// AppShell (handoff §3.1, §4, §7): the top bar, the side nav and main. From
// 1024 px up the nav is inline; below, it is a modal drawer behind the menu
// button: focus moves in and is trapped, the page behind is inert, and Esc, a
// link or the scrim closes it and returns focus to the menu button. A link is
// a route change, so there focus moves to the new view's h1 instead (#473,
// WCAG 2.4.3). Only a click on an in-app link does that: the initial load and
// browser back or forward leave focus where it is.
// The role chip (#495) shows the session's role (account is false on the public
// sign-in route); its menu's Sign out is emitted.
withDefaults(defineProps<{ role?: 'reader' | 'operator'; account?: boolean }>(), { role: 'reader', account: true });
const emit = defineEmits<{ signout: [] }>();

const open = ref(false);
const menuButton = ref<HTMLButtonElement>();
const drawer = ref<HTMLElement>();
const main = ref<HTMLElement>();
let wide: MediaQueryList | undefined;
const router = useRouter();
let linkClicked = false;
// Whether the click now being handled asked for a route change. linkClicked is
// consumed by afterEach, which can run between the router's click handler and
// the drawer's: the router finishes in microtasks, and the browser runs them
// between two listeners of one real click.
let clickRequestedRoute = false;
let removeAfterEach: (() => void) | undefined;

const focusables = () => Array.from(drawer.value?.querySelectorAll<HTMLElement>('a[href], button') ?? []);

async function openDrawer() {
  open.value = true;
  await nextTick();
  focusables()[0]?.focus();
}

async function closeDrawer(returnFocus = true) {
  if (!open.value) return;
  open.value = false;
  await nextTick();
  if (returnFocus) menuButton.value?.focus();
}

function onKeydown(e: KeyboardEvent) {
  if (!open.value) return;
  if (e.key === 'Escape') {
    e.preventDefault();
    void closeDrawer();
    return;
  }
  if (e.key !== 'Tab') return;
  const els = focusables();
  const first = els[0];
  const last = els[els.length - 1];
  const active = document.activeElement;
  const target = !drawer.value?.contains(active) ? first : e.shiftKey && active === first ? last : !e.shiftKey && active === last ? first : undefined;
  if (target) {
    e.preventDefault();
    target.focus();
  }
}

// A link in the open drawer closes it once the router has taken the click.
function onDrawerClick(e: MouseEvent) {
  if (open.value && e.target instanceof Element && e.target.closest('a[href]')) void closeDrawer(!clickRequestedRoute);
}

// An unmodified click on an in-app link to another route marks the next route
// change as one the user asked for. Captured, so it is set before the router
// takes the click. A link to the current route, or one the browser follows
// itself (protocol-relative, a target or download), is no route change and
// leaves no mark behind to steal focus from a later one.
function onShellClick(e: MouseEvent) {
  const a = e.target instanceof Element ? e.target.closest('a[href^="/"]') : null;
  const href = a?.getAttribute('href') ?? '';
  linkClicked =
    !!a &&
    !href.startsWith('//') &&
    !e.defaultPrevented &&
    e.button === 0 &&
    !(e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) &&
    !a.getAttribute('target') &&
    !a.hasAttribute('download') &&
    router.resolve(href).fullPath !== router.currentRoute.value.fullPath;
  clickRequestedRoute = linkClicked;
}

// Back and forward are not link clicks: drop a mark a modified click left.
function onPopState() {
  linkClicked = false;
}

async function focusHeading() {
  // The router can finish before the drawer's own click handler runs: close
  // the drawer here, so main is no longer inert, and let the new view render.
  await closeDrawer(false);
  await nextTick();
  const h1 = main.value?.querySelector('h1');
  if (!h1) return;
  h1.tabIndex = -1;
  h1.focus();
}

// Growing past 1024 px puts the nav inline: close the drawer, keep focus.
function onWide(e: MediaQueryListEvent) {
  if (e.matches) void closeDrawer(false);
}

function skipToMain() {
  main.value?.focus();
}

onMounted(() => {
  document.addEventListener('keydown', onKeydown);
  wide = window.matchMedia('(min-width: 1024px)');
  wide.addEventListener('change', onWide);
  window.addEventListener('popstate', onPopState);
  removeAfterEach = router.afterEach(() => {
    const requested = linkClicked;
    linkClicked = false;
    if (requested) void focusHeading();
  });
});

onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown);
  wide?.removeEventListener('change', onWide);
  window.removeEventListener('popstate', onPopState);
  removeAfterEach?.();
});
</script>

<template>
  <div class="shell" @click.capture="onShellClick">
    <header class="topbar" :inert="open">
      <a class="skip-link" href="#main" @click.prevent="skipToMain">Skip to content</a>
      <RouterLink to="/" class="brand"><GoldieMark /><span class="sr-only sr-only-phone">goldie</span></RouterLink>
      <ProjectSelector />
      <div class="actions">
        <ThemeToggle />
        <RoleMenu v-if="account" :role="role" @signout="emit('signout')" />
        <button
          ref="menuButton"
          type="button"
          class="icon-button menu-button"
          aria-label="Menu"
          aria-controls="nav-drawer"
          :aria-expanded="open"
          @click="openDrawer"
        >
          <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true" focusable="false">
            <path d="M4 7h16M4 12h16M4 17h16" />
          </svg>
        </button>
      </div>
    </header>
    <div v-if="open" class="scrim" aria-hidden="true" @click="closeDrawer()"></div>
    <div
      id="nav-drawer"
      ref="drawer"
      class="sidebar"
      :class="{ open }"
      :role="open ? 'dialog' : undefined"
      :aria-modal="open ? 'true' : undefined"
      :aria-label="open ? 'Navigation' : undefined"
      @click="onDrawerClick"
    >
      <SideNav :role="role">
        <button type="button" class="icon-button close-button" aria-label="Close menu" @click="closeDrawer()">
          <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true" focusable="false">
            <path d="M6 6l12 12M18 6L6 18" />
          </svg>
        </button>
      </SideNav>
    </div>
    <main id="main" ref="main" tabindex="-1" :inert="open">
      <slot />
    </main>
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: 248px minmax(0, 1fr);
  grid-template-rows: var(--topbar-height) 1fr;
  grid-template-areas: 'top top' 'nav main';
  min-height: 100vh;
}
.topbar {
  grid-area: top;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  height: var(--topbar-height);
  padding: 0 var(--space-6);
  border-bottom: 1px solid var(--border-default);
  background: var(--surface-1);
}
.skip-link {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
.skip-link:focus {
  position: fixed;
  top: var(--space-2);
  left: var(--space-2);
  z-index: 30;
  display: inline-flex;
  align-items: center;
  width: auto;
  height: auto;
  min-height: var(--target-min);
  padding: 0 var(--space-4);
  clip-path: none;
  border-radius: var(--radius-sm);
  background: var(--surface-1);
  box-shadow: var(--shadow-lg);
  font-weight: 600;
}
.brand {
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  min-height: var(--target-min);
  color: var(--fg-default);
  font-family: var(--font-display);
  font-size: var(--text-display-sm);
  font-weight: 600;
  text-decoration: none;
}
.actions {
  margin-left: auto;
  display: flex;
  gap: var(--space-2);
}
.menu-button,
.close-button {
  display: none;
}
.sidebar {
  grid-area: nav;
  /* An inset edge, not a border, so the nav keeps the full column width. */
  box-shadow: inset -1px 0 0 var(--border-default);
  background: var(--surface-1);
}
main {
  grid-area: main;
  min-width: 0;
  padding: var(--space-10);
}
main:focus-visible {
  outline: none;
}
.scrim {
  position: fixed;
  inset: 0;
  z-index: 10;
  background: var(--scrim);
}

/* §7: 220 px nav from 1024 to 1279 px. */
@media (width < 1280px) {
  .shell {
    grid-template-columns: 220px minmax(0, 1fr);
  }
}

@media (width < 480px) {
  .topbar {
    gap: var(--space-2);
  }
  .brand {
    justify-content: center;
    min-width: var(--target-min);
  }
}

/* §7: below 1024 px the nav is a drawer behind the menu button. */
@media (width < 1024px) {
  .shell {
    grid-template-columns: minmax(0, 1fr);
    grid-template-areas: 'top' 'main';
  }
  .topbar {
    padding: 0 var(--space-4);
  }
  .menu-button {
    display: inline-grid;
  }
  .sidebar {
    display: none;
  }
  .sidebar.open {
    position: fixed;
    inset: 0 auto 0 0;
    z-index: 20;
    display: block;
    width: min(280px, calc(100vw - 64px));
    overflow-y: auto;
    box-shadow: var(--shadow-lg);
    transition: transform var(--transition-base);
  }
  @starting-style {
    .sidebar.open {
      transform: translateX(-100%);
    }
  }
  .close-button {
    display: inline-grid;
    margin: 0 0 var(--space-2) var(--space-3);
  }
  main {
    padding: var(--space-6) var(--space-4);
  }
}
</style>
