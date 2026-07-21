// Command maintenance-reconcile repairs lost boundary events for one maintenance
// cycle: it reads the workflow's state, reads the real CI/review verdict of the
// branches' PRs via gh, and re-sends any signal the workflow is still missing.
// Run periodically by an order as the narrow safety net behind the signal path.
//
// Usage:
//
//	maintenance-reconcile --address 127.0.0.1:7233 --repo gastownhall/gascity \
//	  --cycle <k> --author-pr <n> --review-pr <n>
//
// Either PR flag may be omitted when that branch has no PR this cycle. It exits 0
// whether or not it repaired anything; a non-zero exit means it could not read
// state or reach gh.
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
		address  = flag.String("address", envOr("TEMPORAL_ADDRESS", client.DefaultHostPort), "Temporal frontend host:port")
		ns       = flag.String("namespace", envOr("TEMPORAL_NAMESPACE", "maintenance"), "Temporal namespace")
		repo     = flag.String("repo", "", "owner/name for gh, e.g. gastownhall/gascity")
		wfRepo   = flag.String("workflow-repo", "", "repo token used in the workflow id (default: --repo with / -> -)")
		cycle    = flag.String("cycle", "", "cycle key")
		authorPR = flag.String("author-pr", "", "author branch PR number (optional)")
		reviewPR = flag.String("review-pr", "", "review branch PR number (optional)")
		ghDir    = flag.String("gh-dir", ".", "working directory for gh")
	)
	flag.Parse()

	if *repo == "" || *cycle == "" {
		log.Fatal("maintenance-reconcile: --repo and --cycle are required")
	}
	workflowRepo := *wfRepo
	if workflowRepo == "" {
		workflowRepo = defaultWorkflowRepo(*repo)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c, err := client.Dial(client.Options{HostPort: *address, Namespace: *ns})
	if err != nil {
		log.Fatalf("maintenance-reconcile: dial %s ns=%s: %v", *address, *ns, err)
	}
	defer c.Close()

	fetcher := tm.NewGHStatusFetcher(*ghDir)
	truth, err := buildTruth(ctx, fetcher, *repo, *authorPR, *reviewPR)
	if err != nil {
		log.Fatalf("maintenance-reconcile: read PR truth: %v", err)
	}

	rec := tm.NewReconciler(tm.NewClientStateReader(c), tm.NewSignaler(c))
	repaired, err := rec.Reconcile(ctx, workflowRepo, *cycle, truth)
	if err != nil {
		log.Fatalf("maintenance-reconcile: %v", err)
	}
	if len(repaired) == 0 {
		fmt.Printf("maintenance-reconcile: nothing to repair for %s\n", tm.WorkflowID(workflowRepo, *cycle))
		return
	}
	fmt.Printf("maintenance-reconcile: repaired %v for %s\n", repaired, tm.WorkflowID(workflowRepo, *cycle))
}

func buildTruth(ctx context.Context, f tm.PRStatusFetcher, repo, authorPR, reviewPR string) ([]tm.BranchTruth, error) {
	var truth []tm.BranchTruth
	if authorPR != "" {
		st, err := f.Fetch(ctx, repo, authorPR)
		if err != nil {
			return nil, err
		}
		truth = append(truth, tm.BranchTruth{Branch: "author", CIVerdict: st.CIVerdict})
	}
	if reviewPR != "" {
		st, err := f.Fetch(ctx, repo, reviewPR)
		if err != nil {
			return nil, err
		}
		truth = append(truth, tm.BranchTruth{Branch: "review", ReviewVerdict: st.ReviewVerdict})
	}
	return truth, nil
}

// defaultWorkflowRepo turns a gh "owner/name" into the "owner-name" token the
// workflow id uses.
func defaultWorkflowRepo(ghRepo string) string {
	out := make([]rune, 0, len(ghRepo))
	for _, r := range ghRepo {
		if r == '/' {
			r = '-'
		}
		out = append(out, r)
	}
	return string(out)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
