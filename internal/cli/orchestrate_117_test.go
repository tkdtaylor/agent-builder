package cli

// TC-117-04 test — assembleOrchestrate inbound selector (task 117, ADR 054 §2).
//
// Verifies that:
//   A) AGENT_BUILDER_INBOUND unset / "env" → inboundFromEnv returns env/stdin source, nil reporter.
//   B) AGENT_BUILDER_INBOUND=telegram + required vars → returns *telegram.Adapter, *telegram.ReplyAdapter.
//   C) AGENT_BUILDER_INBOUND=telegram, missing required var → fail-fast assembly error (not nil-adapter panic).
//   D) AGENT_BUILDER_INBOUND=<unknown> → ExitUsage error.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
)

// TestTC117_04A_DefaultEnvInboundIsEnvStdin verifies that when AGENT_BUILDER_INBOUND
// is unset or "env", inboundFromEnv returns the env/stdin MessageSource and a nil reporter.
func TestTC117_04A_DefaultEnvInboundIsEnvStdin(t *testing.T) {
	sink := audit.NewFakeSink()

	// Case 1: unset
	src, rep, err := inboundFromEnv(func(string) string { return "" }, strings.NewReader(""), sink, nil)
	if err != nil {
		t.Fatalf("TC-117-04A (unset): inboundFromEnv error: %v", err)
	}
	if src == nil {
		t.Error("TC-117-04A (unset): expected non-nil MessageSource for default channel")
	}
	if rep != nil {
		t.Errorf("TC-117-04A (unset): expected nil reporter for default channel, got %T", rep)
	}

	// Case 2: explicitly "env"
	src2, rep2, err2 := inboundFromEnv(func(key string) string {
		if key == EnvInbound {
			return "env"
		}
		return ""
	}, strings.NewReader(""), sink, nil)
	if err2 != nil {
		t.Fatalf("TC-117-04A (env): inboundFromEnv error: %v", err2)
	}
	if src2 == nil {
		t.Error("TC-117-04A (env): expected non-nil MessageSource")
	}
	if rep2 != nil {
		t.Errorf("TC-117-04A (env): expected nil reporter, got %T", rep2)
	}
}

// TestTC117_04B_TelegramInboundAssembled verifies that AGENT_BUILDER_INBOUND=telegram
// with valid key material returns a *telegram.Adapter and *telegram.ReplyAdapter.
func TestTC117_04B_TelegramInboundAssembled(t *testing.T) {
	opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv := tc117CliKeyMaterial(t)
	orchEdPub, orchEdPriv, _, _, opXPubReply, _ := tc117CliKeyMaterial(t)
	_ = opEdPub
	_ = opXPub
	_ = orchXPub
	_ = orchEdPub

	sink := audit.NewFakeSink()

	getenv := func(key string) string {
		switch key {
		case EnvInbound:
			return "telegram"
		case EnvTelegramBotToken:
			return "test-bot-token-117"
		case EnvTelegramBaseURL:
			return "https://api.telegram.org"
		case EnvTelegramChatID:
			return "12345"
		case EnvTelegramSigningKey:
			return hex.EncodeToString(opEdPriv.Public().(ed25519.PublicKey))
		case EnvTelegramX25519Pub:
			return hex.EncodeToString(opXPriv[:])
		case EnvTelegramOrchPriv:
			return hex.EncodeToString(orchXPriv[:])
		case EnvTelegramOrchEdPriv:
			return hex.EncodeToString(orchEdPriv)
		case EnvTelegramOpX25519Pub:
			return hex.EncodeToString(opXPubReply[:])
		}
		return ""
	}

	src, rep, err := inboundFromEnv(getenv, nil, sink, nil)
	if err != nil {
		t.Fatalf("TC-117-04B: inboundFromEnv error: %v", err)
	}

	// TC-117-04B: source is *telegram.Adapter (satisfies supervisor.MessageSource)
	if src == nil {
		t.Fatal("TC-117-04B: expected non-nil MessageSource")
	}
	if _, ok := src.(*telegram.Adapter); !ok {
		t.Errorf("TC-117-04B: src type = %T, want *telegram.Adapter", src)
	}
	// TC-117-04B: reporter is *telegram.ReplyAdapter (satisfies supervisor.Reporter)
	if rep == nil {
		t.Fatal("TC-117-04B: expected non-nil Reporter for telegram inbound")
	}
	if _, ok := rep.(*telegram.ReplyAdapter); !ok {
		t.Errorf("TC-117-04B: rep type = %T, want *telegram.ReplyAdapter", rep)
	}

	// Both satisfy their interfaces at compile time (verified by the type assertions above).
	_ = src
	_ = rep
}

// TestTC117_04C_MissingTelegramVarFailsFast verifies that AGENT_BUILDER_INBOUND=telegram
// with a missing required variable returns a clear assembly error — not a nil-adapter
// panic at first Next() call.
func TestTC117_04C_MissingTelegramVarFailsFast(t *testing.T) {
	sink := audit.NewFakeSink()

	requiredVars := []string{
		EnvTelegramBotToken,
		EnvTelegramSigningKey,
		EnvTelegramX25519Pub,
		EnvTelegramOrchPriv,
		EnvTelegramOrchEdPriv,
		EnvTelegramOpX25519Pub,
		EnvTelegramChatID,
	}

	// Generate minimal valid key material
	opEdPub, opEdPriv, _ := ed25519.GenerateKey(rand.Reader)
	_ = opEdPub
	opXPub, opXPriv, _ := envelope.GenerateKeyPair()
	_ = opXPub
	orchXPub, orchXPriv, _ := envelope.GenerateKeyPair()
	_ = orchXPub
	_, orchEdPriv, _ := ed25519.GenerateKey(rand.Reader)
	_, opXPubReply, _ := envelope.GenerateKeyPair()

	allVars := map[string]string{
		EnvInbound:            "telegram",
		EnvTelegramBotToken:   "test-token",
		EnvTelegramChatID:     "9999",
		EnvTelegramSigningKey: hex.EncodeToString(opEdPriv.Public().(ed25519.PublicKey)),
		EnvTelegramX25519Pub:  hex.EncodeToString(opXPriv[:]),
		EnvTelegramOrchPriv:   hex.EncodeToString(orchXPriv[:]),
		EnvTelegramOrchEdPriv: hex.EncodeToString(orchEdPriv),
		EnvTelegramOpX25519Pub: hex.EncodeToString(opXPubReply[:]),
	}

	for _, missing := range requiredVars {
		missing := missing // capture
		t.Run("missing "+missing, func(t *testing.T) {
			getenv := func(key string) string {
				if key == missing {
					return ""
				}
				return allVars[key]
			}
			_, _, err := inboundFromEnv(getenv, nil, sink, nil)
			if err == nil {
				t.Errorf("TC-117-04C: expected error when %s is missing, got nil", missing)
			}
			// Error must mention the missing var name
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("TC-117-04C: error %q does not mention %q", err.Error(), missing)
			}
		})
	}
}

// TestTC117_04D_UnknownInboundIsUsageError verifies that an unknown AGENT_BUILDER_INBOUND
// value returns an ExitUsage-type error (errUsageConfig).
func TestTC117_04D_UnknownInboundIsUsageError(t *testing.T) {
	sink := audit.NewFakeSink()
	_, _, err := inboundFromEnv(func(key string) string {
		if key == EnvInbound {
			return "grpc" // unknown value
		}
		return ""
	}, strings.NewReader(""), sink, nil)
	if err == nil {
		t.Fatal("TC-117-04D: expected error for unknown AGENT_BUILDER_INBOUND, got nil")
	}
	// Must be a usage-config error (wrapped errUsageConfig)
	if !isUsageConfig(err) {
		t.Errorf("TC-117-04D: err = %v, want errUsageConfig (ExitUsage)", err)
	}
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// tc117CliKeyMaterial generates Ed25519 + X25519 keypairs for orchestrate tests.
// Returns: edPub, edPriv, xPub, xPriv, xPub2, xPriv2 (two X25519 pairs).
func tc117CliKeyMaterial(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, [32]byte, [32]byte, [32]byte, [32]byte) {
	t.Helper()
	edPub, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("tc117CliKeyMaterial: ed25519: %v", err)
	}
	xPub, xPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("tc117CliKeyMaterial: X25519 pair 1: %v", err)
	}
	xPub2, xPriv2, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("tc117CliKeyMaterial: X25519 pair 2: %v", err)
	}
	return edPub, edPriv, xPub, xPriv, xPub2, xPriv2
}

// isUsageConfig reports whether err wraps errUsageConfig.
func isUsageConfig(err error) bool {
	return err != nil && strings.Contains(err.Error(), errUsageConfig.Error())
}
