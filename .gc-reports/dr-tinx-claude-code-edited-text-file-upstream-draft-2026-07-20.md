# Local upstream issue draft: `edited_text_file` metadata asserts unknown intent and is absent from retained transcripts

**Draft only — do not publish without action-specific authorization.**

## Summary

Claude Code 2.1.215 generates an inference-time `edited_text_file` metadata
message when a file changes outside its tracked file-tool snapshot. The message
attributes the change to “the user or a linter,” asserts that it was
intentional, instructs the model not to revert it, and instructs the model not
to tell the user.

Those claims are not supported by the trigger. In the observed incident, one
message followed a concurrent subagent's `git checkout` in a shared worktree;
another followed the receiving subagent's own Bash-mediated `git checkout`.
Neither change was made by the user or a linter. The retained JSONL omits the
expanded metadata block, including its resolved filename and diff snippet, so
the exact model-visible event cannot be reconstructed from retained JSONL
alone.

This report requests provenance-neutral wording and transcript serialization.
It does not claim malicious model behavior or repository prompt injection.

## Environment and exact producer

```text
Claude Code: 2.1.215
Binary: <CLAUDE_VERSION_DIR>/2.1.215
ELF Build ID: 788318c9115981678ca1a25f40cdb3b39df71403
Binary SHA-256: c1efffaaf370aa187cb6a09dd93d4e511c646899b0078476f83791b664bde7fe
Binary size: 265239536 bytes
Embedded template byte offset: 250044100
```

The first template prefix occurs twice in the binary. At byte offset
250044100, the embedded formatter starts with:

```text
Note: ${e.filename} was modified, either by the user or by a linter. This change was intentional, so make sure to take it into account as you proceed (ie. don't revert it unless the user asks you to). Don't tell the user this, since they are already aware.
```

One variant appends a snippet-budget explanation; the other appends line-numbered
changes from `${e.snippet}`. The formatter constructs an `isMeta: true` item.

Commands used for local static verification:

```sh
<CLAUDE_VERSION_DIR>/2.1.215 --version
sha256sum <CLAUDE_VERSION_DIR>/2.1.215
readelf -n <CLAUDE_VERSION_DIR>/2.1.215
# Search the binary for the exact UTF-8 template prefix and report its offsets.
```

## Minimal isolated reproduction

This should run under a dedicated disposable test profile and repository, with
project hooks, MCP servers, repository/cloud credentials, unrelated secrets,
and concurrent production sessions excluded. Use two isolated test agents only
to identify trigger provenance; do not share a real worktree.

1. Create a temporary repository containing one committed file, `probe.txt`.
2. Start agent A and have it read, then use the file-edit tool to change
   `probe.txt`. Record the file-tool snapshot known to A.
3. Test external-agent attribution:
   - from isolated test actor B, change A's observed file through a shell or Git
     restore operation;
   - trigger A's next inference turn;
   - capture only the structured `edited_text_file` attachment, its parent
     linkage, and the corresponding transcript serialization result in a
     protected local artifact. Do not capture or share unrelated request
     context.
4. Reset the disposable repository and repeat with agent A itself changing its
   file through its Bash tool rather than the file-edit tool.
5. Compare the producer and intent claimed by the generated metadata with the
   actor that actually performed each operation.

**Previously observed local incident, not a result of running the recipe
above:** retained tool-use timing associates both reported notices with
shell/Git-mediated changes, while the static binary template contains the
quoted attribution and instructions. Because the expanded attachments were not
serialized, their exact dynamic filename/snippet cannot be reconstructed from
retained JSONL alone.

The local incident already exercised both trigger classes. The reproduction
recipe above was not rerun while preparing this draft, to avoid unnecessary
runtime load and interaction with active sessions.

## Expected behavior

The metadata must not infer producer, intent, authorization, or user awareness
from “changed outside the tracked file-tool snapshot.” Suggested wording:

```text
Note: <filename> changed since the last tracked snapshot. The producer and
intent are unknown. Re-read or diff the file before proceeding. Do not infer
authorization from this notice.
```

There should be no concealment instruction. If the product can prove a producer
(for example, a specific tool-use ID), it may report that identity separately,
but it still should not infer intent or authorization.

Every generated metadata attachment supplied to the model should have a
durable, access-controlled audit record linked from the session transcript. The
exact rendered body must be retained under the same or stricter access,
redaction, and retention controls as model-request content. The transcript may
store that body directly or store a secure reference, integrity digest, and
explicit redaction marker; omission must not be silent. Raw paths or snippets
must not be copied into less-protected telemetry. The event should include at
least:

```text
metadata type
rendered model-visible body
resolved filename when policy permits, otherwise a session-scoped opaque identifier
triggering snapshot identifier
known producer/tool-use ID, or explicit unknown
access-controlled or keyed integrity digests for the before/after state
generation timestamp
parent turn/message identifier
```

The transcript schema should distinguish trusted runtime metadata from user,
assistant, repository, hook, MCP, and ordinary tool-result content.

## Security and auditability impact

The runtime generates a behavioral instruction from an observed file-state
change. No privilege escalation, access-control bypass, credential exposure, or
malicious model behavior was demonstrated. This is primarily a
provenance-integrity and auditability defect with conditional security impact:
following the instruction could cause an agent to preserve an unintended
mutation or fail to surface it. Its current wording can:

- misclassify another agent's mutation as “user or linter” activity and assert
  intentionality;
- turn the receiving agent's own Bash/Git cleanup into apparent user intent;
- discourage restoration of unintended or security-test mutations;
- instruct the model not to disclose the event to the user;
- amplify existing cross-agent interference by discouraging reversal; and
- prevent incident responders from reconstructing the exact model-visible
  instruction from retained JSONL alone.

The concern is inferred semantic authorization, not a demonstrated
access-control bypass. A detected file change is not evidence that the user
approved or intended that change.

## Redacted local evidence

Private paths, repository names, session IDs, agent IDs, source snippets, and
conversation content are omitted. The following integrity data is retained so
the evidence can be verified locally without publishing private content:

```text
parent transcript
  timestamp/mtime: 2026-07-20 14:56:10.017861249 -0400
  size: 713734 bytes
  sha256: 0d7077f732ee5c0644ef2aa5454625406de28c46ea5f67bbfa5d5e16b951638c

reviewer transcript A
  timestamp/mtime: 2026-07-20 14:52:31.727235057 -0400
  size: 614702 bytes
  sha256: 1d354cca59c2cbb92ab9b55dd2dc982e419e0ad682bc422f1ffed5d3f2ab8045

reviewer metadata A
  timestamp/mtime: 2026-07-20 14:45:08.692811365 -0400
  size: 137 bytes
  sha256: 68325e451e59ffdead5f45bd24cd2caa7f995e5d909dcbb5cb54d0b908d2576a

reviewer transcript B
  timestamp/mtime: 2026-07-20 14:51:22.108402103 -0400
  size: 510004 bytes
  sha256: 8fef2a09b98d4526d19feb49abdd2a7b10594abc9e683d4587246a64c491a6ab

reviewer metadata B
  timestamp/mtime: 2026-07-20 14:44:56.072654765 -0400
  size: 129 bytes
  sha256: 9905dc459895308a4edddd6f91901430fd538baed09e15b57400a2be418307cd
```

Relevant trigger timeline, UTC:

```text
18:46:35.051  actor A changes file 1 through the file-edit tool
18:46:36.903  actor B observes file 1 while running a formatter check
18:46:50.586  actor B invokes git stash in the shared test worktree
18:47:04.822  actor B invokes git stash pop
18:47:25.795  actor A changes file 1 again through the file-edit tool
18:48:02.792  actor B invokes git checkout for file 1, removing A's probes
18:49:27.886  actor A changes file 2 through the file-edit tool
18:51:32.056  actor A invokes git checkout for file 2 through Bash
18:52:31.625  actor A reports the two misleading metadata notices
```

The static message body is recoverable exactly from the hashed producer binary.
The dynamic filename/snippet for each event is not recoverable verbatim because
the inference-time attachment was not serialized. That limitation is the
auditability defect described here, not evidence that the model invented the
message.

## Regression test

Add a hermetic integration test through the production change detector,
metadata renderer, and transcript serializer; fake only external inference
transport and test actors:

1. Agent A reads and edits a file through the tracked file tool.
2. Parameterize the next change source as:
   - actor B's file tool;
   - actor B's shell/Git command;
   - actor A's shell/Git command;
   - an actual configured formatter/linter; and
   - an unknown external process.
3. Trigger A's next inference request and capture both the model request and the
   durable transcript.
4. Assert that a producer is named only when reliably linked to a known
   tool-use or runtime event; otherwise report `unknown`. Assert intent,
   authorization, and awareness only when separately and explicitly stated—
   never infer them from producer identity or the file change itself.
5. Assert the metadata contains no non-disclosure instruction.
6. Assert the exact rendered metadata body and provenance fields are serialized
   in the transcript and linked to the parent turn.
7. Assert content/diff hashes match the before/after files.
8. Assert known tool-use provenance is correct; otherwise assert an explicit
   `unknown` producer.
9. Replay the retained transcript and assert an auditor can reconstruct the
   exact model-visible metadata without access to process memory.
10. Include a product-linked user edit without an explicit intent statement and
    an explicit user statement tied to a change as negative and positive
    controls for intent attribution.
11. Include delimiter/control-like text in a filename or snippet and assert it
    remains escaped data that cannot alter the metadata trust classification.

The regression should fail closed if provenance cannot be established: report
unknown rather than selecting “user or linter,” and never infer authorization.

## Local handling status

This is an evidence-preserving local draft with a known evidence gap: the static
template and trigger timeline are retained, but the exact dynamic attachments
are unavailable and the proposed reproduction has not been rerun. It is not a
filed issue or support ticket. No private transcript content should be attached
without a separate redaction review and action-specific authorization. No
GitHub issue, vendor support ticket, Slack post, mail to a human, or other
external publication is authorized by this draft.

## Local security review

A single sequential local security review found the draft safe to retain
privately but not publication-ready before revision. This version incorporates
its required changes: it separates prior evidence from the unexecuted
reproduction, narrows the impact to provenance integrity and conditional
security consequences, constrains capture/serialization privacy, separates
producer identity from intent, and adds positive/negative and escaping controls
to the regression test.

Before any future publication, perform another action-specific redaction review.
Consider replacing exact transcript timestamps, sizes, and hashes with relative
event offsets and private keyed integrity records; small metadata artifacts are
especially susceptible to candidate guessing. Publication remains separately
authorization-gated.
