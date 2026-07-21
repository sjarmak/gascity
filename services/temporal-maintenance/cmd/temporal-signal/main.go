// Command temporal-signal delivers one typed maintenance Signal to a running
// MaintenanceCycleWorkflow. It is the exec behind the bead.closed / bead.created
// event orders (the wake-mayor-on-slung-close mechanism): the order's wrapper
// resolves which cycle a closed bead belongs to and invokes this with explicit
// args, keeping all Temporal interaction in one typed place.
//
// Usage:
//
//	temporal-signal --address 127.0.0.1:7233 --repo <r> --cycle <k> \
//	  --signal bead-closed --bead <id>
//	temporal-signal ... --signal ci     --branch author --verdict pass
//	temporal-signal ... --signal review --branch review --verdict pass
//	temporal-signal ... --signal escalation --branch author
//
// It exits 0 on success. A workflow that isn't running (already completed, or the
// cycle key is stale) is reported and exits non-zero, so the caller can log-and-
// continue without treating it as fatal.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	tm "github.com/sjarmak/gas-city/services/temporal-maintenance"

	"go.temporal.io/sdk/client"
)

func main() {
	var (
		address = flag.String("address", envOr("TEMPORAL_ADDRESS", client.DefaultHostPort), "Temporal frontend host:port")
		ns      = flag.String("namespace", envOr("TEMPORAL_NAMESPACE", "maintenance"), "Temporal namespace")
		repo    = flag.String("repo", "", "repo, e.g. gastownhall-gascity")
		cycle   = flag.String("cycle", "", "cycle key")
		signal  = flag.String("signal", "", "bead-closed | ci | review | escalation")
		bead    = flag.String("bead", "", "bead id (for --signal bead-closed)")
		branch  = flag.String("branch", "", "author | review (for ci/review/escalation)")
		verdict = flag.String("verdict", "", "pass | fail | skip (for ci/review)")
	)
	flag.Parse()

	if *repo == "" || *cycle == "" || *signal == "" {
		log.Fatal("temporal-signal: --repo, --cycle and --signal are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := client.Dial(client.Options{HostPort: *address, Namespace: *ns})
	if err != nil {
		log.Fatalf("temporal-signal: dial %s ns=%s: %v", *address, *ns, err)
	}
	defer c.Close()

	s := tm.NewSignaler(c)
	if err := send(ctx, s, *signal, *repo, *cycle, *branch, *bead, *verdict); err != nil {
		log.Fatalf("temporal-signal: %v", err)
	}
	fmt.Printf("temporal-signal: sent %s to %s\n", *signal, tm.WorkflowID(*repo, *cycle))
}

func send(ctx context.Context, s *tm.Signaler, signal, repo, cycle, branch, bead, verdict string) error {
	switch signal {
	case "bead-closed":
		if bead == "" {
			return fmt.Errorf("--bead is required for bead-closed")
		}
		return s.BeadClosed(ctx, repo, cycle, bead)
	case "ci":
		// CI only completes the author branch; the branch is fixed by the signal
		// kind so a stray --branch can't record the verdict on a field the
		// workflow never inspects (a silent forever-stall).
		return s.CICompleted(ctx, repo, cycle, fixedBranch("ci", "author", branch), mustVerdict(verdict))
	case "review":
		return s.ReviewCompleted(ctx, repo, cycle, fixedBranch("review", "review", branch), mustVerdict(verdict))
	case "escalation":
		return s.Escalation(ctx, repo, cycle, requireBranch(branch))
	default:
		return fmt.Errorf("unknown --signal %q", signal)
	}
}

// fixedBranch returns want for a signal whose branch is determined by its kind,
// rejecting a --branch that contradicts it rather than silently mis-signalling.
func fixedBranch(signal, want, got string) string {
	if got != "" && got != want {
		log.Fatalf("temporal-signal: --signal %s always targets the %q branch, not %q", signal, want, got)
	}
	return want
}

func requireBranch(b string) string {
	if b != "author" && b != "review" {
		log.Fatalf("temporal-signal: --branch must be author|review, got %q", b)
	}
	return b
}

func mustVerdict(v string) tm.Verdict {
	switch v {
	case "pass":
		return tm.VerdictPass
	case "fail":
		return tm.VerdictFail
	case "skip":
		return tm.VerdictSkip
	default:
		log.Fatalf("temporal-signal: --verdict must be pass|fail|skip, got %q", v)
		return tm.VerdictUnknown
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
