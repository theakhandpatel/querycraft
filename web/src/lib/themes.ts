/*
 * The theme registry: every preset the app ships, as data.
 *
 * The tokens themselves live in styles/themes.css — one block per theme over
 * the same 32 custom properties the whole UI reads. Adding a theme is one CSS
 * block here plus one entry in THEMES; nothing else changes. The registry is
 * what the Appearance screen's swatch grid renders, and what the contrast
 * audit walks (scripts/theme-contrast.mjs): a preset that fails 4.5:1 on its
 * key text pairs does not ship.
 */

interface ThemeDef {
  id: string;
  label: string;
  mode: 'light' | 'dark';
  /** The three swatches the picker previews: page canvas, surface, accent. */
  swatch: { bg: string; surface: string; accent: string };
}

export const THEMES: ThemeDef[] = [
  // Light family.
  { id: 'light', label: 'Light · Ink & Pine', mode: 'light', swatch: { bg: '#f4f5f7', surface: '#ffffff', accent: '#0c6f5c' } },
  { id: 'paper', label: 'Paper · warm reading', mode: 'light', swatch: { bg: '#f5f0e5', surface: '#fbf7ee', accent: '#0c6f5c' } },
  { id: 'ocean-light', label: 'Ocean · cool light', mode: 'light', swatch: { bg: '#edf1f6', surface: '#ffffff', accent: '#1d5fb0' } },
  { id: 'contrast-light', label: 'High contrast · light', mode: 'light', swatch: { bg: '#ffffff', surface: '#ffffff', accent: '#003ea0' } },
  // Dark family.
  { id: 'dark', label: 'Dark · Pine on Slate', mode: 'dark', swatch: { bg: '#121316', surface: '#181a1f', accent: '#34d3a6' } },
  { id: 'midnight', label: 'Midnight · deep blue', mode: 'dark', swatch: { bg: '#0b0e17', surface: '#101524', accent: '#7da7ff' } },
  { id: 'ember', label: 'Ember · warm dark', mode: 'dark', swatch: { bg: '#151110', surface: '#1d1917', accent: '#f0a24a' } },
  { id: 'ocean-dark', label: 'Ocean · deep dark', mode: 'dark', swatch: { bg: '#0a0f16', surface: '#0f1723', accent: '#4fc3f7' } },
  { id: 'terminal', label: 'Terminal · green on black', mode: 'dark', swatch: { bg: '#050a06', surface: '#081109', accent: '#4ade80' } },
  { id: 'contrast-dark', label: 'High contrast · dark', mode: 'dark', swatch: { bg: '#000000', surface: '#0d0d0d', accent: '#7ab7ff' } },
  { id: 'one-dark', label: 'One Dark', mode: 'dark', swatch: { bg: '#282c34', surface: '#21252b', accent: '#61afef' } },
  { id: 'dracula', label: 'Dracula', mode: 'dark', swatch: { bg: '#282a36', surface: '#21222c', accent: '#bd93f9' } },
  { id: 'tokyo-night', label: 'Tokyo Night', mode: 'dark', swatch: { bg: '#1a1b26', surface: '#16161e', accent: '#7aa2f7' } },
  { id: 'nord', label: 'Nord', mode: 'dark', swatch: { bg: '#2e3440', surface: '#292e39', accent: '#88c0d0' } },
  { id: 'monokai', label: 'Monokai Classic', mode: 'dark', swatch: { bg: '#272822', surface: '#1f201a', accent: '#a6e22e' } },
  { id: 'rose-pine', label: 'Rosé Pine', mode: 'dark', swatch: { bg: '#191724', surface: '#1f1d2e', accent: '#c4a7e7' } },
  { id: 'solarized-light', label: 'Solarized Light', mode: 'light', swatch: { bg: '#fdf6e3', surface: '#fffbf0', accent: '#268bd2' } },
  { id: 'github-dimmed', label: 'GitHub Dimmed', mode: 'dark', swatch: { bg: '#22272e', surface: '#1c2128', accent: '#539bf5' } },
];