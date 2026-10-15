import { useEffect, useRef, useState } from 'react';
import { renderMarkdown } from '../../lib/markdown';

const RENDERED_ATTR = 'data-rendered';

let mermaidSeq = 0;

function isDark(): boolean {
  return document.documentElement.getAttribute('data-theme') === 'dark';
}

function showMermaidSource(host: HTMLElement, source: string): void {
  const pre = document.createElement('pre');
  pre.textContent = source;
  host.replaceChildren(pre);
  host.setAttribute(RENDERED_ATTR, '1');
}

export default function Markdown({ source, className }: { source: string; className?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  /* Mermaid paints its diagram into inline SVG attributes at render time, so a
     diagram rendered for one palette keeps that palette's label fills when the
     learner switches theme: the dark theme's label plates against the light
     theme's text measured 2.55:1, and the reverse 1.09:1 — a diagram whose
     labels cannot be read at all. Track the theme and re-render on change. */
  const [theme, setTheme] = useState<'light' | 'dark'>(() => (isDark() ? 'dark' : 'light'));

  useEffect(() => {
    const el = document.documentElement;
    const sync = () => setTheme(isDark() ? 'dark' : 'light');
    sync();
    const obs = new MutationObserver(sync);
    obs.observe(el, { attributes: true, attributeFilter: ['data-theme'] });
    return () => obs.disconnect();
  }, []);

  useEffect(() => {
    let alive = true;
    const host = ref.current;
    if (!host) return;

    /* Any node rendered under a previous theme is put back to its source-only
       state first: its `data-mermaid` attribute still carries the definition,
       so clearing the children is enough to render it again. */
    const nodes = Array.from(host.querySelectorAll<HTMLDivElement>('div.qc-mermaid'));
    for (const node of nodes) {
      if (node.hasAttribute(RENDERED_ATTR)) {
        node.removeAttribute(RENDERED_ATTR);
        node.replaceChildren();
      }
    }
    if (nodes.length === 0) return;

    const run = async (): Promise<void> => {
      let mermaid: typeof import('mermaid').default;
      try {
        mermaid = (await import('mermaid')).default;
      } catch (err) {
        if (!alive) return;
        for (const node of nodes) {
          showMermaidSource(node, node.getAttribute('data-mermaid') ?? '');
        }
        return;
      }
      if (!alive) return;

      /* Mermaid measures label text with the font it is told to use, then the
         browser renders it with the page font. When those differ the labels
         overflow the boxes it computed for them and get clipped, so hand it the
         font the diagram will actually be rendered in. */
      const pageFont = getComputedStyle(host).fontFamily || 'sans-serif';
      /* The webfonts load with `display=swap`, so on first paint the measuring
         pass would run against the fallback font and size every label too
         small. Wait for the real faces before asking Mermaid to measure. */
      try {
        await document.fonts.ready;
      } catch {
        /* Font Loading API unavailable — measure with whatever is loaded. */
      }
      if (!alive) return;
      mermaid.initialize({
        startOnLoad: false,
        securityLevel: 'strict',
        theme: theme === 'dark' ? 'dark' : 'default',
        fontFamily: pageFont,
        themeVariables: { fontFamily: pageFont, fontSize: '14px' },
      });

      for (const node of nodes) {
        if (!alive) return;
        const text = node.getAttribute('data-mermaid') ?? '';
        const id = `qc-mermaid-${(mermaidSeq++).toString(36)}`;
        try {
          const { svg } = await mermaid.render(id, text);
          if (!alive) return;
          node.innerHTML = svg;
          node.setAttribute(RENDERED_ATTR, '1');
        } catch (err) {
          document.getElementById(`d${id}`)?.remove();
          document.getElementById(id)?.remove();
          if (!alive) return;
          showMermaidSource(node, text);
        }
      }
    };

    void run();

    return () => {
      alive = false;
    };
  }, [source, theme]);

  return (
    <div
      ref={ref}
      className={'mdbody' + (className ? ' ' + className : '')}
      dangerouslySetInnerHTML={{ __html: renderMarkdown(source) }}
    />
  );
}
