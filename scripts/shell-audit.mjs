/*
 * Shell audit — the two layout invariants the learner-facing UI pass proved
 * matter (progress.md, "UI pass — defects found by driving the product").
 *
 * Both defects below were found by driving the built app with a real browser,
 * and both were invisible to every other audit in this repo: the audits exercise
 * the API, the graders and the content, and none of them renders a page.
 *
 *   1. The document was 4994 px tall on a lesson page. `.visually-hidden` is
 *      `position:absolute`; with no positioned ancestor its containing block is
 *      the initial one, so it is not clipped by the `static` shell's
 *      `overflow:hidden` and its static position — thousands of pixels down the
 *      chapter — became the document's scrollable overflow. The whole shell
 *      (sidebar, header) then scrolled out from under the learner whenever the
 *      browser revealed a focused control.
 *
 *   2. Below 760 px the rail is 64 px and `.navtext` is `display:none`, and the
 *      only glyph is `aria-hidden`. Nothing carried the label, so the nav was
 *      nine anonymous icons — on touch there is no hover to recover the name.
 *
 * There is **no layout engine in this repo** (no Chrome, no Puppeteer, no
 * Playwright, no axe — see docs/accessibility.md), so these cannot be asserted
 * the way the other audits assert behaviour. What this script can do, honestly,
 * is hold the *rules* that made the fixes work, and refuse to let them be
 * deleted by a later cleanup:
 *
 *   - a container that clips its overflow must be a containing block, or an
 *     absolutely-positioned descendant escapes the clip;
 *   - if the narrow layout hides a control's visible text, the control must
 *     carry the name some other way.
 *
 * It proves neither that the page is 800 px tall nor that a screen reader
 * announces "Workbook" — only that the two rules those facts rest on are still
 * in the source. Stated here so a green line is not mistaken for a rendering
 * check.
 *
 *   make audit-shell        # node scripts/shell-audit.mjs
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..');
const cssPath = path.join(root, 'web', 'src', 'styles', 'querycraft.css');
const shellPath = path.join(root, 'web', 'src', 'components', 'Shell.tsx');

const css = fs.readFileSync(cssPath, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');
const shell = fs.readFileSync(shellPath, 'utf8');

let failed = 0;
function check(name, cond, detail) {
  if (cond) {
    console.log(`  ok     ${name}`);
  } else {
    failed++;
    console.log(`  FAIL   ${name}${detail === undefined ? '' : ' — ' + detail}`);
  }
}

/* `selector { body }` pairs at any depth. Declarations never contain braces and
   an at-rule prelude carries none either, so a flat scan sees every rule —
   including the ones inside `@media` — without a parser dependency. */
function rules(source) {
  const out = [];
  const re = /([^{}]+)\{([^{}]*)\}/g;
  let m;
  while ((m = re.exec(source)) !== null) {
    out.push({ selector: m[1].trim().replace(/\s+/g, ' '), body: m[2].trim() });
  }
  return out;
}

/** The declaration body of the first rule whose selector list names `sel`. */
function ruleFor(source, sel) {
  for (const rule of rules(source)) {
    if (rule.selector.split(',').some((s) => s.trim() === sel)) return rule;
  }
  return undefined;
}

function decls(body) {
  const out = {};
  for (const part of body.split(';')) {
    const i = part.indexOf(':');
    if (i === -1) continue;
    out[part.slice(0, i).trim()] = part.slice(i + 1).trim();
  }
  return out;
}

/* Every `@media` body whose prelude matches, braces balanced. Plural on
   purpose: the invariant below is about a *rule that exists somewhere* inside a
   narrow-layout block, not about the first such block. A stylesheet may hold
   several `@media (max-width:760px)` blocks (one per concern), and the rule can
   move between them in a cleanup without the layout changing at all. Reading
   only the first block made this audit fail on a stylesheet that was correct. */
function atMediaBlocks(source, pattern) {
  const out = [];
  const re = new RegExp(pattern.source, pattern.flags.includes('g') ? pattern.flags : pattern.flags + 'g');
  let m;
  while ((m = re.exec(source)) !== null) {
    let i = m.index + m[0].indexOf('{');
    if (i === -1) i = source.indexOf('{', m.index);
    if (i === -1) continue;
    let depth = 0;
    for (let j = i; j < source.length; j++) {
      if (source[j] === '{') depth++;
      else if (source[j] === '}') {
        depth--;
        if (depth === 0) {
          out.push(source.slice(i + 1, j));
          break;
        }
      }
    }
  }
  return out;
}

const all = rules(css);

/* 1. The shell clips. Which rule is the shell is not a guess: it is the rule
   that sets `overflow:hidden` together with a viewport height. */
const clippers = all.filter((r) => {
  const d = decls(r.body);
  return d.overflow === 'hidden' && (d.height === '100vh' || d['min-height'] === '100vh');
});

check('a clipping shell rule exists to audit', clippers.length > 0, 'no `overflow:hidden;height:100vh` rule found in querycraft.css');

for (const rule of clippers) {
  const d = decls(rule.body);
  check(
    `\`${rule.selector}\` clips and is a containing block`,
    d.position !== undefined && d.position !== 'static',
    `position is ${d.position === undefined ? 'unset (static)' : d.position} — an absolutely positioned descendant (e.g. .visually-hidden) would escape the clip and hand the document its own height`
  );
}

/* 2. `.visually-hidden` really is absolutely positioned, so rule 1 is load-bearing
   rather than a coincidence. */
const vh = ruleFor(css, '.visually-hidden');
const vhPos = vh ? decls(vh.body).position : undefined;
check('.visually-hidden is still position:absolute, so rule 1 stays load-bearing', vhPos === 'absolute', vh ? `position is ${vhPos}` : 'rule not found');

/* 3. The narrow layout hides the rail's visible text. If that stops being true
   the label check below is vacuous, and this says so out loud. The rule may live
   in any `@media (max-width:760px)` block, so every one is searched. */
const narrowBlocks = atMediaBlocks(css, /@media\s*\(max-width:\s*760px\)/);
const navtextRule = narrowBlocks.map((b) => ruleFor(b, '.navtext')).find(Boolean);
const navtextHidden = !!navtextRule && decls(navtextRule.body).display === 'none';
check('the narrow layout hides .navtext (the 64px icon rail)', navtextHidden, narrowBlocks.length ? 'no @media(max-width:760px) block hides .navtext' : 'no @media(max-width:760px) block found');

/* 4. With the text hidden, the name has to come from somewhere. */
if (navtextHidden) {
  check(
    'NavButton carries the name it hides (aria-label + title)',
    /aria-label=\{label\}/.test(shell) && /title=\{label\}/.test(shell),
    'Shell.tsx NavButton no longer passes aria-label={label} / title={label}'
  );
  check(
    'the recent-chapter buttons carry a name too',
    /aria-label=\{`Chapter \$\{f\.module\.n\}/.test(shell),
    'Shell.tsx recent-chapter buttons no longer pass an aria-label'
  );
}

/* 5. The theme toggle is the other control the narrow layout keeps; a cleanup
   should not take its name along with its label. */
check(
  'the theme toggle keeps its accessible name',
  /aria-label="Toggle theme"/.test(shell) && /title="Toggle theme"/.test(shell),
  'Shell.tsx theme toggle lost aria-label/title'
);

console.log(failed === 0 ? '\nshell-audit: OK' : `\nshell-audit: ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
