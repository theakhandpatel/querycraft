import { Effect, Schema } from 'effect';
import type { AuthSession, Evaluation, QueryResult, User } from '../types';

/* Effect Schema decoders for the JSON the Go gateway returns. These mirror
   contracts/query-result.schema.json and contracts/evaluation.schema.json, so a
   malformed gateway response fails decoding instead of silently corrupting the
   UI. */

export const ColumnSchema = Schema.Struct({ name: Schema.String, type: Schema.String });

export const QueryErrorSchema = Schema.Struct({
  code: Schema.String,
  message: Schema.String,
  detail: Schema.optional(Schema.NullOr(Schema.String)),
});

export const QueryResultSchema = Schema.Struct({
  status: Schema.Literals(['succeeded', 'failed']),
  columns: Schema.Array(ColumnSchema),
  rows: Schema.Array(Schema.Array(Schema.Unknown)),
  row_count: Schema.Number,
  truncated: Schema.Boolean,
  duration_ms: Schema.Number,
  error: Schema.NullOr(QueryErrorSchema),
  request_id: Schema.optional(Schema.NullOr(Schema.String)),
  engine: Schema.optional(Schema.NullOr(Schema.String)),
});

export const CheckResultSchema = Schema.Struct({
  label: Schema.String,
  code: Schema.String,
  ok: Schema.Boolean,
});

export const EvaluationSchema = Schema.Struct({
  qr: QueryResultSchema,
  checks: Schema.Array(CheckResultSchema),
  passed: Schema.Boolean,
  duration_ms: Schema.Union([Schema.Number, Schema.String]),
});

export const decodeQueryResult = (u: unknown): Effect.Effect<QueryResult, Schema.SchemaError> =>
  Schema.decodeUnknownEffect(QueryResultSchema)(u).pipe(Effect.map((r) => r as unknown as QueryResult));

export const decodeEvaluation = (u: unknown): Effect.Effect<Evaluation, Schema.SchemaError> =>
  Schema.decodeUnknownEffect(EvaluationSchema)(u).pipe(Effect.map((r) => r as unknown as Evaluation));

/* Auth payloads. Signup/login return the account plus a bearer token; /auth/me
   returns the account alone. The token is the only credential the client sends;
   progress and events are owner-scoped server-side, so no user id ever travels. */

export const UserSchema = Schema.Struct({
  id: Schema.String,
  email: Schema.String,
  name: Schema.String,
  role: Schema.Literals(['admin', 'learner']),
  createdAt: Schema.String,
});

export const AuthSessionSchema = Schema.Struct({
  ok: Schema.Boolean,
  user: UserSchema,
  token: Schema.String,
  expiresAt: Schema.String,
});

export const AuthUserSchema = Schema.Struct({
  ok: Schema.Boolean,
  user: UserSchema,
});

export const decodeAuthSession = (u: unknown): Effect.Effect<AuthSession, Schema.SchemaError> =>
  Schema.decodeUnknownEffect(AuthSessionSchema)(u).pipe(
    Effect.map((r): AuthSession => ({ user: r.user as User, token: r.token, expiresAt: r.expiresAt })),
  );

export const decodeUser = (u: unknown): Effect.Effect<User, Schema.SchemaError> =>
  Schema.decodeUnknownEffect(AuthUserSchema)(u).pipe(Effect.map((r) => r.user as User));
