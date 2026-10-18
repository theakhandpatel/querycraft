import React from 'react';

const SYMBOLS = {
  'i-terminal': '<path d="M4 17l5-5-5-5"/><path d="M12 19h8"/>',
  'i-plus': '<path d="M12 5v14M5 12h14"/>',
  'i-palette': '<path d="M12 21a9 9 0 1 1 9-9c0 2-1.5 3-3 3h-1.5a1.5 1.5 0 0 0-1.2 2.4c.4.5.7 1 .7 1.6 0 1.1-.9 2-2 2z"/><circle cx="7.5" cy="10.5" r=".9"/><circle cx="12" cy="7.5" r=".9"/><circle cx="16.5" cy="10.5" r=".9"/>',
  'i-menu': '<path d="M4 7h16M4 12h16M4 17h16"/>',
  'i-logout': '<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><path d="M16 17l5-5-5-5M21 12H9"/>',
  'i-list': '<circle cx="4.5" cy="6" r="1"/><circle cx="4.5" cy="12" r="1"/><circle cx="4.5" cy="18" r="1"/><path d="M9 6h11M9 12h11M9 18h11"/>',
  'i-search': '<circle cx="11" cy="11" r="7"/><path d="M20 20l-3.4-3.4"/>',
  'i-layers': '<path d="M12 3l9 5-9 5-9-5 9-5z"/><path d="M3 13l9 5 9-5"/>',
  'i-clock': '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  'i-settings': '<circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M19.1 4.9L17 7M7 17l-2.1 2.1"/>',
  'i-database': '<ellipse cx="12" cy="6" rx="8" ry="3"/><path d="M4 6v6c0 1.66 3.58 3 8 3s8-1.34 8-3V6"/><path d="M4 12v6c0 1.66 3.58 3 8 3s8-1.34 8-3v-6"/>',
  'i-book': '<path d="M4 5.5A2.5 2.5 0 0 1 6.5 3H19v15H6.5A2.5 2.5 0 0 0 4 20.5z"/><path d="M4 20.5A2.5 2.5 0 0 1 6.5 18H19"/>',
  'i-arrow-right': '<path d="M5 12h14M13 6l6 6-6 6"/>',
  'i-arrow-left': '<path d="M19 12H5M11 18l-6-6 6-6"/>',
  'i-arrow-up': '<path d="M12 19V5M6 11l6-6 6 6"/>',
  'i-arrow-down': '<path d="M12 5v14M6 13l6 6 6-6"/>',
  'i-chevron-down': '<path d="M6 9l6 6 6-6"/>',
  'i-chevron-right': '<path d="M9 6l6 6-6 6"/>',
  'i-play': '<path d="M8 5l11 7-11 7z"/>',
  'i-check': '<path d="M4 12.5l5 5L20 6.5"/>',
  'i-x': '<path d="M6 6l12 12M18 6L6 18"/>',
  'i-edit': '<path d="M4 20h4l10-10-4-4L4 16v4z"/><path d="M13.5 6.5l4 4"/>',
  'i-eye': '<path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7-10-7-10-7z"/><circle cx="12" cy="12" r="3"/>',
  'i-info': '<circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8h.01"/>',
  'i-flask': '<path d="M9 3h6M10 3v6.5L5.5 17A2 2 0 0 0 7.3 20h9.4a2 2 0 0 0 1.8-3L14 9.5V3"/><path d="M7.5 14h9"/>',
  'i-target': '<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="5"/><circle cx="12" cy="12" r="1.5"/>',
  'i-bookmark': '<path d="M6 4h12v17l-6-4-6 4z"/>',
  'i-note': '<path d="M5 3h9l5 5v13H5z"/><path d="M14 3v5h5M8 13h8M8 17h5"/>',
  'i-refresh': '<path d="M3 12a9 9 0 0 1 15-6.7L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-15 6.7L3 16"/><path d="M3 21v-5h5"/>',
  'i-alert': '<path d="M12 3l9 16H3z"/><path d="M12 10v4M12 17h.01"/>',
  'i-table': '<rect x="3" y="4" width="18" height="16" rx="1.5"/><path d="M3 10h18M9 4v16"/>',
  'i-sparkles': '<path d="M12 3l1.6 4.4L18 9l-4.4 1.6L12 15l-1.6-4.4L6 9l4.4-1.6z"/><path d="M18 15l.8 2.2L21 18l-2.2.8L18 21l-.8-2.2L15 18l2.2-.8z"/>',
  'i-graduation': '<path d="M12 4l10 5-10 5L2 9z"/><path d="M6 11v5c0 1 2.7 2.5 6 2.5s6-1.5 6-2.5v-5"/>',
  'i-shield': '<path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z"/><path d="M9 12l2 2 4-4"/>',
  'i-download': '<path d="M12 3v12"/><path d="M7 10l5 5 5-5"/><path d="M4 20h16"/>',
  'i-upload': '<path d="M12 16V4"/><path d="M7 9l5-5 5 5"/><path d="M4 20h16"/>',
};

export function IconSprite() {
  return (
    <svg width="0" height="0" style={{ position: 'absolute' }} aria-hidden="true" focusable="false">
      {Object.entries(SYMBOLS).map(([id, body]) => (
        <symbol key={id} id={id} viewBox="0 0 24 24" dangerouslySetInnerHTML={{ __html: body }} />
      ))}
    </svg>
  );
}

export default function Icon({ name, className = 'ico', style }: { name: string; className?: string; style?: React.CSSProperties }) {
  return (
    <svg className={className} aria-hidden="true" style={style}>
      <use href={`#i-${name}`} />
    </svg>
  );
}
