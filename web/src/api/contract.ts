import { Effect, Schema } from 'effect';
import { QUERY_ERROR_CODES } from '../types';
import type { AuthSession, Evaluation, QueryResult, User } from '../types';

/* Effect Schema decoders for the JSON the Go gateway returns. These mirror
   contracts/query-result.schema.json and contracts/evaluation.schema.json, so a
   malformed gateway response fails decoding instead of silently corrupting the
   UI. */

const ColumnSchema = Schema.Struct({ name: Schema.String, type: Schema.String });

const QueryErrorSchema = Schema.Struct({
  /* Literals, not String: the gateway's code must be one the contract names,
     or the response fails decoding instead of reaching the UI unvalidated. */
  code: Schema.Literals(QUERY_ERROR_CODES),
  message: Schema.String,
  detail: Schema.optional(Schema.NullOr(Schema.String)),
});

const QueryResultSchema = Schema.Struct({
  status: Schema.Literals(['succeeded', 'failed']),
  columns: Schema.Array(ColumnSchema),
  rows: Schema.Array(Schema.Array(Schema.Unknown)),
  row_count: Schema.Number,
  truncated: Schema.Boolean,
  duration_ms: Schema.Number,
  error: Schema.NullOr(QueryErrorSchema),
  request_id: Schema.optional(Schema.NullOr(Schema.String)),
  engine: Schema.optional(Schema.NullOr(Schema.String)),
  dialect: Schema.Literals(['postgresql', 'sqlite']),
});

const CheckResultSchema = Schema.Struct({
  label: Schema.String,
  code: Schema.String,
  ok: Schema.Boolean,
});

const EvaluationSchema = Schema.Struct({
  qr: QueryResultSchema,
  checks: Schema.Array(CheckResultSchema),
  passed: Schema.Boolean,
  /* Milliseconds as a string, matching contracts/evaluation.schema.json and what
     both the gateway and the browser adapters now emit. The union that was here
     existed only because the adapters disagreed. */
  duration_ms: Schema.String,
});

export const decodeQueryResult = (u: unknown): Effect.Effect<QueryResult, Schema.SchemaError> =>
  Schema.decodeUnknownEffect(QueryResultSchema)(u).pipe(Effect.map((r) => r as unknown as QueryResult));

export const decodeEvaluation = (u: unknown): Effect.Effect<Evaluation, Schema.SchemaError> =>
  Schema.decodeUnknownEffect(EvaluationSchema)(u).pipe(Effect.map((r) => r as unknown as Evaluation));

/* Auth payloads. Signup/login return the account plus a bearer token; /auth/me
   returns the account alone. The token is the only credential the client sends;
   progress and events are owner-scoped server-side, so no user id ever travels. */

const UserSchema = Schema.Struct({
  id: Schema.String,
  email: Schema.String,
  name: Schema.String,
  role: Schema.Literals(['admin', 'learner']),
  createdAt: Schema.String,
});

const AuthSessionSchema = Schema.Struct({
  ok: Schema.Boolean,
  user: UserSchema,
  token: Schema.String,
  expiresAt: Schema.String,
});

const AuthUserSchema = Schema.Struct({
  ok: Schema.Boolean,
  user: UserSchema,
});

export const decodeAuthSession = (u: unknown): Effect.Effect<AuthSession, Schema.SchemaError> =>
  Schema.decodeUnknownEffect(AuthSessionSchema)(u).pipe(
    Effect.map((r): AuthSession => ({ user: r.user as User, token: r.token, expiresAt: r.expiresAt })),
  );

export const decodeUser = (u: unknown): Effect.Effect<User, Schema.SchemaError> =>
  Schema.decodeUnknownEffect(AuthUserSchema)(u).pipe(Effect.map((r) => r.user as User));
