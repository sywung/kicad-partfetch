<script setup lang="ts">
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import type { PreviewData } from '../types/wails'
import { displayPartID } from '../lib/partid'
import { recolorKiCadSVG, type KiCadDrawing } from '../lib/kicadColors'
import { usePanZoom } from '../lib/panzoom'
import { useThemeVersion } from '../lib/theme'

const ModelViewer = defineAsyncComponent(() => import('./ModelViewer.vue'))
const props = defineProps<{ preview: PreviewData | null; loading: boolean; activePartID: string; error: string }>()
const emit = defineEmits<{ 'open-url': [url: string] }>()
type Tab = 'symbol' | 'footprint' | 'model'
const tab = ref<Tab>('symbol')
const unit = ref(0)
const footprintUnit = ref(0)
const tabs = computed(() => [
  { id: 'symbol' as const, label: '符號', marker: 'S' },
  { id: 'footprint' as const, label: '封裝', marker: 'F' },
  { id: 'model' as const, label: '3D 模型', marker: '3D' },
])
const safeTab = (target: Tab) => {
  if (target === 'model' && !props.preview?.HasModel) return
  tab.value = target
}
const themeVersion = useThemeVersion()
const view = usePanZoom()
const zoomPercent = computed(() => Math.round(view.scale.value * 100))
// kicad-cli SVGs are shown through <img>, where page CSS cannot reach, so their colours are swapped for the theme's here.
function svgData(svg: string, drawing: KiCadDrawing) {
  themeVersion.value
  const styles = getComputedStyle(document.documentElement)
  const themed = recolorKiCadSVG(svg, drawing, name => styles.getPropertyValue(name).trim())
  const bytes = new TextEncoder().encode(themed)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return `data:image/svg+xml;base64,${btoa(binary)}`
}
const hasDrawing = computed(() => {
  const current = props.preview
  if (!current) return false
  if (tab.value === 'symbol') return Boolean(current.SymbolSVGs[unit.value])
  return Boolean(current.External ? current.FootprintSVGs[footprintUnit.value] : current.FootprintSVG)
})
const stageEvents = {
  onWheel: (event: WheelEvent) => { event.preventDefault(); view.onWheel(event) },
  onPointerdown: view.onPointerDown,
  onPointermove: view.onPointerMove,
  onPointerup: view.onPointerUp,
  onPointercancel: view.onPointerUp,
  onDblclick: () => view.reset(),
  onKeydown: view.onKeydown,
}
const modelFiles = computed(() => (props.preview?.Files ?? []).filter(path => /\.(step|stp|wrl)$/i.test(path)))
watch(() => props.activePartID, () => { tab.value = 'symbol'; unit.value = 0; footprintUnit.value = 0 })
watch([() => props.activePartID, tab, unit, footprintUnit], () => view.reset())
</script>

<template>
  <section class="preview-pane panel" aria-label="零件預覽">
    <header class="preview-header">
      <div class="preview-heading">
        <div class="section-code mono">02 / INSPECTION</div>
        <div class="preview-title-row">
          <h2 :title="preview?.Name">{{ preview?.Name || '零件檢視' }}</h2>
          <span v-if="activePartID" class="part-stamp mono" :title="activePartID">{{ displayPartID(activePartID) }}</span>
        </div>
      </div>
      <div class="view-tabs" role="tablist" aria-label="預覽類型">
        <button
          v-for="item in tabs"
          :key="item.id"
          type="button"
          role="tab"
          :aria-selected="tab === item.id"
          :disabled="item.id === 'model' && !preview?.HasModel"
          :class="{ selected: tab === item.id }"
          @click="safeTab(item.id)"
        ><span class="tab-marker mono">{{ item.marker }}</span>{{ item.label }}</button>
      </div>
    </header>

    <div v-if="preview?.Source === 'easyeda'" class="community-warning">△ 使用者貢獻，未經審核，請自行確認腳位與尺寸</div>
    <div v-if="preview?.Source === 'library'" class="external-source-row"><span>元件庫內的檔案</span><span>由 kicad-cli 繪製，與 KiCad 中所見相同</span></div>
    <div v-else-if="preview?.External" class="external-source-row"><a :href="preview.URL" @click.prevent="emit('open-url', preview.URL)">{{ preview.PartID.slice('github:'.length) }} ↗</a><span>{{ preview.License || '未聲明授權' }}</span><span>{{ preview.Files.length }} 個檔案</span><strong>第三方檔案，請自行確認腳位與尺寸</strong></div>

    <div class="drawing-area">
      <div class="sheet-label mono"><span>PART / {{ activePartID ? displayPartID(activePartID) : '—' }}</span><span>REV 00 · MM</span></div>
      <div v-if="loading" class="preview-message"><span class="large-spinner" />載入零件資料與圖形</div>
      <div v-else-if="error" class="preview-message error-message"><span class="message-symbol">!</span><strong>{{ error }}</strong></div>
      <div v-else-if="!preview" class="preview-message waiting-message">
        <div class="target-mark"><i /><i /><i /><i /></div>
        <strong>選取一個零件開始檢視</strong>
        <span>符號、封裝與 3D 模型會顯示在此圖框</span>
        <span class="waiting-coord mono">X ——— Y ——— Z ———</span>
      </div>
      <template v-else>
        <div v-if="tab === 'symbol' && preview.External && preview.SymbolSVGs[unit]" class="svg-stage" :class="{ dragging: view.dragging.value }" role="img" tabindex="0" :aria-label="`${preview.Name} 符號`" v-bind="stageEvents"><div class="zoom-layer external-svg-layer" :style="view.layerStyle.value"><img :src="svgData(preview.SymbolSVGs[unit], 'symbol')" :alt="`${preview.Name} 符號`" draggable="false" /></div></div>
        <div v-else-if="tab === 'symbol' && preview.External" class="external-empty">沒有可顯示的符號預覽</div>
        <div v-else-if="tab === 'symbol'" class="svg-stage" :class="{ dragging: view.dragging.value }" role="img" tabindex="0" :aria-label="`${preview.Name} 符號`" v-bind="stageEvents"><div class="zoom-layer svg-preview" :style="view.layerStyle.value" v-html="preview.SymbolSVGs[unit]" /></div>
        <div v-else-if="tab === 'footprint' && preview.External && preview.FootprintSVGs[footprintUnit]" class="svg-stage footprint-stage" :class="{ dragging: view.dragging.value }" role="img" tabindex="0" :aria-label="`${preview.Name} 封裝`" v-bind="stageEvents"><div class="zoom-layer external-svg-layer" :style="view.layerStyle.value"><img :src="svgData(preview.FootprintSVGs[footprintUnit], 'footprint')" :alt="`${preview.Name} 封裝`" draggable="false" /></div></div>
        <div v-else-if="tab === 'footprint' && preview.External" class="external-empty">沒有可顯示的封裝預覽</div>
        <div v-else-if="tab === 'footprint'" class="svg-stage footprint-stage" :class="{ dragging: view.dragging.value }" role="img" tabindex="0" :aria-label="`${preview.Package} 封裝`" v-bind="stageEvents"><div class="zoom-layer svg-preview" :style="view.layerStyle.value" v-html="preview.FootprintSVG" /></div>
        <div v-else-if="tab === 'model' && preview.Source === 'library'" class="model-stage">
          <ModelViewer :key="`installed:${preview.Name}`" :part-id="activePartID" :installed-name="preview.Name" footprint-svg="" :model-title="preview.ModelTitle" @open-url="emit('open-url', $event)" />
        </div>
        <div v-else-if="tab === 'model' && preview.Source === 'github'" class="model-stage">
          <ModelViewer :key="`external:${preview.PartID}`" :part-id="activePartID" :external-part-id="preview.PartID" footprint-svg="" :model-title="preview.ModelTitle" @open-url="emit('open-url', $event)" />
        </div>
        <div v-else-if="tab === 'model' && preview.External" class="external-model-stage"><strong>這個來源的 3D 模型無法在此預覽</strong><ul><li v-for="file in modelFiles" :key="file">{{ file }}</li></ul></div>
        <div v-else class="model-stage">
          <ModelViewer :key="activePartID" :part-id="activePartID" :footprint-svg="preview.FootprintSVG" :model-title="preview.ModelTitle" @open-url="emit('open-url', $event)" />
        </div>
        <div v-if="tab !== 'model' && hasDrawing" class="view-hint mono">
          <span>拖曳平移 <i /> 滾輪縮放</span>
          <button v-if="view.zoomed.value" type="button" class="view-reset" @click="view.reset()">{{ zoomPercent }}% · 重設</button>
        </div>
        <div v-if="tab === 'symbol' && preview.SymbolSVGs.length > 1" class="unit-picker">
          <span class="mono">UNIT</span>
          <button v-for="(_, index) in preview.SymbolSVGs" :key="index" type="button" :class="{ chosen: unit === index }" @click="unit = index">{{ index + 1 }}</button>
          <span class="unit-total mono">/ {{ preview.SymbolSVGs.length }}</span>
        </div>
        <div v-if="tab === 'footprint' && preview.FootprintSVGs.length > 1" class="unit-picker footprint-picker"><span class="mono">FOOTPRINT</span><button v-for="(_, index) in preview.FootprintSVGs" :key="index" type="button" :class="{ chosen: footprintUnit === index }" @click="footprintUnit = index">{{ index + 1 }}</button><span class="unit-total mono">/ {{ preview.FootprintSVGs.length }}</span></div>
      </template>
      <div class="sheet-cross cross-a" aria-hidden="true">┼</div><div class="sheet-cross cross-b" aria-hidden="true">┼</div>
      <div class="drawing-title-block">
        <div class="title-identity"><span class="title-kicker">COMPONENT RECORD</span><strong class="mono" :title="preview?.PartID">{{ preview?.PartID ? displayPartID(preview.PartID) : 'NO SELECTION' }}</strong></div>
        <div class="title-cell"><span>封裝 / PACKAGE</span><strong class="mono">{{ preview?.Package || '—' }}</strong></div>
        <div class="title-cell pin-cell"><span>腳數 / PINS</span><strong class="mono">{{ preview && !preview.External ? preview.PinCount : '—' }}</strong></div>
      </div>
    </div>

    <footer class="info-strip">
      <div class="info-main">
        <span class="info-label">製造商 / 型號</span>
        <strong>{{ preview?.Manufacturer || '—' }} <span v-if="preview?.MPN" class="mono">· {{ preview.MPN }}</span></strong>
      </div>
      <div class="info-stat"><span class="info-label">焊盤</span><strong class="mono">{{ preview && !preview.External ? preview.PadCount : '—' }}</strong></div>
      <div class="info-warning" :class="{ 'has-warning': (preview?.Warnings.length ?? 0) > 0 }">
        <span class="warning-diamond">△</span>
        <span>{{ preview?.Warnings.length ? `${preview.Warnings.length} 項注意` : '檢視無警告' }}</span>
        <span v-if="preview?.Warnings.length" class="warning-detail" :title="preview.Warnings.join('\n')">{{ preview.Warnings[0] }}</span>
      </div>
    </footer>
  </section>
</template>

<style scoped>
.preview-pane { display: flex; flex-direction: column; min-height: 0; }
.preview-header { min-height: 85px; display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 15px 20px 12px; border-bottom: 1px solid var(--rule); }
.section-code { color: var(--fab); font-size: 10px; letter-spacing: .12em; }
/* Long repo names must shrink here instead of squeezing the tabs into a vertical stack. */
.preview-heading { flex: 1; min-width: 0; }
.preview-title-row { display: flex; flex-direction: column; gap: 1px; min-width: 0; min-height: 28px; }
h2 { min-width: 0; margin: 1px 0 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 17px; font-weight: 500; }
.part-stamp { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--accent); font-size: 11px; }
.community-warning { padding: 7px 14px; border-bottom: 1px solid var(--rule); background: color-mix(in srgb, var(--warning) 8%, var(--paper-raised)); color: var(--warning); font-size: 10px; }
.external-source-row { display: flex; align-items: center; gap: 12px; padding: 7px 14px; border-bottom: 1px solid var(--rule); color: var(--ink-soft); font-size: 9px; }
.external-source-row a { color: var(--focus); text-decoration: none; }
.external-source-row strong { margin-left: auto; color: var(--warning); font-weight: 500; }
.view-tabs { display: flex; flex: none; align-self: end; gap: 4px; }
.view-tabs button { white-space: nowrap; border: 1px solid transparent; padding: 7px 9px; background: transparent; color: var(--ink-soft); cursor: pointer; font-size: 11px; }
.view-tabs button:hover:not(:disabled) { border-color: var(--rule); color: var(--ink); }
.view-tabs button.selected { border-color: var(--rule-strong); background: var(--paper-raised); color: var(--ink); box-shadow: inset 0 -2px var(--accent); }
.view-tabs button:disabled { opacity: .4; cursor: not-allowed; }
.tab-marker { color: var(--accent); font-size: 9px; margin-right: 5px; }
.drawing-area { position: relative; flex: 1; min-height: 280px; margin: 13px; overflow: hidden; border: 1px solid var(--rule-strong); background-color: var(--paper-raised); background-image: linear-gradient(var(--grid) 1px, transparent 1px), linear-gradient(90deg, var(--grid) 1px, transparent 1px), linear-gradient(var(--grid-major) 1px, transparent 1px), linear-gradient(90deg, var(--grid-major) 1px, transparent 1px); background-size: 10px 10px, 10px 10px, 50px 50px, 50px 50px; }
.drawing-area::before { content: ''; position: absolute; inset: 6px; border: 1px solid color-mix(in srgb, var(--rule) 55%, transparent); pointer-events: none; }
.sheet-label { position: absolute; top: 12px; left: 16px; right: 16px; display: flex; justify-content: space-between; z-index: 2; color: var(--ink-faint); font-size: 9px; letter-spacing: .08em; pointer-events: none; }
/* The stage is the viewport; the layer inside it is what pan and zoom move. A zoomed drawing may
   spill past the stage and is clipped by the drawing area, under the sheet label and title block. */
.svg-stage { position: absolute; inset: 9% 10% 22%; cursor: grab; touch-action: none; user-select: none; }
.svg-stage.dragging { cursor: grabbing; }
.svg-stage:focus-visible { outline: 1px dashed var(--focus); outline-offset: 6px; }
.footprint-stage { inset: 10% 10% 22%; }
.zoom-layer { position: absolute; }
.view-hint { position: absolute; left: 17px; top: 36px; z-index: 1; display: flex; align-items: center; gap: 10px; color: var(--ink-faint); font-size: 9px; }
.view-hint i { display: inline-block; width: 3px; height: 3px; margin: 0 5px 2px; border-radius: 50%; background: var(--accent); }
.view-reset { padding: 2px 6px; border: 1px solid var(--rule); background: var(--paper-raised); color: var(--accent); cursor: pointer; font: 9px var(--mono); }
.view-reset:hover { border-color: var(--accent); }
/* kicad-cli SVGs carry a small intrinsic size in mm; fill the stage instead of showing them at that size. */
.external-svg-layer img { display: block; width: 100%; height: 100%; object-fit: contain; }
.external-empty { position: absolute; inset: 15% 8% 20%; display: grid; place-items: center; color: var(--ink-faint); font-size: 11px; }
.external-model-stage { position: absolute; inset: 15% 10% 20%; overflow: auto; color: var(--ink-soft); font-size: 10px; }
.external-model-stage strong { color: var(--warning); font-weight: 500; }
.external-model-stage ul { padding-left: 18px; line-height: 1.7; }
.footprint-picker { max-width: calc(100% - 34px); overflow-x: auto; }
/* Fills the drawing area: a smaller opaque box left a strip of grid showing around the model. */
.model-stage { position: absolute; inset: 0; }
.preview-message { position: absolute; inset: 42px 20px 100px; display: flex; align-items: center; justify-content: center; flex-direction: column; gap: 9px; text-align: center; color: var(--ink-soft); font-size: 11px; }
.preview-message strong { color: var(--ink); font-size: 13px; font-weight: 500; }
.waiting-coord { color: var(--ink-faint); font-size: 9px; margin-top: 8px; letter-spacing: .1em; }
.target-mark { position: relative; width: 54px; height: 54px; border: 1px solid var(--rule-strong); border-radius: 50%; margin-bottom: 8px; }
.target-mark::before,.target-mark::after { content: ''; position: absolute; background: var(--rule-strong); }
.target-mark::before { width: 72px; height: 1px; left: -10px; top: 26px; }
.target-mark::after { height: 72px; width: 1px; top: -10px; left: 26px; }
.target-mark i { position: absolute; width: 5px; height: 5px; border: 1px solid var(--accent); z-index: 1; }
.target-mark i:nth-child(1) { left: -3px; top: -3px; }.target-mark i:nth-child(2) { right: -3px; top: -3px; }.target-mark i:nth-child(3) { left: -3px; bottom: -3px; }.target-mark i:nth-child(4) { right: -3px; bottom: -3px; }
.large-spinner { width: 24px; height: 24px; border: 1px solid var(--rule); border-top-color: var(--accent); border-radius: 50%; animation: turn 1s linear infinite; }
@keyframes turn { to { transform: rotate(360deg); } }
.error-message { color: var(--danger); }.message-symbol { display: grid; place-items: center; width: 29px; height: 29px; border: 1px solid var(--danger); border-radius: 50%; font: 16px var(--mono); }
.unit-picker { position: absolute; left: 17px; bottom: 76px; display: flex; align-items: center; gap: 5px; z-index: 1; color: var(--ink-soft); font-size: 9px; }
.unit-picker > span:first-child { margin-right: 3px; color: var(--ink-faint); }
.unit-picker button { width: 23px; height: 23px; border: 1px solid var(--rule); background: var(--paper-raised); cursor: pointer; font: 10px var(--mono); }
.unit-picker button:hover,.unit-picker button.chosen { border-color: var(--accent); color: var(--accent); }
.unit-total { margin-left: 2px; color: var(--ink-faint); }
.drawing-title-block { position: absolute; right: 12px; bottom: 12px; display: grid; grid-template-columns: minmax(120px, 1fr) minmax(120px, .8fr) 64px; min-width: min(480px, 80%); border: 1px solid var(--rule-strong); background: var(--paper-raised); z-index: 1; }
.title-identity,.title-cell { min-width: 0; min-height: 44px; display: flex; flex-direction: column; justify-content: center; padding: 5px 9px; }
.title-identity { border-right: 1px solid var(--rule); }.title-cell { border-right: 1px solid var(--rule); }
.title-identity strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--accent); font-size: 13px; }
.title-kicker,.title-cell span { color: var(--ink-faint); font-size: 8px; letter-spacing: .08em; }
.title-cell strong { overflow: hidden; white-space: nowrap; text-overflow: ellipsis; font-size: 10px; font-weight: 500; }
.pin-cell { border: 0; }.pin-cell strong { font-size: 16px; }
.sheet-cross { position: absolute; z-index: 1; color: var(--rule-strong); font-size: 13px; line-height: 1; }.cross-a { left: 13px; bottom: 13px; }.cross-b { right: 13px; top: 34px; }
.info-strip { display: flex; align-items: center; gap: 18px; min-height: 65px; padding: 8px 19px 11px; border-top: 1px solid var(--rule); }
.info-main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }.info-main strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 11px; font-weight: 500; }.info-main strong .mono { color: var(--ink-soft); font-weight: 400; }
.info-label { color: var(--ink-faint); font-size: 9px; letter-spacing: .04em; }.info-stat { display: flex; min-width: 45px; flex-direction: column; align-items: center; border-left: 1px solid var(--rule); border-right: 1px solid var(--rule); padding: 0 12px; }.info-stat strong { font-size: 14px; font-weight: 400; }
.info-warning { display: flex; align-items: center; gap: 5px; color: var(--success); font-size: 10px; }.warning-diamond { font-size: 13px; }.info-warning.has-warning { color: var(--warning); }.warning-detail { max-width: 160px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--ink-faint); font-size: 9px; }
@media (prefers-reduced-motion: reduce) { .large-spinner { animation-duration: 2s; } }
</style>
