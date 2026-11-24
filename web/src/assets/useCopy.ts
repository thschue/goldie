// Copy to the clipboard with a short result message (CommandSnippet, the Instructions digest):
// the message shows for 2.5 s and `clear` drops it at once.
import { onBeforeUnmount, ref } from 'vue';

export function useCopy(what: string) {
  const status = ref('');
  let timer: ReturnType<typeof setTimeout> | undefined;
  const clear = () => {
    clearTimeout(timer);
    status.value = '';
  };
  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      status.value = 'Copied';
    } catch {
      status.value = `Copy failed. Select the ${what} and copy it.`;
    }
    clearTimeout(timer);
    timer = setTimeout(() => (status.value = ''), 2500);
  }
  onBeforeUnmount(() => clearTimeout(timer));
  return { status, copy, clear };
}
