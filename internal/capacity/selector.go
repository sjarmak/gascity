package capacity

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/processgroup"
)

// Environment variables a selector command receives. The command is free to
// ignore any of them.
const (
	// EnvAccountExclude lists accounts the ledger has already filled, comma
	// separated. It is always set, empty when nothing is excluded.
	EnvAccountExclude = "GC_ACCOUNT_EXCLUDE"
	// EnvAccountAgent names the agent the reservation is for.
	EnvAccountAgent = "GC_ACCOUNT_AGENT"
	// EnvAccountProvider names the resolved provider, when there is one.
	EnvAccountProvider = "GC_ACCOUNT_PROVIDER"
)

// maxAccountLen bounds an account identifier from an external command.
const maxAccountLen = 128

// defaultSelectorTimeout bounds a selector command. Selection sits in front of
// every spawn, so a wedged command must fail rather than stall the scheduler.
// Generous enough for a usage-cache refresh over the network.
const defaultSelectorTimeout = 60 * time.Second

// PickRequest asks a selector for an account.
type PickRequest struct {
	Agent    string
	Provider string
	// Exclude lists accounts already at their cap in the ledger. A selector
	// should avoid them; the ledger re-checks the answer regardless.
	Exclude []string
}

// AccountSelector picks the account a reservation should hold.
//
// Selection is install-side policy: which accounts exist, how loaded they are,
// and which are throttled is knowledge the SDK does not have and must not
// encode. A selector answers with an opaque identifier that the ledger stores
// and compares but never parses.
//
// It is a function rather than an interface, matching ScaleCheckRunner: any
// closure or method value satisfies it, so CommandSelector.Pick plugs in
// directly.
type AccountSelector func(ctx context.Context, req PickRequest) (string, error)

// CommandRunner runs a selector command and returns its stdout. It mirrors
// ScaleCheckRunner so account selection reaches install-side tooling through
// the same configured-command seam pool sizing already uses.
//
// Implementations must be safe for concurrent use.
type CommandRunner func(ctx context.Context, command string, env map[string]string) (string, error)

// CommandSelector delegates account selection to a configured command.
type CommandSelector struct {
	command string
	runner  CommandRunner
	timeout time.Duration
}

// SelectorOption configures a CommandSelector.
type SelectorOption func(*CommandSelector)

// WithRunner overrides how the command is executed.
func WithRunner(r CommandRunner) SelectorOption { return func(c *CommandSelector) { c.runner = r } }

// WithSelectorTimeout overrides the command timeout.
func WithSelectorTimeout(d time.Duration) SelectorOption {
	return func(c *CommandSelector) { c.timeout = d }
}

// NewCommandSelector returns a selector that runs command to choose an account.
func NewCommandSelector(command string, opts ...SelectorOption) *CommandSelector {
	c := &CommandSelector{command: command, timeout: defaultSelectorTimeout}
	for _, opt := range opts {
		opt(c)
	}
	if c.runner == nil {
		c.runner = shellRunner
	}
	return c
}

// Pick runs the configured command and returns the account it names.
func (c *CommandSelector) Pick(ctx context.Context, req PickRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	env := map[string]string{
		EnvAccountExclude:  strings.Join(req.Exclude, ","),
		EnvAccountAgent:    req.Agent,
		EnvAccountProvider: req.Provider,
	}
	out, err := c.runner(ctx, c.command, env)
	if err != nil {
		return "", fmt.Errorf("account selector %q: %w", c.command, err)
	}
	return parseAccountPick(c.command, out)
}

// parseAccountPick validates a selector command's stdout. The identifier is
// opaque, so this checks shape only: exactly one bare token, bounded length.
func parseAccountPick(command, out string) (string, error) {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return "", fmt.Errorf("account selector %q produced empty output", command)
	}
	if len(trimmed) > maxAccountLen {
		return "", fmt.Errorf("account selector %q output is %d bytes, over the %d limit", command, len(trimmed), maxAccountLen)
	}
	if strings.ContainsAny(trimmed, " \t\r\n") {
		return "", fmt.Errorf("account selector %q output %q is not a single token", command, trimmed)
	}
	return trimmed, nil
}

// shellRunner is the production CommandRunner: sh -c, inheriting the
// environment with the selector variables merged in.
func shellRunner(ctx context.Context, command string, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	processgroup.StartCommandInNewGroup(cmd)
	cmd.Cancel = func() error {
		return processgroup.TerminateCommand(cmd, 0, 500*time.Millisecond, processgroup.Options{})
	}
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("running command %q: %w", command, err)
	}
	return string(out), nil
}
