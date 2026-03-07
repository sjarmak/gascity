package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/gastownhall/gascity/internal/supervisor"
	"github.com/spf13/cobra"
)

func newRegisterCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "register [path]",
		Short: "Register a city with the machine-wide supervisor",
		Long: `Register a city directory with the machine-wide supervisor.

If no path is given, registers the current city (discovered from cwd).
Registration is idempotent — registering the same city twice is a no-op.
City names (derived from directory basename or workspace.name) must be
unique across all registered cities.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if doRegister(args, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	return cmd
}

func doRegister(args []string, stdout, stderr io.Writer) int {
	var cityPath string
	var err error
	if len(args) > 0 {
		cityPath, err = filepath.Abs(args[0])
	} else {
		cityPath, err = resolveCity()
	}
	if err != nil {
		fmt.Fprintf(stderr, "gc register: %v\n", err) //nolint:errcheck
		return 1
	}

	// Verify it's a city directory (city.toml is the defining marker).
	if _, sErr := os.Stat(filepath.Join(cityPath, "city.toml")); sErr != nil {
		fmt.Fprintf(stderr, "gc register: %s is not a city directory (no city.toml found)\n", cityPath) //nolint:errcheck
		return 1
	}

	reg := supervisor.NewRegistry(supervisor.RegistryPath())
	if err := reg.Register(cityPath); err != nil {
		fmt.Fprintf(stderr, "gc register: %v\n", err) //nolint:errcheck
		return 1
	}
	fmt.Fprintf(stdout, "Registered city '%s' (%s)\n", filepath.Base(cityPath), cityPath) //nolint:errcheck
	return 0
}

func newUnregisterCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unregister [path]",
		Short: "Remove a city from the machine-wide supervisor",
		Long: `Remove a city from the machine-wide supervisor registry.

If no path is given, unregisters the current city (discovered from cwd).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if doUnregister(args, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	return cmd
}

func doUnregister(args []string, stdout, stderr io.Writer) int {
	var cityPath string
	var err error
	if len(args) > 0 {
		cityPath, err = filepath.Abs(args[0])
	} else {
		cityPath, err = resolveCity()
	}
	if err != nil {
		fmt.Fprintf(stderr, "gc unregister: %v\n", err) //nolint:errcheck
		return 1
	}

	reg := supervisor.NewRegistry(supervisor.RegistryPath())
	if err := reg.Unregister(cityPath); err != nil {
		fmt.Fprintf(stderr, "gc unregister: %v\n", err) //nolint:errcheck
		return 1
	}
	fmt.Fprintf(stdout, "Unregistered city '%s' (%s)\n", filepath.Base(cityPath), cityPath) //nolint:errcheck
	return 0
}

func newCitiesCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cities",
		Short: "List registered cities",
		Long:  `List all cities registered with the machine-wide supervisor.`,
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if doCities(stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	return cmd
}

func doCities(stdout, stderr io.Writer) int {
	reg := supervisor.NewRegistry(supervisor.RegistryPath())
	entries, err := reg.List()
	if err != nil {
		fmt.Fprintf(stderr, "gc cities: %v\n", err) //nolint:errcheck
		return 1
	}

	if len(entries) == 0 {
		fmt.Fprintln(stdout, "No cities registered. Use 'gc register' to add a city.") //nolint:errcheck
		return 0
	}

	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPATH") //nolint:errcheck
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%s\n", e.Name(), e.Path) //nolint:errcheck
	}
	tw.Flush() //nolint:errcheck
	return 0
}
