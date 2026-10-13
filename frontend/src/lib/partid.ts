// Shortens a PartID for display. The full value stays in titles and in every call to the backend.
export function displayPartID(id: string): string {
  const community = /^easyeda:([0-9a-f]{8})[0-9a-f]{24}$/.exec(id)
  if (community) return `EasyEDA·${community[1]}`
  return id.startsWith('github:') ? id.slice('github:'.length) : id
}
