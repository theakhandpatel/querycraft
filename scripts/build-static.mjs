#!/usr/bin/env node
/*
 * Build the public browser sample.
 *
 *   make static
 *   node scripts/build-static.mjs            # build the SPA, then assemble
 *   node scripts/build-static.mjs --skip-build
 *
 * The output is web/dist-static:
 *
 *   index.html              the landing, rewritten for a project path
 *   landing/assets/         the landing images and icon
 *   app/                    the SPA, with relative asset URLs
 *   .nojekyll               GitHub Pages must not run Jekyll
 *   build-info.json         what this artifact is, for debugging
 *
 * The SPA is built with VITE_APP_MODE=simple and VITE_API_MODE=pglite. It
 * carries all built-in courses, grades locally, and stores progress in
 * IndexedDB. The gateway, accounts, AI generation, runs and admin are absent by
 * build, not by a disabled button.
 *
 * The relative base is the reason this works on GitHub Pages. A project site
 * lives at /querycraft/, so `/assets/...` would ask the domain root; `./assets`
 * resolves under the app. `make web` keeps the gateway build at `/`.
 */
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { staticLanding } from './static-landing.mjs';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const WEB = path.join(ROOT, 'web');
const DIST = path.join(WEB, 'dist');
const OUT = path.join(WEB, 'dist-static');
const LANDING = path.join(WEB, 'landing');
const APP_OUT = path.join(OUT, 'app');

const skipBuild = process.argv.includes('--skip-build');

function run(command, args, options) {
  execFileSync(command, args, { stdio: 'inherit', ...options });
}

function fail(message) {
  console.error(`build-static: ${message}`);
  process.exit(1);
}

function copyDir(from, to) {
  fs.cpSync(from, to, { recursive: true });
}

/** Copy only the landing images the static document still references. The
    hosted walkthrough and course-builder art are stripped with the hosted copy,
    so shipping them would add megabytes nobody can see. */
function copyLandingAssets(html) {
  const refs = new Set();
  for (const match of html.matchAll(/(?:src|href)="(landing\/assets\/[^"]+)"/g)) refs.add(match[1]);
  for (const match of html.matchAll(/url\(["']?(landing\/assets\/[^"')]+)/g)) refs.add(match[1]);
  for (const ref of refs) {
    const from = path.join(WEB, ref);
    const to = path.join(OUT, ref);
    if (!fs.existsSync(from)) fail(`the static landing references a missing asset: ${ref}`);
    fs.mkdirSync(path.dirname(to), { recursive: true });
    fs.copyFileSync(from, to);
  }
  return refs.size;
}

function readJson(file) {
  return JSON.parse(fs.readFileSync(file, 'utf8'));
}

function gitCommit() {
  try {
    return execFileSync('git', ['rev-parse', '--short', 'HEAD'], { cwd: ROOT, encoding: 'utf8' }).trim();
  } catch {
    return 'unknown';
  }
}

function checkRelativeAppIndex() {
  const index = path.join(DIST, 'index.html');
  if (!fs.existsSync(index)) fail('web/dist/index.html is missing');
  const html = fs.readFileSync(index, 'utf8');
  /* Vite's `--base=./` makes both the entry script and the favicon relative.
     A leading slash would still work at a domain root and fail on Pages. */
  if (/(?:src|href)="\/(?:assets|src|@vite)/.test(html)) {
    fail('the app index still has an absolute asset URL; build with --base=./');
  }
  if (!html.includes('./assets/') && !html.includes('assets/')) {
    fail('the app index has no asset link; the build did not produce a bundle');
  }
}

function writeBuildInfo() {
  const pkg = readJson(path.join(WEB, 'package.json'));
  const info = {
    app: 'querycraft',
    mode: 'simple',
    transport: 'pglite',
    base: './',
    version: pkg.version,
    commit: gitCommit(),
    builtAt: new Date().toISOString(),
  };
  fs.writeFileSync(path.join(OUT, 'build-info.json'), `${JSON.stringify(info, null, 2)}\n`);
}

function report() {
  const files = [];
  const walk = (dir) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) walk(full);
      else files.push({ path: path.relative(OUT, full), size: fs.statSync(full).size });
    }
  };
  walk(OUT);
  files.sort((a, b) => b.size - a.size);
  const total = files.reduce((sum, file) => sum + file.size, 0);
  console.log(`\nbuild-static: ${OUT}`);
  console.log(`build-static: ${files.length} files, ${(total / 1024 / 1024).toFixed(1)} MB`);
  for (const file of files.slice(0, 6)) {
    console.log(`  ${(file.size / 1024 / 1024).toFixed(1).padStart(6)} MB  ${file.path}`);
  }
}

function main() {
  if (!skipBuild) {
    console.log('build-static: building the SPA (simple mode, relative base)');
    run('npm', ['run', 'build:simple'], {
      cwd: WEB,
      env: {
        ...process.env,
        VITE_APP_MODE: 'simple',
        VITE_API_MODE: 'pglite',
      },
    });
  } else if (!fs.existsSync(path.join(DIST, 'index.html'))) {
    fail('--skip-build needs an existing web/dist; run without it first');
  }

  checkRelativeAppIndex();

  fs.rmSync(OUT, { recursive: true, force: true });
  fs.mkdirSync(OUT, { recursive: true });

  /* The SPA. `make web` may have copied the landing into web/dist; it belongs
     at the site root here, not inside app/. */
  copyDir(DIST, APP_OUT);
  const copiedLanding = path.join(APP_OUT, 'landing');
  if (fs.existsSync(copiedLanding)) fs.rmSync(copiedLanding, { recursive: true, force: true });

  /* The landing and the images the static document still uses. */
  const landingHtml = fs.readFileSync(path.join(LANDING, 'index.html'), 'utf8');
  const staticHtml = staticLanding(landingHtml);
  fs.writeFileSync(path.join(OUT, 'index.html'), staticHtml);
  const assetCount = copyLandingAssets(staticHtml);
  console.log(`build-static: landing assets ${assetCount}`);

  /* GitHub Pages runs Jekyll on branch-based deployments unless this exists.
     It is harmless with the Actions artifact and makes a manual upload work. */
  fs.writeFileSync(path.join(OUT, '.nojekyll'), '');

  writeBuildInfo();
  report();
}

main();
