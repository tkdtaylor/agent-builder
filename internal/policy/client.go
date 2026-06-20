// Package policy provides a minimal client for the policy-engine block's
// newline-delimited-JSON Unix socket protocol.
//
// It is a leaf package: it imports only the Go standard library, never any
// other agent-builder/internal package. The dependency direction is one-way —
// internal/runtime depends on internal/policy, not the reverse. This keeps
// the package liftable (ADR 038).
//
// The client speaks the AuthZEN-compatible decide protocol:
//
//	{"op":"decide","request":{<AuthZEN>}} \n
//	← {"decision":"allow|deny|require_approval","context":{"obligations":[…]}} \n
//
// and liveness:
//
//	{"op":"ping"} \n
//	← {"ok":true} \n
//
// SECURITY: Fail-closed is a load-bearing invariant. Any failure on the decide
// path — socket error, timeout, malformed JSON, empty response, error-shaped
// response, or unknown decision value — maps to DecisionDeny. The client never
// returns DecisionAllow on any error path.
package policy

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// dialTimeout bounds how long a single request waits to connect to the socket.
const dialTimeout = 5 * time.Second

// requestTimeout bounds a single request/response round trip.
const requestTimeout = 5 * time.Second

// Decision is a typed string enum representing a policy engine decision.
type Decision string

const (
	// DecisionAllow means the policy engine authorized the requested action.
	DecisionAllow Decision = "allow"
	// DecisionDeny means the policy engine denied the requested action.
	DecisionDeny Decision = "deny"
	// DecisionRequireApproval means the action requires explicit human approval.
	DecisionRequireApproval Decision = "require_approval"
)

// Obligation is an instruction the policy engine attaches to a decision.
// Type identifies the obligation kind; Value carries its payload (string, bool,
// or numeric depending on the obligation).
type Obligation struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

// Subject identifies the entity requesting authorization (agent-builder itself).
type Subject struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Action names the operation being authorized.
type Action struct {
	Name string `json:"name"`
}

// ResourceProperties carries per-resource metadata included in the AuthZEN request.
type ResourceProperties struct {
	EgressHosts []string `json:"egress_hosts,omitempty"`
}

// Resource identifies the target of the requested action.
type Resource struct {
	Type       string             `json:"type"`
	ID         string             `json:"id"`
	Properties ResourceProperties `json:"properties,omitempty"`
}

// DecideContext carries additional context for the decide call (e.g. risk level).
type DecideContext struct {
	Risk string `json:"risk,omitempty"`
}

// DecideRequest is the AuthZEN-compatible input to PolicyClient.Decide.
type DecideRequest struct {
	Subject  Subject       `json:"subject"`
	Action   Action        `json:"action"`
	Resource Resource      `json:"resource"`
	Context  DecideContext `json:"context,omitempty"`
}

// DecideResponseContext carries human-facing context returned by the policy engine
// alongside a decision. The Reason field contains the policy engine's human-readable
// reason string for the decision (used by audit_emit events — task 073).
type DecideResponseContext struct {
	Reason string `json:"reason,omitempty"`
}

// DecideResponse is the output of PolicyClient.Decide.
// Decision is always set — on any error path it is DecisionDeny.
// Obligations is nil or empty on denial.
// Context carries the policy engine's reason string (non-empty when the engine provides one).
type DecideResponse struct {
	Decision    Decision             `json:"decision"`
	Obligations []Obligation         `json:"obligations,omitempty"`
	Context     DecideResponseContext `json:"context,omitempty"`
}

// PolicyClient is a minimal client for the policy-engine Unix socket. The zero
// value is not usable; construct it with NewClient.
type PolicyClient struct {
	socketPath string
}

// NewClient constructs a PolicyClient bound to the given Unix socket path.
func NewClient(socketPath string) *PolicyClient {
	return &PolicyClient{socketPath: socketPath}
}

// SocketPath returns the Unix socket path this client dials.
func (c *PolicyClient) SocketPath() string {
	return c.socketPath
}

// Ping checks policy-engine liveness. Returns nil when the daemon responds ok==true.
func (c *PolicyClient) Ping() error {
	resp, err := c.do(map[string]any{"op": "ping"})
	if err != nil {
		return fmt.Errorf("policy ping: %w", err)
	}
	if resp.wireErr != nil {
		return fmt.Errorf("policy ping: %s: %s", resp.wireErr.Code, resp.wireErr.Message)
	}
	if !resp.OK {
		return errors.New("policy ping: daemon did not return ok")
	}
	return nil
}

// Decide sends a decide request to the policy engine and returns the decision
// with its obligations.
//
// SECURITY: Fail-closed is mandatory. On any error (dial, timeout, malformed
// JSON, error-shaped response, unknown decision value), the returned
// DecideResponse.Decision is DecisionDeny, never DecisionAllow. The caller
// must use response.Decision, not the error, to determine whether to proceed.
func (c *PolicyClient) Decide(req DecideRequest) (DecideResponse, error) {
	wireReq := map[string]any{
		"op":      "decide",
		"request": req,
	}
	raw, err := c.do(wireReq)
	if err != nil {
		// Fail-closed: any transport error → deny.
		return DecideResponse{Decision: DecisionDeny}, err
	}

	// Error-shaped response → deny.
	if raw.wireErr != nil {
		return DecideResponse{Decision: DecisionDeny},
			fmt.Errorf("policy decide: %s: %s", raw.wireErr.Code, raw.wireErr.Message)
	}

	// No decision field in response (e.g. ping-shaped {"ok":true}) → deny.
	if raw.Decision == "" {
		return DecideResponse{Decision: DecisionDeny},
			errors.New("policy decide: response missing decision field")
	}

	// Map the raw decision string to a typed Decision.
	d := Decision(raw.Decision)
	switch d {
	case DecisionAllow, DecisionDeny, DecisionRequireApproval:
		// Known values — pass through.
	default:
		// Unknown future decision value → fail-closed → deny.
		return DecideResponse{Decision: DecisionDeny}, nil
	}

	return DecideResponse{
		Decision:    d,
		Obligations: raw.Obligations,
		Context:     DecideResponseContext{Reason: raw.Reason},
	}, nil
}

// -- wire types (unexported) --------------------------------------------------

type wireDecideContext struct {
	Reason      string             `json:"reason"`
	Obligations []wireObligation   `json:"obligations"`
}

type wireObligation struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

type wireErrorShape struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// genericWire is the union of all response shapes the policy daemon sends.
type genericWire struct {
	// decide response fields
	Decision string           `json:"decision"`
	Context  wireDecideContext `json:"context"`
	// ping response fields
	OK bool `json:"ok"`
	// error response
	Error *wireErrorShape `json:"error"`
}

// rawResponse is what do() returns after transport.
type rawResponse struct {
	Decision    string
	Obligations []Obligation
	Reason      string
	OK          bool
	wireErr     *wireErrorShape
}

// do dials the socket, writes one newline-delimited JSON request, and reads one
// newline-delimited JSON response. One dial per call — no persistent connection.
func (c *PolicyClient) do(req any) (rawResponse, error) {
	conn, err := net.DialTimeout("unix", c.socketPath, dialTimeout)
	if err != nil {
		return rawResponse{}, fmt.Errorf("dial policy socket %s: %w", c.socketPath, err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(time.Now().Add(requestTimeout)); err != nil {
		return rawResponse{}, fmt.Errorf("set deadline: %w", err)
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return rawResponse{}, errors.New("marshal policy request failed")
	}
	payload = append(payload, '\n')
	if _, err := conn.Write(payload); err != nil {
		return rawResponse{}, fmt.Errorf("write policy request: %w", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return rawResponse{}, fmt.Errorf("read policy response: %w", err)
	}

	var wire genericWire
	if err := json.Unmarshal(line, &wire); err != nil {
		return rawResponse{}, fmt.Errorf("parse policy response: %w", err)
	}

	// Convert wire obligations to typed Obligation slice.
	var obligations []Obligation
	for _, wo := range wire.Context.Obligations {
		obligations = append(obligations, Obligation(wo))
	}

	return rawResponse{
		Decision:    wire.Decision,
		Obligations: obligations,
		Reason:      wire.Context.Reason,
		OK:          wire.OK,
		wireErr:     wire.Error,
	}, nil
}
