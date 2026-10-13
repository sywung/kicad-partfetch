<script setup lang="ts">
import { computed, ref } from 'vue'
import type { GuiSettings, LibraryEntry } from '../types/wails'
import { displayPartID } from '../lib/partid'

interface LibraryRecord { Title: string; Summary: string; Status: string; Mark: string; Error: string; Warnings: string[] }

const settings = defineModel<GuiSettings>('settings', { required: true })
const props = defineProps<{
  selectedCount: number
  previewTarget: string
  busy: boolean
  results: LibraryRecord[]
  entries: LibraryEntry[]
  settingsDirty: boolean
  removeEntry: (name: string) => Promise<void>
}>()
const emit = defineEmits<{ save: []; pick: []; add: []; 'import-files': []; 'import-directory': []; open: []; copy: []; 'select-installed': [name: string] }>()
const copied = ref(false)
function statusText(value: string) {
  const labels: Record<string, string> = { added: '已新增', updated: '已更新', exists: '已在元件庫中，未變更', failed: '失敗' }
  return labels[value] ?? value
}
const filter = ref('')
const confirmingName = ref('')
const removingName = ref('')
const modelMode = computed(() => ['absolute', 'kiprjmod'].includes(settings.value.ModelBase) ? settings.value.ModelBase : 'custom')
const filteredEntries = computed(() => {
  const query = filter.value.trim().toLocaleLowerCase()
  if (!query) return props.entries
  return props.entries.filter(entry => `${entry.Name} ${entry.Footprint} ${entry.PartID}`.toLocaleLowerCase().includes(query))
})
function changeModelMode(event: Event) {
  const value = (event.target as HTMLSelectElement).value
  if (value === 'absolute' || value === 'kiprjmod') settings.value.ModelBase = value
  else if (modelMode.value !== 'custom') settings.value.ModelBase = '${KIPRJMOD}/libs'
}
async function removeEntry(name: string) {
  removingName.value = name
  try {
    await props.removeEntry(name)
  } finally {
    removingName.value = ''
    confirmingName.value = ''
  }
}
</script>

<template>
  <section class="library-pane panel" aria-label="元件庫設定與加入紀錄">
    <header class="library-header">
      <div class="section-code mono">03 / FABRICATION</div>
      <div class="library-heading"><h2>元件庫</h2><span class="library-mark">⌁</span></div>
    </header>

    <div class="settings-form">
      <div class="setting-block">
        <label class="setting-label" for="out-dir">目標目錄 <span class="setting-index mono">A</span></label>
        <div class="directory-control"><input id="out-dir" v-model="settings.OutDir" placeholder="選擇 KiCad library 目錄" /><button type="button" aria-label="選擇目錄" @click="emit('pick')">選擇…</button></div>
        <div class="setting-hint mono">{{ settings.OutDir ? 'LOCAL LIBRARY PATH' : 'PATH REQUIRED' }}</div>
      </div>
      <div class="setting-block">
        <label class="setting-label" for="lib-name">符號庫名稱 <span class="setting-index mono">B</span></label>
        <div class="library-name"><input id="lib-name" v-model="settings.LibName" spellcheck="false" /><span class="mono">.kicad_sym</span></div>
        <div class="folder-preview mono">{{ settings.LibName }}.pretty/</div>
      </div>
      <div class="setting-block">
        <label class="setting-label" for="model-base">3D 模型路徑模式 <span class="setting-index mono">C</span></label>
        <select id="model-base" :value="modelMode" @change="changeModelMode">
          <option value="absolute">絕對路徑</option><option value="kiprjmod">專案相對（KIPRJMOD）</option><option value="custom">自訂前綴</option>
        </select>
        <input v-if="modelMode === 'custom'" v-model="settings.ModelBase" class="custom-path mono" aria-label="自訂 3D 路徑前綴" placeholder="${KIPRJMOD}/libs" />
      </div>

      <div class="option-rules">
        <label class="check-setting"><input v-model="settings.With3D" type="checkbox" /><span class="check-visual" /><span><strong>下載 3D 模型</strong><small>附加經位置校正的 STEP 檔</small></span><span class="option-code mono">3D</span></label>
        <label class="check-setting"><input v-model="settings.Overwrite" type="checkbox" /><span class="check-visual" /><span><strong>覆寫既有元件</strong><small>先備份再更新符號</small></span><span class="option-code mono">OVR</span></label>
      </div>
      <button class="save-settings" type="button" :disabled="!settingsDirty" @click="emit('save')">{{ settingsDirty ? '儲存設定' : '設定已儲存' }}<span>↧</span></button>
    </div>

    <button class="add-button" type="button" :disabled="busy || selectedCount === 0" @click="emit('add')">
      <span class="add-button-icon">＋</span><span>{{ busy ? '正在寫入元件庫…' : '加入元件庫' }}<small>{{ previewTarget ? `目前預覽的 ${displayPartID(previewTarget)}` : `${selectedCount} 個零件已排入` }}</small></span><span class="add-enter mono">↵</span>
    </button>

    <div class="import-buttons">
      <button type="button" :disabled="busy" @click="emit('import-files')">匯入檔案…</button>
      <button type="button" :disabled="busy" @click="emit('import-directory')">匯入資料夾…</button>
    </div>

    <div class="record-section">
      <div class="record-heading"><span>匯入紀錄</span><span class="mono">{{ results.length.toString().padStart(2, '0') }}</span></div>
      <div v-if="results.length" class="record-list">
        <article v-for="(result, index) in results" :key="`${result.Title}-${index}`" class="record-row">
          <span class="status-mark" :class="result.Status">{{ result.Mark }}</span>
          <div class="record-main"><div><strong class="mono" :title="result.Title">{{ displayPartID(result.Title) }}</strong><span class="record-status" :class="result.Status">{{ statusText(result.Status) }}</span></div><small>{{ result.Summary }}</small><small v-if="result.Error" class="record-error">{{ result.Error }}</small><small v-for="warning in result.Warnings" :key="warning" class="record-warning">△ {{ warning }}</small></div>
        </article>
      </div>
      <div v-else class="record-empty">新增項目與警告會記錄在這裡</div>
      <div v-if="entries.length" class="installed-list">
        <div class="installed-heading">已安裝符號 <span class="mono">{{ entries.length }}</span></div>
        <input v-if="entries.length > 8" v-model="filter" class="installed-filter" aria-label="過濾已安裝符號" placeholder="過濾名稱、封裝或編號" />
        <div class="installed-scroll">
          <article v-for="entry in filteredEntries" :key="entry.Name" class="installed-item">
            <div v-if="confirmingName === entry.Name" class="installed-confirm">
              <span><strong class="mono">{{ entry.Name }}</strong>：移除符號，以及沒有其他符號使用的封裝與 3D 檔？</span>
              <div><button type="button" class="installed-remove-confirm" :disabled="Boolean(removingName)" @click="removeEntry(entry.Name)">移除</button><button type="button" class="installed-cancel" :disabled="Boolean(removingName)" @click="confirmingName = ''">取消</button></div>
            </div>
            <div v-else class="installed-row">
              <button type="button" class="installed-select" :title="`預覽元件庫裡的 ${entry.Name}`" @click="emit('select-installed', entry.Name)">
                <span class="installed-dot" />
                <span class="installed-name">{{ entry.Name }}</span>
                <span class="installed-footprint mono">{{ entry.Footprint || '—' }}<small v-if="!entry.HasFootprint" class="missing-footprint">缺封裝檔</small></span>
                <small v-if="entry.PartID" class="installed-lcsc mono">{{ entry.PartID.startsWith('C') ? entry.PartID : '社群' }}</small>
              </button>
              <button type="button" class="installed-remove" :aria-label="`移除 ${entry.Name}`" :disabled="Boolean(removingName)" @click="confirmingName = entry.Name">移除</button>
            </div>
          </article>
          <div v-if="!filteredEntries.length" class="installed-no-match">沒有符合的符號</div>
        </div>
      </div>
    </div>

    <div v-if="results.length" class="completion-actions">
      <div class="complete-rule"><span /> 已寫入工作目錄</div>
      <button type="button" class="open-manager" @click="emit('open')"><span>▣</span> 在檔案管理員中顯示</button>
      <button type="button" class="copy-config" @click="emit('copy'); copied = true">{{ copied ? 'KiCad 設定已複製 ✓' : '複製 KiCad 設定說明' }}</button>
    </div>
    <footer class="library-foot mono"><span>KiCad 8 / 9 / 10</span><span>LOCAL · READY</span></footer>
  </section>
</template>

<style scoped>
.library-pane { min-height: 0; display: flex; flex-direction: column; overflow: auto; }
/* Sections keep their natural height and the pane scrolls; shrinking made them overlap. */
.library-pane > * { flex-shrink: 0; }
.library-header { padding: 22px 19px 13px; border-bottom: 1px solid var(--rule); }.section-code { color: var(--silk); font-size: 10px; letter-spacing: .12em; }
.library-heading { display: flex; align-items: center; justify-content: space-between; }.library-heading h2 { margin: 4px 0 0; font-size: 19px; font-weight: 500; letter-spacing: -.02em; }.library-mark { color: var(--silk); font: 26px var(--mono); transform: rotate(90deg); }
.settings-form { display: grid; gap: 13px; padding: 14px 15px 10px; }.setting-block { display: grid; gap: 5px; }.setting-label { display: flex; justify-content: space-between; align-items: center; font-size: 10px; font-weight: 600; }.setting-index { color: var(--ink-faint); font-size: 9px; font-weight: 400; }
.directory-control { display: flex; min-width: 0; }.directory-control input,.library-name input,.setting-block select,.custom-path { min-width: 0; height: 31px; border: 1px solid var(--rule); border-radius: 0; background: var(--paper-raised); color: var(--ink); font-size: 10px; outline: none; }.directory-control input { width: 0; flex: 1; padding: 0 7px; font: 9px var(--mono); }.directory-control input::placeholder { color: var(--ink-faint); font: 10px var(--sans); }.directory-control button { flex: none; height: 31px; padding: 0 8px; border: 1px solid var(--rule); border-left: 0; background: var(--paper-shadow); color: var(--ink); cursor: pointer; font-size: 10px; }.directory-control button:hover { color: var(--accent); }
.setting-hint { color: var(--ink-faint); font-size: 8px; letter-spacing: .05em; }.library-name { display: flex; align-items: center; min-width: 0; }.library-name input { flex: 1; width: 0; padding: 0 7px; font: 10px var(--mono); }.library-name span { padding-left: 5px; color: var(--ink-soft); font-size: 9px; }.folder-preview { margin: -2px 0 0 2px; color: var(--fab); font-size: 9px; }
.setting-block select { width: 100%; padding: 0 7px; }.custom-path { width: 100%; padding: 0 7px; font-size: 9px; }
.option-rules { border-top: 1px solid var(--rule); }.check-setting { position: relative; display: grid; grid-template-columns: 15px 1fr auto; align-items: center; gap: 8px; min-height: 49px; border-bottom: 1px solid var(--rule); cursor: pointer; }.check-setting > input { position: absolute; opacity: 0; }.check-visual { width: 14px; height: 14px; display: grid; place-items: center; border: 1px solid var(--rule-strong); background: var(--paper-raised); }.check-setting input:checked + .check-visual { border-color: var(--accent); background: var(--accent); }.check-setting input:checked + .check-visual::after { content: '✓'; color: var(--paper-raised); font-size: 10px; }.check-setting input:focus-visible + .check-visual { outline: 2px solid var(--focus); outline-offset: 2px; }.check-setting strong,.check-setting small { display: block; }.check-setting strong { font-size: 10px; font-weight: 500; }.check-setting small { color: var(--ink-faint); font-size: 9px; }.option-code { color: var(--ink-faint); font-size: 8px; }
.save-settings { justify-self: end; padding: 4px 0 4px 8px; border: 0; background: transparent; color: var(--focus); cursor: pointer; font-size: 9px; }.save-settings span { margin-left: 5px; }.save-settings:disabled { color: var(--ink-faint); opacity: .75; }
.add-button { display: flex; align-items: center; gap: 10px; width: calc(100% - 30px); min-height: 51px; margin: 1px 15px 14px; padding: 7px 10px; border: 1px solid var(--accent); background: var(--accent); color: var(--paper-raised); text-align: left; cursor: pointer; }.add-button:not(:disabled):hover { transform: translateY(-1px); background: var(--accent-strong); }.add-button:disabled { opacity: .48; cursor: not-allowed; }.add-button-icon { font: 24px/1 var(--sans); }.add-button > span:nth-child(2) { flex: 1; font-size: 11px; font-weight: 600; }.add-button small { display: block; font-size: 9px; font-weight: 400; opacity: .82; }.add-enter { font-size: 13px; }
.import-buttons { display: grid; grid-template-columns: 1fr 1fr; gap: 6px; margin: -7px 15px 12px; }
.import-buttons button { min-height: 29px; border: 1px solid var(--rule); background: var(--paper-raised); color: var(--ink-soft); cursor: pointer; font-size: 9px; }
.import-buttons button:hover:not(:disabled) { border-color: var(--focus); color: var(--focus); }
.import-buttons button:disabled { opacity: .48; cursor: not-allowed; }
.record-section { min-height: 100px; border-top: 1px solid var(--rule); }.record-heading { display: flex; justify-content: space-between; padding: 12px 16px 7px; color: var(--ink-soft); font-size: 10px; font-weight: 600; }.record-heading .mono { color: var(--ink-faint); font-weight: 400; }.record-empty { padding: 4px 16px 15px; color: var(--ink-faint); font-size: 9px; }.record-list { display: grid; gap: 7px; padding: 1px 11px 10px; }.record-row { display: flex; gap: 7px; padding: 8px; border: 1px solid var(--rule); background: var(--paper-raised); }.status-mark { width: 16px; height: 16px; flex: none; display: grid; place-items: center; border: 1px solid currentColor; border-radius: 50%; font-size: 10px; }.status-mark.added,.record-status.added { color: var(--success); }.status-mark.updated,.record-status.updated { color: var(--focus); }.status-mark.exists,.record-status.exists { color: var(--warning); }.status-mark.failed,.record-status.failed { color: var(--danger); }.record-main { min-width: 0; flex: 1; }.record-main > div { display: flex; justify-content: space-between; align-items: center; }.record-main strong { font-size: 10px; font-weight: 500; }.record-status { font-size: 9px; }.record-main small { display: block; color: var(--ink-faint); font-size: 8px; line-height: 1.5; }.record-main .record-error { color: var(--danger); }.record-main .record-warning { color: var(--warning); }
.installed-list { border-top: 1px dashed var(--rule); padding: 8px 14px 10px; }
.installed-heading { display: flex; justify-content: space-between; padding-bottom: 5px; color: var(--ink-soft); font-size: 9px; }
.installed-heading .mono { color: var(--ink-faint); }
.installed-filter { width: 100%; height: 27px; margin: 3px 0 6px; padding: 0 7px; border: 1px solid var(--rule); background: var(--paper-raised); color: var(--ink); font-size: 9px; }
.installed-filter::placeholder { color: var(--ink-faint); }
.installed-scroll { max-height: 260px; overflow-y: auto; }
.installed-item { min-width: 0; border-bottom: 1px solid var(--rule); }
.installed-row { display: flex; align-items: center; gap: 4px; min-width: 0; padding: 3px 0; font-size: 9px; }
/* Two lines per entry: a long footprint name on one line squeezed the symbol name to a few pixels. */
.installed-select { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; column-gap: 6px; row-gap: 1px; min-width: 0; flex: 1; padding: 2px 0; border: 0; background: transparent; color: var(--ink); text-align: left; cursor: pointer; font-size: 9px; }
.installed-select:disabled { cursor: default; }
.installed-dot { grid-column: 1; grid-row: 1; }
.installed-name { grid-column: 2; grid-row: 1; overflow: hidden; min-width: 0; text-overflow: ellipsis; white-space: nowrap; }
.installed-footprint { grid-column: 2 / 4; grid-row: 2; overflow: hidden; min-width: 0; color: var(--ink-faint); font-size: 8px; text-overflow: ellipsis; white-space: nowrap; }
.missing-footprint { margin-left: 4px; color: var(--warning); font-size: 8px; }
.installed-lcsc { grid-column: 3; grid-row: 1; color: var(--ink-soft); font-size: 8px; }
.installed-dot { width: 4px; height: 4px; flex: none; border: 1px solid var(--fab); }
.installed-remove,.installed-cancel,.installed-remove-confirm { flex: none; padding: 3px 5px; border: 1px solid var(--rule); background: var(--paper-raised); color: var(--ink-soft); cursor: pointer; font-size: 8px; }
.installed-remove:hover,.installed-cancel:hover { border-color: var(--focus); color: var(--focus); }
.installed-remove:disabled,.installed-remove-confirm:disabled { opacity: .5; cursor: wait; }
.installed-confirm { display: grid; gap: 6px; padding: 7px 0; color: var(--ink-soft); font-size: 9px; line-height: 1.5; }
.installed-confirm > div { display: flex; gap: 5px; }
.installed-remove-confirm { border-color: var(--danger); background: var(--danger); color: var(--paper-raised); }
.installed-cancel { background: transparent; }
.installed-no-match { padding: 8px 0; color: var(--ink-faint); font-size: 9px; }
.completion-actions { display: grid; gap: 6px; padding: 10px 14px; border-top: 1px solid var(--rule); }.complete-rule { display: flex; align-items: center; gap: 6px; color: var(--success); font-size: 9px; }.complete-rule span { width: 5px; height: 5px; border-radius: 50%; background: var(--success); }.open-manager,.copy-config { min-height: 29px; border: 1px solid var(--rule); background: var(--paper-raised); cursor: pointer; font-size: 9px; }.open-manager:hover,.copy-config:hover { border-color: var(--focus); color: var(--focus); }.open-manager span { margin-right: 4px; color: var(--fab); }.copy-config { border-style: dashed; color: var(--ink-soft); }
.library-foot { display: flex; justify-content: space-between; margin-top: auto; padding: 9px 15px; border-top: 1px solid var(--rule); color: var(--ink-faint); font-size: 8px; letter-spacing: .04em; }
</style>
