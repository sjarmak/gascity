# Agent Management

Agents are the workers in a Gas City workspace. Each runs in its own
session (tmux pane, container, etc).

## Listing and inspecting

```
gc agent list                          # List all agents and their status
gc agent peek <name>                   # Capture recent output from agent session
gc agent peek <name> 100               # Peek with custom line count
gc agent status <name>                 # Show detailed agent status
```

## Adding agents

```
gc agent add --name <name>             # Add agent to city root
gc agent add --name <name> --dir <rig> # Add agent scoped to a rig
```

## Communication

```
gc agent nudge <name> <message>        # Send a message to wake/redirect agent
gc agent attach <name>                 # Attach to agent's live session
gc agent claim <name> <bead-id>        # Put a bead on agent's hook
```

## Sessions from templates

Every configured template can now spawn sessions directly.

Use the session commands directly:

```
gc session new <template>              # Create and attach to a new session
gc session new <template> --no-attach  # Create a detached background session
gc session suspend <id-or-template>    # Suspend a session
gc session close <id-or-template>      # Close a session permanently
```

The legacy aliases still exist:

```
gc agent start <template>              # Alias for gc session new <template> --no-attach
gc agent start <template> --name foo   # Same alias; --name becomes the session title
gc agent stop <session-id-or-name>     # Alias for gc session suspend
gc agent destroy <session-id-or-name>  # Alias for gc session close
```

When multiple sessions exist for the same template, use the session ID.

## Pools

Pools still control controller-managed worker capacity. Pool `max`
limits pool-managed workers, not manually created interactive sessions.

## Lifecycle

```
gc agent suspend <name>                # Suspend agent (reconciler skips it)
gc agent resume <name>                 # Resume a suspended agent
gc agent drain <name>                  # Signal agent to wind down gracefully
gc agent undrain <name>                # Cancel drain
gc agent drain-check <name>            # Check if agent has been drained
gc agent drain-ack <name>              # Acknowledge drain (agent confirms exit)
gc agent request-restart <name>        # Request graceful restart
gc agent kill <name>                   # Force-kill agent session
```
