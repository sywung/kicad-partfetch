import type { AddResult, GuiSettings, ImportResult, LibraryEntry, ModelData, PreviewData, ProgressEvent, RemoveResult, SearchResult } from '../types/wails'

function app() {
  const bound = window.go?.app?.App
  if (!bound) throw new Error('請在 kicad-partfetch 桌面程式中使用此功能。')
  return bound
}

export const api = {
  search: (source: string, query: string, page: number): Promise<SearchResult> => app().Search(source, query, page),
  preview: (partID: string): Promise<PreviewData> => app().Preview(partID),
  previewInstalled: (name: string): Promise<PreviewData> => app().PreviewInstalled(name),
  modelInstalled: (name: string): Promise<{ GLB: string; File: string; Note: string }> => app().ModelInstalled(name),
  modelExternal: (partID: string): Promise<{ GLB: string; File: string; Note: string }> => app().ModelExternal(partID),
  model: (partID: string): Promise<ModelData> => app().Model(partID),
  getSettings: (): Promise<GuiSettings> => app().GetSettings(),
  saveSettings: (settings: GuiSettings): Promise<void> => app().SaveSettings(settings),
  pickDirectory: (): Promise<string> => app().PickDirectory(),
  pickImportFiles: (): Promise<string[]> => app().PickImportFiles(),
  pickImportDirectory: (): Promise<string> => app().PickImportDirectory(),
  add: (partIDs: string[]): Promise<AddResult[]> => app().Add(partIDs),
  import: (paths: string[]): Promise<ImportResult[]> => app().Import(paths),
  listLibrary: (): Promise<LibraryEntry[]> => app().ListLibrary(),
  removeFromLibrary: (names: string[]): Promise<RemoveResult[]> => app().RemoveFromLibrary(names),
  openPath: (path: string): Promise<void> => app().OpenInFileManager(path),
  openLog: (): Promise<void> => app().OpenLog(),
  openURL: (url: string): Promise<void> => app().OpenURL(url),
}

export function onProgress(callback: (event: ProgressEvent) => void): () => void {
  const cancel = window.runtime?.EventsOn?.('add:progress', callback)
  return typeof cancel === 'function' ? cancel : () => undefined
}
