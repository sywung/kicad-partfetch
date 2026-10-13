<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import SearchPane from './search/SearchPane.vue'
import PreviewPane from './preview/PreviewPane.vue'
import LibraryPane from './library/LibraryPane.vue'
import { api, onProgress } from './lib/bridge'
import { useTheme, type Theme } from './lib/theme'
import { APP_VERSION } from './lib/version'
import type { AddResult, GuiSettings, ImportResult, LibraryEntry, PreviewData, ProgressEvent, RemoveResult, SearchItem } from './types/wails'

interface LibraryRecord { Title: string; Summary: string; Status: string; Mark: string; Error: string; Warnings: string[] }

const query = ref('')
const { theme, setTheme } = useTheme()
const themeOptions: { value: Theme; label: string }[] = [
  { value: 'auto', label: '自動' },
  { value: 'light', label: '淺色' },
  { value: 'dark', label: '深色' },
]
const themeButtons: (HTMLButtonElement | null)[] = []
type SearchSource = 'lcsc' | 'easyeda' | 'github'
function storedSource(): SearchSource {
  try {
    const value = localStorage.getItem('partfetch-search-source')
    return value === 'easyeda' || value === 'github' ? value : 'lcsc'
  } catch { return 'lcsc' }
}
const source = ref<SearchSource>(storedSource())
const items = ref<SearchItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(25)
const selected = ref<string[]>([])
const activePartID = ref('')
const loadingSearch = ref(false)
const loadingPreview = ref(false)
const adding = ref(false)
const importing = ref(false)
const previewData = ref<PreviewData | null>(null)
const previewError = ref('')
const message = ref('')
const messageKind = ref<'error' | 'note'>('note')
const settings = ref<GuiSettings>({ OutDir: '', LibName: 'partfetch', ModelBase: 'absolute', With3D: true, Overwrite: false })
const savedSettings = ref('')
const results = ref<LibraryRecord[]>([])
const entries = ref<LibraryEntry[]>([])
const disposeProgress = ref<(() => void) | undefined>()

// With nothing ticked, the add button acts on the part being previewed.
// A part previewed from the library itself is already installed, so it is never an add target.
const previewingInstalled = ref(false)
const previewTarget = computed(() => !selected.value.length && previewData.value && !previewingInstalled.value ? activePartID.value : '')
const addTargets = computed(() => selected.value.length ? selected.value : previewTarget.value ? [previewTarget.value] : [])
const settingsDirty = computed(() => JSON.stringify(settings.value) !== savedSettings.value)
const footerState = computed(() => adding.value || importing.value ? 'WRITING LIBRARY' : loadingSearch.value || loadingPreview.value ? 'FETCHING DATA' : messageKind.value === 'error' && message.value ? 'ATTENTION' : 'READY FOR INPUT')

onMounted(async () => {
  disposeProgress.value = onProgress(handleProgress)
  try {
    settings.value = await api.getSettings()
    savedSettings.value = JSON.stringify(settings.value)
    await refreshLibrary()
  } catch (cause) {
    showMessage(errorText(cause), 'error')
  }
})
onBeforeUnmount(() => disposeProgress.value?.())
watch(settings, () => { if (messageKind.value === 'error' && message.value.includes('設定')) message.value = '' }, { deep: true })
watch(source, value => {
  try { localStorage.setItem('partfetch-search-source', value) } catch {}
  items.value = []
  total.value = 0
  page.value = 1
  activePartID.value = ''
  previewData.value = null
  previewError.value = ''
  loadingSearch.value = false
  loadingPreview.value = false
})

function showMessage(text: string, kind: 'error' | 'note' = 'error') {
  message.value = text
  messageKind.value = kind
}
function errorText(cause: unknown) { return typeof cause === 'string' ? cause : cause instanceof Error ? cause.message : '作業失敗，請稍後重試。' }
function togglePart(id: string) {
  selected.value = selected.value.includes(id) ? selected.value.filter(part => part !== id) : [...selected.value, id]
}
async function searchParts(nextPage = 1) {
  if (!query.value.trim()) { showMessage('請輸入零件型號或 C 編號。'); return }
  loadingSearch.value = true
  const requestedSource = source.value
  message.value = ''
  page.value = nextPage
  try {
    const response = await api.search(requestedSource, query.value, nextPage)
    if (source.value !== requestedSource) return
    items.value = response.Items ?? []
    total.value = response.Total
    pageSize.value = response.PageSize || 25
    if (items.value.length) await selectItem(items.value[0])
    else { activePartID.value = ''; previewData.value = null }
    if (!items.value.length) showMessage('沒有符合的零件。', 'note')
  } catch (cause) {
    if (source.value !== requestedSource) return
    showMessage(errorText(cause))
  } finally {
    if (source.value === requestedSource) loadingSearch.value = false
  }
}
function selectItem(item: Pick<SearchItem, 'PartID'>) { return loadPreview(item.PartID, false) }
// Installed entries are drawn from the library files: that is what KiCad will use, and imported parts have no online source.
function selectInstalled(name: string) { return loadPreview(name, true) }
async function loadPreview(id: string, installed: boolean) {
  activePartID.value = id
  previewingInstalled.value = installed
  const requestedPartID = id
  loadingPreview.value = true
  previewData.value = null
  previewError.value = ''
  try {
    const response = installed ? await api.previewInstalled(id) : await api.preview(id)
    if (activePartID.value !== requestedPartID) return
    previewData.value = response
  } catch (cause) {
    if (activePartID.value !== requestedPartID) return
    previewError.value = errorText(cause)
  } finally {
    if (activePartID.value === requestedPartID) loadingPreview.value = false
  }
}
async function saveSettings() {
  try {
    await api.saveSettings(settings.value)
    savedSettings.value = JSON.stringify(settings.value)
    showMessage('元件庫設定已儲存。', 'note')
    await refreshLibrary()
  } catch (cause) { showMessage(errorText(cause)) }
}
async function pickDirectory() {
  try {
    const path = await api.pickDirectory()
    if (path) settings.value.OutDir = path
  } catch (cause) { showMessage(errorText(cause)) }
}
async function refreshLibrary() {
  try { entries.value = await api.listLibrary() } catch (cause) { showMessage(errorText(cause)) }
}
function removalStatusText(value: string) {
  const labels: Record<string, string> = { removed: '已移除', kept: '保留（仍被使用）', missing: '檔案不存在', none: '無', failed: '失敗' }
  return labels[value] ?? value
}
async function removeLibraryEntry(name: string) {
  try {
    const results: RemoveResult[] = await api.removeFromLibrary([name])
    await refreshLibrary()
    const result = results[0]
    if (!result) {
      showMessage('移除失敗，未收到結果。', 'error')
    } else if (result.Error) {
      showMessage(result.Error, 'error')
    } else {
      showMessage(`已移除 ${result.Name}（封裝 ${removalStatusText(result.Footprint)}、3D ${removalStatusText(result.Model)}）`, 'note')
    }
  } catch (cause) {
    showMessage(errorText(cause), 'error')
    await refreshLibrary()
  }
}
function handleProgress(event: ProgressEvent) {
  const result = event.result
  if (!result) return
  results.value[event.index - 1] = addRecord(result)
  results.value = [...results.value]
}
function addRecord(result: AddResult): LibraryRecord {
  const status = result.Error ? 'failed' : result.Symbol === 'exists' && result.Footprint === 'exists' ? 'exists' : result.Symbol === 'updated' || result.Footprint === 'updated' ? 'updated' : 'added'
  const labels: Record<string, string> = { added: '已新增', updated: '已更新', exists: '已存在', failed: '失敗', skipped: '略過' }
  const label = (value: string) => labels[value] ?? value
  return { Title: result.PartID, Summary: `符號 ${label(result.Symbol)} · 封裝 ${label(result.Footprint)} · 3D ${label(result.Model)}`, Status: status, Mark: result.Error ? '×' : status === 'exists' ? '=' : '✓', Error: result.Error, Warnings: result.Warnings ?? [] }
}
async function addSelected() {
  const targets = [...addTargets.value]
  if (!targets.length) return
  adding.value = true
  results.value = []
  message.value = ''
  try {
    results.value = (await api.add(targets)).map(addRecord)
    await refreshLibrary()
    const failed = results.value.some(record => record.Status === 'failed')
    const unchanged = results.value.every(record => record.Status === 'exists')
    showMessage(failed ? '部分項目加入失敗，詳見匯入紀錄。' : unchanged ? '這些零件先前已在元件庫中，沒有變更。' : '已加入元件庫。', failed ? 'error' : 'note')
  } catch (cause) { showMessage(errorText(cause)) }
  finally { adding.value = false }
}
async function importFiles() {
  if (adding.value || importing.value) return
  importing.value = true
  try {
    const paths = await api.pickImportFiles()
    if (paths.length) await importPaths(paths)
  } catch (cause) { showMessage(errorText(cause)) }
  finally { importing.value = false }
}
async function importDirectory() {
  if (adding.value || importing.value) return
  importing.value = true
  try {
    const path = await api.pickImportDirectory()
    if (path) await importPaths([path])
  } catch (cause) { showMessage(errorText(cause)) }
  finally { importing.value = false }
}
async function importPaths(paths: string[]) {
  results.value = []
  message.value = ''
  try {
    const imported = await api.import(paths)
    results.value = imported.map(importRecord)
    await refreshLibrary()
    const failed = imported.some(result => result.Error || [...result.Symbols, ...result.Footprints, ...result.Models].some(item => item.Status === 'failed'))
    showMessage(failed ? '部分項目匯入失敗，詳見匯入紀錄。' : '匯入完成。', failed ? 'error' : 'note')
  } catch (cause) { showMessage(errorText(cause)) }
}
function importRecord(result: ImportResult): LibraryRecord {
  const counts = [result.Symbols, result.Footprints, result.Models].map(items => {
    const written = items.filter(item => item.Status === 'added' || item.Status === 'updated').length
    const exists = items.filter(item => item.Status === 'exists').length
    return `${written}${exists ? `（已存在 ${exists}）` : ''}`
  })
  const failed = Boolean(result.Error) || [...result.Symbols, ...result.Footprints, ...result.Models].some(item => item.Status === 'failed')
  const statuses = [...result.Symbols, ...result.Footprints, ...result.Models].map(item => item.Status)
  const status = failed ? 'failed' : statuses.includes('updated') ? 'updated' : statuses.includes('added') ? 'added' : 'exists'
  const title = result.Source.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || result.Source
  return { Title: title, Summary: `符號 ${counts[0]} · 封裝 ${counts[1]} · 3D ${counts[2]}`, Status: status, Mark: failed ? '×' : status === 'exists' ? '=' : '✓', Error: result.Error, Warnings: result.Warnings ?? [] }
}
async function openOutput() {
  try { await api.openPath(settings.value.OutDir) } catch (cause) { showMessage(errorText(cause)) }
}
async function openLog() {
  try { await api.openLog() } catch (cause) { showMessage(errorText(cause)) }
}
function setThemeButton(element: unknown, index: number) {
  themeButtons[index] = element instanceof HTMLButtonElement ? element : null
}
function onThemeKeydown(event: KeyboardEvent, index: number) {
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
  event.preventDefault()
  const step = event.key === 'ArrowRight' ? 1 : -1
  const next = (index + step + themeOptions.length) % themeOptions.length
  setTheme(themeOptions[next].value)
  themeButtons[next]?.focus()
}
async function openURL(target: string) {
  try { await api.openURL(target) } catch (cause) { showMessage(errorText(cause)) }
}
async function copyConfig() {
  const base = settings.value.OutDir.replace(/[\\/]+$/, '')
  const symbol = `${base}/${settings.value.LibName}.kicad_sym`
  const footprint = `${base}/${settings.value.LibName}.pretty`
  const text = `KiCad 符號庫檔案：\n${symbol}\n\nKiCad 封裝庫目錄：\n${footprint}\n\n在 KiCad「偏好設定 → 管理符號庫」新增符號庫，並在「管理封裝庫」新增 .pretty 目錄。`
  try { await navigator.clipboard.writeText(text); showMessage('KiCad 符號庫與封裝庫路徑已複製。', 'note') }
  catch { showMessage('無法使用剪貼簿，請從設定欄確認輸出路徑。') }
}
</script>

<template>
  <div class="application-shell">
    <header class="topbar">
      <div class="brand-lockup">
        <div class="brand-mark" aria-hidden="true"><i /><i /><b /></div>
        <div class="brand-name"><strong>PARTFETCH</strong><span>KiCad Component Desk</span></div>
      </div>
      <div class="topbar-center"><span class="project-rule" /><span>零件擷取與元件庫製作</span><span class="project-rule" /></div>
      <div class="topbar-meta">
        <span class="source-indicator"><i /> LCSC / EASYEDA / GITHUB</span>
        <div class="theme-switch" role="radiogroup" aria-label="顯示主題">
          <button
            v-for="(option, index) in themeOptions"
            :key="option.value"
            :ref="element => setThemeButton(element, index)"
            type="button"
            role="radio"
            :aria-checked="theme === option.value"
            :tabindex="theme === option.value ? 0 : -1"
            @click="setTheme(option.value)"
            @keydown="onThemeKeydown($event, index)"
          >{{ option.label }}</button>
        </div>
        <span class="version-stamp mono">v{{ APP_VERSION }}</span>
      </div>
    </header>

    <main class="workbench">
      <SearchPane
        v-model:query="query"
        v-model:source="source"
        :items="items"
        :selected="selected"
        :active-part-i-d="activePartID"
        :total="total"
        :page="page"
        :page-size="pageSize"
        :loading="loadingSearch"
        @search="searchParts(1)"
        @page="searchParts"
        @select="selectItem"
        @toggle="togglePart"
        @open-url="openURL"
      />
      <PreviewPane :preview="previewData" :loading="loadingPreview" :active-part-i-d="activePartID" :error="previewError" @open-url="openURL" />
      <LibraryPane
        v-model:settings="settings"
        :selected-count="addTargets.length"
      :preview-target="previewTarget"
        :busy="adding || importing"
        :results="results"
        :entries="entries"
        :settings-dirty="settingsDirty"
        :remove-entry="removeLibraryEntry"
        @save="saveSettings"
        @pick="pickDirectory"
        @add="addSelected"
        @import-files="importFiles"
        @import-directory="importDirectory"
        @open="openOutput"
        @copy="copyConfig"
        @select-installed="selectInstalled"
      />
    </main>

    <footer class="statusbar" role="status" aria-live="polite">
      <div class="status-left"><span class="status-light" :class="{ warning: footerState === 'ATTENTION', busy: adding || importing || loadingSearch || loadingPreview }" /><span class="mono">{{ footerState }}</span><span class="status-divider">/</span><span>{{ message || '資料來源將於需要時連線' }}</span></div>
      <div class="status-right"><span>MM</span><span class="status-divider">·</span><span>繁體中文</span><span class="status-divider">·</span><span class="mono">DRAFT SHEET 001</span><button type="button" class="text-link log-link" @click="openLog">紀錄檔 ↗</button></div>
    </footer>
  </div>
</template>

<style>
.application-shell { height: 100%; display: flex; flex-direction: column; padding: 0 15px; }
.topbar { display: flex; align-items: center; justify-content: space-between; height: 58px; flex: none; border-bottom: 1px solid var(--rule-strong); }
.brand-lockup { display: flex; align-items: center; gap: 10px; min-width: 230px; }.brand-mark { position: relative; width: 27px; height: 27px; border: 1px solid var(--accent); transform: rotate(45deg); }.brand-mark::before,.brand-mark::after { content: ''; position: absolute; background: var(--accent); }.brand-mark::before { left: 12px; top: -4px; width: 1px; height: 33px; }.brand-mark::after { top: 12px; left: -4px; height: 1px; width: 33px; }.brand-mark i { position: absolute; z-index: 1; width: 4px; height: 4px; border: 1px solid var(--accent); background: var(--paper); }.brand-mark i:first-child { top: -3px; left: -3px; }.brand-mark i:nth-child(2) { right: -3px; bottom: -3px; }.brand-mark b { position: absolute; z-index: 2; left: 9px; top: 9px; width: 7px; height: 7px; border-radius: 50%; background: var(--accent); }
.brand-name { display: flex; flex-direction: column; line-height: 1.1; }.brand-name strong { font: 600 12px var(--mono); letter-spacing: .09em; }.brand-name span { margin-top: 3px; color: var(--ink-faint); font-size: 9px; letter-spacing: .03em; }
.topbar-center { display: flex; align-items: center; gap: 11px; color: var(--ink-soft); font-size: 10px; letter-spacing: .08em; }.project-rule { width: 27px; height: 1px; background: var(--rule-strong); }
.topbar-meta { display: flex; justify-content: flex-end; align-items: center; gap: 13px; min-width: 230px; font-size: 8px; letter-spacing: .07em; }.source-indicator { color: var(--ink-soft); }.source-indicator i { display: inline-block; width: 5px; height: 5px; margin: 0 4px 1px 0; border-radius: 50%; background: var(--success); }.version-stamp { padding: 4px 6px; border: 1px solid var(--rule); color: var(--accent); font-size: 8px; }
.topbar-meta {
  flex-wrap: nowrap;
}
.source-indicator {
  flex: none;
  white-space: nowrap;
}
.theme-switch {
  display: flex;
  flex: none;
  border: 1px solid var(--rule);
  white-space: nowrap;
}
.theme-switch button {
  padding: 4px 6px;
  border: 0;
  background: transparent;
  color: var(--ink-soft);
  cursor: pointer;
  font-size: 8px;
  letter-spacing: 0;
}
.theme-switch button + button {
  border-left: 1px solid var(--rule);
}
.theme-switch button[aria-checked="true"] {
  background: var(--paper-shadow);
  color: var(--ink);
}
.theme-switch button:focus-visible {
  position: relative;
  z-index: 1;
  outline: 2px solid var(--focus);
  outline-offset: 1px;
}
.workbench { display: grid; grid-template-columns: 276px minmax(420px, 1fr) 285px; gap: 10px; flex: 1; min-height: 0; padding: 10px 0; }.panel { border: 1px solid var(--rule-strong); background: color-mix(in srgb, var(--paper) 91%, var(--paper-raised)); box-shadow: var(--shadow); }
.statusbar { display: flex; align-items: center; justify-content: space-between; min-height: 29px; flex: none; border-top: 1px solid var(--rule-strong); color: var(--ink-faint); font-size: 9px; }.status-left,.status-right { display: flex; align-items: center; gap: 8px; overflow: hidden; white-space: nowrap; }.status-left > span:last-child { overflow: hidden; text-overflow: ellipsis; }.status-left .mono { color: var(--ink-soft); font-size: 8px; letter-spacing: .05em; }.status-divider { color: var(--rule-strong); }.status-light { width: 6px; height: 6px; border-radius: 50%; background: var(--success); box-shadow: 0 0 0 2px color-mix(in srgb, var(--success) 17%, transparent); }.status-light.warning { background: var(--warning); }.status-light.busy { background: var(--focus); animation: blink 1.1s ease-in-out infinite alternate; }
.log-link {
  flex: none;
  margin-left: 5px;
  font-size: 9px;
}
.status-right .text-link {
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--focus);
  cursor: pointer;
}
@keyframes blink { to { opacity: .35; } }
@media (max-width: 1220px) { .workbench { grid-template-columns: 255px minmax(380px, 1fr) 270px; gap: 7px; }.application-shell { padding: 0 10px; }.brand-lockup,.topbar-meta { min-width: 195px; } }
@media (prefers-reduced-motion: reduce) { .status-light.busy { animation-duration: 2s; } }
</style>
