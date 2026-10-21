/*
 * The static landing is the same authored page as the gateway landing, with the
 * hosted promises hidden and every internal URL relative.
 *
 * One source file is deliberate. A second landing would drift the moment the
 * library or the proof changed. The page marks hosted-only blocks with
 * `data-gateway-only` and browser-only alternatives with `data-static-only`;
 * `web/landing/index.html` hides the browser-only blocks by default, and this
 * transform sets `data-build="static"` on the document so the CSS flips them.
 *
 * The build fails when a marker or a path is missing. A silent rewrite would
 * publish a page that still says "sign in" or a link that 404s under a project
 * path, which is exactly the failure this script exists to prevent.
 */

const GITHUB_DOCS = 'https://github.com/theakhandpatel/querycraft/tree/main/docs';

const VOID_TAGS = new Set(['area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input', 'link', 'meta', 'source', 'track', 'wbr']);

function replaceAll(html, from, to, label) {
  const count = html.split(from).length - 1;
  if (count === 0) {
    throw new Error(`static landing: ${label} not found (expected ${JSON.stringify(from)})`);
  }
  return html.split(from).join(to);
}

/**
 * Remove every element marked with `attribute`, including its descendants.
 *
 * The source keeps the hosted copy beside the browser copy so one file can
 * serve both. Publishing that hidden copy would still put "sign in" and the AI
 * builder in the page source. The scanner tracks the tag it opened, so nested
 * divs inside the workspace section close at the right place.
 */
function stripElements(html, attribute) {
  const opener = new RegExp(`<([a-z][\\w-]*)\\b[^>]*\\b${attribute}\\b[^>]*>`, 'gi');
  let out = '';
  let cursor = 0;
  let match;
  while ((match = opener.exec(html)) !== null) {
    const tag = match[1].toLowerCase();
    out += html.slice(cursor, match.index);
    if (VOID_TAGS.has(tag)) {
      cursor = opener.lastIndex;
      continue;
    }
    const sameTag = new RegExp(`<\\/?${tag}\\b[^>]*>`, 'gi');
    sameTag.lastIndex = opener.lastIndex;
    let depth = 1;
    let close;
    while ((close = sameTag.exec(html)) !== null) {
      if (close[0].startsWith('</')) depth -= 1;
      else depth += 1;
      if (depth === 0) break;
    }
    if (depth !== 0) {
      throw new Error(`static landing: <${tag} ${attribute}> has no matching close tag`);
    }
    cursor = sameTag.lastIndex;
    opener.lastIndex = cursor;
  }
  out += html.slice(cursor);
  if (new RegExp(`<[^>]*\\b${attribute}\\b`).test(out)) {
    throw new Error(`static landing: a ${attribute} marker survived the strip`);
  }
  return out;
}

export function staticLanding(html) {
  if (/<html[^>]*data-build="static"/.test(html)) {
    throw new Error('static landing: the document already declares data-build="static"');
  }
  if (!html.includes('data-static-only') || !html.includes('data-gateway-only')) {
    throw new Error('static landing: the hosted/browser copy markers are missing');
  }

  let out = html;

  /* The CSS in web/landing/index.html flips on this attribute. */
  out = replaceAll(out, '<html lang="en">', '<html lang="en" data-build="static">', 'document element');

  /* Internal links and assets become relative. The static site is served from
     a project path such as /querycraft/, so a leading slash would point at the
     domain root instead of the site root. */
  out = replaceAll(out, 'href="/app', 'href="app', 'app links');
  out = replaceAll(out, 'src="/landing/assets/', 'src="landing/assets/', 'landing image sources');
  out = replaceAll(out, 'href="/landing/assets/', 'href="landing/assets/', 'landing asset links');
  out = out.split('url(/landing/assets/').join('url(landing/assets/');
  out = replaceAll(out, 'href="/"', 'href="./"', 'brand link');

  /* The API route is a gateway surface. The link is marked hosted-only and is
     not visible in the static page; point it at the public documentation so a
     copied URL is still useful. */
  out = replaceAll(out, 'href="/docs"', `href="${GITHUB_DOCS}"`, 'API route link');

  /* The hosted copy is not merely hidden in the published document. Remove it,
     so the page source, the page weight, and any text search agree that this is
     the browser sample. */
  out = stripElements(out, 'data-gateway-only');

  out = replaceAll(
    out,
    '<meta charset="utf-8">',
    '<meta charset="utf-8">\n<meta name="querycraft:build" content="static">',
    'charset meta',
  );

  for (const forbidden of ['href="/app/', 'src="/app/', 'src="/landing/', 'href="/landing/', 'href="/docs"']) {
    if (out.includes(forbidden)) {
      throw new Error(`static landing: an absolute internal URL survived: ${forbidden}`);
    }
  }

  return out;
}
