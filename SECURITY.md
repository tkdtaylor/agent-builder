# Security Policy

## Supported versions

agent-builder has not yet cut a tagged release. Until a `v1.0.0` ships, only the
current `main` branch receives security fixes. This table will be filled in once
releases begin.

| Version | Security fixes |
|---------|---------------|
| `main` (pre-release) | ✅ Yes |

## Reporting a vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**
A public report exposes the flaw to everyone before a fix is available.

### Option 1 — GitHub private vulnerability reporting (preferred)

Use GitHub's built-in private advisory flow:
<https://github.com/tkdtaylor/agent-builder/security/advisories/new>

GitHub keeps the report confidential and notifies only maintainers.

### Option 2 — Email

Send a report to <tools@taylorguard.me> with:

- A concise description of the vulnerability
- Reproduction steps (configuration, target repo shape, command line)
- The commit or `main` state you observed it on
- Your assessment of severity (CVSS or plain English is fine)
- Any suggested mitigations

Encrypt with PGP if you prefer — open an issue requesting a public key and
we will publish one.

## Response expectations

- **Acknowledgement:** within 7 days of receipt.
- **Status update:** within 30 days (triaged, confirmed, or declined with
  reasoning).
- **Fix shipped:** within 90 days for confirmed vulnerabilities. Critical
  issues (CVSS ≥ 9.0) target a 14-day patch window. If more time is needed
  we will coordinate a disclosure date with the reporter.

## Scope

**In scope:**

- The `agent-builder` orchestrator binary (`cmd/agent-builder`)
- The verification gate and its blocking checks (build, lint, `dep-scan`,
  `code-scanner`)
- The execution-box containment profile: the rootless-Podman launcher, the
  egress allowlist, and any escape from `--cap-drop=all` / non-root / disabled-DNS
  to broader host or network access
- The secret-source / vault-brokering seam (token handling, `internal/secrets`,
  `internal/vault`)
- A bypass that lets an executed task read executor tokens off disk or exfiltrate
  them past the egress allowlist

**Out of scope:**

- Vulnerabilities in the ecosystem blocks consumed over their published
  contracts (`exec-sandbox`, `vault`, `policy-engine`, `audit-trail`) — report
  those to the respective block's repository.
- Vulnerabilities in the provider CLIs (Claude, Gemini) or local LLM harnesses
  driven as subprocesses.
- Bugs in Podman, gVisor, or the host container runtime.
- Findings that require an already-compromised host or operator-supplied
  malicious configuration.

## Recognition

Reporters are credited in the changelog and release notes unless they
request anonymity. We do not currently offer a bug bounty.

## Maintainer note

After merging this file, enable **Settings → Code security and analysis →
Private vulnerability reporting** in the GitHub repository settings so the
"Report a vulnerability" button is visible on the repo page.
