// The load every asset list tab shares (#758): one list for the Project in the URL, or the global
// list when none is selected. useLoad treats an empty key as "nothing to load", so the global view
// has a key of its own and the fetcher is given an empty Project (no `project` parameter).
import { computed, type Ref } from 'vue';
import { useLoad } from '../api/useLoad';

const global = '\0global';

export function useAssetLoad<T>(project: Ref<string>, fetcher: (project: string, signal: AbortSignal) => Promise<T>) {
  const key = computed(() => project.value || global);
  return useLoad(key, (k, signal) => fetcher(k === global ? '' : k, signal));
}
