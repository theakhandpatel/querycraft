#!/usr/bin/env node
/*
 * Theme contrast audit — every shipped preset clears 4.5:1 on its key text
 * pairs. The palettes live in web/src/styles/themes.css (one block per theme
 * over the shared custom properties) plus querycraft.css's :root (light
 * default) and [data-theme="dark"].
 *
 * What is checked, per theme: body text (--text) and secondary text
 * (--text-2) on the page canvas and the surface; small metadata text
 * (--text-3) on both; the accent ink, the verdict inks (green/amber/red) and
 * the primary-button ink on the fills and washes they sit on. Alpha tokens
 * (the soft washes) are composited over the surface before the ratio is
 * taken, because that is where they render. 4.5:1 is WCAG AA for the sizes
 * this UI sets these tokens at.
 *
 *   node scripts/theme-contrast.mjs        (make audit-themes)
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const css = fs.readFileSync(path.join(root, 'web/src/styles/querycraft.css'), 'utf8');
const themes = fs.readFileSync(path.join(root, 'web/src/styles/themes.css'), 'utf8');

let failures = 0;
let checked = 0;

function tokensFrom(block) {
  const map = {};
  for (const m of block.matchAll(/--([a-z0-9-]+)\s*:\s*([^;]+);/g)) {
    map[`--${m[1]}`] = m[2].trim();
  }
  return map;
}

function parseHex(hex) {
  const h = hex.replace('#', '');
  const full = h.length === 3 ? h.split('').map((c) => c + c).join('') : h;
  return [parseInt(full.slice(0, 2), 16), parseInt(full.slice(2, 4), 16), parseInt(full.slice(4, 6), 16)];
}

function parseColor(value, over) {
  /* Returns [r, g, b] composited over `over` when the value carries alpha. */
  const v = value.trim();
  if (v.startsWith('#')) return parseHex(v);
  const rgba = v.match(/rgba?\(([^)]+)\)/);
  if (!rgba) return null;
  const parts = rgba[1].split(',').map((s) => parseFloat(s.trim()));
  const [r, g, b, a] = [parts[0], parts[1], parts[2], parts[3] ?? 1];
  if (a === undefined || a >= 1) return [r, g, b];
  if (!over) return null;
  const base = Array.isArray(over) ? over : parseColor(over, null);
  if (!base) return null;
  return [
    Math.round(r * a + base[0] * (1 - a)),
    Math.round(g * a + base[1] * (1 - a)),
    Math.round(b * a + base[2] * (1 - a)),
  ];
}

function luminance([r, g, b]) {
  const lin = [r, g, b].map((c) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * lin[0] + 0.7152 * lin[1] + 0.0722 * lin[2];
}

function ratio(fg, bg) {
  const lf = luminance(fg);
  const lb = luminance(bg);
  const [hi, lo] = lf >= lb ? [lf, lb] : [lb, lf];
  return (hi + 0.05) / (lo + 0.05);
}

function check(theme, pairs) {
  for (const [fgName, bgName, bgValue] of pairs) {
    checked++;
    const fg = parseColor(theme[fgName], theme['--surface']);
    const bg = bgValue ?? parseColor(theme[bgName], theme['--bg']);
    if (!fg || !bg) {
      failures++;
      console.log(`  FAIL   ${theme.__id}: ${fgName} or ${bgName} could not be parsed`);
      continue;
    }
    const r = ratio(fg, bg);
    if (r < 4.5) {
      failures++;
      console.log(`  FAIL   ${theme.__id}: ${fgName} on ${bgName} = ${r.toFixed(2)}:1 (need 4.5)`);
    }
  }
}

const blocks = [];
const rootBlock = css.match(/:root\{([^}]*)\}/);
blocks.push({ __id: 'light (default)', __aliases: ['light'], ...tokensFrom(rootBlock[1]) });
for (const m of css.matchAll(/\[data-theme="(dark)"\]\{([^}]*)\}/g)) {
  blocks.push({ __id: m[1], ...tokensFrom(m[2]) });
}
for (const m of themes.matchAll(/\[data-theme="([a-z-]+)"\]\{([^}]*)\}/g)) {
  blocks.push({ __id: m[1], ...tokensFrom(m[2]) });
}

const seen = new Set();
for (const block of blocks) {
  const ids = [block.__id, ...(block.__aliases ?? [])];
  for (const id of ids) {
    if (seen.has(id)) {
      failures++;
      console.log(`  FAIL   duplicate theme block: ${id}`);
    }
    seen.add(id);
  }
  const surface = block['--surface'];
  const bg = block['--bg'];
  check(block, [
    ['--text', '--bg', bg],
    ['--text', '--surface', surface],
    ['--text-2', '--bg', bg],
    ['--text-2', '--surface', surface],
    ['--text-3', '--bg', bg],
    ['--text-3', '--surface', surface],
    ['--accent-ink', '--bg', bg],
    ['--accent-ink', '--surface', surface],
    ['--green-ink', '--surface', surface],
    ['--amber-ink', '--surface', surface],
    ['--red-ink', '--surface', surface],
    ['--on-accent', '--accent', block['--accent']],
    ['--on-accent', '--accent-solid', block['--accent-solid']],
    ['--on-accent', '--accent-hover', block['--accent-hover']],
  ]);
}

for (const t of ['light', 'dark']) {
  if (!seen.has(t)) {
    failures++;
    console.log(`  FAIL   the ${t} default block is missing`);
  }
}

console.log(`theme-contrast: ${blocks.length} themes, ${checked} pairs checked`);
if (failures > 0) {
  console.log(`theme-contrast: FAIL (${failures})`);
  process.exit(1);
}
console.log('theme-contrast: OK');