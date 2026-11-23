// The Agent assets tabs (handoff §3.5). The router and the side nav read this list.
export const assetTabs = [
  { slug: 'skills', name: 'Skills' },
  { slug: 'agents', name: 'Agents' },
  { slug: 'loops', name: 'Loops' },
  { slug: 'instructions', name: 'Instructions' },
] as const;

export type AssetTab = (typeof assetTabs)[number]['slug'];
