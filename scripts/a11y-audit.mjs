#!/usr/bin/env node
/*
 * Accessibility audit — the automated runner docs/accessibility.md said the
 * build did not have.
 *
 * docs/accessibility.md records a scripted keyboard walkthrough as the evidence
 * that the M2 gate ("keyboard-only use of a full lesson works") held. That
 * walkthrough was driven by hand, and nothing re-ran it, so a regression in a
 * role, a name, an ARIA state or a contrast pair would not fail `make test`. This
 * runner is that check:
 *
 *   make audit-a11y               # builds web/dist, then drives it (test-all runs it)
 *   node scripts/a11y-audit.mjs   # drives the web/dist that is already built
 *
 * What it does. It serves web/dist from a tiny static server (the hash routes
 * need only index.html), launches the Chrome binary through playwright-core, and
 * runs axe-core with the WCAG 2.x A/AA tags on each real route. The bundle is the
 * default PGlite adapter, so the lesson route waits for the in-browser database
 * to boot and for the chapter markdown it seeds to appear in the reading column.
 *
 * What fails the run. Any `critical` or `serious` violation, named by rule,
 * impact and offending node. `moderate`/`minor` violations are printed as counts:
 * information for the reader, not a gate. A route that never renders is a
 * failure, with the browser console log that explains why. A violation listed in
 * ALLOWED is printed with its written reason instead of failing.
 *
 * What it does not prove. This is not a screen reader and not a keyboard
 * walkthrough. It checks the accessibility-tree inputs a tool can (roles, names,
 * ARIA states, contrast); the keyboard evidence stays in docs/accessibility.md as
 * the separate, still-valid proof.
 */
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { createRequire } from 'node:module';
import { Reporter, repoRoot } from './lib/gateway.mjs';

const WEB_DIR = path.join(repoRoot, 'web');
const DIST_DIR = path.join(WEB_DIR, 'dist');

/* The Chrome the environment ships. QC_CHROME points the audit at another build
   (a system chromium in CI, a newer channel while iterating). */
const DEFAULT_CHROME = '/home/darkoptimus/.agent-browser/browsers/chrome-155.0.8059.39/chrome';

const WEB_REQUIRE = createRequire(path.join(WEB_DIR, 'package.json'));

/* The axe bundle is injected into every frame before the app's own scripts run,
   so `window.axe` is there when the route is settled. */
const AXE_PATH = (() => {
  try {
    return path.join(path.dirname(WEB_REQUIRE.resolve('axe-core')), 'axe.min.js');
  } catch {
    console.error('a11y-audit: axe-core is not installed — run `cd web && npm install` (it is a web devDependency).');
    process.exit(1);
  }
})();

/* WCAG 2.x level A and AA. The product commits to AA; level AAA rules are not
   run because the UI does not claim them. */
const WCAG_TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'];

/* Axe impacts that fail the run. `serious` and `critical` are the ones a learner
   cannot work around; `moderate` and `minor` are reported, not gated. */
const FAIL_IMPACTS = new Set(['critical', 'serious']);

/* The routes the runner drives. Each names the real hash and the screen it must
   render before axe runs. The eyebrow is the screen's own heading text, so a
   stale screen is not mistaken for the new one. The lesson also waits for the
   seeded chapter markdown, which only renders once the in-browser database has
   booted and returned its rows. */
const ROUTES = [
  { name: 'Home', hash: '#/', text: 'Workbench' },
  { name: 'Library', hash: '#/library', text: 'Library' },
  { name: 'Workbook', hash: '#/workbook', text: 'Your workbook' },
  { name: 'Progress', hash: '#/progress', text: 'Progress' },
  { name: 'Appearance', hash: '#/appearance', text: 'Appearance' },
  { name: 'Lesson', hash: '#/course/sql-analytics/m1', text: 'Chapter 1', settle: '.screen.active .lessonmain .reader .mdbody' },
];

/* Violations that are expected, each with the reason it is not a failure. The
   list is deliberately one rule at a time: suppressing a whole rule or excluding
   a region would stop the runner watching that surface, which is the opposite of
   the point. Nothing is listed today — the routes above axe clean. */
const ALLOWED = [
  // { route: 'Lesson', id: 'rule-id', reason: 'why this rule does not apply here' },
];

function allowedFor(route, id) {
  return ALLOWED.find((a) => a.id === id && (a.route === undefined || a.route === route));
}

/* Chrome refuses to start as a non-root-owned sandbox on many CI images; the
   container also gets a small /dev/shm, which crashes the renderer unless it is
   told to use /tmp instead. */
function chromeArgs() {
  const args = ['--no-sandbox'];
  try {
    const shm = fs.statfsSync('/dev/shm');
    const bytes = Number(shm.bsize) * Number(shm.blocks);
    if (bytes > 0 && bytes < 1_000_000_000) args.push('--disable-dev-shm-usage');
  } catch {
    /* No /dev/shm to measure; leave the default. */
  }
  return args;
}

const MIME = {
  '.html': 'text/html',
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

/* A static server over web/dist. Hash routes never ask for a path, so an
   unknown path is a real 404 rather than an index.html fallback: that keeps the
   app's `/health` probe honest (a 404 leaves it on the PGlite adapter). */
function serveDist() {
  return new Promise((resolve, reject) => {
    const server = http.createServer((req, res) => {
      const url = new URL(req.url, 'http://localhost');
      let rel = decodeURIComponent(url.pathname);
      if (rel === '/') rel = '/index.html';
      const file = path.join(DIST_DIR, rel);
      if (!file.startsWith(DIST_DIR)) {
        res.writeHead(403);
        res.end();
        return;
      }
      fs.stat(file, (statErr, stat) => {
        if (statErr || !stat.isFile()) {
          res.writeHead(404);
          res.end('not found');
          return;
        }
        fs.readFile(file, (readErr, body) => {
          if (readErr) {
            res.writeHead(404);
            res.end('not found');
            return;
          }
          res.writeHead(200, { 'content-type': MIME[path.extname(file)] ?? 'application/octet-stream' });
          res.end(body);
        });
      });
    });
    server.on('error', reject);
    server.listen(0, '127.0.0.1', () => resolve(server));
  });
}

const report = new Reporter('a11y-audit');
const ROUTE_TIMEOUT_MS = 90_000;

async function main() {
  if (!fs.existsSync(path.join(DIST_DIR, 'index.html'))) {
    console.error('a11y-audit: web/dist is missing — run `make audit-a11y`, which builds it first.');
    process.exit(1);
  }
  const chrome = process.env.QC_CHROME || DEFAULT_CHROME;
  if (!fs.existsSync(chrome)) {
    console.error(`a11y-audit: Chrome not found at ${chrome}. Set QC_CHROME to a Chrome or Chromium binary.`);
    process.exit(1);
  }

  const { chromium } = WEB_REQUIRE('playwright-core');
  const server = await serveDist();
  const base = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch({ executablePath: chrome, headless: true, args: chromeArgs() });
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  const page = await context.newPage();
  page.setDefaultTimeout(ROUTE_TIMEOUT_MS);
  await page.addInitScript({ path: AXE_PATH });

  const consoleLog = [];
  page.on('console', (m) => {
    const text = m.text();
    if (m.type() === 'error' || m.type() === 'warning') consoleLog.push(`[${m.type()}] ${text}`);
  });
  page.on('pageerror', (e) => consoleLog.push(`[pageerror] ${e.message}`));

  try {
    let first = true;
    for (const route of ROUTES) {
      report.section(`${route.name} ${route.hash}`);
      const mark = consoleLog.length;
      const rendered = await loadRoute(page, route, first, base);
      first = false;
      if (!rendered.ok) {
        report.check(`${route.name} renders`, false, rendered.detail);
        for (const line of consoleLog.slice(mark).slice(-12)) console.log(`         ${line}`);
        continue;
      }
      await runAxe(page, route);
    }
  } finally {
    await browser.close().catch(() => {});
    await new Promise((r) => server.close(r));
  }

  console.log(`\n${report.markdownTable(['Route', 'Hash', 'Violations', 'critical/serious'])}`);
  report.finish();
}

/**
 * loadRoute loads the first route as a document and moves between the rest with
 * `hashchange`, which is how the app navigates. It resolves `{ok, detail}`:
 * failure carries the last console lines, because a route that never renders is
 * almost always explained by one of them.
 */
async function loadRoute(page, route, first, base) {
  try {
    if (first) {
      await page.goto(`${base}/${route.hash}`, { waitUntil: 'domcontentloaded', timeout: ROUTE_TIMEOUT_MS });
    } else {
      await page.evaluate((hash) => {
        window.location.hash = hash;
      }, route.hash);
    }
    await page.waitForSelector('main.workspace .screen.active', { state: 'attached', timeout: ROUTE_TIMEOUT_MS });
    /* `authReady` gates the first paint behind a spinner; the shell only exists
       after it clears. */
    await page.waitForFunction(() => !document.querySelector('.authwrap .spin'), undefined, { timeout: ROUTE_TIMEOUT_MS });
    await page.waitForFunction(
      (text) => {
        const el = document.querySelector('.screen.active .eyebrow');
        return !!el && el.textContent.includes(text);
      },
      route.text,
      { timeout: ROUTE_TIMEOUT_MS },
    );
    if (route.settle) await page.waitForSelector(route.settle, { state: 'visible', timeout: ROUTE_TIMEOUT_MS });
    /* Let labels, fonts and any post-mount fetch (the lesson's chapter) settle
       before the tree is measured. */
    await page.waitForTimeout(750);
    return { ok: true };
  } catch (e) {
    return { ok: false, detail: `did not settle: ${String(e && e.message ? e.message : e).split('\n')[0]}` };
  }
}

/** runAxe runs the tags over the settled document and reports the outcome. */
async function runAxe(page, route) {
  const summary = await page.evaluate(async (tags) => {
    const results = await window.axe.run(document, {
      runOnly: { type: 'tag', values: tags },
      resultTypes: ['violations'],
    });
    return results.violations.map((v) => ({
      id: v.id,
      impact: v.impact,
      help: v.help,
      helpUrl: v.helpUrl,
      nodes: v.nodes.map((n) => ({ target: n.target.join(' '), html: n.html, summary: n.failureSummary })),
    }));
  }, WCAG_TAGS);

  const counted = { critical: 0, serious: 0, moderate: 0, minor: 0 };
  let failed = 0;
  for (const v of summary) {
    const impact = v.impact ?? 'minor';
    counted[impact] = (counted[impact] ?? 0) + 1;
    const allow = allowedFor(route.name, v.id);
    if (allow) {
      report.skipped++;
      console.log(`  info   ${v.id} [${impact}] allowed — ${allow.reason}`);
      continue;
    }
    if (FAIL_IMPACTS.has(impact)) {
      failed++;
      for (const node of v.nodes) {
        report.check(`no ${v.id} [${impact}] at ${node.target || '(document)'}`, false, `${v.help}\n         ${node.html}\n         ${v.helpUrl}`);
      }
    } else {
      console.log(`  info   ${v.id} [${impact}] ${v.nodes.length} node(s) — informational`);
    }
  }

  if (failed === 0) {
    report.check('no critical or serious violations', true);
  }
  console.log(
    `  info   ${summary.length} violation(s): ${counted.critical} critical, ${counted.serious} serious, ${counted.moderate} moderate, ${counted.minor} minor`,
  );
  report.record(
    route.name,
    route.hash,
    String(summary.length),
    `${counted.critical}/${counted.serious}`,
  );
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
