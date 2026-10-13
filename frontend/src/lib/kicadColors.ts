// kicad-cli draws its SVGs in KiCad's stock palette (the export pins
// `--theme _builtin_default`). Those colours are meant for KiCad's own canvas:
// pale silkscreen disappears on light paper. Because the palette is fixed, each
// colour can be swapped for the matching design token, so a part looks the same
// before and after it is added to the library and follows the app theme.

export type KiCadDrawing = 'symbol' | 'footprint'

const footprintTokens: Record<string, string> = {
  C83434: '--copper-front', // F.Cu
  '4D7FC4': '--copper-back', // B.Cu
  F2EDA1: '--silk', // F.SilkS
  E8B2A7: '--silk', // B.SilkS
  AFAFAF: '--fab', // F.Fab
  '585D84': '--fab', // B.Fab
  FF26E2: '--courtyard', // F.CrtYd
  '26E9FF': '--courtyard', // B.CrtYd
  D0D2CD: '--edge', // Edge.Cuts
  C2C2C2: '--user-layer', // Dwgs.User
  '5994DC': '--user-layer', // Cmts.User
  FFFFFF: '--hole', // drill holes
  '000000': '--ink', // non-plated holes
}

const symbolTokens: Record<string, string> = {
  '840000': '--accent', // body outline, pins, reference
  A90000: '--accent-strong', // pin numbers
  '006464': '--ink', // pin names, value
  FFFFC2: '--paper-shadow', // body fill
  '000000': '--ink',
}

const hexColor = /#([0-9a-fA-F]{6})\b/g

export function recolorKiCadSVG(svg: string, drawing: KiCadDrawing, readToken: (name: string) => string): string {
  const tokens = drawing === 'symbol' ? symbolTokens : footprintTokens
  return svg.replace(hexColor, (original, hex: string) => {
    const token = tokens[hex.toUpperCase()]
    if (!token) return original
    return readToken(token) || original
  })
}
