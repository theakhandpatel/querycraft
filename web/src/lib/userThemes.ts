/*
 * User-defined themes: one accent color over a base preset's token set.
 *
 * The derivation is the same math the contrast audit applies to shipped
 * presets (scripts/theme-contrast.mjs): the accent family is filled in from
 * the chosen accent, and every ink that has to clear 4.5:1 — accent-ink on
 * the surface, the button ink on the accent fills — is tuned toward the base
 * text color until it passes. A theme a user makes cannot fail the gate the
 * shipped presets pass, because it is built by the same rule.
 *
 * The base token sets below mirror querycraft.css's :root (light) and
 * [data-theme="dark"] (dark) blocks. They are duplicated here because
 * derivation needs a base the user is not currently looking at, and computed
 * styles only answer for the active theme. If the shipped palettes change,
 * these two maps change with them — the contrast audit cannot check what a
 * user derives at runtime, which is exactly why the derivation tunes inks
 * itself.
 */

import type { ThemeName } from '../types';

export interface UserTheme {
  id: string;
  name: string;
  /** Which family the neutral tokens come from. */
  base: Exclude<ThemeName, 'system'>;
  /** The accent as a hex color. */
  accent: string;
  /** The full derived token set, stored so rendering needs no re-derivation. */
  tokens: Record<string, string>;
}

const LIGHT_BASE = {
  '--bg': '#f4f5f7',
  '--surface': '#ffffff',
  '--surface-2': '#f7f8fa',
  '--surface-3': '#eef0f3',
  '--line': '#e3e6eb',
  '--line-strong': '#cbd1da',
  '--text': '#14161a',
  '--text-2': '#494f5a',
  '--text-3': '#626873',
  '--green': '#1a7f4f',
  '--green-soft': 'rgba(26,127,79,.11)',
  '--green-ink': '#15663f',
  '--amber': '#8a5a10',
  '--amber-soft': 'rgba(138,90,16,.12)',
  '--amber-ink': '#6f4909',
  '--red': '#b3352f',
  '--red-soft': 'rgba(179,53,47,.10)',
  '--red-ink': '#8f2a25',
  '--code-bg': '#fbfcfd',
  '--code-text': '#20303f',
  '--code-line': '#e6e9ee',
  '--shadow': '0 1px 2px rgba(20,22,26,.05),0 10px 30px -18px rgba(20,22,26,.28)',
  '--shadow-lg': '0 24px 60px -30px rgba(20,22,26,.42)',
  '--accent-soft-alpha': '0.09',
  '--accent-line-alpha': '0.26',
};

const DARK_BASE = {
  '--bg': '#121316',
  '--surface': '#181a1f',
  '--surface-2': '#1d2025',
  '--surface-3': '#242830',
  '--line': '#2a2e36',
  '--line-strong': '#3a404a',
  '--text': '#eef0f2',
  '--text-2': '#a7aeb8',
  '--text-3': '#99a1ab',
  '--green': '#4bd39a',
  '--green-soft': 'rgba(75,211,154,.13)',
  '--green-ink': '#7ce3b0',
  '--amber': '#e0b35c',
  '--amber-soft': 'rgba(224,179,92,.14)',
  '--amber-ink': '#f0c877',
  '--red': '#f08a8a',
  '--red-soft': 'rgba(240,138,138,.13)',
  '--red-ink': '#f7a3a3',
  '--code-bg': '#0e1013',
  '--code-text': '#d6dde6',
  '--code-line': '#252a31',
  '--shadow': '0 1px 2px rgba(0,0,0,.3),0 12px 34px -20px rgba(0,0,0,.7)',
  '--shadow-lg': '0 30px 70px -34px rgba(0,0,0,.85)',
  '--accent-soft-alpha': '0.12',
  '--accent-line-alpha': '0.30',
};

/* ---------- color math, shared with scripts/theme-contrast.mjs ---------- */

function hexrgb(h: string): [number, number, number] {
  const v = h.replace('#', '');
  const full = v.length === 3 ? v.split('').map((c) => c + c).join('') : v;
  return [parseInt(full.slice(0, 2), 16), parseInt(full.slice(2, 4), 16), parseInt(full.slice(4, 6), 16)];
}

function tohex(rgb: [number, number, number]): string {
  return '#' + rgb.map((c) => Math.max(0, Math.min(255, c)).toString(16).padStart(2, '0')).join('');
}

function mix(a: [number, number, number], b: [number, number, number], t: number): [number, number, number] {
  return [
    Math.round(a[0] * (1 - t) + b[0] * t),
    Math.round(a[1] * (1 - t) + b[1] * t),
    Math.round(a[2] * (1 - t) + b[2] * t),
  ];
}

function luminance(rgb: [number, number, number]): number {
  const lin = rgb.map((c) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * lin[0] + 0.7152 * lin[1] + 0.0722 * lin[2];
}

function contrastRatio(a: string, b: string): number {
  const la = luminance(hexrgb(a));
  const lb = luminance(hexrgb(b));
  const [hi, lo] = la >= lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
}

/** Tune `color` toward white (dark bases) or black (light bases) until it
    clears 4.5:1 on `surfaceHex`. The port generator's rule, in TS. */
function tuneInk(color: string, surfaceHex: string, dark: boolean): string {
  const rgb = hexrgb(color);
  const target: [number, number, number] = dark ? [255, 255, 255] : [0, 0, 0];
  for (let step = 0; step <= 40; step++) {
    const candidate = mix(rgb, target, step / 40);
    if (contrastRatio(tohex(candidate), surfaceHex) >= 4.5) return tohex(candidate);
  }
  return tohex(target);
}

/** Derive a full token set from a base family plus the user's accent. */
export function deriveUserTokens(accentHex: string, base: 'light' | 'dark') {
  const source = base === 'dark' ? DARK_BASE : LIGHT_BASE;
  const accent = hexrgb(accentHex);
  const dark = base === 'dark';
  const white: [number, number, number] = [255, 255, 255];
  const black: [number, number, number] = [0, 0, 0];
  const solidHex = tohex(dark ? mix(accent, black, 0.08) : mix(accent, black, 0.04));
  const tokens = {
    ...source,
    '--accent': accentHex,
    '--accent-solid': solidHex,
    '--accent-hover': tohex(dark ? mix(accent, white, 0.18) : mix(accent, black, 0.16)),
    '--accent-soft': `rgba(${accent.join(',')},${source['--accent-soft-alpha']})`,
    '--accent-line': `rgba(${accent.join(',')},${source['--accent-line-alpha']})`,
    // The two inks are tuned, never taken raw: a neon accent must not become
    // unreadable text, and a dark accent must not become a black button.
    '--accent-ink': tuneInk(accentHex, source['--surface'], dark),
    '--on-accent': contrastRatio(tohex(white), solidHex) >= 4.5 ? '#ffffff' : '#000000',
  };
  if (contrastRatio(tokens['--on-accent'], tokens['--accent-solid']) < 4.5) {
    // Neither pure ink clears it: pull the fill toward the accent until the
    // chosen ink does, keeping the hue.
    const inkIsWhite = tokens['--on-accent'] === '#ffffff';
    const solid = hexrgb(tokens['--accent-solid']);
    for (let step = 1; step <= 40; step++) {
      const adjusted = mix(solid, inkIsWhite ? black : white, step / 40);
      if (contrastRatio(tokens['--on-accent'], tohex(adjusted)) >= 4.5) {
        tokens['--accent-solid'] = tohex(adjusted);
        break;
      }
    }
  }
  return tokens;
}

/** Build a user theme: id, name, and the derived tokens. */
export function buildUserTheme(name: string, accentHex: string, base: 'light' | 'dark'): UserTheme {
  const id = 'user-' + Math.random().toString(36).slice(2, 8);
  return { id, name, base, accent: accentHex, tokens: deriveUserTokens(accentHex, base) };
}

/** Render one user theme's tokens as a style element's text. */
function userThemeCSS(theme: UserTheme): string {
  const lines = Object.entries(theme.tokens)
    .map(([k, v]) => `  ${k}:${v};`)
    .join('\n');
  return `:root[data-theme="${theme.id}"]{\n  color-scheme:${theme.base === 'dark' ? 'dark' : 'light'};\n${lines}\n}`;
}

/** Apply every user theme to the document, so any id can resolve at once. */
export function injectUserThemes(themes: UserTheme[]): void {
  const id = 'querycraft-user-themes';
  let el = document.getElementById(id) as HTMLStyleElement | null;
  if (!themes.length) {
    el?.remove();
    return;
  }
  if (!el) {
    el = document.createElement('style');
    el.id = id;
    document.head.appendChild(el);
  }
  el.textContent = themes.map(userThemeCSS).join('\n');
}