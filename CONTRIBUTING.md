# Contributing

Agree on the problem and scope in an issue before changing behavior. A pull request should focus on a single purpose and include the relevant tests and documentation.

## Basic workflow

1. Fork the repository and create a working branch
2. Write code and tests
3. Run `make test`, and `npm test` in `apps/web` when the console changes
4. Open a pull request describing the change's purpose and how you verified it

## Style

Comments state constraints the code cannot show on its own, in a short noun phrase.
They describe the current behaviour, not how it came to be: no change history, no
version notes, no "previously".

The console has no build step and no runtime dependencies. Keep it that way: plain ES
modules, one `render()` and one `bind()`, and translation through the existing
dictionary rather than per-view string handling.

## Agent and server contract

`apps/server` and `apps/agent` are separate Go modules and cannot import each other,
so the values both sides must agree on live in `docs/contracts/agent-server.json`:
the protocol version, the capabilities an agent may advertise, the operation types it
can execute, and the log sources it can capture. Each side asserts against that file
in its own tests, and the Docker builds run those tests, so a value added to one side
alone fails the build rather than the fleet.
