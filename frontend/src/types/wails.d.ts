export interface SearchItem {
  PartID: string
  MPN: string
  Manufacturer: string
  Package: string
  Description: string
  Stock: number
  Price: string
  ImageURL: string
  DatasheetURL: string
  Source: string
  URL: string
  License: string
  Stars: number
}
export interface SearchResult { Items: SearchItem[]; Total: number; Page: number; PageSize: number }
export interface PreviewData {
  PartID: string
  Name: string
  Package: string
  Manufacturer: string
  MPN: string
  SymbolSVGs: string[]
  FootprintSVG: string
  FootprintSVGs: string[]
  Files: string[]
  Source: string
  URL: string
  License: string
  External: boolean
  PinCount: number
  PadCount: number
  HasModel: boolean
  ModelTitle: string
  Warnings: string[]
}
export interface ModelData { OBJ: string; Offset: [number, number, number]; Rotate: [number, number, number] }
export interface GuiSettings { OutDir: string; LibName: string; ModelBase: string; With3D: boolean; Overwrite: boolean }
export interface AddResult { PartID: string; Symbol: string; Footprint: string; Model: string; Warnings: string[]; Error: string; ErrorKind: string }
export interface LibraryEntry { Name: string; Footprint: string; LCSC: string; PartID: string; HasFootprint: boolean; HasModel: boolean }
export interface RemoveResult { Name: string; Symbol: string; Footprint: string; Model: string; Error: string }
export interface ImportItem { Name: string; Status: string }
export interface ImportResult { Source: string; Symbols: ImportItem[]; Footprints: ImportItem[]; Models: ImportItem[]; Warnings: string[]; Error: string }
export interface ProgressEvent { index: number; total: number; result: AddResult }

interface AppAPI {
  Search(source: string, query: string, page: number): Promise<SearchResult>
  Preview(partID: string): Promise<PreviewData>
  PreviewInstalled(name: string): Promise<PreviewData>
  ModelInstalled(name: string): Promise<{ GLB: string; File: string; Note: string }>
  ModelExternal(partID: string): Promise<{ GLB: string; File: string; Note: string }>
  Model(partID: string): Promise<ModelData>
  GetSettings(): Promise<GuiSettings>
  SaveSettings(s: GuiSettings): Promise<void>
  PickDirectory(): Promise<string>
  PickImportFiles(): Promise<string[]>
  PickImportDirectory(): Promise<string>
  Add(partIDs: string[]): Promise<AddResult[]>
  Import(paths: string[]): Promise<ImportResult[]>
  ListLibrary(): Promise<LibraryEntry[]>
  RemoveFromLibrary(names: string[]): Promise<RemoveResult[]>
  OpenInFileManager(path: string): Promise<void>
  LogPath(): string
  OpenLog(): Promise<void>
  OpenURL(url: string): Promise<void>
}
declare global {
  interface Window {
    go?: { app?: { App?: AppAPI } }
    runtime?: { EventsOn?: (name: string, callback: (payload: ProgressEvent) => void) => (() => void) | void }
  }
}
