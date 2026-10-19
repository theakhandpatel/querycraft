#!/usr/bin/env node
/*
 * Check the public browser sample where it will actually live.
 *
 *   make static-check
 *   node scripts/check-static.mjs
 *   STATIC_PREFIX=/querycraft/ node scripts/check-static.mjs --screenshot
 *
 * The build is served under a project path, not at the domain root. That is the
 * failure this check is for: a page built with `/assets` can look perfect on a
 * local root and break on every GitHub Pages project site.
 *
 * It drives the real landing and the real SPA through playwright-core:
 *
 *   - the landing has no visible sign-in or AI course-building promise;
 *   - every internal link is relative and the Start action reaches the app;
 *   - the app boots as the local workspace on SQLite Essentials;
 *   - the first load does not request the PGlite data bundle;
 *   - static assets do not 404 and the page raises no uncaught error.
 *
 * The browser is the same Chrome the accessibility audit uses. Set QC_CHROME to
 * another binary on a machine that has one somewhere else.
 */
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const WEB = path.join(ROOT, 'web');
const DIST = path.join(WEB, 'dist-static');
const SHOTS = path.join(ROOT, '.agents', 'archive', 'static-shots');
const PREFIX = normalisePrefix(process.env.STATIC_PREFIX || '/querycraft/');
const DEFAULT_CHROME = '/home/darkoptimus/.agent-browser/browsers/chrome-155.0.8059.39/chrome';
const screenshot = process.argv.includes('--screenshot');

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript',
  '.mjs': 'text/javascript',
  '.css': 'text/css',
  '.wasm': 'application/wasm',
  '.json': 'application/json',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.gif': 'image/gif',
  '.ico': 'image/x-icon',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.ttf': 'font/ttf',
  '.data': 'application/octet-stream',
  '.map': 'application/json',
};

function normalisePrefix(value) {
  const withLead = value.startsWith('/') ? value : `/${value}`;
  return withLead.endsWith('/') ? withLead : `${withLead}/`;
}

function fail(message) {
  console.error(`check-static: ${message}`);
  process.exitCode = 1;
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function serve() {
  return new Promise((resolve, reject) => {
    const server = http.createServer((req, res) => {
      const url = new URL(req.url, 'http://localhost');
      if (url.pathname === PREFIX.slice(0, -1)) {
        res.writeHead(301, { location: PREFIX });
        res.end();
        return;
      }
      if (!url.pathname.startsWith(PREFIX)) {
        res.writeHead(404);
        res.end('not found');
        return;
      }
      let rel;
      try {
        rel = decodeURIComponent(url.pathname.slice(PREFIX.length));
      } catch {
        res.writeHead(400);
        res.end('bad path');
        return;
      }
      if (rel === '' || rel.endsWith('/')) rel += 'index.html';
      const file = path.resolve(DIST, rel);
      if (!file.startsWith(`${DIST}${path.sep}`) && file !== DIST) {
        res.writeHead(403);
        res.end();
        return;
      }
      fs.stat(file, (statError, stat) => {
        if (statError || !stat.isFile()) {
          res.writeHead(404);
          res.end('not found');
          return;
        }
        fs.readFile(file, (readError, body) => {
          if (readError) {
            res.writeHead(500);
            res.end('read failed');
            return;
          }
          res.writeHead(200, {
            'content-type': MIME[path.extname(file)] ?? 'application/octet-stream',
            'cache-control': 'no-store',
          });
          res.end(body);
        });
      });
    });
    server.on('error', reject);
    server.listen(0, '127.0.0.1', () => resolve(server));
  });
}

async function main() {
  if (!fs.existsSync(path.join(DIST, 'index.html'))) {
    fail('web/dist-static is missing; run `make static` first');
    process.exit(1);
  }
  const chrome = process.env.QC_CHROME || DEFAULT_CHROME;
  if (!fs.existsSync(chrome)) {
    fail(`Chrome not found at ${chrome}; set QC_CHROME`);
    process.exit(1);
  }

  const webRequire = createRequire(path.join(WEB, 'package.json'));
  const { chromium } = webRequire('playwright-core');
  const server = await serve();
  const base = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch({ executablePath: chrome, headless: true, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  page.setDefaultTimeout(90_000);

  const requests = [];
  const failures = [];
  const errors = [];
  page.on('request', (request) => requests.push(request.url()));
  page.on('requestfailed', (request) => failures.push(`${request.url()} ${request.failure()?.errorText ?? ''}`));
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (message) => {
    if (message.type() === 'error') errors.push(message.text());
  });

  try {
    await page.goto(`${base}${PREFIX}`, { waitUntil: 'domcontentloaded' });
    await page.waitForSelector('h1');

    const landing = await page.evaluate(() => {
      const visible = (node) => {
        if (!node) return false;
        const style = window.getComputedStyle(node);
        /* getClientRects is empty when an ancestor is display:none. Checking
           the leaf's own display would call hidden hosted copy visible. */
        return style.visibility !== 'hidden' && node.getClientRects().length > 0;
      };
      return {
        title: document.title,
        heading: document.querySelector('h1')?.textContent ?? '',
        build: document.documentElement.dataset.build ?? '',
        startLinks: [...document.querySelectorAll('a')].filter((a) => a.getAttribute('href') === 'app/').length,
        absoluteInternal: [...document.querySelectorAll('[href], [src]')]
          .map((node) => node.getAttribute('href') || node.getAttribute('src') || '')
          .filter((value) => value.startsWith('/app/') || value.startsWith('/landing/')),
        signInVisible: [...document.querySelectorAll('body *')].some((node) => visible(node) && node.children.length === 0 && /sign in/i.test(node.textContent ?? '')),
        createVisible: [...document.querySelectorAll('body *')].some((node) => visible(node) && node.children.length === 0 && /create a course/i.test(node.textContent ?? '')),
        aiVisible: [...document.querySelectorAll('body *')].some((node) => visible(node) && node.children.length === 0 && /describe the course/i.test(node.textContent ?? '')),
      };
    });

    console.log(`landing: ${landing.title}`);
    assert(landing.build === 'static', 'the landing was not built with data-build="static"');
    assert(/Learn SQL/i.test(landing.heading), 'the landing heading is missing');
    assert(landing.startLinks > 0, 'the landing has no relative Start a lesson link');
    assert(landing.absoluteInternal.length === 0, `the landing has absolute internal URLs: ${landing.absoluteInternal.join(', ')}`);
    assert(!landing.signInVisible, 'the static landing still shows a sign-in promise');
    assert(!landing.createVisible, 'the static landing still shows the AI course builder');
    assert(!landing.aiVisible, 'the static landing still invites the AI course builder');

    /* The first app link in the document belongs to the mobile menu, which is
       closed at this width. Use the visible top call to action. */
    await page.locator('a.top-cta[href="app/"]').click();
    await page.waitForURL((url) => url.pathname === `${PREFIX}app/`, { timeout: 30_000 });
    await page.waitForSelector('main.workspace .screen.active', { state: 'attached' });
    await page.waitForFunction(() => !document.querySelector('.authwrap .spin'));
    await page.waitForFunction(() => {
      const eyebrow = document.querySelector('.screen.active .eyebrow');
      return !!eyebrow && /workbench/i.test(eyebrow.textContent ?? '');
    });

    const app = await page.evaluate(() => ({
      title: document.title,
      body: document.body.innerText,
      screen: document.querySelector('.screen.active .eyebrow')?.textContent ?? '',
    }));
    assert(/SQLite Essentials/i.test(app.body), 'the simple build did not open on SQLite Essentials');
    assert(!/Sign in to keep your progress/i.test(app.body), 'the app still promises a hosted sign-in');
    assert(!/Ask tutor/i.test(app.body), 'the simple build still offers the AI tutor');
    assert(!/Challenge lab/i.test(app.body), 'the simple build still offers the AI challenge lab');
    assert(!/Create your own|Create a course/i.test(app.body), 'the simple build still offers course generation');

    /* Drive the engine and the grader, not just the first paint. The home cell
       is the first SQLite exercise; its authored solution must pass on the
       in-browser engine with no server. */
    const editor = page.locator('main.workspace textarea').first();
    await page.getByRole('button', { name: 'Show solution' }).first().click();
    const reference = page.locator('.fb').filter({ hasText: 'Reference solution' });
    await reference.waitFor();
    const solution = (await reference.locator('pre').innerText()).trim();
    assert(solution.length > 0, 'the reference solution was empty');
    await editor.fill(solution);
    await page.getByRole('button', { name: 'Check answer' }).first().click();
    await page.waitForFunction(() => document.querySelector('.verdict-stamp') || document.querySelector('.fb.failed'));
    if ((await page.locator('.verdict-stamp').count()) === 0) {
      const failure = await page.locator('.fb.failed').innerText();
      throw new Error(`the first SQLite exercise did not pass:\n${failure}`);
    }
    const verdict = (await page.locator('.verdict-stamp').innerText()).trim();
    assert(/passed/i.test(verdict), `the first SQLite exercise did not pass (verdict: ${verdict})`);

    const pgliteData = requests.filter((url) => /pglite.*\.(data|wasm)(\?|$)/i.test(url) || /pglite\.data/i.test(url));
    assert(pgliteData.length === 0, `the first load requested the PGlite engine: ${pgliteData.join(', ')}`);
    assert(failures.length === 0, `static assets failed: ${failures.join('; ')}`);
    assert(errors.length === 0, `the page logged an error: ${errors.join('; ')}`);

    if (screenshot) {
      fs.mkdirSync(SHOTS, { recursive: true });
      /* Let the stamp and the result rows finish their entrance. */
      await page.waitForTimeout(600);
      await page.screenshot({ path: path.join(SHOTS, 'app-1440-simple.png'), fullPage: false });
      await page.locator('.verdict-stamp').scrollIntoViewIfNeeded();
      await page.waitForTimeout(300);
      await page.screenshot({ path: path.join(SHOTS, 'app-1440-simple-pass.png'), fullPage: false });
      await page.goto(`${base}${PREFIX}`, { waitUntil: 'domcontentloaded' });
      await page.waitForSelector('h1');
      await page.screenshot({ path: path.join(SHOTS, 'landing-1440-static.png'), fullPage: true });
      console.log(`screenshots: ${SHOTS}`);
    }

    console.log(`check-static: OK (${PREFIX}, SQLite Essentials, no PGlite data)`);
    console.log(`  requests: ${requests.length}; app screen: ${app.screen.trim()}`);
  } finally {
    await browser.close().catch(() => {});
    await new Promise((resolve) => server.close(resolve));
  }
}

main().catch((error) => {
  fail(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
});
