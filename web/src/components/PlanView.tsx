import React, { useState } from 'react';
import Icon from './ui/Icon';
import {
  ESTIMATE_OFF_RATIO,
  SEQ_SCAN_LARGE_ROWS,
  type PlanBundle,
  type PlanCheck,
  type PlanNode,
  type PerfContract,
  type TimingPrecision,
} from '../api/planContract';

/**
 * The M5 plan viewer.
 *
 * Two rules shape this component:
 *
 *   - Timing numbers are never shown without saying what they are worth. When
 *     `timing_precision` is `analyze` the per-node times are labelled as
 *     measured on this machine; when it is `estimate` the UI says "planner
 *     estimate, not measured" and shows no time at all; when it is `none` no
 *     number is rendered.
 *   - The raw plan is untrusted engine output. It is disclosed as escaped text
 *     through React, never as HTML.
 *
 * The judgement controls (the seq-scan warning, the estimate flag, the
 * cost-share bar) are presentational: the verdict comes from `gradePlan`, which
 * reads only structural facts.
 */

const PRECISION_LABEL: Record<TimingPrecision, string> = {
  analyze: 'measured by EXPLAIN ANALYZE on this machine — reported, not graded',
  estimate: 'planner estimate, not measured',
  none: 'no plan',
};

/** Below this share the bar still gets a sliver, so a cheap node is visible. */
const MIN_BAR_PCT = 2;

function fmt(n: number | null | undefined): string {
  if (n === null || n === undefined) return '—';
  return Number.isInteger(n) ? String(n) : n.toFixed(2).replace(/\.?0+$/, '');
}

/** An estimate that is off by orders of magnitude, recomputed here so the flag
    survives a bundle exported by an engine that did not annotate the node. */
function estimateOff(node: PlanNode): boolean {
  if (node.warnings.includes('PLAN_ESTIMATE_OFF_BY_ORDERS_OF_MAGNITUDE')) return true;
  const est = node.estimated_rows;
  const act = node.actual_rows;
  if (est === null || act === null) return false;
  if (est > 0 && act / est >= ESTIMATE_OFF_RATIO) return true;
  return act > 0 && est / act >= ESTIMATE_OFF_RATIO;
}

function PrecisionBadge({ precision }: { precision: TimingPrecision }) {
  const tone = precision === 'analyze' ? 'measured' : precision === 'estimate' ? 'estimate' : 'none';
  return (
    <span className={`planprec ${tone}`} title={PRECISION_LABEL[precision]}>
      {precision === 'analyze' ? 'ANALYZE' : precision === 'estimate' ? 'PLANNER ESTIMATE' : 'NO PLAN'}
      <span className="planpreclabel">{PRECISION_LABEL[precision]}</span>
    </span>
  );
}

function KeyValues({ entries }: { entries: [string, string][] }) {
  if (!entries.length) return <span className="muted">none recorded</span>;
  return (
    <dl className="plankv">
      {entries.map(([k, v]) => (
        <React.Fragment key={k}>
          <dt>{k}</dt>
          <dd>{v}</dd>
        </React.Fragment>
      ))}
    </dl>
  );
}

export default function PlanView({
  bundle,
  contract,
  checks,
}: {
  bundle: PlanBundle;
  contract?: PerfContract;
  checks?: PlanCheck[];
}) {
  const [rawOpen, setRawOpen] = useState(false);

  if (bundle.status !== 'succeeded' || bundle.plan === null) {
    return (
      <div className="planview">
        <div className="qerr" role="alert">
          <span className="code">{bundle.error?.code ?? 'ENVIRONMENT_UNAVAILABLE'}</span>{' '}
          {bundle.error?.message ?? 'No plan was captured.'}
          {bundle.error?.detail ? <pre>{bundle.error.detail}</pre> : null}
        </div>
        <p className="planhint">
          <Icon name="info" />
          <span>A plan is only reported when a real engine produced one. Nothing is shown rather than a guess.</span>
        </p>
      </div>
    );
  }

  const summary = bundle.summary;
  const env = bundle.environment;
  const totalCost = summary.total_cost > 0 ? summary.total_cost : 0;
  const seqScanWarning =
    bundle.warnings.some((w) => w.code === 'PLAN_SEQ_SCAN_LARGE_TABLE') ||
    summary.seq_scans.some((s) => s.plan_rows >= SEQ_SCAN_LARGE_ROWS);

  return (
    <div className="planview">
      <div className="planhead">
        <div className="planheadmain">
          <span className="plantitle"><Icon name="flask" />Query plan</span>
          <PrecisionBadge precision={bundle.timing_precision} />
        </div>
        <div className="planmeta">
          <span className="mono">{bundle.engine}</span>
          {env.engine_version ? <span className="mono">{env.engine_version}</span> : null}
          {bundle.analyze ? <span className="pill subtle">executed</span> : <span className="pill subtle">planned only</span>}
          {/* A time is only rendered when its precision label sits beside it.
              In `estimate` mode there are no timings at all, so none is shown. */}
          {bundle.timing_precision === 'analyze' ? (
            <span className="plancaptured">
              <span className="mono">{bundle.duration_ms} ms</span>
              <span className="planpreclabel">measured on this machine, not graded</span>
            </span>
          ) : null}
        </div>
      </div>

      <div className="plancounts">
        <span className="planstat"><b>{summary.node_count}</b> nodes</span>
        <span className="planstat"><b>{fmt(summary.plan_rows)}</b> planner rows (root)</span>
        <span className="planstat"><b>{fmt(summary.total_cost)}</b> total cost</span>
        <span className="planstat"><b>{summary.indexes_used.length}</b> indexes used</span>
        <span className="planstat"><b>{summary.seq_scans.length}</b> seq scans</span>
      </div>

      {seqScanWarning ? (
        <div className="planwarn" role="alert">
          <Icon name="alert" />
          <span>
            A sequential scan reads {summary.seq_scans.filter((s) => s.plan_rows >= SEQ_SCAN_LARGE_ROWS).map((s) => s.relation).join(', ') || 'a relation'}
            {' '}over {SEQ_SCAN_LARGE_ROWS}+ planned rows. On this scale that is often the thing to fix — an index may be the point of the exercise.
          </span>
        </div>
      ) : null}

      {bundle.warnings.filter((w) => w.code !== 'PLAN_SEQ_SCAN_LARGE_TABLE').map((w, i) => (
        <div className="planwarn subtle" key={i}>
          <Icon name="info" />
          <span><span className="fbcode">{w.code}</span> {w.message}</span>
        </div>
      ))}

      <div className="plantree" role="tree" aria-label="Plan nodes, depth first">
        {summary.nodes.map((node, i) => {
          const share = totalCost > 0 && node.estimated_cost !== null ? Math.min(100, (node.estimated_cost / totalCost) * 100) : 0;
          const off = estimateOff(node);
          return (
            <div
              className={`plannode depth-${Math.min(node.depth, 6)}`}
              role="treeitem"
              aria-level={node.depth + 1}
              key={i}
              style={{ marginLeft: node.depth * 18 }}
            >
              <div className="plannoderow">
                <span className="plantype">{node.node_type}</span>
                {node.relation ? (
                  <span className="planrel mono">
                    {node.relation}
                    {node.alias && node.alias !== node.relation ? <span className="muted"> {node.alias}</span> : null}
                  </span>
                ) : null}
                {node.index ? <span className="planindex mono">{node.index}</span> : null}
                {node.join_type ? <span className="planjoin">{node.join_type}</span> : null}
                {node.parent_relationship ? <span className="planrel muted">{node.parent_relationship}</span> : null}
                {off ? <span className="planflag" title="The planner's estimate is off by 100x or more">estimate off &gt;100x</span> : null}
              </div>

              <div className="planbars">
                <span className="planbar" aria-hidden="true">
                  <span className="planbarfill" style={{ width: `${Math.max(share, MIN_BAR_PCT)}%` }} />
                </span>
                <span className="planshare mono">{share.toFixed(1)}% of cost</span>
              </div>

              <div className="plannums">
                <span className="plannum"><span className="plannumk">est. rows</span> <b>{fmt(node.estimated_rows)}</b></span>
                <span className="plannum"><span className="plannumk">est. cost</span> <b>{fmt(node.estimated_cost)}</b></span>
                {bundle.timing_precision === 'analyze' ? (
                  <>
                    <span className="plannum"><span className="plannumk">actual rows</span> <b>{fmt(node.actual_rows)}</b></span>
                    {/* Every timing sits beside its precision label. */}
                    <span className="plannum plantime">
                      <span className="plannumk">actual time</span> <b>{fmt(node.actual_time_ms)} ms</b>
                      <span className="planpreclabel">measured, not graded</span>
                    </span>
                    <span className="plannum"><span className="plannumk">loops</span> <b>{fmt(node.loops)}</b></span>
                    <span className="plannum"><span className="plannumk">buffers read</span> <b>{fmt(node.buffers_read)}</b></span>
                  </>
                ) : null}
              </div>

              {node.index_condition ? <div className="planexpr mono"><span className="planexprk">Index Cond</span> {node.index_condition}</div> : null}
              {node.filter ? <div className="planexpr mono"><span className="planexprk">Filter</span> {node.filter}</div> : null}
              {node.sort_key && node.sort_key.length ? (
                <div className="planexpr mono"><span className="planexprk">Sort Key</span> {node.sort_key.join(', ')}</div>
              ) : null}
            </div>
          );
        })}
      </div>

      {checks && checks.length ? (
        <div className="planchecks">
          <h3>Against the exercise contract</h3>
          <ul className="checklist">
            {checks.map((c, i) => (
              <li key={i}>
                <span className={`mk ${c.ok ? 'ok' : 'no'}`}>{c.ok ? '✓' : '✕'}</span>
                <span>
                  {c.label}
                  {!c.ok ? <span className="fbcode"> {c.code}</span> : null}
                </span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {contract ? (
        <div className="plancontract">
          <h3>This exercise asks for</h3>
          <ul className="plancontractlist">
            {(contract.require_nodes ?? []).map((n, i) => (
              <li key={`r${i}`}>a <span className="mono">{n.node_type}</span>{n.relation ? <> on <span className="mono">{n.relation}</span></> : null}</li>
            ))}
            {(contract.forbid_nodes ?? []).map((n, i) => (
              <li key={`f${i}`}>no <span className="mono">{n.node_type}</span> on <span className="mono">{n.relation}</span>
                {n.above_plan_rows ? <> above {n.above_plan_rows} est. rows</> : null}</li>
            ))}
            {(contract.require_indexes ?? []).map((idx, i) => (
              <li key={`i${i}`}>the index <span className="mono">{idx}</span></li>
            ))}
            {contract.max_plan_rows != null ? <li>at most {contract.max_plan_rows} planner-estimated rows</li> : null}
            {contract.min_index_scans != null ? <li>at least {contract.min_index_scans} index scan{contract.min_index_scans === 1 ? '' : 's'}</li> : null}
          </ul>
          <p className="muted planmark">
            The verdict reads node types, relations, indexes and the planner's row estimate. No timing is ever graded.
          </p>
        </div>
      ) : null}

      <details className="planenv" open>
        <summary><Icon name="database" />Environment</summary>
        <div className="planenvbody">
          <div className="planenvgrid">
            <div><span className="planenvk">engine</span><span className="mono">{env.engine}</span></div>
            <div><span className="planenvk">version</span><span className="mono">{env.engine_version || 'unknown'}</span></div>
            <div><span className="planenvk">scale factor</span><span className="mono">{env.scale_factor}</span></div>
            {env.dataset ? <div><span className="planenvk">dataset</span><span className="mono">{env.dataset}</span></div> : null}
          </div>
          <h4>Row counts</h4>
          <KeyValues entries={Object.entries(env.row_counts).map(([k, v]) => [k, String(v)])} />
          <h4>Indexes</h4>
          {env.indexes.length ? (
            <ul className="planidx">
              {env.indexes.map((idx) => (
                <li key={`${idx.relation}.${idx.name}`}>
                  <span className="mono">{idx.name}</span> <span className="muted">on {idx.relation}</span>
                  {summary.indexes_used.includes(idx.name) ? <span className="planused">used</span> : null}
                </li>
              ))}
            </ul>
          ) : (
            <span className="muted">none recorded</span>
          )}
          <h4>Planner settings <span className="muted">(reported read-only)</span></h4>
          <KeyValues entries={Object.entries(env.settings).map(([k, v]) => [k, v])} />
        </div>
      </details>

      <div className="planraw">
        <button className="small ghost" onClick={() => setRawOpen((v) => !v)} aria-expanded={rawOpen}>
          <Icon name={rawOpen ? 'chevron-down' : 'chevron-right'} />
          {rawOpen ? 'Hide raw plan' : 'Show raw plan'}
        </button>
        <span className="muted">the engine's own JSON, shown as text — it is untrusted and never graded</span>
        {rawOpen ? <pre className="planrawjson">{JSON.stringify(bundle.plan, null, 2)}</pre> : null}
      </div>
    </div>
  );
}
