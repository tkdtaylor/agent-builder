// Package memoryguard is the binary IPC adapter leaf for the memory-guard block.
//
// memory-guard is package main (not importable as a Go library). This package
// speaks memory-guard's JSON IPC contract to the configured binary over per-op
// subprocess calls (one subprocess per validate_write / verify_delete call).
//
// Three verbs are implemented:
//
//	validate_write(entry, identity) → { allow, stored_id, flags }   — write-gate
//	verify_delete(id)               → { confirmed, residue_detected, … } — delete-verify
//	validate_read(query, identity)  → { allow, content_redacted, flags } — read-gate
//
// The adapter is a strict leaf: it imports only the Go standard library, never
// any other agent-builder/internal package. The F-012 fitness check enforces this.
//
// Governing ADR: docs/architecture/decisions/049-memory-guard-adoption.md
package memoryguard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
)

// EnvVarMemoryGuardBin is the environment variable that configures the path to
// the memory-guard binary. When unset, the orchestrator degrades to in-memory
// plan state with a structured warning (ADR 049 §3).
const EnvVarMemoryGuardBin = "AGENT_BUILDER_MEMORY_GUARD_BIN"

// ErrWriteGateDenied is returned by ValidateWrite when the memory-guard block
// denies the write (allow=false). It is distinct from transport/parse errors so
// callers can distinguish a security rejection from an IPC failure.
var ErrWriteGateDenied = errors.New("memoryguard: write-gate denied by memory-guard block")

// ErrTamperDetected is returned by VerifyDelete when the memory-guard block
// reports tamper: confirmed=false or residue_detected=true. The caller must treat
// this as a security signal and halt the in-flight plan.
var ErrTamperDetected = errors.New("memoryguard: tamper detected by memory-guard block (delete-verify failed)")

// ErrReadGateDenied is returned by ValidateRead when the memory-guard block
// denies the read (allow=false). It is distinct from transport/parse errors so
// callers can distinguish a security rejection from an IPC failure.
var ErrReadGateDenied = errors.New("memoryguard: read-gate denied by memory-guard block")

// ExecRunner is the subprocess seam for the memory-guard binary. Tests supply a
// recording stub via Client.WithRunner; the default is realRunner (shells out to
// the configured binary).
type ExecRunner interface {
	// Run executes the binary, writes reqJSON to its stdin, and returns combined
	// stdout output and the process error. A non-zero exit must produce a non-nil error.
	Run(binPath string, reqJSON []byte) ([]byte, error)
}

// realRunner is the production ExecRunner that shells out to the memory-guard binary.
type realRunner struct{}

func (r *realRunner) Run(binPath string, reqJSON []byte) ([]byte, error) {
	cmd := exec.Command(binPath) //nolint:gosec // path is caller-supplied and validated at construction
	cmd.Stdin = bytes.NewReader(reqJSON)
	return cmd.Output()
}

// Client is a thin adapter for the memory-guard binary's JSON IPC contract.
// The zero value is not usable; construct it with NewClient or NewClientWithRunner.
type Client struct {
	binPath string
	runner  ExecRunner
}

// NewClient constructs a Client that calls the given binary path for each IPC op.
// The binary path is not validated at construction; a missing or non-executable
// binary surfaces as a hard error on the first call.
func NewClient(binPath string) *Client {
	return &Client{binPath: binPath, runner: &realRunner{}}
}

// NewClientWithRunner constructs a Client with an injectable ExecRunner for tests.
func NewClientWithRunner(binPath string, runner ExecRunner) *Client {
	return &Client{binPath: binPath, runner: runner}
}

// BinPath returns the binary path this client calls.
func (c *Client) BinPath() string {
	return c.binPath
}

// -- wire shapes (unexported) -------------------------------------------------

type validateWriteRequest struct {
	Op       string `json:"op"`
	Entry    string `json:"entry"`
	Identity string `json:"identity"`
}

type validateWriteResponse struct {
	Allow    bool     `json:"allow"`
	StoredID string   `json:"stored_id"`
	Flags    []string `json:"flags"`
}

type verifyDeleteRequest struct {
	Op string `json:"op"`
	ID string `json:"id"`
}

type verifyDeleteResponse struct {
	Confirmed       bool   `json:"confirmed"`
	ResidueDetected bool   `json:"residue_detected"`
	ResidueSummary  string `json:"residue_summary"`
	DeletionHash    string `json:"deletion_hash"`
}

type validateReadRequest struct {
	Op       string `json:"op"`
	Query    string `json:"query"`
	Identity string `json:"identity"`
}

type validateReadResponse struct {
	Allow           bool     `json:"allow"`
	ContentRedacted string   `json:"content_redacted"`
	Flags           []string `json:"flags"`
}

// ValidateWrite sends a validate_write IPC request to the memory-guard binary.
// It returns the stored_id on success. It returns ErrWriteGateDenied when the
// block denies the write (allow=false). Any transport or parse error is returned
// as-is (wrapped).
func (c *Client) ValidateWrite(entry, identity string) (storedID string, err error) {
	req := validateWriteRequest{
		Op:       "validate_write",
		Entry:    entry,
		Identity: identity,
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("memoryguard: marshal validate_write request: %w", err)
	}

	out, err := c.runner.Run(c.binPath, reqJSON)
	if err != nil {
		return "", fmt.Errorf("memoryguard: validate_write subprocess: %w", err)
	}

	var resp validateWriteResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", fmt.Errorf("memoryguard: parse validate_write response %q: %w", out, err)
	}

	if !resp.Allow {
		return "", ErrWriteGateDenied
	}
	return resp.StoredID, nil
}

// VerifyDelete sends a verify_delete IPC request to the memory-guard binary with
// the given stored_id. It returns nil when the block confirms the deletion is
// clean. It returns ErrTamperDetected when confirmed=false or
// residue_detected=true. Any transport or parse error is returned as-is.
func (c *Client) VerifyDelete(storedID string) error {
	req := verifyDeleteRequest{
		Op: "verify_delete",
		ID: storedID,
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("memoryguard: marshal verify_delete request: %w", err)
	}

	out, err := c.runner.Run(c.binPath, reqJSON)
	if err != nil {
		return fmt.Errorf("memoryguard: verify_delete subprocess: %w", err)
	}

	var resp verifyDeleteResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return fmt.Errorf("memoryguard: parse verify_delete response %q: %w", out, err)
	}

	if !resp.Confirmed || resp.ResidueDetected {
		return fmt.Errorf("%w: confirmed=%v residue_detected=%v summary=%q hash=%q",
			ErrTamperDetected, resp.Confirmed, resp.ResidueDetected, resp.ResidueSummary, resp.DeletionHash)
	}
	return nil
}

// ValidateRead sends a validate_read IPC request to the memory-guard binary.
// It returns the redacted content on success. It returns ErrReadGateDenied
// when the block denies the read (allow=false); the response's flags are
// still returned alongside the error so a caller can log why the read was
// denied. Any transport or parse error is returned as-is (wrapped).
func (c *Client) ValidateRead(query, identity string) (contentRedacted string, flags []string, err error) {
	req := validateReadRequest{
		Op:       "validate_read",
		Query:    query,
		Identity: identity,
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return "", nil, fmt.Errorf("memoryguard: marshal validate_read request: %w", err)
	}

	out, err := c.runner.Run(c.binPath, reqJSON)
	if err != nil {
		return "", nil, fmt.Errorf("memoryguard: validate_read subprocess: %w", err)
	}

	var resp validateReadResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", nil, fmt.Errorf("memoryguard: parse validate_read response %q: %w", out, err)
	}

	if !resp.Allow {
		return "", resp.Flags, ErrReadGateDenied
	}
	return resp.ContentRedacted, resp.Flags, nil
}
