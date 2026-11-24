import { ref } from 'vue';

// True while the API is not answering (#638, handoff §5.1): the fetch failed, or the UI-mode
// proxy wrote its 502 `upstream_unavailable` envelope. Any other answer, errors included,
// clears it: those are the API talking.
export const unreachable = ref(false);

// Changes on every "Retry now", so the routed view is mounted again and asks again.
export const attempt = ref(0);

// note records one answer (or, with no response, a failed fetch). It reads the envelope code
// only; nothing of the body is kept.
export async function note(res?: Response): Promise<void> {
  if (!res) {
    unreachable.value = true;
    return;
  }
  if (res.status !== 502) {
    unreachable.value = false;
    return;
  }
  try {
    const body = (await res.clone().json()) as { error?: { code?: unknown } };
    if (body.error?.code === 'upstream_unavailable') unreachable.value = true;
  } catch {
    // Not the proxy's envelope: another 502 keeps its own alert.
  }
}
