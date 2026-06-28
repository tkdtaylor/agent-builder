package supervisor

import "context"

// Reporter is the outbound seam for sending a message back to the human over
// the channel (approval solicitation, result summary). It is the symmetric
// outbound counterpart to GoalSource (the inbound seam).
//
// text is rendered at the channel edge (ADR 046 §2: typed result in the
// orchestrator core, plain text at the boundary). Implementations must not
// log or otherwise expose private key material or the raw plaintext on the
// wire — callers are responsible for keeping text free of secrets.
//
// Defined in internal/supervisor to match the GoalSource home; the interface
// signature is pure-stdlib so adding it drags no new import into this package
// (F-003 / F-007 remain satisfied). Crypto lives exclusively in the concrete
// channel implementation (internal/channel/telegram).
type Reporter interface {
	Report(ctx context.Context, text string) error
}
