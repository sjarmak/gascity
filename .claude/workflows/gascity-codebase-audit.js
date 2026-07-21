export const meta = {
  name: 'gascity-codebase-audit',
  description: 'Durable, repeatable codebase audit primitive (mol-codebase-audit): parallel subsystem + cross-cutting auditors over a repo -> adversarial verify of high/critical -> one prioritized maintainer report (ship-now vs needs-decision). Parameterized by repo + subsystem list via args. ZFC-clean: auditor MODELS judge; the harness only fans out + collects.',
  phases: [
    { title: 'Audit', detail: 'parallel auditors over subsystems + cross-cutting (CI/observability/guardrails/regression) + approved PRs' },
    { title: 'Verify', detail: 'adversarial verification of high/critical findings' },
    { title: 'Synthesize', detail: 'dedup + prioritize into one maintainer-grade report (+ Slack summary)' },
  ],
}

// Parameterized (mayor gc-431375): args = { repo, units?: [{label, scope}], cross?: [...] }.
// Defaults target the gascity Go monorepo. Adapted from the gold-standard 2026-06-28
// ultracode run (33 agents / 99 findings / 14-15 verified).
const REPO = (args && args.repo) || '/home/ds/gascity-main'

const CRITERIA = `Evaluate against these standards. CITE file:line for EVERY finding. Report only real, specific, defensible issues — no nitpicks, no false positives.
- SLOP/EROSION (lower=better): narration/echo comments; lonely interfaces / single-impl abstractions; single-entry registries; factory-returns-constant; delegation-only classes; premature caching/parallelism/lazy-init; defensive over-handling (handling excluded cases, null-coalescing on guaranteed values, redundant null checks, defensive cloning); hidden behavior (silent resource creation, silent fallbacks, auto-correction without warning, implicit default params); error-obscuring (boolean success flags, sentinel-as-error, error-message destruction, retry loops hiding failures, ignored return values, silent truncation, lenient parsing); incomplete impl (scaffold remnants, stubs, placeholder/TODO standing in for in-scope work); spec deviation (unrequested features, validation theater); stringly-typed logic; hand-rolled std-lib/well-known-lib utilities.
- ZFC (AI-orchestration code ONLY — not CRUD/infra/hot paths): the app layer is plumbing; semantic classification, heuristic scoring with hardcoded thresholds, keyword/regex meaning-detection, and planning/composition must be delegated to MODELS, not coded. Allowed in code: IO, schema/structural validation, policy enforcement (budgets/limits/timeouts/sandboxing), mechanical transforms, state/lifecycle, deterministic math. Flag coded heuristics that should be model-delegated.
- ARCHITECTURE: SRP (one reason to change), DRY (rule of three — don't flag <3 occurrences), KISS, YAGNI, low coupling/high cohesion, one-directional layered deps, no swallowed errors, no placeholder code, immutability by default, input validation at boundaries, files <800 lines, functions <50 lines, nesting <4.
For EACH finding return: title, file, line, category, severity (critical|high|medium|low), dimension (slop|zfc|architecture|maintainability), fix_type (deterministic = a mechanical lint/check/guardrail could catch or prevent it | needs-decision = requires human/design judgment), recommendation (concrete).`

const FINDINGS_SCHEMA = {
  type: 'object', additionalProperties: false,
  required: ['unit', 'files_reviewed', 'findings'],
  properties: {
    unit: { type: 'string' },
    files_reviewed: { type: 'integer' },
    findings: {
      type: 'array',
      items: {
        type: 'object', additionalProperties: false,
        required: ['title', 'file', 'severity', 'dimension', 'fix_type', 'recommendation'],
        properties: {
          title: { type: 'string' }, file: { type: 'string' }, line: { type: 'string' },
          category: { type: 'string' },
          severity: { type: 'string', enum: ['critical', 'high', 'medium', 'low'] },
          dimension: { type: 'string', enum: ['slop', 'zfc', 'architecture', 'maintainability', 'observability', 'ci', 'regression', 'guardrail'] },
          fix_type: { type: 'string', enum: ['deterministic', 'needs-decision'] },
          recommendation: { type: 'string' },
        },
      },
    },
  },
}

const CROSS_SCHEMA = {
  type: 'object', additionalProperties: false,
  required: ['area', 'summary', 'recommendations'],
  properties: {
    area: { type: 'string' }, summary: { type: 'string' },
    recommendations: {
      type: 'array',
      items: {
        type: 'object', additionalProperties: false,
        required: ['title', 'dimension', 'impact', 'effort', 'fix_type', 'detail'],
        properties: {
          title: { type: 'string' },
          dimension: { type: 'string', enum: ['slop', 'zfc', 'architecture', 'maintainability', 'observability', 'ci', 'regression', 'guardrail'] },
          impact: { type: 'string', enum: ['high', 'medium', 'low'] },
          effort: { type: 'string', enum: ['high', 'medium', 'low'] },
          fix_type: { type: 'string', enum: ['deterministic', 'needs-decision'] },
          detail: { type: 'string' },
        },
      },
    },
  },
}

const VERDICT_SCHEMA = {
  type: 'object', additionalProperties: false,
  required: ['real', 'note'],
  properties: { real: { type: 'boolean' }, severity_adjust: { type: 'string' }, note: { type: 'string' } },
}

const REPORT_SCHEMA = {
  type: 'object', additionalProperties: false,
  required: ['overall_status', 'slack_summary', 'full_report_md'],
  properties: {
    overall_status: { type: 'string' },
    slack_summary: { type: 'string' },
    full_report_md: { type: 'string' },
  },
}

const DEFAULT_UNITS = [
  { label: 'cmd/gc:reconciler+lifecycle', scope: 'In ' + REPO + '/cmd/gc, audit the session reconciler + lifecycle: files matching session_reconciler*, session_*, reconcile*, pool_*, scale*. This is the highest-blast-radius code. Grep for the slop/error-obscuring/concurrency signatures, spot-read the hot functions.' },
  { label: 'cmd/gc:dispatch+sling+routing', scope: 'In ' + REPO + '/cmd/gc, audit dispatch/sling/routing/hook/nudge: files matching sling*, dispatch*, hook*, route*, nudge*, cmd_sling*. Focus ZFC (routing decisions) + error handling.' },
  { label: 'cmd/gc:dolt+beads+store+maintenance', scope: 'In ' + REPO + '/cmd/gc, audit dolt/beads/store/maintenance commands: files matching dolt*, beads*, bead_*, store*, maintenance*, gc_maintenance*. Focus error handling, silent fallbacks, resource lifecycle.' },
  { label: 'cmd/gc:orders+formula+convoy+misc', scope: 'In ' + REPO + '/cmd/gc, audit orders/formula/convoy/molecule + remaining command files NOT covered by reconciler/dispatch/dolt scopes (order*, formula*, convoy*, molecule*, and misc command files). Focus YAGNI/over-abstraction + stringly-typed.' },
  { label: 'cmd/gc:dashboard+api-serving+remainder', scope: 'In ' + REPO + '/cmd/gc, audit the dashboard/web + api-serving + any remaining files (dashboard*, web*, serve*, api*, and whatever is left). Note: skip generated files. Focus maintainability + slop.' },
  { label: 'internal/api', scope: 'Audit ' + REPO + '/internal/api. Distinguish generated code (skip codegen output, but flag if codegen drift is possible) from hand-written. Focus error handling, validation at boundaries, slop.' },
  { label: 'internal/runtime', scope: 'Audit ' + REPO + '/internal/runtime — the tmux/provider/runtime layer incl dialog detection. Focus ZFC (dialog/screen detection heuristics — prime ZFC + lenient-parsing candidates), concurrency, error handling.' },
  { label: 'internal/config+materialize+overlay+bootstrap', scope: 'Audit ' + REPO + '/internal/{config,materialize,overlay,bootstrap,cityinit}. Focus validation, silent auto-correction, defaults-hiding-failures, schema drift.' },
  { label: 'internal/beads+meta+molecule+convoy+sling', scope: 'Audit ' + REPO + '/internal/{beads,beadmeta,molecule,convoy,sling}. Focus error handling, state/lifecycle, stringly-typed, slop.' },
  { label: 'internal/worker+session+supervisor+process', scope: 'Audit ' + REPO + '/internal/{worker,session,sessionlog,supervisor,processgroup,processenv}. Focus concurrency/race, lifecycle, error handling, observability.' },
  { label: 'internal/doctor+formula+convergence+orders+packman', scope: 'Audit ' + REPO + '/internal/{doctor,formula,convergence,reviewquorum,orders,packman,packregistry}. Focus ZFC (convergence/reviewquorum scoring), YAGNI, slop.' },
  { label: 'internal/events+telemetry+mail+extmsg+misc', scope: 'Audit ' + REPO + '/internal/{events,telemetry,mail,extmsg,logutil,usage,pricing,reliability,docgen,fsys,dispatch}. Focus observability completeness, error handling, slop.' },
]

const DEFAULT_CROSS = [
  { label: 'CI', prompt: 'Analyze CI for the repo at ' + REPO + ': read .github/workflows/*.yml, the Makefile, and the test layout. Identify (a) BUILD-TIME improvements — redundant/overlapping jobs, missing/poor caching, shard imbalance, serial steps that could parallelize, slow test suites; (b) VALIDITY gaps — checks that run but do not actually gate merge, flaky-test patterns, missing required checks, and any known baseline setup failure (e.g. mise-config-not-trusted) that blocks local pre-push hooks (root-cause it). For each: title, dimension=ci, impact, effort, fix_type, detail with concrete file refs.' },
  { label: 'Observability', prompt: 'Audit observability across the repo at ' + REPO + ': logging, metrics, tracing, the typed event bus (internal/events), and sessionlog. Identify gaps: state-mutating actions with no event/trace, errors dropped to stderr or /dev/null, missing correlation/request IDs in async/distributed flows, log-level-hiding, inconsistent structured logging. For each: title, dimension=observability, impact, effort, fix_type, detail with file refs.' },
  { label: 'Guardrails', prompt: 'Identify DETERMINISTIC guardrails to add to the repo at ' + REPO + ' that would mechanically prevent recurring bug/slop/regression classes: static-analysis lints (custom analyzers, golangci-lint rules), CI gates, pre-commit/pre-push checks, JSON-schema/openapi/codegen drift checks, contract tests, ZFC-boundary checks. Each must be mechanical (no semantic judgment) and target a real pattern you can point to. For each: title, dimension=guardrail, impact, effort, fix_type=deterministic, detail (what it checks + where it plugs in).' },
  { label: 'Regression', prompt: 'Identify the highest regression-risk surfaces in the repo at ' + REPO + ': areas combining high blast-radius + low test coverage + high churn. Use git log for churn signals and grep for thin test coverage. For each surface: title, dimension=regression, impact, effort, fix_type, detail (the risk + the specific test/guard to add).' },
]

// An explicitly-passed array (even empty) is honored — so a CI-focused run can
// pass units:[] to skip the subsystem fan-out and run only the cross-cutting set.
const UNITS = (args && Array.isArray(args.units)) ? args.units : DEFAULT_UNITS
const CROSS = (args && Array.isArray(args.cross) && args.cross.length) ? args.cross : DEFAULT_CROSS

phase('Audit')
log('Auditing ' + UNITS.length + ' code units + ' + CROSS.length + ' cross-cutting areas + approved PRs in ' + REPO)

const auditThunks = [
  ...UNITS.map(u => () => agent(
    'You are a rigorous code auditor for the gascity Go monorepo. ' + u.scope + '\n\n' + CRITERIA +
    '\n\nUse Grep/Glob/Read/Bash to inspect efficiently — grep for signatures first, then spot-read the suspicious spots; you do NOT need to read every file. Return unit="' + u.label + '", files_reviewed (approx), and findings[]. Quality over quantity: a handful of real, file:line-cited findings beats a long list of nitpicks.',
    { label: 'audit:' + u.label, phase: 'Audit', schema: FINDINGS_SCHEMA }
  )),
  ...CROSS.map(c => () => agent(
    c.prompt + '\n\nUse Read/Grep/Glob/Bash. Be concrete and cite files. Return area="' + c.label + '", summary, recommendations[].',
    { label: 'cross:' + c.label, phase: 'Audit', schema: CROSS_SCHEMA }
  )),
  () => agent(
    'Audit the currently-open APPROVED incoming PRs on gastownhall/gascity. First enumerate them: `gh pr list --repo gastownhall/gascity --state open --json number,title,author,reviewDecision,autoMergeRequest` and also check local review records at /home/ds/gas-city/.gc/pr-pipeline/reviews/pr-*.md — an "approved" PR is one with autoMergeRequest!=null OR reviewDecision=APPROVED OR a local review record with an approve/take-the-good verdict that is still OPEN. For up to 6 such PRs, fetch the diff (`gh pr diff <N> --repo gastownhall/gascity`) and audit it against the criteria below. Goal: catch slop/criteria issues that our merge queue would let through.\n\n' + CRITERIA + '\n\nReturn unit="approved-prs", files_reviewed (= # PRs audited), findings[] where each finding\'s file field is prefixed with the PR number, e.g. "PR#3670:internal/x/y.go". If there are no open approved PRs, return an empty findings list.',
    { label: 'audit:approved-prs', phase: 'Audit', schema: FINDINGS_SCHEMA }
  ),
]

const auditResults = (await parallel(auditThunks)).filter(Boolean)

const allFindings = []
const crossRecs = []
for (const r of auditResults) {
  if (Array.isArray(r.findings)) for (const f of r.findings) allFindings.push({ ...f, unit: r.unit || 'unknown' })
  if (Array.isArray(r.recommendations)) for (const x of r.recommendations) crossRecs.push({ ...x, area: r.area || 'unknown' })
}
log('Audit done: ' + allFindings.length + ' findings + ' + crossRecs.length + ' cross-cutting recs')

phase('Verify')
const toVerify = allFindings.filter(f => f.severity === 'critical' || f.severity === 'high')
log('Adversarially verifying ' + toVerify.length + ' high/critical findings')
const verified = await parallel(toVerify.map((f) => () =>
  agent(
    'Adversarially verify this code-audit finding against the actual code at ' + REPO + '. Default to real=false if you cannot confirm it concretely — we want zero false positives in the final report.\n\nFinding: ' + JSON.stringify(f) + '\n\nOpen the cited file:line, confirm the issue actually exists as described and matters (not a misread, not already-handled elsewhere, not out-of-scope per ZFC i.e. don\'t flag CRUD/infra/hot-path as ZFC violations). Return real (bool), severity_adjust (optional corrected severity), note (one line).',
    { label: 'verify:' + (f.file || '?').slice(0, 40), phase: 'Verify', schema: VERDICT_SCHEMA }
  ).then(v => ({ ...f, verdict: v })).catch(() => ({ ...f, verdict: { real: true, note: 'verify-errored-kept-conservatively' } }))
))
const confirmedHigh = verified.filter(Boolean).filter(f => f.verdict && f.verdict.real)
const lowerFindings = allFindings.filter(f => f.severity === 'medium' || f.severity === 'low')
log('Verify done: ' + confirmedHigh.length + '/' + toVerify.length + ' high/critical confirmed real')

phase('Synthesize')
const synthInput = JSON.stringify({
  confirmed_high_critical: confirmedHigh.map(f => ({ title: f.title, file: f.file, line: f.line, severity: f.verdict && f.verdict.severity_adjust ? f.verdict.severity_adjust : f.severity, dimension: f.dimension, fix_type: f.fix_type, recommendation: f.recommendation, unit: f.unit })),
  medium_low: lowerFindings.map(f => ({ title: f.title, file: f.file, severity: f.severity, dimension: f.dimension, fix_type: f.fix_type, recommendation: f.recommendation })),
  cross_cutting: crossRecs,
}).slice(0, 240000)

const report = await agent(
  'You are the lead synthesizer for a comprehensive codebase audit. Below is the deduplicated finding set (high/critical are adversarially-verified; plus medium/low; plus cross-cutting CI/observability/guardrail/regression recommendations).\n\nDATA:\n' + synthInput + '\n\nProduce a maintainer-grade audit report for the maintainer (Stephanie). Dedup overlapping items. Prioritize by impact x effort. Tag each recommendation **ship-now** (deterministic, mechanically fixable/guardable) vs **needs-decision** (design/policy judgment).\n\nReturn THREE fields:\n1. overall_status: 2-3 sentences — the codebase\'s current health vs our slop/ZFC/architecture criteria (honest, specific).\n2. full_report_md: a complete markdown report organized by these 6 sections IN THIS ORDER: (A) Status vs slop criteria; (B) Deterministic guardrails to add; (C) CI — build-time + validity; (D) Maintainability; (E) Observability; (F) Regression reduction. Under each, the prioritized recommendations with file:line refs and the ship-now/needs-decision tag. Lead each section with its 1-line takeaway.\n3. slack_summary: a TIGHT Slack-formatted digest UNDER 3500 characters — open with the overall status line, then the top ~10-12 recommendations across all 6 dimensions (one line each: `dimension — recommendation [impact/effort] [ship-now|needs-decision]`), then a one-line pointer to the full report. Use bold sparingly. No preamble.',
  { label: 'synthesize', phase: 'Synthesize', schema: REPORT_SCHEMA, effort: 'high' }
)

return {
  repo: REPO,
  counts: { findings: allFindings.length, cross_recs: crossRecs.length, verified_high: confirmedHigh.length, of_high: toVerify.length },
  report,
}
