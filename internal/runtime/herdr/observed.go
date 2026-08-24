package herdr

import "context"

// This file holds the one view of "every gc session herdr can currently see",
// shared by activity stamping (activity.go) and session-event attribution
// (events.go). Both need the same join and must not drift apart.
//
// herdr offers two overlapping listings and neither is sufficient alone:
//
//   - agent.list names panes, but only ones gc launched as a detected agent
//     KIND (`agent start --kind claude ...`). A raw command pane and a bare
//     shell pane never appear in it — verified live on herdr 0.8.0, where
//     agent.list answers {"agents":[]} for a pane running `sleep 120`.
//   - pane.list sees every pane, carrying the same pane_id, agent_status and
//     revision fields, but no gc session name.
//
// The sidecar binding closes the gap: it is where gc already records
// name → pane for EVERY session it starts, raw ones included. ListRunning has
// merged the two halves this way since the binding was introduced; this is
// the same merge, factored out so the newer consumers get it too.

// observedSessions returns one snapshot keyed by gc session name. The agent
// registry wins where it has a name (it is authoritative and carries the
// detected status); bound panes fill in everything it cannot see. The
// returned agentInfo always has Name set to the gc session name, so callers
// need no second lookup.
//
// pane.list is fetched only when the binding index holds a pane the registry
// did not already account for, so a deployment running only detected agents
// keeps its previous one-call-per-poll cost.
func (p *Provider) observedSessions(ctx context.Context) (map[string]agentInfo, error) {
	agents, err := p.c.sockAgentList(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]agentInfo, len(agents))
	covered := make(map[string]bool, len(agents))
	for _, a := range agents {
		if a.Name == "" || a.PaneID == "" {
			continue
		}
		out[a.Name] = a
		covered[a.PaneID] = true
	}

	bound := p.boundPaneIndex()
	missing := false
	for pane := range bound {
		if !covered[pane] {
			missing = true
			break
		}
	}
	if !missing {
		return out, nil
	}

	panes, err := p.c.sockPaneList(ctx)
	if err != nil {
		// The registry half is still a truthful snapshot; degrade to it
		// rather than failing the whole poll and freezing every session.
		return out, nil
	}
	for _, pn := range panes {
		name := bound[pn.PaneID]
		if name == "" || covered[pn.PaneID] {
			continue
		}
		pn.Name = name
		out[name] = pn
	}
	return out, nil
}
