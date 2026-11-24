// One load with its states (#587): loading, ready, error and the
// 429 countdown. The skeleton shows only after `delay` ms, so a fast load never flashes one.
import { computed, onBeforeUnmount, ref, watch, type Ref } from 'vue';
import { ApiError } from './client';

export function useLoad<T>(key: Ref<string>, fetcher: (key: string, signal: AbortSignal) => Promise<T>, delay = 300) {
  const state = ref<'idle' | 'loading' | 'ready' | 'error'>('idle');
  const data = ref<T>();
  const failure = ref<ApiError>();
  const wait = ref(0);
  const slow = ref(false);
  let controller: AbortController | undefined;
  let ticker: ReturnType<typeof setInterval> | undefined;
  let slowTimer: ReturnType<typeof setTimeout> | undefined;

  const stop = () => {
    clearInterval(ticker);
    clearTimeout(slowTimer);
  };

  function countdown(seconds: number) {
    wait.value = seconds;
    ticker = setInterval(() => {
      if (--wait.value <= 0) clearInterval(ticker);
    }, 1000);
  }

  async function load() {
    controller?.abort();
    stop();
    wait.value = 0;
    slow.value = false;
    data.value = undefined;
    if (!key.value) {
      state.value = 'idle';
      return;
    }
    const mine = (controller = new AbortController());
    state.value = 'loading';
    slowTimer = setTimeout(() => (slow.value = true), delay);
    try {
      const v = await fetcher(key.value, mine.signal);
      if (mine.signal.aborted) return;
      data.value = v;
      state.value = 'ready';
    } catch (e) {
      if (mine.signal.aborted) return;
      failure.value = e instanceof ApiError ? e : new ApiError(0, "Couldn't load this.");
      if (failure.value.status === 429) countdown(failure.value.retryAfter);
      state.value = 'error';
    } finally {
      if (!mine.signal.aborted) clearTimeout(slowTimer);
    }
  }

  watch(key, load, { immediate: true });
  onBeforeUnmount(() => {
    controller?.abort();
    stop();
  });

  return {
    state,
    data,
    failure,
    wait,
    skeleton: computed(() => state.value === 'loading' && slow.value),
    limited: computed(() => failure.value?.status === 429),
    retry: load,
  };
}
