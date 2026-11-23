// The account chip shows that it opens a menu (#545): a decorative caret next
// to the role, and the design record says so. Rendered with Vue's server
// renderer; the browser flow is in web/e2e.
import { describe, expect, it } from 'vitest';
import { createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import RoleMenu from './RoleMenu.vue';
import doc from '../../../docs/design/ui-handoff.md?raw';

async function render(role: 'reader' | 'operator') {
  return renderToString(createSSRApp({ render: () => h(RoleMenu, { role }) }));
}

// The chip button's inner HTML.
const chipOf = (html: string) => /<button[^>]*aria-haspopup="menu"[^>]*>([\s\S]*?)<\/button>/.exec(html)?.[1] ?? '';

describe('RoleMenu chip affordance', () => {
  for (const role of ['reader', 'operator'] as const) {
    it(`the ${role} chip carries a decorative caret that is hidden from assistive technology`, async () => {
      const html = await render(role);
      const inner = chipOf(html);
      expect(inner, 'the chip still reads the role').toContain(role);
      expect(inner, 'a caret element sits inside the chip').toMatch(/<(svg|span)[^>]*aria-hidden="true"/);
    });
  }

  it('the menu stays closed until the chip is used', async () => {
    expect(await render('reader')).not.toContain('Sign out');
  });
});

describe('the design record', () => {
  it('says the account chip shows a caret that it opens a menu', () => {
    expect(doc).toMatch(/(account|role) chip[^\n]*caret|caret[^\n]*(account|role) chip/i);
  });
});
