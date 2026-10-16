import { useEffect, useMemo, useRef, useState } from 'react';
import Icon from './ui/Icon';
import { useStore } from '../state/store';
import { COURSES } from '../data/curriculum';
import { BUNDLED_CONTENT } from '../api/content';
import { buildIndex, groupHits, search, SEARCH_KIND_LABEL } from '../lib/search';
import type { SearchDoc, SearchKind } from '../lib/search';
import type { ContentSection } from '../types';

/* One search field for the whole shell (design §10). It searches the catalog
   plus the learner's own workbook, purely client-side in M2: the index is
   rebuilt when the content or the progress changes. `Cmd/Ctrl+K` focuses it and
   Escape closes the panel, so it is reachable without a pointer (M2 exit gate). */
export default function SearchBox() {
  const s = useStore();
  const [query, setQuery] = useState('');
  const [open, setOpen] = useState(false);
  const boxRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const index = useMemo(() => {
    /* The database copy wins where it exists; the bundled markdown is the
       fallback, so search works before/without a content row. */
    const content: Record<string, ContentSection[]> = { ...BUNDLED_CONTENT };
    Object.entries(s.content || {}).forEach(([id, sections]) => {
      if (sections && sections.length) content[id] = sections;
    });
    return buildIndex({ courses: COURSES, content, progress: s.progress });
  }, [s.content, s.progress]);

  const hits = useMemo(() => search(index, query), [index, query]);
  const groups = useMemo(() => groupHits(hits), [hits]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        inputRef.current?.focus();
        setOpen(true);
      } else if (e.key === 'Escape') {
        setOpen(false);
        inputRef.current?.blur();
      }
    };
    const onDown = (e: MouseEvent) => {
      if (boxRef.current && !boxRef.current.contains(e.target as Node)) setOpen(false);
    };
    window.addEventListener('keydown', onKey);
    window.addEventListener('mousedown', onDown);
    return () => {
      window.removeEventListener('keydown', onKey);
      window.removeEventListener('mousedown', onDown);
    };
  }, []);

  function choose(doc: SearchDoc) {
    s.openSearchHit(doc);
    setOpen(false);
    setQuery('');
    inputRef.current?.blur();
  }

  const trimmed = query.trim();

  return (
    <div className="searchbox" ref={boxRef}>
      <div className="searchfield">
        <Icon name="search" />
        <input
          ref={inputRef}
          type="search"
          value={query}
          placeholder="Search chapters, exercises, notes…"
          aria-label="Search the course and your workbook"
          aria-controls="search-results"
          onChange={(e) => {
            setQuery(e.target.value);
            setOpen(true);
          }}
          onFocus={() => setOpen(true)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && hits[0]) choose(hits[0].doc);
          }}
        />
        <span className="kbd" aria-hidden="true">Ctrl</span>
        <span className="kbd" aria-hidden="true">K</span>
      </div>
      {open && trimmed ? (
        <div className="searchresults" id="search-results">
          {hits.length === 0 ? (
            <div className="empty">
              Nothing matches “{trimmed}”. Searched chapter titles, objectives, sections, exercises, notes,
              bookmarks and saved queries.
            </div>
          ) : (
            groups.map((g: { kind: SearchKind; hits: { doc: SearchDoc }[] }) => (
              <div className="searchgroup" key={g.kind}>
                <div className="label">{SEARCH_KIND_LABEL[g.kind]}</div>
                {g.hits.map((h) => (
                  <button className="searchhit" key={h.doc.id} onClick={() => choose(h.doc)}>
                    <span className="searchhit-title">{h.doc.title}</span>
                    <span className="searchhit-meta">
                      {h.doc.courseTitle} · chapter {h.doc.lesson}
                    </span>
                  </button>
                ))}
              </div>
            ))
          )}
        </div>
      ) : null}
    </div>
  );
}
