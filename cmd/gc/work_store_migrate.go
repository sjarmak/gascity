package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/storebinding"
)

var (
	errWorkMigrationSourceInvalid   = errors.New("work migration source is invalid")
	errWorkCopyUnconfirmed          = errors.New("work migration copy is unconfirmed")
	errWorkVerificationUnavailable  = errors.New("work migration verification unavailable")
	errWorkMigrationSourceChanged   = errors.New("work migration source changed during copy")
	errWorkMigrationWritersUnfenced = errors.New("work migration writers are not fenced")
)

const (
	workMigrationProofVersion = 1
	storageWorkMigrationVerb  = "migrate-work"
)

type workMigrationRequest struct {
	FromFile             string
	DestinationWorkspace string
	DryRun               bool
	FleetStopped         bool
}

type workMigrationResult struct {
	DryRun            bool     `json:"dry_run"`
	Disposition       string   `json:"disposition"`
	Rows              int      `json:"rows"`
	Dependencies      int      `json:"dependencies"`
	SourceFileSHA256  string   `json:"source_file_sha256"`
	SourceWitness     string   `json:"source_witness"`
	DependencyWitness string   `json:"dependency_witness"`
	ProofPath         string   `json:"proof_path,omitempty"`
	AmbientRedirects  []string `json:"ambient_redirects,omitempty"`
}

type workMigrationProof struct {
	Version               int      `json:"version"`
	SourceFile            string   `json:"source_file"`
	SourceFileSHA256      string   `json:"source_file_sha256"`
	SourceWitness         string   `json:"source_witness"`
	WorkIDs               []string `json:"work_ids"`
	DependencyWitness     string   `json:"dependency_witness"`
	DestinationWorkspace  string   `json:"destination_workspace"`
	DestinationPrefix     string   `json:"destination_prefix"`
	Disposition           string   `json:"disposition"`
	ImportedIDs           []string `json:"imported_ids"`
	NativeReadbackWitness string   `json:"native_readback_witness"`
	CommandVersion        string   `json:"command_version"`
	VerifiedAt            string   `json:"verified_at"`
}

type workMigrationDestination interface {
	beads.Store
	ImportExactWorkSnapshot(beads.ExactWorkSnapshot) (beads.ExactWorkImportResult, error)
	CloseStore() error
	IDPrefix() string
}

type workMigrationGuard interface {
	Release() error
}

type workMigrationRuntime struct {
	readSource           func(string) (beads.Store, []byte, error)
	openDestination      func(context.Context, string) (workMigrationDestination, error)
	reopenDestination    func(context.Context, string) (workMigrationDestination, error)
	foreignControllerPID func(string) int
	acquireGuard         func(context.Context, string) (workMigrationGuard, error)
	writeProof           func(string, workMigrationProof) error
	now                  func() time.Time
}

func runWorkMigration(ctx context.Context, request workMigrationRequest, runtime workMigrationRuntime) (result workMigrationResult, returnErr error) {
	source, sourceBytes, err := runtime.readSource(request.FromFile)
	if err != nil {
		return workMigrationResult{}, err
	}
	snapshot, err := readWorkMigrationSnapshot(source)
	if err != nil {
		return workMigrationResult{}, err
	}
	result = workMigrationResult{
		DryRun: request.DryRun, Disposition: "preview", Rows: len(snapshot.Exact.Rows),
		Dependencies:     workMigrationDependencyCount(snapshot.Exact.Rows),
		SourceFileSHA256: sha256String(sourceBytes), SourceWitness: snapshot.Exact.SourceWitness,
		DependencyWitness: snapshot.DependencyWitness,
		AmbientRedirects:  workMigrationRedirectKeys(),
	}
	if request.DryRun {
		return result, nil
	}
	if runtime.foreignControllerPID(request.DestinationWorkspace) != 0 || !request.FleetStopped {
		return workMigrationResult{}, errWorkMigrationWritersUnfenced
	}
	guard, err := runtime.acquireGuard(ctx, cityMigrationGuardDirectory(request.DestinationWorkspace))
	if err != nil {
		return workMigrationResult{}, fmt.Errorf("acquiring work migration guard: %w", err)
	}
	defer func() {
		if err := guard.Release(); err != nil {
			releaseErr := fmt.Errorf("releasing work migration guard: %w", err)
			if returnErr != nil {
				returnErr = errors.Join(returnErr, releaseErr)
				return
			}
			result = workMigrationResult{}
			returnErr = releaseErr
		}
	}()

	writer, err := runtime.openDestination(ctx, request.DestinationWorkspace)
	if err != nil {
		return workMigrationResult{}, fmt.Errorf("opening native Work destination: %w", err)
	}
	destinationPrefix := writer.IDPrefix()
	writerOpen := true
	defer func() {
		if writerOpen {
			if err := writer.CloseStore(); err != nil {
				closeErr := fmt.Errorf("closing native Work writer after failure: %w", err)
				if returnErr != nil {
					returnErr = errors.Join(returnErr, closeErr)
					return
				}
				result = workMigrationResult{}
				returnErr = closeErr
			}
		}
	}()
	existing, err := workMigrationCollisionCount(writer, snapshot.Exact.Rows)
	if err != nil {
		return workMigrationResult{}, err
	}
	var importedIDs []string
	switch existing {
	case 0:
		imported, err := writer.ImportExactWorkSnapshot(snapshot.Exact)
		if err != nil {
			return workMigrationResult{}, err
		}
		importedIDs = slices.Clone(imported.IDs)
		result.Disposition = "imported"
	case len(snapshot.Exact.Rows):
		result.Disposition = "already_proven"
	default:
		return workMigrationResult{}, fmt.Errorf("%w: %d of %d source IDs already exist", beads.ErrWorkMigrationCollision, existing, len(snapshot.Exact.Rows))
	}
	writerOpen = false
	if err := writer.CloseStore(); err != nil {
		return workMigrationResult{}, fmt.Errorf("closing native Work writer: %w", err)
	}

	reader, err := runtime.reopenDestination(ctx, request.DestinationWorkspace)
	if err != nil {
		return workMigrationResult{}, fmt.Errorf("%w: reopening native destination: %w", errWorkVerificationUnavailable, err)
	}
	readbackWitness, err := verifyWorkMigrationSnapshot(reader, snapshot.Exact)
	closeErr := reader.CloseStore()
	if err != nil || closeErr != nil {
		if closeErr != nil {
			closeErr = fmt.Errorf("%w: closing native verifier: %w", errWorkVerificationUnavailable, closeErr)
		}
		return workMigrationResult{}, errors.Join(err, closeErr)
	}
	_, currentSourceBytes, err := runtime.readSource(request.FromFile)
	if err != nil {
		return workMigrationResult{}, fmt.Errorf("%w: rereading source: %w", errWorkMigrationSourceChanged, err)
	}
	if !slices.Equal(sourceBytes, currentSourceBytes) {
		return workMigrationResult{}, errWorkMigrationSourceChanged
	}
	ids := workMigrationIDs(snapshot.Exact.Rows)
	proof := workMigrationProof{
		Version: workMigrationProofVersion, SourceFile: request.FromFile,
		SourceFileSHA256: result.SourceFileSHA256, SourceWitness: result.SourceWitness,
		WorkIDs: ids, DependencyWitness: result.DependencyWitness,
		DestinationWorkspace: request.DestinationWorkspace, DestinationPrefix: destinationPrefix,
		Disposition: result.Disposition, ImportedIDs: importedIDs,
		NativeReadbackWitness: readbackWitness, CommandVersion: version + "@" + commit,
		VerifiedAt: runtime.now().UTC().Format(time.RFC3339Nano),
	}
	if err := runtime.writeProof(request.DestinationWorkspace, proof); err != nil {
		return workMigrationResult{}, fmt.Errorf("writing proven Work copy: %w", err)
	}
	result.ProofPath = workMigrationProofPath(request.DestinationWorkspace)
	return result, nil
}

func newStorageMigrateWorkCmd(stdout, stderr io.Writer) *cobra.Command {
	return newStorageMigrateWorkCmdWithRuntime(defaultWorkMigrationRuntime(), stdout, stderr)
}

func newStorageMigrateWorkCmdWithRuntime(runtime workMigrationRuntime, stdout, stderr io.Writer) *cobra.Command {
	var request workMigrationRequest
	cmd := &cobra.Command{
		Use:          storageWorkMigrationVerb,
		Short:        "Copy the explicit file-ledger Work slice into a native Dolt workspace",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if request.FromFile == "" || !filepath.IsAbs(request.FromFile) {
				fmt.Fprintln(stderr, "gc storage migrate-work: --from-file must name an absolute file") //nolint:errcheck // best-effort stderr
				return errExit
			}
			if request.DestinationWorkspace == "" || !filepath.IsAbs(request.DestinationWorkspace) {
				fmt.Fprintln(stderr, "gc storage migrate-work: --destination-workspace must name an absolute workspace") //nolint:errcheck // best-effort stderr
				return errExit
			}
			result, err := runWorkMigration(cmd.Context(), request, runtime)
			if err != nil {
				fmt.Fprintf(stderr, "gc storage migrate-work: %v\n", err) //nolint:errcheck // best-effort stderr
				return errExit
			}
			if err := json.NewEncoder(stdout).Encode(result); err != nil {
				fmt.Fprintf(stderr, "gc storage migrate-work: writing result: %v\n", err) //nolint:errcheck // best-effort stderr
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&request.FromFile, "from-file", "", "absolute path to the retained file-provider beads.json")
	cmd.Flags().StringVar(&request.DestinationWorkspace, "destination-workspace", "", "absolute path to the existing native Dolt workspace")
	cmd.Flags().BoolVar(&request.DryRun, "dry-run", false, "validate and witness the source without opening or writing the destination")
	cmd.Flags().BoolVar(&request.FleetStopped, storageFleetStoppedFlag, false, "attest that "+storageFleetStoppedAttestation)
	return cmd
}

func defaultWorkMigrationRuntime() workMigrationRuntime {
	open := func(ctx context.Context, workspace string) (workMigrationDestination, error) {
		return beads.OpenNativeDoltStoreAtWithoutAmbientEnv(ctx, workspace)
	}
	return workMigrationRuntime{
		readSource:           openWorkMigrationFileSource,
		openDestination:      open,
		reopenDestination:    open,
		foreignControllerPID: infraMigrationForeignControllerPID,
		acquireGuard: func(ctx context.Context, dir string) (workMigrationGuard, error) {
			return storebinding.AcquireMigrationGuard(ctx, dir, storageMigrationGeneration)
		},
		writeProof: writeWorkMigrationProof,
		now:        time.Now,
	}
}

func openWorkMigrationFileSource(path string) (beads.Store, []byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading explicit Work source %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("explicit Work source %s is not a regular file", path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading explicit Work source %s: %w", path, err)
	}
	var file struct {
		Seq   int          `json:"seq"`
		Beads []beads.Bead `json:"beads"`
		Deps  []beads.Dep  `json:"deps"`
	}
	if err := json.Unmarshal(contents, &file); err != nil {
		return nil, nil, fmt.Errorf("decoding explicit Work source %s: %w", path, err)
	}
	return beads.NewMemStoreFrom(file.Seq, file.Beads, file.Deps), contents, nil
}

func writeWorkMigrationProof(workspace string, proof workMigrationProof) error {
	dir := filepath.Dir(workMigrationProofPath(workspace))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".proven-copy-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func(cause error) error {
		_ = tmp.Close()
		_ = os.Remove(name)
		return cause
	}
	if err := tmp.Chmod(0o600); err != nil {
		return cleanup(err)
	}
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(proof); err != nil {
		return cleanup(err)
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		return cleanup(err)
	}
	if err := os.Rename(name, workMigrationProofPath(workspace)); err != nil {
		return cleanup(err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close() //nolint:errcheck // sync error below is authoritative
	return directory.Sync()
}

func workMigrationProofPath(workspace string) string {
	return filepath.Join(workspace, ".gc", "work-migration", "proven-copy.json")
}

func workMigrationCollisionCount(destination beads.Store, rows []beads.Bead) (int, error) {
	count := 0
	for _, row := range rows {
		_, err := destination.Get(row.ID)
		switch {
		case err == nil:
			count++
		case errors.Is(err, beads.ErrNotFound):
		default:
			return 0, fmt.Errorf("checking destination ID %q: %w", row.ID, err)
		}
	}
	return count, nil
}

func workMigrationDependencyCount(rows []beads.Bead) int {
	count := 0
	for _, row := range rows {
		count += len(row.Dependencies)
	}
	return count
}

func workMigrationIDs(rows []beads.Bead) []string {
	ids := make([]string, len(rows))
	for index := range rows {
		ids[index] = rows[index].ID
	}
	sort.Strings(ids)
	return ids
}

func sha256String(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func workMigrationRedirectKeys() []string {
	var keys []string
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok && (strings.HasPrefix(key, "BEADS_") || strings.HasPrefix(key, "BD_")) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}
