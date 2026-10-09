# Security Policy

## Supported Versions

Security fixes are provided for the latest minor release line. The previous minor
release line also receives security fixes for 90 days after the next minor release
is published. Older lines are unsupported.

| Version line                  | Status                                   |
| ----------------------------- | ---------------------------------------- |
| Latest minor (1.1.x after release) | Supported                           |
| Previous minor (1.0.x)        | Security fixes for 90 days after the next minor |
| Older                         | Unsupported                              |

## Reporting a Vulnerability

Please report suspected vulnerabilities privately through GitHub private security
advisories for this repository:

<https://github.com/Anubisx404/Extent/security/advisories/new>

Do not open public issues, discussions, or pull requests for undisclosed
vulnerabilities.

Include:

- A description of the vulnerability and its impact.
- Affected Extent version(s), OS, Go version, and Docker version if relevant.
- Reproduction steps, a proof of concept, or a minimal target repository.
- Any suggested remediation, if known.

We aim to acknowledge reports within 48 to 72 hours and to provide an initial
assessment within 5 business days. These are operational goals, not contractual
guarantees. Please coordinate with maintainers before any public disclosure so a
fix can be prepared and released.

## Threat Model

### Target repositories are untrusted input

Extent reads, analyzes, and in some modes modifies the repository you point it at.
Treat every target repository as untrusted input. Some commands execute code that
comes from the target (see the next section), and that code runs with your user's
permissions. Review a repository before analyzing it with `--dynamic` if you did not
write it, and run unfamiliar projects in a container or throwaway VM without
credentials.

Extent does not sandbox the processes it starts.

### Generated stack is local-only

The LGTM stack that `extent stack up` generates is intended for local use only.
Its published ports are bound to `127.0.0.1`, and the default Grafana admin
credentials are development defaults that you should change before exposing the
stack in any other way. Do not publish these ports to a network interface.

### Commands that execute external processes

Every process Extent starts is listed below, with the command that triggers it.

| Command | Process(es) started | What runs |
| ------- | ------------------- | --------- |
| `extent analyze --dynamic` | `node` | Loads each `.js` entrypoint of the target, so the target's module code runs. The child receives only a minimal environment (`PATH`, `HOME`/`USERPROFILE`, `SYSTEMROOT` on Windows, and `NODE_ENV=extent-analysis`), so secrets in your shell environment are not passed to it. Each run is capped at 3 seconds and 1 MiB of output. Failures are reported as warnings. |
| `extent deps --install` | `pnpm install`, `yarn install`, `npm install`, `python -m pip install -r requirements.txt`, `go mod tidy`, or `dotnet restore` | The package manager resolves and installs dependencies. Package install scripts and build hooks in the target can execute arbitrary code. |
| `extent instrument --mode deep --experimental --apply` | None in the current release | Writes the codemod bundle (`extent.codemods/`) into the target. The codemod runner that would execute Node, Python, .NET, and Java toolchains is not yet wired into the CLI, and the instrumenter refuses to run it outside a transaction. When it is enabled, it will run those toolchains against target source and mutate files outside Extent's file transaction, so use it only on a clean worktree after review. |
| `extent stack up`, `extent stack down`, `extent stack status` | `docker --version`, `docker compose version`, `docker info` (preflight); `docker compose config --quiet`, `up -d --force-recreate` (with `--wait` when requested), `down`, `ps --format json` | Validates, starts, stops, and inspects the generated Compose stack. |
| `extent apply --branch <name>` | `git` (`status --porcelain`, `rev-parse`, branch creation or switch) | Checks for uncommitted changes and creates or switches to the branch before writing files. |
| `extent doctor`, `extent verify` | None for the Docker and Git CLI checks, which only look up `docker` and `git` on `PATH`; the stack preflight runs the `docker` version and info commands listed above | Reports tool availability and generated-artifact health. These checks do not modify the target. |

This table is maintained by hand. Update it in the same change that adds any
command that launches an external process.

## Dependencies and Release Integrity

Release archives are published with a SHA-256 `checksums.txt`, which is signed
keylessly with Sigstore cosign, and each archive carries a GitHub build provenance
attestation. See `RELEASING.md` for how to verify a download.
