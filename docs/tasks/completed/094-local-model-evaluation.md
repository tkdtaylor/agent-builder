# Task 094: Local-model evaluation

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** completed

## Goal

Empirically benchmark locally-runnable LLMs against the target hardware to pick the
**most capable model that stays responsive** (VRAM-resident sweet spot) for the local
registry entry. Output is config (`ModelID`, `Endpoint`), not code.

### Target hardware (probed 2026-06-27)

- **CPU:** Intel Core Ultra 9 185H (Meteor Lake) — 16 cores / 22 threads, 5.1 GHz
- **RAM:** 62 GiB (≈45 GiB available)
- **GPU:** NVIDIA RTX 4060 Laptop (Max-Q/Mobile), **8 GB VRAM** (driver 595.71.05) + Intel Arc iGPU
- **Disk:** ~2.7 TB free; **OS:** Ubuntu 26.04 LTS, x86_64; CUDA available

The **8 GB VRAM is the responsiveness ceiling.** Models or quantizations that fit
fully in VRAM run fast; offloaded layers degrade latency. The eval optimizes "most
capable that stays responsive," so the VRAM-resident sweet spot (≈7–14B at 4-bit, or
a small MoE) is the expected region — but the actual pick is the benchmark's output.

### Candidate models (starting list; executor may extend)

| Model | Quant | Est. VRAM | Notes |
|-------|-------|-----------|-------|
| Qwen2.5-Coder-7B-Instruct | Q4_K_M | ≈5 GB | Code-specialized, 7B |
| Qwen2.5-Coder-14B-Instruct | Q4_K_M | ≈9 GB | May need slight offload |
| DeepSeek-Coder-V2-Lite | Q4_K_M | ≈6 GB | MoE, code-specialized |
| Mistral-7B-Instruct-v0.3 | Q4_K_M | ≈5 GB | General, smaller |
| Llama-3.1-8B-Instruct | Q4_K_M | ≈5 GB | General, popular |
| Phi-3.5-mini-instruct | Q4_K_M | ≈3 GB | Very small, may lack agentic capability |

The list is a starting point. The executor should add any model that appears promising
based on current model releases at time of execution.

### Translation-proxy choice

The translation proxy must present an Anthropic-compatible endpoint. Known options:
- **LiteLLM** (Python, `pip install litellm`; `litellm --model ollama/model-name`)
- **claude-code-router** (purpose-built for Claude Code + local models)

The executor picks whichever is easier to operate on the target host. Document the
choice in the verify commit.

## Context

ADR 043 names the local-model evaluation as a distinct, hardware-specific task that
is "re-runnable and worth periodically revisiting." This task is the first run.

## Requirements

| Req ID     | Description                                                                                                                                                                                                                                                                    | Priority  |
|------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-094-01 | Benchmark at least three candidate models (from the list above) on the target hardware. Record TTFT, TPS, and VRAM usage for each. Acceptance bar: TTFT < 5s for a 200-token prompt, TPS > 10 tokens/s. Select the highest-capability model that meets both bars. | must have |
| REQ-094-02 | Verify the selected model drives the Claude Code CLI harness successfully via the translation proxy: `agent-builder run` completes a simple task (branch produced, gate passes for a trivial case). Record whether the harness plumbing (protocol translation) works. | must have |
| REQ-094-03 | Document the benchmark results and the selected model's `ModelID` and `Endpoint` in the verify commit. Add the recommended env-var config to `docs/spec/configuration.md` (or a dedicated note). Mark as re-runnable/periodic. | must have |

## Readiness gate

- [x] Test spec `094-local-model-evaluation-test-spec.md` exists (written first)
- [x] Task 091 merged (local entry + translation-proxy seam — provides the config
  plumbing this evaluation populates)
- [x] Task 092 merged (router — the routing path the local entry will participate in)
- [x] Inference server and translation proxy installed on target hardware
- [x] CUDA backend confirmed available (`nvidia-smi` shows RTX 4060)

## Acceptance criteria

- [x] [REQ-094-01] TC-094-01: ≥3 models benchmarked; TTFT/TPS/VRAM recorded; selected model meets the bars; results in verify commit
- [x] [REQ-094-02] TC-094-02: selected model drives `agent-builder run`; branch produced; gate passes for a trivial task — achieved via the native Ollama harness (ADR 051), which supersedes the original claude-CLI-via-translation-proxy mechanism. L6 PASSED on target host: `qwen3:8b`/`ollama-native` produced branch `task/001-add-product` (commit `f71191a`) with all 7 gate steps green
- [x] [REQ-094-03] TC-094-03: benchmark results + recommended config documented in verify commit + `docs/spec/configuration.md`; marked re-runnable

## Verification plan

- **Highest level achievable:** L6 — operator-run on the real hardware. No CI-
  automatable unit test exists for hardware-specific benchmarks.
- **Methodology (operator-run):**
  1. Install llama.cpp (CUDA build) or Ollama on the target host.
  2. Install a translation proxy (LiteLLM or claude-code-router).
  3. For each candidate model: load → benchmark TTFT/TPS/VRAM → record.
  4. Select the winner. Configure env vars. Run `agent-builder run` (simple task).
  5. Record results and config in the verify commit.
- **Evidence recorded in `Verified by` column:** benchmark methodology + model list +
  selected recommendation + config snippet.

## Out of scope

- Building an inference server or translation proxy (external tools).
- Automated CI benchmarks.
- Model fine-tuning.
- A general benchmarking framework.

## Dependencies

- Task 091 (local entry + translation-proxy seam — the plumbing to configure).
- Task 092 (router — the local entry participates in routing after this eval).
- Does NOT block tasks 092, 093, or 095 — can be worked in parallel with 092/093.
- Informs: task 095 (the routing spec for the coding-agent recipe; the local entry
  will be a real routing option after this eval sets its config).
