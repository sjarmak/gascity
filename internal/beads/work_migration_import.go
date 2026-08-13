package beads

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gastownhall/gascity/internal/beadmeta"
	beadslib "github.com/steveyegge/beads"
)

var (
	// ErrUnsupportedWorkMigrationShape reports source state the exact native
	// migration seam cannot represent without changing its meaning.
	ErrUnsupportedWorkMigrationShape = errors.New("unsupported work migration source shape")
	// ErrWorkMigrationCollision reports a source ID already present in the
	// destination, which the exact import refuses rather than overwriting.
	ErrWorkMigrationCollision = errors.New("work migration destination collision")
)

const (
	workMigrationLegacyParentMetadataKey  = beadmeta.WorkMigrationLegacyParentMetadataKey
	workMigrationLegacyTitleMetadataKey   = beadmeta.WorkMigrationLegacyTitleMetadataKey
	workMigrationSourceWitnessMetadataKey = beadmeta.WorkMigrationSourceWitnessMetadataKey
	nativeWorkTitleMaxBytes               = 500
)

// ExactWorkSnapshot is one validated file-ledger Work slice and its canonical
// source identity.
type ExactWorkSnapshot struct {
	SourceWitness string
	Rows          []Bead
}

// ExactWorkImportResult identifies the rows written by a successful atomic
// import.
type ExactWorkImportResult struct {
	IDs []string
}

// ImportExactWorkSnapshot atomically creates a collision-free Work snapshot
// with explicit IDs and its complete within-snapshot dependency set.
func (s *NativeDoltStore) ImportExactWorkSnapshot(snapshot ExactWorkSnapshot) (ExactWorkImportResult, error) {
	rows, deps, err := prepareExactWorkSnapshot(snapshot)
	if err != nil {
		return ExactWorkImportResult{}, err
	}
	issues := make([]*beadslib.Issue, 0, len(rows))
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		issue, err := nativeIssueFromBead(row)
		if err != nil {
			return ExactWorkImportResult{}, err
		}
		issue.Dependencies = nil
		issues = append(issues, issue)
		ids = append(ids, row.ID)
	}

	storage, release, err := s.acquireStorage()
	if err != nil {
		return ExactWorkImportResult{}, err
	}
	defer release()
	ctx, cancel := nativeDoltOperationContext(context.TODO())
	defer cancel()
	err = storage.RunInTransaction(ctx, fmt.Sprintf("gc: import exact work snapshot %s", snapshot.SourceWitness), func(tx beadslib.Transaction) error {
		if err := refuseExactWorkCollisions(ctx, tx, ids); err != nil {
			return err
		}
		if err := tx.CreateIssues(ctx, issues, s.actor); err != nil {
			return fmt.Errorf("creating exact work rows: %w", err)
		}
		for _, dep := range deps {
			if err := tx.AddDependency(ctx, dep, s.actor); err != nil {
				return fmt.Errorf("creating exact work dependency %s -> %s: %w", dep.IssueID, dep.DependsOnID, err)
			}
		}
		return nil
	})
	if err != nil {
		return ExactWorkImportResult{}, err
	}
	return ExactWorkImportResult{IDs: ids}, nil
}

func prepareExactWorkSnapshot(snapshot ExactWorkSnapshot) ([]Bead, []*beadslib.Dependency, error) {
	if !validWorkMigrationWitness(snapshot.SourceWitness) {
		return nil, nil, fmt.Errorf("%w: source witness must be sha256:<64 lowercase hex>", ErrUnsupportedWorkMigrationShape)
	}
	if len(snapshot.Rows) == 0 {
		return nil, nil, fmt.Errorf("%w: snapshot has no rows", ErrUnsupportedWorkMigrationShape)
	}
	rows := cloneWorkMigrationRows(snapshot.Rows)
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	ids := make(map[string]struct{}, len(rows))
	for index := range rows {
		if err := normalizeExactWorkRow(&rows[index], snapshot.SourceWitness, ids); err != nil {
			return nil, nil, err
		}
		ids[rows[index].ID] = struct{}{}
	}
	deps, err := exactWorkDependencies(rows, ids)
	if err != nil {
		return nil, nil, err
	}
	return rows, deps, nil
}

func normalizeExactWorkRow(row *Bead, witness string, priorIDs map[string]struct{}) error {
	if strings.TrimSpace(row.ID) == "" || row.CreatedAt.IsZero() {
		return fmt.Errorf("%w: row needs id and created_at", ErrUnsupportedWorkMigrationShape)
	}
	if _, duplicate := priorIDs[row.ID]; duplicate {
		return fmt.Errorf("%w: duplicate row %q", ErrUnsupportedWorkMigrationShape, row.ID)
	}
	if row.Ephemeral || row.NoHistory || len(row.Needs) != 0 {
		return fmt.Errorf("%w: row %q uses a non-atomic tier or legacy needs", ErrUnsupportedWorkMigrationShape, row.ID)
	}
	if row.Metadata == nil {
		row.Metadata = make(StringMap)
	}
	for _, key := range []string{workMigrationLegacyParentMetadataKey, workMigrationLegacyTitleMetadataKey, workMigrationSourceWitnessMetadataKey} {
		if _, exists := row.Metadata[key]; exists {
			return fmt.Errorf("%w: row %q already carries reserved key %q", ErrUnsupportedWorkMigrationShape, row.ID, key)
		}
	}
	if row.Title == "" || len(row.Title) > nativeWorkTitleMaxBytes {
		row.Metadata[workMigrationLegacyTitleMetadataKey] = row.Title
		row.Title = nativeWorkStoredTitle(row.ID, row.Title)
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = row.CreatedAt
	}
	row.CreatedAt = CanonicalWorkMigrationTime(row.CreatedAt)
	row.UpdatedAt = CanonicalWorkMigrationTime(row.UpdatedAt)
	if row.DeferUntil != nil {
		canonical := CanonicalWorkMigrationTime(*row.DeferUntil)
		row.DeferUntil = &canonical
	}
	switch beadslib.Status(row.Status) {
	case "":
		row.Status = "open"
	case beadslib.StatusOpen, beadslib.StatusInProgress, beadslib.StatusClosed:
	default:
		return fmt.Errorf("%w: row %q has status %q, which the native read contract cannot preserve", ErrUnsupportedWorkMigrationShape, row.ID, row.Status)
	}
	if row.Type == "" {
		row.Type = "task"
	}
	row.Metadata[workMigrationSourceWitnessMetadataKey] = witness
	return nil
}

// CanonicalWorkMigrationTime matches the nearest-second precision of the
// native Beads issues table. The proof retains the exact source-file hash.
func CanonicalWorkMigrationTime(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.UTC().Round(time.Second)
}

func nativeWorkTitlePrefix(title string) string {
	if len(title) <= nativeWorkTitleMaxBytes {
		return title
	}
	prefix := title[:nativeWorkTitleMaxBytes]
	for len(prefix) > 0 && !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix
}

func nativeWorkStoredTitle(id, title string) string {
	if title == "" {
		return id
	}
	return nativeWorkTitlePrefix(title)
}

func exactWorkDependencies(rows []Bead, ids map[string]struct{}) ([]*beadslib.Dependency, error) {
	deps := make([]*beadslib.Dependency, 0)
	pairs := make(map[string]struct{})
	for index := range rows {
		row := &rows[index]
		parentDep := false
		for _, dep := range row.Dependencies {
			if dep.IssueID != row.ID {
				return nil, fmt.Errorf("%w: dependency owner %q does not match row %q", ErrUnsupportedWorkMigrationShape, dep.IssueID, row.ID)
			}
			if dep.Type == "" || dep.IssueID == dep.DependsOnID {
				return nil, fmt.Errorf("%w: dependency %q -> %q needs a type and distinct endpoints", ErrUnsupportedWorkMigrationShape, dep.IssueID, dep.DependsOnID)
			}
			if _, ok := ids[dep.DependsOnID]; !ok {
				return nil, fmt.Errorf("%w: dependency %q -> %q leaves the snapshot", ErrUnsupportedWorkMigrationShape, dep.IssueID, dep.DependsOnID)
			}
			pair := dep.IssueID + "\x00" + dep.DependsOnID
			if _, duplicate := pairs[pair]; duplicate {
				return nil, fmt.Errorf("%w: duplicate dependency pair %q -> %q", ErrUnsupportedWorkMigrationShape, dep.IssueID, dep.DependsOnID)
			}
			pairs[pair] = struct{}{}
			if dep.Type == "parent-child" && dep.DependsOnID == row.ParentID {
				parentDep = true
			}
			deps = append(deps, &beadslib.Dependency{IssueID: dep.IssueID, DependsOnID: dep.DependsOnID, Type: beadslib.DependencyType(dep.Type)})
		}
		if row.ParentID == "" {
			continue
		}
		if _, retained := ids[row.ParentID]; retained {
			if !parentDep {
				return nil, fmt.Errorf("%w: row %q has retained parent %q without matching parent-child dependency", ErrUnsupportedWorkMigrationShape, row.ID, row.ParentID)
			}
		} else {
			row.Metadata[workMigrationLegacyParentMetadataKey] = row.ParentID
		}
		row.ParentID = ""
	}
	sort.Slice(deps, func(i, j int) bool {
		left, right := deps[i], deps[j]
		if left.IssueID != right.IssueID {
			return left.IssueID < right.IssueID
		}
		return left.DependsOnID < right.DependsOnID
	})
	return deps, nil
}

func refuseExactWorkCollisions(ctx context.Context, tx beadslib.Transaction, ids []string) error {
	for _, id := range ids {
		issue, err := tx.GetIssue(ctx, id)
		if err == nil && issue != nil {
			return fmt.Errorf("%w: %s", ErrWorkMigrationCollision, id)
		}
		if err != nil && !errors.Is(err, ErrNotFound) && !nativeUpstreamNotFound(err) {
			return fmt.Errorf("checking work migration collision %q: %w", id, err)
		}
	}
	return nil
}

func cloneWorkMigrationRows(input []Bead) []Bead {
	rows := make([]Bead, len(input))
	for index, row := range input {
		rows[index] = row
		rows[index].Labels = append([]string(nil), row.Labels...)
		rows[index].Needs = append([]string(nil), row.Needs...)
		rows[index].Dependencies = append([]Dep(nil), row.Dependencies...)
		rows[index].Metadata = maps.Clone(row.Metadata)
		rows[index].DeferUntil = cloneTimePtr(row.DeferUntil)
		rows[index].Priority = cloneIntPtr(row.Priority)
	}
	return rows
}

func validWorkMigrationWitness(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, char := range strings.TrimPrefix(value, "sha256:") {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
