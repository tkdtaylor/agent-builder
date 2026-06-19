// Package vault provides a minimal client and lifecycle manager for the vault
// block's newline-delimited-JSON Unix socket protocol.
//
// It is a leaf package: it imports only the Go standard library, never any other
// agent-builder/internal package. The dependency direction is one-way —
// internal/secrets and internal/runtime depend on internal/vault, not the
// reverse. This keeps vault liftable into another project (ADR 036).
//
// agent-builder uses three verbs: ping (liveness), put (store a secret value and
// register its binding), and resolve (exchange a secret_ref for an opaque
// handle). The inject verb is fired by exec-sandbox at spawn time inside the
// sandbox, never by agent-builder, so this client has no Inject method.
//
// SECURITY: Put and Resolve never log the secret value, and never include the
// secret value in any error they return. The plaintext secret is present in
// agent-builder's memory only for the duration of the Put call; afterwards the
// caller holds opaque handles, which are safe to log.
package vault

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

// Client is a minimal client for the vault Unix socket. The zero value is not
// usable; construct it with NewClient.
type Client struct {
	socketPath string
}

// NewClient constructs a vault Client bound to the given Unix socket path.
func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath}
}

// SocketPath returns the Unix socket path this client dials.
func (c *Client) SocketPath() string {
	return c.socketPath
}

// Binding controls which header the egress proxy injects a resolved secret on,
// for which allowlisted host. It mirrors the vault put protocol's binding object.
type Binding struct {
	Host   string `json:"host"`
	Header string `json:"header"`
	Scheme string `json:"scheme"`
	EnvVar string `json:"env_var"`
}

// ResolveResult is the response to a resolve request: an opaque handle plus the
// effective injection mode and TTL. It never contains the plaintext secret.
type ResolveResult struct {
	Handle        string
	TTL           int
	InjectionMode string
}

// wire request/response shapes (newline-delimited JSON over the Unix socket).

type putRequest struct {
	Op             string  `json:"op"`
	SecretRef      string  `json:"secret_ref"`
	Value          string  `json:"value"`
	InjectionFloor string  `json:"injection_floor"`
	Binding        Binding `json:"binding"`
}

type resolveRequest struct {
	Op        string `json:"op"`
	SecretRef string `json:"secret_ref"`
	TTL       int    `json:"ttl"`
}

type pingRequest struct {
	Op string `json:"op"`
}

// genericResponse captures any of the vault response shapes. Fields that are
// absent in a given response stay zero-valued.
type genericResponse struct {
	OK            bool       `json:"ok"`
	Handle        string     `json:"handle"`
	TTL           int        `json:"ttl"`
	InjectionMode string     `json:"injection_mode"`
	Error         *wireError `json:"error"`
}

type wireError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *wireError) String() string {
	if e == nil {
		return ""
	}
	if e.Code != "" && e.Message != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	if e.Code != "" {
		return e.Code
	}
	return e.Message
}

// Ping checks vault liveness. It returns nil when the daemon responds ok==true.
func (c *Client) Ping() error {
	resp, err := c.do(pingRequest{Op: "ping"})
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("vault ping: %s", resp.Error.String())
	}
	if !resp.OK {
		return errors.New("vault ping: daemon did not return ok")
	}
	return nil
}

// Put stores a secret value under secretRef with the given injection floor and
// binding. It returns an error on transport failure or a vault-side error.
//
// SECURITY: the secret value is sent to vault but is never logged and never
// appears in any returned error — the caller passes the plaintext exactly once.
func (c *Client) Put(secretRef, value, floor string, binding Binding) error {
	resp, err := c.do(putRequest{
		Op:             "put",
		SecretRef:      secretRef,
		Value:          value,
		InjectionFloor: floor,
		Binding:        binding,
	})
	if err != nil {
		// err comes from the transport layer (do), which never embeds the value.
		return fmt.Errorf("vault put %s: %w", secretRef, err)
	}
	if resp.Error != nil {
		return fmt.Errorf("vault put %s: %s", secretRef, resp.Error.String())
	}
	if !resp.OK {
		return fmt.Errorf("vault put %s: daemon did not return ok", secretRef)
	}
	return nil
}

// Resolve exchanges a secretRef for an opaque handle. The returned handle never
// contains the plaintext secret; it is safe to log and to place in a RunRequest.
func (c *Client) Resolve(secretRef string, ttl int) (ResolveResult, error) {
	resp, err := c.do(resolveRequest{Op: "resolve", SecretRef: secretRef, TTL: ttl})
	if err != nil {
		return ResolveResult{}, fmt.Errorf("vault resolve %s: %w", secretRef, err)
	}
	if resp.Error != nil {
		return ResolveResult{}, fmt.Errorf("vault resolve %s: %s", secretRef, resp.Error.String())
	}
	if resp.Handle == "" {
		return ResolveResult{}, fmt.Errorf("vault resolve %s: empty handle in response", secretRef)
	}
	return ResolveResult{
		Handle:        resp.Handle,
		TTL:           resp.TTL,
		InjectionMode: resp.InjectionMode,
	}, nil
}

// do dials the socket, writes one newline-delimited JSON request, and reads one
// newline-delimited JSON response. It is the single transport choke point — it
// never logs the request payload (which may contain a secret value) and never
// embeds the marshalled request in an error.
func (c *Client) do(req any) (genericResponse, error) {
	conn, err := net.DialTimeout("unix", c.socketPath, dialTimeout)
	if err != nil {
		return genericResponse{}, fmt.Errorf("dial vault socket %s: %w", c.socketPath, err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(time.Now().Add(requestTimeout)); err != nil {
		return genericResponse{}, fmt.Errorf("set deadline: %w", err)
	}

	payload, err := json.Marshal(req)
	if err != nil {
		// Do NOT include the marshalled payload in the error — it may carry a value.
		return genericResponse{}, errors.New("marshal vault request failed")
	}
	payload = append(payload, '\n')
	if _, err := conn.Write(payload); err != nil {
		return genericResponse{}, fmt.Errorf("write vault request: %w", err)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return genericResponse{}, fmt.Errorf("read vault response: %w", err)
	}

	var resp genericResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return genericResponse{}, fmt.Errorf("parse vault response: %w", err)
	}
	return resp, nil
}
