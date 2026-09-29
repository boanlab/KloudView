# Terminal security

KloudView terminal sessions require RBAC access, a short-lived single-use stream ticket, and approval by a second user. Self-approval takes the `terminal:approve-self` grant, which is granted by name and never inherited: a role holding `*:*` does not carry it. Both interactive PTY input and queued single commands are screened by the server and the agent before execution.

## Execution identity

The systemd unit runs the agent as the unprivileged `kloudview` account. Both the interactive PTY and queued single commands inherit that account by default. `KLOUDVIEW_TERMINAL_USER` can select a different account when the agent has permission to change identities; failure to change identity rejects execution instead of falling back to the agent's own identity.

## Command policy

The baseline policy blocks recursive forced deletion of the root filesystem, filesystem formatting and wiping, raw device writes, and host shutdown commands. It applies to interactive input and to queued single commands, and every line of a multi-line command is checked independently so a destructive statement cannot hide after the first line. It is a guardrail in addition to operating-system permissions, not a shell sandbox.

## Recording

Input and output are stored as ordered audit events after common password, token, secret, and bearer credential patterns are replaced with `[REDACTED]`. Each session is limited to 1 MiB and retained for 30 days. Expired data is removed lazily during recording access or append operations. Authorized operators can replay, download, or permanently delete a recording from Remote shell.

Masking is performed per stream message. Applications that emit a secret across multiple output chunks can evade pattern matching; sensitive commands should avoid printing credentials and production deployments should add a dedicated secret-filtering pipeline before extending retention.
