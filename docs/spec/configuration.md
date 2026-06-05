# Configuration

**Project:** agent-builder
**Last updated:** 2026-06-04

Every knob the system exposes — env vars, config files, runtime parameters, deployment settings. Each entry is a public contract: changes to defaults or accepted values are observable.

Not in this file:
- What gets configured (the behaviors live in [behaviors.md](behaviors.md))
- How values get into the process (the parsing is in code; the *contract* is here)

---

## Configuration files

### File: <name> (e.g. `config.toml`, `orb.toml`, `app.yaml`)

- **Location:** absolute or relative path the system looks for, search order if multiple
- **Format:** TOML / YAML / JSON / .env
- **Required vs optional:** what happens if the file is missing
- **Reload behavior:** loaded once at startup vs. watched for changes

#### Schema

| Key | Type | Default | Required | Effect |
|-----|------|---------|----------|--------|
| `section.key` | string | `"value"` | no | what this changes |
| | | | | |

#### Example

```toml
[section]
key = "value"
```

> Add one section per config file. For complex schemas, paste the canonical config struct definition (e.g. a Pydantic model, a Rust struct with serde derives) so this stays the source of truth.

---

## Environment variables

> Variables read from the process environment. Distinguish required-at-startup from optional overrides.

| Variable | Type | Default | Required | Effect |
|----------|------|---------|----------|--------|
| `EXAMPLE_VAR` | string | — | yes | what this controls |
| | | | | |

**Hook profile env vars** (consumed by `.claude/scripts/`, not the application itself):
- `CLAUDE_HOOK_PROFILE` — `minimal` / `standard` / `strict` (default `standard`)
- `CLAUDE_DISABLED_HOOKS` — comma-separated list of hook names to disable

---

## Runtime flags

> CLI flags that affect runtime mode rather than acting like commands. List here if [interfaces.md](interfaces.md) doesn't already cover them — avoid duplication. Cross-reference rather than restate.

---

## Secrets

> Sensitive configuration that lives **outside** the repo. Never commit values; document only the names and where they come from.

| Secret | Source | Used for |
|--------|--------|----------|
| `ANTHROPIC_API_KEY` | `.env` (Docker) or `sbx secret set -g anthropic` (Sandbox) | Claude API access in Docker / Sandbox env |
| `GIT_TOKEN` | `.env` (Docker) or `sbx secret set -g github` (Sandbox) | Pushing commits from inside Docker / Sandbox |
| | | |

**Rule:** secrets are never pasted into the chat, never logged, and never written into the repo. The `protect-secrets` hook blocks writes to common credential filenames.

---

## Deployment configuration

> If the project has a deployment story (Docker, devcontainer, sandbox), document the runtime contract here: ports exposed, volumes mounted, image tags, resource expectations.

| Aspect | Value | Notes |
|--------|-------|-------|
| Container image | | |
| Ports exposed | | |
| Volumes / mounts | | |
| Resource floor (CPU / RAM / disk) | | |

---

## Defaults policy

> The rule for what constitutes a sensible default. Examples:
>
> - "Defaults are safe — never start with destructive behavior enabled by default."
> - "Defaults match production — local dev should look like prod unless explicitly overridden."
>
> One paragraph is enough. This is the principle that adjudicates "what should the default be?" arguments.

