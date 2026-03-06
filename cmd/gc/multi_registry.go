package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

// multiRegistry queries and mutates multi-instance agent beads in a city-level
// bead store. Each multi instance is tracked as an open bead with labels
// encoding its template, instance name, and state.
type multiRegistry struct {
	store beads.Store
}

// MultiInstance is the in-memory view of a multi-instance bead.
type MultiInstance struct {
	BeadID   string
	Template string // template qualified name
	Name     string // instance name
	State    string // "running" or "stopped"
	Created  time.Time
}

func newMultiRegistry(store beads.Store) *multiRegistry {
	return &multiRegistry{store: store}
}

// start creates a new instance or resumes a stopped one. Returns the instance
// and whether it was resumed (vs newly created).
func (r *multiRegistry) start(template, name string) (MultiInstance, bool, error) {
	mi, err := r.findInstance(template, name)
	if err != nil {
		return MultiInstance{}, false, err
	}
	if mi != nil {
		if mi.State == "running" {
			return MultiInstance{}, false, fmt.Errorf("instance %s/%s is already running", template, name)
		}
		// Resume stopped instance: swap state label.
		err := r.store.Update(mi.BeadID, beads.UpdateOpts{
			RemoveLabels: []string{"state:stopped"},
			Labels:       []string{"state:running"},
		})
		if err != nil {
			return MultiInstance{}, false, fmt.Errorf("resuming instance %s/%s: %w", template, name, err)
		}
		mi.State = "running"
		return *mi, true, nil
	}
	// Create new instance bead.
	b, err := r.store.Create(beads.Bead{
		Type:   "agent",
		Title:  "multi:" + template + "/" + name,
		Labels: []string{"multi:" + template, "instance:" + name, "state:running"},
	})
	if err != nil {
		return MultiInstance{}, false, fmt.Errorf("creating instance %s/%s: %w", template, name, err)
	}
	return MultiInstance{
		BeadID:   b.ID,
		Template: template,
		Name:     name,
		State:    "running",
		Created:  b.CreatedAt,
	}, false, nil
}

// stop marks a running instance as stopped.
func (r *multiRegistry) stop(template, name string) error {
	mi, err := r.findInstance(template, name)
	if err != nil {
		return err
	}
	if mi == nil {
		return fmt.Errorf("instance %s/%s not found", template, name)
	}
	if mi.State != "running" {
		return fmt.Errorf("instance %s/%s is not running (state: %s)", template, name, mi.State)
	}
	return r.store.Update(mi.BeadID, beads.UpdateOpts{
		RemoveLabels: []string{"state:running"},
		Labels:       []string{"state:stopped"},
	})
}

// destroy closes the instance bead. The instance must be stopped first.
func (r *multiRegistry) destroy(template, name string) error {
	mi, err := r.findInstance(template, name)
	if err != nil {
		return err
	}
	if mi == nil {
		return fmt.Errorf("instance %s/%s not found", template, name)
	}
	if mi.State == "running" {
		return fmt.Errorf("instance %s/%s is running; stop it first", template, name)
	}
	return r.store.Close(mi.BeadID)
}

// instancesForTemplate returns all open instances for a template.
func (r *multiRegistry) instancesForTemplate(template string) ([]MultiInstance, error) {
	all, err := r.store.ListByLabel("multi:"+template, 0)
	if err != nil {
		return nil, err
	}
	var result []MultiInstance
	for _, b := range all {
		if b.Status != "open" {
			continue
		}
		mi := beadToInstance(b, template)
		if mi != nil {
			result = append(result, *mi)
		}
	}
	return result, nil
}

// findInstance looks up a specific instance by template and name.
// Returns nil (not error) if not found.
func (r *multiRegistry) findInstance(template, name string) (*MultiInstance, error) {
	all, err := r.store.ListByLabel("multi:"+template, 0)
	if err != nil {
		return nil, err
	}
	for _, b := range all {
		if b.Status != "open" {
			continue
		}
		for _, l := range b.Labels {
			if l == "instance:"+name {
				mi := beadToInstance(b, template)
				return mi, nil
			}
		}
	}
	return nil, nil
}

// nextName generates the next sequential instance name for a template.
// Scans all beads (including closed/destroyed) so names are never reused.
func (r *multiRegistry) nextName(template string) (string, error) {
	all, err := r.store.ListByLabel("multi:"+template, 0)
	if err != nil {
		return "", err
	}
	maxN := 0
	prefix := template + "-"
	// Also check for rig-scoped templates where prefix would be just the name part.
	_, baseName := splitTemplateName(template)
	basePrefix := baseName + "-"
	for _, b := range all {
		for _, l := range b.Labels {
			if !strings.HasPrefix(l, "instance:") {
				continue
			}
			instName := strings.TrimPrefix(l, "instance:")
			// Try matching against both template-N and baseName-N patterns.
			for _, p := range []string{prefix, basePrefix} {
				if strings.HasPrefix(instName, p) {
					suffix := instName[len(p):]
					if n, err := strconv.Atoi(suffix); err == nil && n > maxN {
						maxN = n
					}
				}
			}
		}
	}
	return fmt.Sprintf("%s-%d", baseName, maxN+1), nil
}

// allRunning returns all running multi instances across all templates.
func (r *multiRegistry) allRunning() ([]MultiInstance, error) {
	all, err := r.store.List()
	if err != nil {
		return nil, err
	}
	var result []MultiInstance
	for _, b := range all {
		if b.Status != "open" || b.Type != "agent" {
			continue
		}
		template := ""
		name := ""
		state := ""
		for _, l := range b.Labels {
			switch {
			case strings.HasPrefix(l, "multi:"):
				template = strings.TrimPrefix(l, "multi:")
			case strings.HasPrefix(l, "instance:"):
				name = strings.TrimPrefix(l, "instance:")
			case strings.HasPrefix(l, "state:"):
				state = strings.TrimPrefix(l, "state:")
			}
		}
		if template != "" && name != "" && state == "running" {
			result = append(result, MultiInstance{
				BeadID:   b.ID,
				Template: template,
				Name:     name,
				State:    state,
				Created:  b.CreatedAt,
			})
		}
	}
	return result, nil
}

// beadToInstance extracts a MultiInstance from a bead with a known template.
func beadToInstance(b beads.Bead, template string) *MultiInstance {
	name := ""
	state := ""
	for _, l := range b.Labels {
		switch {
		case strings.HasPrefix(l, "instance:"):
			name = strings.TrimPrefix(l, "instance:")
		case strings.HasPrefix(l, "state:"):
			state = strings.TrimPrefix(l, "state:")
		}
	}
	if name == "" {
		return nil
	}
	return &MultiInstance{
		BeadID:   b.ID,
		Template: template,
		Name:     name,
		State:    state,
		Created:  b.CreatedAt,
	}
}

// splitTemplateName splits a qualified name like "rig/name" into ("rig", "name").
// If there's no slash, returns ("", name).
func splitTemplateName(qn string) (string, string) {
	if i := strings.LastIndex(qn, "/"); i >= 0 {
		return qn[:i], qn[i+1:]
	}
	return "", qn
}
