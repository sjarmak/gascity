package branchreconciler

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParseForEachRef parses the output of
// `git for-each-ref --format='%(refname:short)' refs/remotes/<remote>/`,
// stripping the "<remote>/" prefix and skipping the remote's symbolic HEAD
// ref, which is not a branch.
func ParseForEachRef(output, remote string) []string {
	prefix := remote + "/"
	var names []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name := strings.TrimPrefix(line, prefix)
		if name == "HEAD" {
			continue
		}
		names = append(names, name)
	}
	return names
}

// prListEntry mirrors the fields this package reads from
// `gh pr list --json headRefName,headRepositoryOwner`.
type prListEntry struct {
	HeadRefName         string `json:"headRefName"`
	HeadRepositoryOwner *struct {
		Login string `json:"login"`
	} `json:"headRepositoryOwner"`
}

// ParsePRHeadRefs parses `gh pr list --json headRefName,headRepositoryOwner`
// output and returns the set of head ref names whose PR was opened from a
// fork owned by forkOwner. A PR whose source fork has since been deleted
// reports a null headRepositoryOwner and is excluded: gh no longer traces it
// back to forkOwner.
func ParsePRHeadRefs(data []byte, forkOwner string) (map[string]bool, error) {
	var entries []prListEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parsing gh pr list output: %w", err)
	}
	refs := make(map[string]bool)
	for _, e := range entries {
		if e.HeadRepositoryOwner == nil || e.HeadRepositoryOwner.Login != forkOwner {
			continue
		}
		refs[e.HeadRefName] = true
	}
	return refs, nil
}
