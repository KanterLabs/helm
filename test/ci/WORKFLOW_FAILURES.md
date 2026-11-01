# Workflow failure inventory

Written before the router integration. These journeys parse real workflow YAML,
resolve local actions and reusable workflows, and retain sanitized JSON captures
under `.artifacts/ci-workflows`. They never contact GitHub or run deployment jobs.

- A draft/ready transition fails to rerun classification, or a branch push outside
  beta/main can enter the trusted router.
- A manual run has no explicit validation scope or can publish/deploy.
- A planner failure silently skips every historical required check and permits a
  misleading green merge result.
- The gate lacks a dependency on a selected validation job or ignores its result.
- Registry write credentials remain in the image build/scan context; publication
  occurs before validation, on a PR, or outside protected branch pushes.
- Publication succeeds without an immutable validation receipt; a deployment
  accepts an image without successful publication and receipt dependencies.
- Extracting a local action/workflow hides a missing security check, credential,
  protected environment, pause switch, exact branch-tip check, or noncancel lock.
- Browser routing admits public PRs to the reviewed homelab runner selector,
  changes its reviewed shape, or increases Playwright workers beyond four.
- Focused execution loses the safe selected-suite input or full execution stops
  using the real browser suite coordinator and evidence verifier.
- A deployment supersedes a successor branch tip, escapes its protected
  environment, or runs after a pause request.

The structural checks complement classifier and receipt CLI journey tests; they
prove that the workflow actually wires those programs and their evidence into
its complete validation/publication/deployment dependency graph.

## Extracted image publisher failure inventory

Before writing the publisher helper, fake-Docker CLI journeys must demonstrate:
PR/manual/other branch events and malformed revisions invoke no Docker command;
a changed archive fails checksum verification before loading; a loaded image with
the wrong source revision cannot log in or push; invalid/wrong-registry immutable
digests cannot produce a release image output; registry tokens travel only on
login stdin and never enter logs/argv; and transient registry credentials are
removed after both successful publication and a failed push. Fixtures use only
synthetic image bytes and credentials, and no real Docker daemon or network.

A trusted same-source promotion deliberately skips fresh browser/race jobs.
Receipt and protected deployment admission must survive those skipped needs while
still requiring successful gate/publication/receipt results; implicit `success()`
must not silently turn a verified promotion into an unreported no-op.
