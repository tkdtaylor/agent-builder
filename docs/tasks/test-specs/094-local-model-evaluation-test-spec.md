# Test spec — Task 094: Local-model evaluation

**Linked task:** `docs/tasks/backlog/094-local-model-evaluation.md`
**Written:** 2026-06-27
**Status:** ready

## Context

ADR 043 calls for an empirical, hardware-specific benchmark of locally-runnable LLMs
against the target hardware (probed 2026-06-27):

- CPU: Intel Core Ultra 9 185H (16 cores / 22 threads, up to 5.1 GHz)
- RAM: 62 GiB (≈45 GiB available)
- GPU: NVIDIA RTX 4060 Laptop (Max-Q/Mobile), 8 GB VRAM + Intel Arc iGPU
- OS: Ubuntu 26.04 LTS, x86_64, CUDA available

The **8 GB VRAM is the responsiveness ceiling** — models (or quantizations) that fit
fully in VRAM run fast; models that spill to CPU/GPU offload degrade latency. The
eval optimizes "most capable that still stays responsive (VRAM-resident sweet spot)".

**This task produces config, not code.** The output is the `ModelID` and `Endpoint`
(translation-proxy URL) to use for the local entry in the registry. These values are
set in the operator's env-var config or deployment configuration.

Verification level: L5/L6 — operator-run benchmark on the real hardware. There is no
CI-automatable unit test for "which model is fastest on the RTX 4060 Laptop". The
verification plan records the benchmark methodology, the models tested, and the
selected recommendation.

## Requirements coverage

| Req ID     | Test cases                     | Covered? |
|------------|--------------------------------|----------|
| REQ-094-01 | TC-094-01                      | yes      |
| REQ-094-02 | TC-094-02                      | yes      |
| REQ-094-03 | TC-094-03                      | yes      |

## Pre-implementation checklist

- [ ] Benchmark methodology documented
- [ ] Models-to-evaluate list specified (see task)
- [ ] Acceptance criteria clear (responsiveness + capability bar)
- [ ] Output is config, not code

---

## Test cases

### TC-094-01 — Benchmark methodology: candidate models + quantizations

- **Requirement:** REQ-094-01
- **Level:** L6 (operator-run on target hardware)
- **Test file / harness:** N/A (operator-run; see verification plan)

**Input:** For each candidate model + quantization listed in the task file, run the
local inference server (llama.cpp or Ollama with CUDA backend) and send a
representative agentic prompt (e.g. "Write a Go function that reverses a string, with
unit test").

**Expected output:**
- Time-to-first-token (TTFT) and tokens-per-second (TPS) recorded.
- VRAM usage recorded (via `nvidia-smi`).
- Model fits in 8 GB VRAM without offloading (or VRAM headroom noted if partial
  offload is required).

**Acceptance bar:**
- TTFT < 5s for a 200-token prompt (operator-subjective "responsive" bar).
- TPS > 10 tokens/s for code generation (code completion must not feel like waiting).
- The selected model is the highest-capability model that meets both bars.

---

### TC-094-02 — Selected model drives the Claude CLI harness successfully

- **Requirement:** REQ-094-02
- **Level:** L6 (operator-run on target hardware)
- **Test file / harness:** N/A (operator-run)

**Input:** Start the translation proxy (LiteLLM or claude-code-router) pointed at the
selected local model's Ollama/llama.cpp endpoint. Set:
```
AGENT_BUILDER_REGISTRY_LOCAL_QWEN_ENABLED=true
AGENT_BUILDER_REGISTRY_LOCAL_QWEN_MODEL=<selected-model-id>
AGENT_BUILDER_REGISTRY_LOCAL_QWEN_ENDPOINT=http://localhost:8080
```
Run `agent-builder run` with the recipe's `RoutingSpec{MinCapability:0}` (or the
lowest tier available) to route to the local entry.

**Expected output:**
- The agent-builder executes a simple task (e.g. "create a file README.md") against
  a test worktree.
- The branch is produced; the gate passes (the local model generates valid, gate-
  passing Go code for a trivial task).
- The run record reflects `containment=exec-sandbox` and shows the local entry
  was selected.

**Note:** Gate failure is acceptable for a complex task; the bar is that the local
model successfully drives the Claude Code CLI protocol via the translation proxy —
i.e. the harness plumbing works. A gate failure on a hard task is a capability
limitation of the model, not a protocol failure.

---

### TC-094-03 — Recommendation documented in ops config / ADR note

- **Requirement:** REQ-094-03
- **Level:** L5 (document assertion)
- **Test file / harness:** N/A (document review)

**Input:** Review the verify commit for this task.

**Expected output:**
- The verify commit records:
  - The models benchmarked and their TTFT/TPS/VRAM measurements.
  - The selected model (`ModelID`) and recommended translation proxy.
  - The env-var config for the local entry (`AGENT_BUILDER_REGISTRY_LOCAL_*`).
  - Whether CUDA backend was used (vs. CPU-only fallback).
- A note in `docs/spec/configuration.md` (or an ADR note) documents the local entry
  config so future operators can reproduce the setup.
- The recommendation is marked as re-runnable/periodic (models improve; this eval
  should be revisited periodically).

---

## Verification plan

- **This task's verification is primarily L5/L6 (operator-run on the real hardware).**
  There is no CI-automatable unit test for hardware-specific benchmark results.
- **L5 methodology document:** TC-094-03 — the verify commit records the benchmark
  methodology and recommendation.
- **L6 real-hardware benchmark:** TC-094-01 and TC-094-02 — operator runs the
  benchmark on the specified hardware and records results.
- **Harness command (operator, not CI):**
  ```
  # Step 1: install inference server + translation proxy
  # Step 2: benchmark candidates
  # Step 3: configure and test the selected model with agent-builder
  # Step 4: record results in verify commit
  ```

## Out of scope

- Building or shipping an inference server or translation proxy (external tools:
  llama.cpp, Ollama, LiteLLM, claude-code-router).
- Automated CI benchmarks (the hardware is specific; CI is not the RTX 4060 Laptop).
- Model fine-tuning or adapter training.
- A general benchmarking framework — this is a one-time (but re-runnable) eval,
  not a regression suite.
