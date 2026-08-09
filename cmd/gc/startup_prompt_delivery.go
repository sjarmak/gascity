package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	session "github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/shellquote"
	"github.com/gastownhall/gascity/internal/worker"
)

// applyStartupPromptDelivery is the single statement of the startup-prompt
// delivery rule: whether this incarnation delivers the rendered prompt as a
// first-turn payload, or re-primes an already-live provider session instead.
//
// There is exactly ONE copy of this decision and both start paths call it:
//   - the reconciler's prepared start (buildPreparedStartWithWorkDirResolver), and
//   - the worker handle's start-time prompt resolver (resolveStartupPromptForSession),
//     which serves gc session attach / restart / seat recycle.
//
// Keeping one copy is a hard constraint of dr-5fek: the defect being fixed is a
// second start path that never learned the rule, and a second COPY of the rule
// would reproduce the same class the moment the two drift.
//
// On a resume the prompt is moved to the Nudge. Be precise about what that is:
// for a provider session whose process died, the runtime sends the nudge as
// input after startup, so it lands as a fresh turn in the existing
// conversation. That is the pre-existing reconciler behavior, not something
// this rule introduces, but it is delivery into a live conversation and should
// not be described as if it were free.
//
// cfg is mutated in place. The returned bool reports whether the prompt reached
// a first-turn delivery mechanism this incarnation; callers stamp the S19
// priming marker from it and must NOT infer it from
// cfg.Env[GC_STARTUP_PROMPT_DELIVERED], which the resume branch re-sets to "1"
// for hook consumption even when nothing was delivered.
func applyStartupPromptDelivery(
	cfg *runtime.Config,
	prompt string,
	hintNudge string,
	delivery promptDeliveryResult,
	firstStart bool,
	forceFresh bool,
	hasResumeKey bool,
) bool {
	if cfg == nil {
		return false
	}
	delivered := delivery.Delivered && (firstStart || forceFresh || !hasResumeKey)
	if !firstStart && !forceFresh && hasResumeKey {
		cfg.PromptSuffix = ""
		cfg.PromptFlag = ""
		cfg.Nudge = restartPromptNudge(prompt, hintNudge)
		if cfg.Env != nil {
			delete(cfg.Env, startupPromptDeliveredEnv)
		}
		if strings.TrimSpace(prompt) != "" {
			if cfg.Env == nil {
				cfg.Env = map[string]string{}
			}
			cfg.Env[startupPromptDeliveredEnv] = "1"
		}
	}
	return delivered
}

// applyInitialMessage appends the one-shot initial_message to the delivered
// payload. Like the delivery rule above this is shared, so the handle path
// cannot silently drop a user's initial message the way it dropped the prompt.
//
// First start only: the message is transient and must never be replayed on a
// later relaunch. Callers gate on (firstStart || forceFresh).
func applyInitialMessage(cfg *runtime.Config, msg string, promptModeNone bool) {
	if cfg == nil || msg == "" {
		return
	}
	if promptModeNone {
		cfg.Nudge = appendInitialMessageToStartupNudge(cfg.Nudge, msg)
		return
	}
	existing := ""
	if cfg.PromptSuffix != "" {
		if parts := shellquote.Split(cfg.PromptSuffix); len(parts) > 0 {
			existing = parts[0]
		}
	}
	if existing != "" {
		cfg.PromptSuffix = shellquote.Quote(existing + "\n\n---\n\nUser message:\n" + msg)
		return
	}
	cfg.PromptSuffix = shellquote.Quote(msg)
}

// workerStartupPromptResolverWithConfig builds the start-time startup-prompt
// resolver handed to the worker Factory.
//
// Why start time and not handle construction (dr-5fek): rendering a template is
// NOT a read. resolveTemplatePrepared stages provider overlay dirs and installs
// hook-backed files, and resolveTemplate writes provider settings and skill
// snapshot files. Factory resolution is eager, so doing this while building a
// handle would make gc session peek / state / kill / stop / observe mutate the
// city on disk. Only Start and Attach bring a runtime up, and they are the only
// callers of this resolver -- which is exactly the staging the reconciler
// already performs when it starts a session.
func workerStartupPromptResolverWithConfig(cityPath string, store beads.Store, sp runtime.Provider, cfg *config.City) worker.StartupPromptResolver {
	if cfg == nil {
		return nil
	}
	return func(info session.Info, metadata map[string]string) (worker.StartupPrompt, error) {
		return resolveStartupPromptForSession(cityPath, store, sp, cfg, info, metadata)
	}
}

// startupPromptAgentForSession identifies which configured agent's prompt this
// session should receive, and refuses rather than guesses.
//
// Delivering a prompt is not neutral: resolution also stages that agent's files
// and settings, so associating a session with the WRONG agent is worse than
// leaving it promptless. Two ownership rules the general lookup precedence does
// not encode:
//
//   - A provider-backed session never borrows an agent's prompt, even when its
//     persisted template name collides with a configured agent. That ownership
//     contract already exists as session.UseAgentTemplateForProviderResolution
//     and is reused here rather than re-derived.
//   - A disagreement between template and agent_name is only an error when
//     nothing STRUCTURAL explains it. Configured named sessions and pool
//     instances legitimately carry an identity distinct from their backing
//     template, so a named session "beta" backed by template "alpha" is valid
//     even when an agent "beta" also exists. Only unexplained disagreement is a
//     genuine ambiguity about whose prompt to deliver.
func startupPromptAgentForSession(cfg *config.City, info session.Info, metadata map[string]string) (*config.Agent, error) {
	byTemplate := findAgentByTemplate(cfg, strings.TrimSpace(info.Template))
	sessionKind := strings.TrimSpace(metadata["real_world_app_session_kind"])
	if !session.UseAgentTemplateForProviderResolution(sessionKind, metadata, "", "", byTemplate != nil) {
		return nil, nil
	}

	var byAgentName *config.Agent
	if name := strings.TrimSpace(sessionBeadAgentNameInfo(info)); name != "" {
		if resolved := resolvedTemplateForIdentity(name, cfg); resolved != "" {
			byAgentName = findAgentByTemplate(cfg, resolved)
		}
	}

	switch {
	case byTemplate != nil && byAgentName != nil:
		if byTemplate.QualifiedName() == byAgentName.QualifiedName() {
			return byTemplate, nil
		}
		// The backing template is authoritative wherever the session's own
		// structure ACCOUNTS FOR the different identity. A configured named
		// session's identity is its agent_name by construction. A pool instance
		// must actually derive agent_name from its slot: a bare non-empty
		// pool_slot proves nothing, and stale pool metadata is an expected
		// repository state, so accepting it would let exactly the case this
		// error exists to catch slip through and stage the wrong agent's files.
		if isNamedSessionInfo(info) {
			return byTemplate, nil
		}
		if poolSlotDerivesAgentName(byTemplate, info, metadata) {
			return byTemplate, nil
		}
		return nil, fmt.Errorf(
			"session %q has conflicting identities with no named-session or pool provenance: template %q resolves to agent %q but agent_name resolves to agent %q; refusing to guess whose startup prompt to deliver",
			info.ID, info.Template, byTemplate.QualifiedName(), byAgentName.QualifiedName())
	case byTemplate != nil:
		return byTemplate, nil
	case byAgentName != nil:
		return byAgentName, nil
	default:
		// Alias-only matches deliberately fall through to promptless: an alias
		// is not evidence of ownership.
		return nil, nil
	}
}

// poolSlotDerivesAgentName reports whether this session's pool slot, expanded
// under the backing template, actually produces its persisted agent_name. That
// is the evidence that a template/agent_name difference is a pool instance
// rather than a mismatch — the slot number alone is not.
func poolSlotDerivesAgentName(byTemplate *config.Agent, info session.Info, metadata map[string]string) bool {
	agentName := strings.TrimSpace(sessionBeadAgentNameInfo(info))
	if byTemplate == nil || agentName == "" {
		return false
	}
	raw := strings.TrimSpace(info.PoolSlot)
	if raw == "" {
		raw = strings.TrimSpace(metadata["pool_slot"])
	}
	slot, err := strconv.Atoi(raw)
	if err != nil || slot <= 0 {
		return false
	}
	// poolInstanceIdentity derives a name for ANY slot number -- past the end of
	// a namepool it falls back to "{base}-N". A slot the repository considers
	// out of bounds is not evidence of ownership, so bound it first or a
	// nonsense slot could manufacture a match against a separately configured
	// agent that happens to be named "{base}-N".
	if !usablePoolIdentitySlot(byTemplate, slot) {
		return false
	}
	instanceName, qualifiedInstance := poolInstanceIdentity(byTemplate, slot, nil)
	return agentName == instanceName || agentName == qualifiedInstance
}

// resolveStartupPromptForSession renders the agent's startup prompt for an
// EXISTING session so that handle-driven starts prime the agent the same way
// the reconciler's prepared start does.
//
// Before this fix, every SessionHandle entry point that is not the reconciler's
// StartResolved -- Start, Attach, restart, recycle -- built its runtime from
// h.startCommand() + h.runtimeHints(), and nothing under internal/worker/ ever
// received a rendered prompt. Those paths recreated the tmux runtime with an
// empty PromptSuffix and the agent came up alive at the provider prompt with no
// instructions and no beacon.
//
// A session whose agent is no longer configured resolves to an empty prompt
// without error: legacy and provider-only sessions legitimately have no agent
// template. A CONFIGURED agent whose template fails to resolve returns the
// error, because silently starting it unprimed is the defect being fixed.
func resolveStartupPromptForSession(
	cityPath string,
	store beads.Store,
	sp runtime.Provider,
	cfg *config.City,
	info session.Info,
	metadata map[string]string,
) (worker.StartupPrompt, error) {
	if cfg == nil {
		return worker.StartupPrompt{}, nil
	}
	baseAgent, err := startupPromptAgentForSession(cfg, info, metadata)
	if err != nil {
		return worker.StartupPrompt{}, err
	}
	if baseAgent == nil {
		return worker.StartupPrompt{}, nil
	}
	// Reconstruct the session's OWN identity rather than the backing template's.
	// Pool instances and configured named sessions must render with the identity
	// and work dir the session was assigned, or the prompt (and the work dir the
	// template resolves) belongs to a different agent.
	cfgAgent, qualifiedName := canonicalSessionIdentityWithConfigInfo(cfg, baseAgent, info)
	if cfgAgent == nil {
		return worker.StartupPrompt{}, nil
	}
	if strings.TrimSpace(qualifiedName) == "" {
		qualifiedName = firstNonEmptyGCString(info.AgentName, info.Template)
	}

	bp := newAgentBuildParams(cfg.EffectiveCityName(), cityPath, cfg, sp, time.Now().UTC(), store, os.Stderr)
	tp, err := resolveTemplateForSessionBeadInfo(bp, cfgAgent, qualifiedName, nil, info)
	if err != nil {
		return worker.StartupPrompt{}, err
	}

	// Route through the same projection the reconciler uses so the delivery
	// mechanism (argv suffix vs flag vs nudge, and the ACP special case) is
	// derived once, in promptDelivery, rather than re-decided here.
	tplCfg, delivery := templateParamsToConfigWithDelivery(tp)
	out := runtime.Config{
		PromptSuffix: tplCfg.PromptSuffix,
		PromptFlag:   tplCfg.PromptFlag,
		Nudge:        tplCfg.Nudge,
	}
	if _, ok := tplCfg.Env[startupPromptDeliveredEnv]; ok {
		out.Env = map[string]string{startupPromptDeliveredEnv: "1"}
	}

	// forceFresh stays false, and that is a deliberate pairing rather than an
	// omission: h.startCommand() builds a RESUME command on every handle path
	// that has a session key, so claiming a fresh launch here would deliver a
	// first-turn prompt into a command that resumes. Command selection and
	// prompt delivery have to make the same call. wake_mode=fresh is therefore
	// not honored by handle starts today -- a pre-existing gap in command
	// selection, tracked separately; it is not made worse here.
	const forceFresh = false
	firstStart := worker.FirstProviderSessionStart(info.State, metadata)
	hasResumeKey := strings.TrimSpace(info.SessionKey) != ""
	applyStartupPromptDelivery(&out, tp.Prompt, tp.Hints.Nudge, delivery, firstStart, forceFresh, hasResumeKey)

	if firstStart || forceFresh {
		// Malformed persisted overrides must not silently cost the user their
		// initial message. The reconciler logs this; here the error can travel,
		// so it does, and the start aborts rather than dropping input.
		overrides, err := session.ParseTemplateOverrides(metadata)
		if err != nil {
			return worker.StartupPrompt{}, fmt.Errorf("session %q: parsing template overrides: %w", info.ID, err)
		}
		promptModeNone := tp.ResolvedProvider != nil && tp.ResolvedProvider.PromptMode == "none"
		applyInitialMessage(&out, overrides["initial_message"], promptModeNone)
	}

	return worker.StartupPrompt{
		PromptSuffix: out.PromptSuffix,
		PromptFlag:   out.PromptFlag,
		Nudge:        out.Nudge,
		Env:          out.Env,
	}, nil
}
