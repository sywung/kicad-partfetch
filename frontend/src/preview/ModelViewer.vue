<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '../lib/bridge'
import type { ModelData } from '../types/wails'

// installedName switches the viewer to a glTF that KiCad exported from the library's own files.
// externalPartId does the same for a GitHub repository that has not been added yet.
const props = defineProps<{ partId: string; footprintSvg: string; modelTitle: string; installedName?: string; externalPartId?: string }>()
const viaKiCad = computed(() => Boolean(props.installedName || props.externalPartId))
const note = ref('')
type Payload = { kind: 'obj'; data: ModelData } | { kind: 'glb'; bytes: ArrayBuffer }
const mountPoint = ref<HTMLDivElement | null>(null)
const busy = ref(true)
const error = ref('')
let disposeScene: (() => void) | undefined
let generation = 0

async function loadModel() {
  const current = ++generation
  disposeScene?.()
  disposeScene = undefined
  busy.value = true
  error.value = ''
  try {
    note.value = ''
    let payload: Payload
    if (viaKiCad.value) {
      const exported = props.installedName ? await api.modelInstalled(props.installedName) : await api.modelExternal(props.externalPartId ?? '')
      if (current !== generation) return
      note.value = [exported.File, exported.Note].filter(Boolean).join(' — ')
      payload = { kind: 'glb', bytes: decodeBase64(exported.GLB) }
    } else {
      payload = { kind: 'obj', data: await api.model(props.partId) }
    }
    if (current !== generation || !mountPoint.value) return
    await draw(payload, current)
  } catch (cause) {
    if (current === generation) error.value = typeof cause === 'string' ? cause : cause instanceof Error ? cause.message : '3D 模型載入失敗'
  } finally {
    if (current === generation) busy.value = false
  }
}

function decodeBase64(value: string): ArrayBuffer {
  const binary = atob(value)
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index++) bytes[index] = binary.charCodeAt(index)
  return bytes.buffer
}

async function draw(payload: Payload, current: number) {
  const [THREE, { OBJLoader }, { OrbitControls }, { SVGLoader }] = await Promise.all([
    import('three'),
    import('three/addons/loaders/OBJLoader.js'),
    import('three/addons/controls/OrbitControls.js'),
    import('three/addons/loaders/SVGLoader.js'),
  ])
  if (current !== generation || !mountPoint.value) return
  const host = mountPoint.value
  const scene = new THREE.Scene()
  const camera = new THREE.PerspectiveCamera(34, 1, 0.01, 10000)
  camera.up.set(0, 0, 1)
  const renderer = new THREE.WebGLRenderer({ alpha: true, antialias: true })
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2))
  renderer.setSize(host.clientWidth, host.clientHeight)
  renderer.outputColorSpace = THREE.SRGBColorSpace
  renderer.setClearColor(0x000000, 0)
  host.replaceChildren(renderer.domElement)

  let palette = getPalette()
  scene.background = new THREE.Color(palette.background)

  scene.add(new THREE.HemisphereLight(0xf2f1e8, 0x344957, 2.1))
  const keyLight = new THREE.DirectionalLight(0xfff1d8, 2.5)
  keyLight.position.set(-40, -35, 70)
  scene.add(keyLight)
  const fillLight = new THREE.DirectionalLight(0xa9d4e0, 1.2)
  fillLight.position.set(30, 45, 20)
  scene.add(fillLight)

  const reference = new THREE.Group()
  reference.name = 'footprint pad reference'
  try {
    // The pad reference is only drawn from the app's own footprint SVG; KiCad already places a glTF model.
    const parsed = payload.kind === 'obj' ? new SVGLoader().parse(props.footprintSvg) : { paths: [] }
    for (const path of parsed.paths) {
      const node = (path as typeof path & { userData: { node?: Element } }).userData?.node
      const layer = node?.getAttribute('class') || ''
      const color = palette[layer]
      if (!color) continue
      for (const shape of SVGLoader.createShapes(path)) {
        const geometry = new THREE.ShapeGeometry(shape)
        geometry.scale(1, -1, 1)
        const material = new THREE.MeshBasicMaterial({ color, transparent: true, opacity: layer === 'fp-hole' ? 1 : 0.72, side: THREE.DoubleSide, depthWrite: false })
        const mesh = new THREE.Mesh(geometry, material)
        mesh.userData.themeLayer = layer
        mesh.position.z = layer === 'fp-hole' ? 0.018 : 0
        reference.add(mesh)
      }
    }
  } catch {
    // The 2D reference remains visible beside the model if a footprint path is unsupported.
  }
  scene.add(reference)

  let object: InstanceType<typeof THREE.Object3D>
  let bodyMaterial: InstanceType<typeof THREE.MeshStandardMaterial> | undefined
  if (payload.kind === 'glb') {
    const { GLTFLoader } = await import('three/addons/loaders/GLTFLoader.js')
    const gltf = await new GLTFLoader().parseAsync(payload.bytes, '')
    if (current !== generation || !mountPoint.value) return
    object = gltf.scene
    // glTF is metres with Y up; the viewer works in millimetres with Z up. Materials stay as KiCad wrote them.
    object.rotation.x = Math.PI / 2
    object.scale.setScalar(1000)
    // VRML exports often carry neither normals nor materials, which renders as random dark facets.
    object.traverse(child => {
      if (!(child instanceof THREE.Mesh)) return
      if (!child.geometry.getAttribute('normal')) child.geometry.computeVertexNormals()
      for (const material of Array.isArray(child.material) ? child.material : [child.material]) material.side = THREE.DoubleSide
    })
  } else {
    const data = payload.data
    object = new OBJLoader().parse(data.OBJ)
    // data.Rotate is the KiCad footprint value, and KiCad turns the opposite way to three.js.
    // Negated angles in ZYX order reproduce the X-then-Y-then-Z rotation the offset was computed with.
    object.rotation.order = 'ZYX'
    object.rotation.set(...data.Rotate.map(value => -value * Math.PI / 180) as [number, number, number])
    object.position.set(...data.Offset)
    bodyMaterial = new THREE.MeshStandardMaterial({ color: palette.model, roughness: 0.58, metalness: 0.08, side: THREE.DoubleSide })
    object.traverse(child => {
      if (child instanceof THREE.Mesh) {
        child.material = bodyMaterial
        child.geometry.computeVertexNormals()
      }
    })
  }
  scene.add(object)
  const updateTheme = () => {
    palette = getPalette()
    scene.background = new THREE.Color(palette.background)
    scene.traverse(child => {
      if (!(child instanceof THREE.Mesh)) return
      const layer = child.userData.themeLayer as string | undefined
      const color = layer ? palette[layer] : palette.model
      if (!color) return
      for (const material of Array.isArray(child.material) ? child.material : [child.material]) {
        if ('color' in material) material.color.set(color)
      }
    })
    if (bodyMaterial) bodyMaterial.color.set(palette.model)
  }
  const themeObserver = new MutationObserver(updateTheme)
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  const systemTheme = window.matchMedia('(prefers-color-scheme: dark)')
  const onSystemTheme = () => {
    if (!document.documentElement.hasAttribute('data-theme')) updateTheme()
  }
  systemTheme.addEventListener('change', onSystemTheme)
  updateTheme()
  object.updateMatrixWorld(true)
  const box = new THREE.Box3().setFromObject(object)
  const center = box.getCenter(new THREE.Vector3())
  const size = box.getSize(new THREE.Vector3())
  const extent = Math.max(size.x, size.y, size.z, 1)
  // Without the pad reference around it, a KiCad-placed model can be framed more tightly.
  const reach = payload.kind === 'glb' ? 0.62 : 1
  camera.position.set(center.x + extent * 1.8 * reach, center.y - extent * 2.4 * reach, center.z + extent * 1.7 * reach)
  // An app-placed model sits on the footprint origin; a KiCad-placed one can be far from it, so orbit its centre.
  const focus = payload.kind === 'glb' ? center : new THREE.Vector3(0, 0, 0)
  camera.lookAt(focus)
  const controls = new OrbitControls(camera, renderer.domElement)
  controls.target.copy(focus)
  controls.enableDamping = true
  controls.dampingFactor = 0.07
  controls.screenSpacePanning = false
  controls.minDistance = extent * 0.4
  controls.maxDistance = extent * 9
  controls.update()

  const resize = new ResizeObserver(() => {
    if (!host.clientWidth || !host.clientHeight) return
    camera.aspect = host.clientWidth / host.clientHeight
    camera.updateProjectionMatrix()
    renderer.setSize(host.clientWidth, host.clientHeight)
  })
  resize.observe(host)
  let frame = 0
  const animate = () => {
    frame = requestAnimationFrame(animate)
    controls.update()
    renderer.render(scene, camera)
  }
  animate()

  disposeScene = () => {
    cancelAnimationFrame(frame)
    resize.disconnect()
    themeObserver.disconnect()
    systemTheme.removeEventListener('change', onSystemTheme)
    controls.dispose()
    scene.traverse(child => {
      if (child instanceof THREE.Mesh) {
        child.geometry.dispose()
        if (Array.isArray(child.material)) child.material.forEach(material => material.dispose())
        else child.material.dispose()
      }
    })
    renderer.dispose()
    renderer.domElement.remove()
  }
}

function getPalette(): Record<string, string> {
  const styles = getComputedStyle(document.documentElement)
  const read = (name: string) => styles.getPropertyValue(name).trim()
  return {
    background: read('--paper-raised'),
    'fp-cu-f': read('--copper-front'),
    'fp-cu-b': read('--copper-back'),
    'fp-cu-th': read('--copper-th'),
    'fp-hole': read('--hole'),
    model: read('--fab'),
  }
}

onMounted(loadModel)
watch(() => [props.partId, props.installedName, props.externalPartId], loadModel)
onBeforeUnmount(() => {
  generation++
  disposeScene?.()
})
</script>

<template>
  <div class="model-viewer">
    <div ref="mountPoint" class="model-canvas" aria-label="可旋轉的零件 3D 模型" />
    <div class="model-coordinate mono"><span>Z ↑</span><span>XY / MM</span></div>
    <div class="model-controls mono">拖曳旋轉 <i /> 滾輪縮放</div>
    <div v-if="note" class="model-note">{{ note }}</div>
    <div v-if="!viaKiCad" class="reference-card">
      <div class="reference-title"><span>PAD ALIGNMENT / XY</span><span class="mono">TOP</span></div>
      <div class="reference-svg svg-preview" v-html="footprintSvg" />
      <div class="reference-caption mono">{{ modelTitle || 'FOOTPRINT DATUM' }}</div>
    </div>
    <div v-if="busy" class="model-overlay"><span class="model-spinner" />{{ viaKiCad ? '由 KiCad 轉換 3D 模型…' : '讀取 OBJ 幾何資料…' }}</div>
    <div v-else-if="error" class="model-overlay model-error"><strong>無法顯示 3D</strong><span>{{ error }}</span></div>
    <div v-else class="model-status"><i /> MODEL READY</div>
  </div>
</template>

<style scoped>
.model-viewer { position: absolute; inset: 0; overflow: hidden; }
.model-canvas { position: absolute; inset: 0; }
.model-canvas :deep(canvas) { display: block; width: 100%; height: 100%; }
/* The viewer fills the whole drawing area, so its labels clear the sheet label and title block drawn on top. */
.model-coordinate { position: absolute; top: 36px; left: 17px; display: flex; flex-direction: column; gap: 2px; color: var(--ink-faint); font-size: 9px; pointer-events: none; }
.model-coordinate span:first-child { color: var(--accent); font-size: 11px; }
.model-controls { position: absolute; left: 17px; bottom: 74px; color: var(--ink-faint); font-size: 9px; pointer-events: none; }
.model-note { position: absolute; left: 17px; right: 17px; bottom: 92px; color: var(--ink-soft); font-size: 9px; line-height: 1.5; pointer-events: none; }
.model-controls i { display: inline-block; width: 3px; height: 3px; margin: 0 5px 2px; border-radius: 50%; background: var(--accent); }
.reference-card { position: absolute; right: 12px; bottom: 70px; width: min(32%, 190px); height: min(46%, 185px); display: flex; flex-direction: column; border: 1px solid var(--rule-strong); background: color-mix(in srgb, var(--paper-raised) 93%, transparent); box-shadow: var(--shadow); }
.reference-title,.reference-caption { display: flex; justify-content: space-between; align-items: center; color: var(--ink-faint); font: 8px var(--mono); letter-spacing: .04em; padding: 5px 7px; }
.reference-title { border-bottom: 1px solid var(--rule); }.reference-caption { border-top: 1px solid var(--rule); color: var(--ink-soft); }
.reference-svg { flex: 1; min-height: 0; padding: 11px; }
.reference-svg :deep(svg) { max-width: 100%; max-height: 100%; }
.model-status { position: absolute; right: 36px; top: 36px; color: var(--success); font: 8px var(--mono); }.model-status i { display: inline-block; width: 5px; height: 5px; margin: 0 4px 1px 0; border-radius: 50%; background: var(--success); }
.model-overlay { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; flex-direction: column; gap: 9px; background: color-mix(in srgb, var(--paper-raised) 82%, transparent); color: var(--ink-soft); text-align: center; font-size: 11px; }
.model-error strong { color: var(--danger); font-weight: 500; }.model-error span { max-width: 80%; font-size: 10px; }
.model-spinner { width: 20px; height: 20px; border: 1px solid var(--rule-strong); border-top-color: var(--accent); border-radius: 50%; animation: orbit .8s linear infinite; }
@keyframes orbit { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .model-spinner { animation-duration: 2s; } }
</style>
