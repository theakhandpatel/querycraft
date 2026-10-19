import DOMPurify from 'dompurify';
import { Marked, Renderer, type Tokens } from 'marked';

const MERMAID_LANG = 'mermaid';
const MERMAID_CLASS = 'qc-mermaid';

const HTML_ESCAPES = new Map<string, string>([
  ['&', '&amp;'],
  ['<', '&lt;'],
  ['>', '&gt;'],
  ['"', '&quot;'],
  ["'", '&#39;'],
]);

export function escapeHtmlAttribute(value: string): string {
  return value.replace(/[&<>"']/g, (ch) => HTML_ESCAPES.get(ch) ?? ch);
}

function isMermaidFence(lang: string | undefined): boolean {
  return (lang ?? '').trim().toLowerCase() === MERMAID_LANG;
}

const marked = new Marked({ gfm: true, async: false });

marked.use({
  renderer: {
    code(token: Tokens.Code): string {
      if (isMermaidFence(token.lang)) {
        return `<div class="${MERMAID_CLASS}" data-mermaid="${escapeHtmlAttribute(token.text)}"></div>\n`;
      }
      return Renderer.prototype.code.call(this, token);
    },
  },
});

const SANITIZE_CONFIG = {
  ADD_TAGS: ['svg'],
  ADD_ATTR: ['data-mermaid', 'data-rendered', 'target', 'rel'],
  ALLOW_DATA_ATTR: true,
  SAFE_FOR_XML: false,
};

export function renderMarkdown(md: string): string {
  const html = marked.parse(md, { async: false });
  if (!DOMPurify.isSupported) {
    return html;
  }
  return DOMPurify.sanitize(html, SANITIZE_CONFIG);
}
