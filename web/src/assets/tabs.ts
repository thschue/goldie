// The Agent assets tabs (handoff §3.5). The MVP cut has no Loops tab: it is
// left out, not disabled. The router and the side nav read this list.
export const assetTabs = [
  { slug: 'skills', name: 'Skills' },
  { slug: 'agents', name: 'Agents' },
  { slug: 'instructions', name: 'Instructions' },
] as const;

export type AssetTab = (typeof assetTabs)[number]['slug'];
