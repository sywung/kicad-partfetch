import { computed, ref } from 'vue'

const MIN_SCALE = 1
const MAX_SCALE = 12
const WHEEL_SENSITIVITY = 0.0015
const KEY_ZOOM_STEP = 1.25
const KEY_PAN_STEP = 40

// Pan and zoom for a drawing that fills its stage. The layer is resized rather
// than transformed so the browser redraws the SVG at the new size and thin pin
// labels stay sharp when zoomed in.
export function usePanZoom() {
  const scale = ref(1)
  const offsetX = ref(0)
  const offsetY = ref(0)
  const dragging = ref(false)
  let lastX = 0
  let lastY = 0

  const zoomed = computed(() => scale.value !== 1 || offsetX.value !== 0 || offsetY.value !== 0)
  const layerStyle = computed(() => ({
    left: `${offsetX.value}px`,
    top: `${offsetY.value}px`,
    width: `${scale.value * 100}%`,
    height: `${scale.value * 100}%`,
  }))

  function reset() {
    scale.value = 1
    offsetX.value = 0
    offsetY.value = 0
  }

  // Keeps the drawing point under (x, y) fixed while the scale changes.
  function zoomAt(x: number, y: number, factor: number) {
    const next = Math.min(MAX_SCALE, Math.max(MIN_SCALE, scale.value * factor))
    if (next === scale.value) return
    const ratio = next / scale.value
    offsetX.value = x - (x - offsetX.value) * ratio
    offsetY.value = y - (y - offsetY.value) * ratio
    scale.value = next
    if (next === MIN_SCALE) {
      offsetX.value = 0
      offsetY.value = 0
    }
  }

  function stagePoint(event: MouseEvent) {
    const bounds = (event.currentTarget as HTMLElement).getBoundingClientRect()
    return { x: event.clientX - bounds.left, y: event.clientY - bounds.top, width: bounds.width, height: bounds.height }
  }

  function onWheel(event: WheelEvent) {
    const point = stagePoint(event)
    zoomAt(point.x, point.y, Math.exp(-event.deltaY * WHEEL_SENSITIVITY))
  }

  function onPointerDown(event: PointerEvent) {
    if (event.button !== 0) return
    dragging.value = true
    lastX = event.clientX
    lastY = event.clientY
    ;(event.currentTarget as HTMLElement).setPointerCapture(event.pointerId)
  }

  function onPointerMove(event: PointerEvent) {
    if (!dragging.value) return
    offsetX.value += event.clientX - lastX
    offsetY.value += event.clientY - lastY
    lastX = event.clientX
    lastY = event.clientY
  }

  function onPointerUp(event: PointerEvent) {
    if (!dragging.value) return
    dragging.value = false
    const target = event.currentTarget as HTMLElement
    if (target.hasPointerCapture(event.pointerId)) target.releasePointerCapture(event.pointerId)
  }

  function onKeydown(event: KeyboardEvent) {
    const stage = event.currentTarget as HTMLElement
    const centerX = stage.clientWidth / 2
    const centerY = stage.clientHeight / 2
    const actions: Record<string, () => void> = {
      '+': () => zoomAt(centerX, centerY, KEY_ZOOM_STEP),
      '=': () => zoomAt(centerX, centerY, KEY_ZOOM_STEP),
      '-': () => zoomAt(centerX, centerY, 1 / KEY_ZOOM_STEP),
      '0': reset,
      ArrowLeft: () => { offsetX.value += KEY_PAN_STEP },
      ArrowRight: () => { offsetX.value -= KEY_PAN_STEP },
      ArrowUp: () => { offsetY.value += KEY_PAN_STEP },
      ArrowDown: () => { offsetY.value -= KEY_PAN_STEP },
    }
    const action = actions[event.key]
    if (!action) return
    event.preventDefault()
    action()
  }

  return { scale, zoomed, dragging, layerStyle, reset, onWheel, onPointerDown, onPointerMove, onPointerUp, onKeydown }
}
