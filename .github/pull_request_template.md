## Summary

- Explain the change and why it is needed.

## Review focus

- What should reviewers verify most carefully?
- What is deliberately outside this pull request's scope?
- What could fail if this change is wrong?

## Reproduction or verification

- Give the shortest command or sequence that reproduces the old behavior and
  verifies the new behavior.

## Testing

- [ ] `make check`
- [ ] `make check-docs` if docs, navigation, or links changed
  > **Note:** `docs/` is authored for [docs.gascityhall.com](https://docs.gascityhall.com) (Mintlify), not for direct GitHub viewing. Use extensionless page links (e.g. `/tutorials/01-beads`, not `/tutorials/01-beads.md`). If something looks broken on GitHub but works on the live site, that's intentional.
- [ ] `make test-integration` if runtime, controller, or workflow behavior changed

## Checklist

- [ ] Linked an issue, or explained why one is not needed
- [ ] Added or updated tests for behavior changes
- [ ] Updated docs for user-facing changes
- [ ] Called out breaking changes or migration notes

## Review record

Each reviewer records the exact head SHA, what they inspected, checks they ran,
and known gaps. See
[REVIEWING.md](https://github.com/gastownhall/gascity/blob/main/REVIEWING.md).

- Head SHA:
- Inspected:
- Checks run:
- Not checked / known gaps:
- Material findings resolved or explicitly declined:
