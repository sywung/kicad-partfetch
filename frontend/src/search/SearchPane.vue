<script setup lang="ts">
import { computed } from 'vue'
import type { SearchItem } from '../types/wails'
import { displayPartID } from '../lib/partid'

const query = defineModel<string>('query', { required: true })
const source = defineModel<'lcsc' | 'easyeda' | 'github'>('source', { required: true })
const props = defineProps<{
  items: SearchItem[]
  selected: string[]
  activePartID: string
  total: number
  page: number
  pageSize: number
  loading: boolean
}>()
const emit = defineEmits<{
  search: []
  select: [item: SearchItem]
  toggle: [partID: string]
  page: [page: number]
  'open-url': [url: string]
}>()

const pages = computed(() => Math.max(1, Math.ceil(props.total / Math.max(1, props.pageSize))))
function isSelected(id: string) { return props.selected.includes(id) }
function focusResult(index: number) {
  const rows = document.querySelectorAll<HTMLElement>('.result-list [role="option"]')
  rows[Math.max(0, Math.min(rows.length - 1, index))]?.focus()
}
async function moveFocus(row: HTMLElement, delta: number) {
  const rows = Array.from(row.parentElement?.querySelectorAll<HTMLElement>('[role="option"]') ?? [])
  const current = rows.indexOf(row)
  rows[Math.max(0, Math.min(rows.length - 1, current + delta))]?.focus()
}
async function onKey(event: KeyboardEvent, item: SearchItem) {
  const row = event.currentTarget as HTMLElement
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault()
    await moveFocus(row, event.key === 'ArrowDown' ? 1 : -1)
  } else if (event.key === 'Enter') {
    event.preventDefault()
    emit('select', item)
  } else if (event.key === ' ' && event.target === row) {
    event.preventDefault()
    emit('toggle', item.PartID)
  }
}
</script>

<template>
  <section class="search-pane panel" aria-label="搜尋與加入佇列">
    <header class="pane-heading">
      <div class="section-code mono">01 / INTAKE</div>
      <h1>搜尋零件</h1>
      <p>輸入型號、關鍵字，或貼上 PartID 清單</p>
    </header>

    <div class="source-switch" role="radiogroup" aria-label="搜尋來源">
      <button type="button" role="radio" :aria-checked="source === 'lcsc'" :class="{ selected: source === 'lcsc' }" @click="source = 'lcsc'">LCSC</button>
      <button type="button" role="radio" :aria-checked="source === 'easyeda'" :class="{ selected: source === 'easyeda' }" @click="source = 'easyeda'">EasyEDA 社群</button>
      <button type="button" role="radio" :aria-checked="source === 'github'" :class="{ selected: source === 'github' }" @click="source = 'github'">GitHub</button>
    </div>

    <form class="search-form" @submit.prevent="emit('search')">
      <label class="field-label" for="part-search">{{ source === 'lcsc' ? 'LCSC 型號 / 關鍵字' : source === 'easyeda' ? 'EasyEDA 社群關鍵字' : 'GitHub repo 關鍵字' }}</label>
      <div class="search-input-wrap">
        <span class="search-glyph" aria-hidden="true">⌕</span>
        <input id="part-search" v-model="query" autocomplete="off" placeholder="例如 STM32F103C8T6" :aria-label="source === 'lcsc' ? '搜尋 LCSC 零件' : source === 'easyeda' ? '搜尋 EasyEDA 社群零件' : '搜尋 GitHub repo'" @keydown.down.prevent="focusResult(0)" @keydown.up.prevent="focusResult(items.length - 1)" />
        <kbd>↵</kbd>
      </div>
      <button class="search-submit" type="submit" :disabled="loading">
        <span>{{ loading ? '搜尋中…' : `搜尋 ${source === 'lcsc' ? 'LCSC' : source === 'easyeda' ? 'EasyEDA 社群' : 'GitHub'}` }}</span><span class="button-arrow" aria-hidden="true">↗</span>
      </button>
    </form>

    <div class="list-caption">
      <span>搜尋結果</span>
      <span v-if="total" class="mono">{{ total.toLocaleString() }} 項</span>
    </div>
    <div class="result-list" role="listbox" aria-label="搜尋結果">
      <div v-if="loading && items.length === 0" class="list-state"><span class="spinner" /> 正在查詢商品目錄</div>
      <div v-else-if="!items.length" class="list-state empty-state">
        <span class="empty-cross" aria-hidden="true">＋</span>
        <strong>等待零件清單</strong>
        <span>結果會列在這張工作表上</span>
      </div>
      <article
        v-for="item in items"
        :key="item.PartID"
        class="result-row"
        :class="{ active: item.PartID === activePartID, queued: isSelected(item.PartID) }"
        role="option"
        :aria-selected="item.PartID === activePartID"
        tabindex="0"
        @click="emit('select', item)"
        @keydown="onKey($event, item)"
      >
        <input
          class="queue-check"
          type="checkbox"
          :checked="isSelected(item.PartID)"
          :aria-label="`將 ${item.PartID} 加入佇列`"
          @click.stop
          @change="emit('toggle', item.PartID)"
        />
        <div class="result-main">
          <div class="result-topline">
            <span class="part-id mono" :title="item.PartID">{{ displayPartID(item.PartID) }}</span>
            <span v-if="item.Source === 'easyeda'" class="community-badge">未審核</span>
            <span v-else-if="item.Source === 'github'" class="part-price mono">★{{ item.Stars.toLocaleString() }}</span>
            <span v-else-if="item.Price" class="part-price mono">{{ item.Price }}</span>
          </div>
          <div class="result-mpn">{{ item.MPN || '直接以編號載入' }}</div>
          <div v-if="item.MPN && item.Source !== 'github'" class="result-meta">{{ item.Manufacturer || '—' }}<span>·</span>{{ item.Package || '未標示封裝' }}</div>
          <div v-if="item.Source === 'github'" class="github-result-meta"><span>{{ item.License || '未聲明授權' }}</span><span>{{ item.Description }}</span><button type="button" class="text-link" @click.stop="emit('open-url', item.URL)">在瀏覽器開啟 ↗</button></div>
          <div v-if="item.MPN && item.Source === 'lcsc'" class="result-stock mono"><i /> {{ item.Stock.toLocaleString() }} 在庫</div>
          <div v-if="item.ImageURL" class="result-extra">
            <img :src="item.ImageURL" :alt="`${item.MPN} 商品圖`" referrerpolicy="no-referrer" loading="lazy" />
            <button v-if="item.DatasheetURL" type="button" class="text-link" @click.stop="emit('open-url', item.DatasheetURL)">資料表 ↗</button>
          </div>
        </div>
        <span class="row-corner" aria-hidden="true">{{ item.PartID === activePartID ? '●' : '○' }}</span>
      </article>
    </div>

    <div class="queue-footer">
      <div class="queue-count"><span class="count-mark">{{ selected.length.toString().padStart(2, '0') }}</span><span>已選入匯入佇列</span></div>
      <div v-if="pages > 1" class="pagination" aria-label="搜尋結果分頁">
        <button type="button" :disabled="page <= 1 || loading" aria-label="上一頁" @click="emit('page', page - 1)">←</button>
        <span class="mono">{{ page }} / {{ pages }}</span>
        <button type="button" :disabled="page >= pages || loading" aria-label="下一頁" @click="emit('page', page + 1)">→</button>
      </div>
    </div>
  </section>
</template>

<style scoped>
.search-pane { min-height: 0; display: flex; flex-direction: column; }
.pane-heading { padding: 22px 20px 16px; border-bottom: 1px solid var(--rule); }
.section-code { color: var(--accent); font-size: 10px; letter-spacing: .12em; }
h1 { font-size: 19px; font-weight: 500; margin: 5px 0 1px; letter-spacing: -.025em; }
.pane-heading p { color: var(--ink-soft); font-size: 11px; margin: 0; }
.source-switch { display: grid; grid-template-columns: repeat(3, 1fr); gap: 3px; margin: 12px 15px 0; padding: 3px; border: 1px solid var(--rule); background: var(--paper-shadow); }
.github-result-meta { display: flex; flex-direction: column; gap: 4px; margin-top: 5px; color: var(--ink-soft); font-size: 9px; line-height: 1.4; }
.github-result-meta > span:nth-child(2) { color: var(--ink-faint); }
.github-result-meta button { align-self: flex-start; padding: 0; }
.source-switch button { min-height: 27px; border: 1px solid transparent; background: transparent; color: var(--ink-soft); cursor: pointer; font-size: 9px; }
.source-switch button.selected { border-color: var(--rule-strong); background: var(--paper-raised); color: var(--accent); }
.source-switch button:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
.search-form { padding: 15px 16px 17px; display: grid; gap: 8px; border-bottom: 1px solid var(--rule); }
.field-label,.list-caption { color: var(--ink-soft); font-size: 10px; font-weight: 600; letter-spacing: .06em; text-transform: uppercase; }
.search-input-wrap { height: 39px; display: flex; align-items: center; gap: 8px; padding: 0 10px; border: 1px solid var(--rule-strong); background: var(--paper-raised); }
.search-glyph { font: 25px/1 var(--sans); color: var(--accent); transform: rotate(-15deg); }
.search-input-wrap input { flex: 1; width: 0; border: 0; outline: 0; background: transparent; color: var(--ink); font-size: 11px; }
.search-input-wrap input::placeholder { color: var(--ink-faint); }
kbd { color: var(--ink-faint); border: 1px solid var(--rule); padding: 0 4px; font: 10px var(--mono); }
.search-submit { display: flex; justify-content: space-between; align-items: center; border: 1px solid var(--accent); padding: 8px 10px; color: var(--paper-raised); background: var(--accent); cursor: pointer; font-size: 11px; font-weight: 600; }
.search-submit:not(:disabled):hover { background: var(--accent-strong); border-color: var(--accent-strong); }
.button-arrow { font-size: 16px; }
.list-caption { display: flex; justify-content: space-between; padding: 14px 17px 8px; text-transform: none; letter-spacing: .02em; }
.list-caption .mono { font-size: 10px; color: var(--ink-faint); }
.result-list { flex: 1; min-height: 60px; overflow: auto; padding: 0 10px 12px; }
.list-state { height: 100%; min-height: 130px; display: flex; justify-content: center; align-items: center; gap: 9px; color: var(--ink-soft); font-size: 11px; }
.empty-state { flex-direction: column; gap: 3px; text-align: center; }
.empty-state strong { font-size: 12px; font-weight: 500; color: var(--ink); }
.empty-state > span:last-child { color: var(--ink-faint); font-size: 10px; }
.empty-cross { color: var(--rule-strong); font-size: 26px; line-height: 1; margin-bottom: 5px; }
.spinner { width: 13px; height: 13px; border: 1px solid var(--rule-strong); border-top-color: var(--accent); border-radius: 50%; animation: spin .8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
.result-row { position: relative; display: flex; gap: 9px; margin: 0 0 7px; padding: 11px 10px 10px; border: 1px solid var(--rule); background: var(--paper-raised); cursor: pointer; }
.result-row:hover { border-color: var(--rule-strong); transform: translateY(-1px); }
.result-row.active { border-color: var(--accent); box-shadow: inset 2px 0 var(--accent); }
.result-row.queued { background: color-mix(in srgb, var(--paper-raised), var(--success) 5%); }
.queue-check { flex: none; width: 14px; height: 14px; margin: 2px 0 0; accent-color: var(--accent); cursor: pointer; }
.result-main { flex: 1; min-width: 0; }
.result-topline { display: flex; justify-content: space-between; align-items: baseline; gap: 6px; }
.part-id { min-width: 0; overflow-wrap: anywhere; font-weight: 500; color: var(--accent-strong); font-size: 12px; }
.part-price { flex: none; white-space: nowrap; color: var(--ink); font-size: 10px; }
.community-badge { border: 1px solid var(--warning); padding: 2px 4px; color: var(--warning); font-size: 8px; white-space: nowrap; }
.result-mpn { overflow: hidden; white-space: nowrap; text-overflow: ellipsis; font-size: 11px; font-weight: 500; margin-top: 2px; }
.result-meta { display: flex; gap: 5px; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; color: var(--ink-soft); font-size: 10px; margin-top: 2px; }
.result-meta > * { flex: none; }
.result-stock { display: flex; align-items: center; gap: 5px; color: var(--ink-faint); font-size: 9px; margin-top: 7px; }
.result-stock i { width: 5px; height: 5px; border-radius: 50%; background: var(--success); }
.row-corner { color: var(--rule-strong); font-size: 8px; margin-top: 2px; }
.active .row-corner { color: var(--accent); }
.result-extra { display: flex; align-items: center; gap: 9px; margin-top: 8px; padding-top: 7px; border-top: 1px dashed var(--rule); }
.result-extra img { width: 30px; height: 30px; object-fit: contain; background: white; }
.text-link { padding: 0; border: 0; background: none; color: var(--focus); cursor: pointer; font-size: 10px; }
.text-link:hover { text-decoration: underline; }
.queue-footer { padding: 10px 15px; border-top: 1px solid var(--rule); display: flex; align-items: center; justify-content: space-between; color: var(--ink-soft); font-size: 10px; }
.queue-count { display: flex; align-items: center; gap: 7px; }
.count-mark { color: var(--accent); font: 16px var(--mono); }
.pagination { display: flex; align-items: center; gap: 8px; }
.pagination button { width: 24px; height: 24px; padding: 0; border: 1px solid var(--rule); background: var(--paper-raised); cursor: pointer; }
.pagination button:hover:not(:disabled) { border-color: var(--accent); color: var(--accent); }
@media (prefers-reduced-motion: reduce) { .spinner { animation-duration: 2s; } }
</style>
