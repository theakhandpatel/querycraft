import { defineConfig } from 'oxlint'

/*
 * The project has no ESLint. Oxlint is the linter because it starts in
 * milliseconds, and the rules that earn their place here are the anti-slop
 * rules vendored under tools/oxlint/anti-slop.
 *
 * Anti-slop rejects low-evidence TypeScript: an assertion chain that discards
 * the compiler's evidence, an `unknown` in a function contract, `typeof` where a
 * decoder belongs. Those patterns compile, pass `tsc`, and then fail on real
 * data, which is why they are worth catching before review.
 *
 * Severities are triaged, not defaulted. A rule that the codebase satisfies
 * today is `error`, so it cannot regress. A rule that reports a small list of
 * real findings is `warn`. A rule whose findings are a known, tracked gap, or
 * that fights the codebase, is `off`, and the number and the reason are here.
 *
 * Run it with `npm run lint`, or `make lint` from the repository root.
 * The rule counts were measured on 2026-10-19 and are in progress.md.
 */
export default defineConfig({
  ignorePatterns: [
    /* Agent tooling, never product code. The repository ignores these too. */
    '.agents/**',
    '.codex/**',
    '.gemini/**',
    '.opencode/**',
    /* The vendored rules are read, not linted. */
    'tools/oxlint/anti-slop/**',
    'dist/**',
  ],
  jsPlugins: [
    { name: 'anti-slop', specifier: './tools/oxlint/anti-slop/index.ts' },
    {
      name: 'anti-slop-effect',
      specifier: './tools/oxlint/anti-slop/effect/index.ts',
    },
  ],
  rules: {
    /* Error: true today, and a regression if that changes. */
    'oxc/no-accumulating-spread': 'error',
    'anti-slop/no-conditional-empty-object-spread': 'error',
    'anti-slop/no-widen-then-assert': 'error',
    'anti-slop/no-reflect-get': 'error',
    'anti-slop/no-reflect-apply': 'error',
    'anti-slop/no-module-mocking': 'error',
    'anti-slop/no-reduce-accumulator-copy': 'error',
    'anti-slop/no-object-parameters': 'error',
    'anti-slop/no-unknown-type-aliases': 'error',
    'anti-slop-effect/no-manual-effect-error-tag': 'error',
    'anti-slop-effect/no-manual-tag-comparison': 'error',
    'anti-slop-effect/no-manual-tagged-construction': 'error',
    'anti-slop-effect/no-service-constructor-imports': 'error',

    /* Warn: a short list, each entry worth reading. */
    'anti-slop/no-chained-type-assertions': 'warn',
    'anti-slop/no-unknown-returns': 'warn',
    'anti-slop/no-array-filter-map': 'warn',

    /*
     * The four contract rules, re-enabled by 16-iii once the last hand-written
     * decoders moved to `Schema`, and burned down to their boundary by 16a.
     *
     * Measured on 2026-10-19: zero warnings and no errors in all. The last 44
     * were `no-unknown-parameters` in src/api/ai.ts, on decoder entry points
     * typed `unknown` while the transport hands over `Schema.Json | null`.
     * Typing `AiPerformResult.body` like `WireResult`, then every decoder
     * parameter as `Schema.Json`, closed them with no behaviour change:
     * `decodeUnknownOption` already takes an arbitrary input. The earlier note
     * that these were a known false positive was wrong, and this run retired
     * it.
     *
     * no-runtime-typeof allows `allowInTypeGuards`: `typeof` inside a predicate
     * (isRecord) establishes the contract its caller then relies on, which is the
     * opposite of the narrowing the rule is for.
     *
     * Off, with the measured count and the reason.
     *
     * require-readable-spacing (1292 sites) is a formatter. The project has no
     * formatter, so switching it on would rewrite the source, not fix it.
     *
     * require-safety-comment-for-type-assertion (120 sites) wants a `SAFETY:`
     * comment before every `as`. The project has not adopted that policy. The
     * assertions that matter are covered by no-chained-type-assertions.
     *
     * no-shape-in-symbol-names (31 sites) rejects the word "shape" in a local
     * type name. Effect calls the type a `Context.Service` resolves to a
     * `Shape`, and ExecutionServiceShape follows that idiom. The framework wins.
     *
     * prefer-effect-match (21 sites) asks for Effect's `Match` where the code
     * branches three ways on one value. Every site is a two- or three-branch map
     * over a closed union, and two of them are pure helpers in presentational
     * files that do not import Effect. The current form is the clearer one.
     */
    'anti-slop/no-runtime-typeof': ['warn', { allowInTypeGuards: true }],
    'anti-slop/no-unknown-parameters': 'warn',
    'anti-slop/no-unsafe-dictionary-type': 'warn',
    'anti-slop/no-known-value-widening': 'warn',
    'anti-slop/require-readable-spacing': 'off',
    'anti-slop/require-safety-comment-for-type-assertion': 'off',
    'anti-slop/no-shape-in-symbol-names': 'off',
    'anti-slop-effect/prefer-effect-match': 'off',
  },
})
