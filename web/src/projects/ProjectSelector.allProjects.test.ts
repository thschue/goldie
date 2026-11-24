// The all projects option is in the design record (#804): handoff §2.1, §4, §5, §8.
// The browser behaviour is in web/e2e/projects.spec.ts.
import { describe, expect, it } from 'vitest';
import doc from '../../../docs/design/ui-handoff.md?raw';

const section = (n: number) => {
  const start = doc.indexOf(`\n## ${n}. `);
  const end = doc.indexOf('\n## ', start + 1);
  return doc.slice(start, end);
};

describe('the design record, Project selector all projects option', () => {
  it('§2.1 says the listbox starts with "All projects (global guidance)", which clears the Project', () => {
    expect(doc).toContain("The listbox starts with 'All projects (global guidance)', which clears the Project.");
  });

  it('§4 notes on ProjectSelector that the first option clears the Project', () => {
    expect(section(4)).toMatch(/`ProjectSelector`[^\n]*first option clears the Project/);
  });

  it('§5 has a table row for the option', () => {
    expect(section(5)).toMatch(/^\|[^\n]*All projects \(global guidance\)[^\n]*\|/m);
  });

  it('§8 has the copy key "Project selector, no Project"', () => {
    expect(section(8)).toContain('Project selector, no Project');
  });
});
