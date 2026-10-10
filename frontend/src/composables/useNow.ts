import { onBeforeUnmount, onMounted, ref, type Ref } from 'vue'

// A reactive clock for countdowns (Spec 115 lease text): ticks every
// `intervalMs` while the component is mounted, so "Lease ends in ..." and the
// Active -> Lease ended transition advance on an open page.
export function useNow(intervalMs = 30_000): Ref<number> {
  const now = ref(Date.now())
  let timer: ReturnType<typeof setInterval> | null = null
  onMounted(() => { timer = setInterval(() => { now.value = Date.now() }, intervalMs) })
  onBeforeUnmount(() => { if (timer) clearInterval(timer) })
  return now
}
