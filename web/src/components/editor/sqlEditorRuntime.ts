/*
 * The CodeMirror runtime. This is the only module that imports CodeMirror, and
 * SqlEditor imports it dynamically, so the editor is a separate chunk.
 *
 * The theme is written in the app's CSS variables. Switching a theme changes
 * the variables on the document; the editor is not rebuilt. The syntax colors
 * reuse tokens the contrast audit already checks.
 */
import { Compartment, EditorState, Prec } from '@codemirror/state';
import { EditorView, drawSelection, dropCursor, highlightActiveLineGutter, keymap, lineNumbers } from '@codemirror/view';
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands';
import { autocompletion, closeBrackets, closeBracketsKeymap, completionKeymap } from '@codemirror/autocomplete';
import { bracketMatching, HighlightStyle, indentOnInput, syntaxHighlighting } from '@codemirror/language';
import { PostgreSQL, SQLite, sql } from '@codemirror/lang-sql';
import { tags } from '@lezer/highlight';
import type { Dialect } from '../../types';
import type { SqlSchema } from '../../lib/sqlSchema';

export interface SqlEditorConfig {
  dialect: Dialect;
  schema: SqlSchema | null;
  ariaLabel: string;
  describedBy?: string;
}

export interface SqlEditorOptions extends SqlEditorConfig {
  value: string;
  onChange: (value: string) => void;
  onRun: () => void;
}

export interface SqlEditorHandle {
  setValue: (value: string) => void;
  setConfig: (config: SqlEditorConfig) => void;
  focus: () => void;
  destroy: () => void;
}

const editorTheme = EditorView.theme({
  '&': {
    color: 'var(--code-text)',
    backgroundColor: 'var(--code-bg)',
    fontFamily: 'var(--mono)',
    fontSize: '13px',
    lineHeight: '1.75',
  },
  '&.cm-focused': {
    outline: 'none',
  },
  '.cm-scroller': {
    fontFamily: 'var(--mono)',
    lineHeight: '1.75',
    overflow: 'auto',
  },
  '.cm-content': {
    caretColor: 'var(--accent)',
    padding: '14px 14px 14px 0',
  },
  '.cm-line': {
    padding: '0 0 0 4px',
  },
  '.cm-gutters': {
    backgroundColor: 'var(--surface-2)',
    color: 'var(--text-3)',
    border: 'none',
    borderRight: '1px solid var(--code-line)',
    paddingLeft: '4px',
  },
  '.cm-lineNumbers .cm-gutterElement': {
    minWidth: '30px',
    padding: '0 8px 0 4px',
  },
  '.cm-activeLineGutter': {
    backgroundColor: 'var(--accent-soft)',
    color: 'var(--accent-ink)',
  },
  '.cm-selectionBackground, .cm-content ::selection': {
    backgroundColor: 'var(--accent-soft) !important',
  },
  '.cm-cursor, .cm-dropCursor': {
    borderLeftColor: 'var(--accent)',
  },
  '.cm-tooltip': {
    backgroundColor: 'var(--surface)',
    border: '1px solid var(--line)',
    color: 'var(--text)',
  },
  '.cm-tooltip-autocomplete > ul > li[aria-selected]': {
    backgroundColor: 'var(--accent-soft)',
    color: 'var(--accent-ink)',
  },
  '.cm-completionLabel': {
    fontFamily: 'var(--mono)',
  },
});

const sqlHighlight = HighlightStyle.define([
  { tag: tags.keyword, color: 'var(--accent-ink)', fontWeight: '500' },
  { tag: [tags.string, tags.special(tags.string)], color: 'var(--green-ink)' },
  { tag: [tags.number, tags.bool, tags.null], color: 'var(--amber-ink)' },
  { tag: tags.comment, color: 'var(--text-3)', fontStyle: 'italic' },
  { tag: [tags.operator, tags.punctuation], color: 'var(--text-2)' },
  { tag: [tags.typeName, tags.standard(tags.name)], color: 'var(--accent-ink)' },
  { tag: tags.variableName, color: 'var(--code-text)' },
]);

function languageFor(config: SqlEditorConfig) {
  return sql({
    dialect: config.dialect === 'sqlite' ? SQLite : PostgreSQL,
    schema: config.schema ?? undefined,
    upperCaseKeywords: false,
  });
}

type EditorContentAttributes = {
  'aria-label': string;
  'aria-multiline': string;
  spellcheck: string;
  autocapitalize: string;
  autocomplete: string;
  'aria-describedby'?: string;
};

function contentAttributes(config: SqlEditorConfig): EditorContentAttributes {
  const attributes: EditorContentAttributes = {
    'aria-label': config.ariaLabel,
    'aria-multiline': 'true',
    spellcheck: 'false',
    autocapitalize: 'off',
    autocomplete: 'off',
  };
  if (config.describedBy) attributes['aria-describedby'] = config.describedBy;
  return attributes;
}

export function createSqlEditor(parent: HTMLElement, options: SqlEditorOptions): SqlEditorHandle {
  const language = new Compartment();
  const attributes = new Compartment();
  let destroyed = false;

  const state = EditorState.create({
    doc: options.value,
    extensions: [
      lineNumbers(),
      highlightActiveLineGutter(),
      history(),
      drawSelection(),
      dropCursor(),
      EditorView.lineWrapping,
      indentOnInput(),
      bracketMatching(),
      closeBrackets(),
      autocompletion({ activateOnTyping: true }),
      keymap.of([...closeBracketsKeymap, ...defaultKeymap, ...historyKeymap, ...completionKeymap]),
      Prec.highest(
        keymap.of([
          {
            key: 'Mod-Enter',
            run: () => {
              options.onRun();
              return true;
            },
          },
        ]),
      ),
      language.of(languageFor(options)),
      attributes.of(EditorView.contentAttributes.of(contentAttributes(options))),
      editorTheme,
      syntaxHighlighting(sqlHighlight),
      EditorView.updateListener.of((update) => {
        if (update.docChanged) options.onChange(update.state.doc.toString());
      }),
    ],
  });

  const view = new EditorView({ state, parent });

  return {
    setValue(next) {
      if (destroyed) return;
      const current = view.state.doc.toString();
      if (next === current) return;
      view.dispatch({
        changes: { from: 0, to: view.state.doc.length, insert: next },
        selection: { anchor: next.length },
      });
    },
    setConfig(config) {
      if (destroyed) return;
      view.dispatch({
        effects: [
          language.reconfigure(languageFor(config)),
          attributes.reconfigure(EditorView.contentAttributes.of(contentAttributes(config))),
        ],
      });
    },
    focus() {
      view.focus();
    },
    destroy() {
      if (destroyed) return;
      destroyed = true;
      view.destroy();
    },
  };
}
