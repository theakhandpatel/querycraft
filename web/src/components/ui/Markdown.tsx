import { useEffect, useRef } from 'react';
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

  useEffect(() => {
    let alive = true;
    const host = ref.current;
    if (!host) return;

    const nodes = Array.from(
      host.querySelectorAll<HTMLDivElement>(`div.qc-mermaid:not([${RENDERED_ATTR}])`),
    );
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
        theme: isDark() ? 'dark' : 'default',
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
  }, [source]);

  return (
    <div
      ref={ref}
      className={'mdbody' + (className ? ' ' + className : '')}
      dangerouslySetInnerHTML={{ __html: renderMarkdown(source) }}
    />
  );
}
