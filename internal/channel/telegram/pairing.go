package telegram

import (
	"context"
	"fmt"
	"strconv"

	"github.com/tkdtaylor/agent-builder/internal/channel/telegram/authz"
)

// This file implements the pairing-mode side effects (ADR 063 Decision 3): the
// unknown-sender pending flow and the owner-gated approve/deny handlers. The DECISION
// (owner gate + grammar parse, ordered before deriveMessage) lives in the authz package
// (authz.DecidePairing); these handlers only carry out the audit + notify + store
// mutations the decision dictates. No handler ever derives a supervisor.Message — a
// pairing update is consumed entirely here, never routed as a command or a goal.

// handlePairingPending processes an unknown sender's message: audit a pairing_request,
// reply "pending" to the sender, and notify the owner's chat with the approve/deny
// instruction. It never mutates the store and never derives a message.
func (a *Adapter) handlePairingPending(update Update) {
	senderID := update.Message.senderID()
	a.logger.Debug("pairing: unknown sender pending", "update_id", update.UpdateID, "sender", senderID)

	// Audit first — the request is recorded even if a notification later fails to send.
	a.emitAuditEvent(string(authz.ReasonPairingRequest))

	// Reply "pending" to the sender's own chat. The sender's chat ID is its own chat in a
	// 1:1 DM (Telegram populates message.chat.id == user id for private chats).
	senderChat := update.Message.chatID()
	a.notify(senderChat, pairingPendingMessage())

	// Notify the owner's chat with the sender's ID and the approve/deny instruction.
	a.notify(a.ownerChatID, pairingOwnerNotification(senderID))
}

// handlePairingApprove processes an owner "approve <id>": add the target to the store,
// persist it (survives restart — the crux fix), audit the approval, and confirm to the
// owner. On a store/persist error it audits the failure and still confirms nothing was
// approved, never crashing the loop.
func (a *Adapter) handlePairingApprove(update Update, targetID int64) {
	target := strconv.FormatInt(targetID, 10)
	a.logger.Debug("pairing: owner approve", "update_id", update.UpdateID, "target", target)

	if a.authStore == nil {
		// Defensive: pairing config guarantees a store; if somehow nil, do not panic.
		a.emitAuditEvent("pairing_approve_no_store")
		a.notify(a.ownerChatID, fmt.Sprintf("approve %s failed: no approval store configured", target))
		return
	}
	if err := a.authStore.Add(target); err != nil {
		a.logger.Debug("pairing: approve add failed", "error", err)
		a.emitAuditEvent("pairing_approve_add_failed")
		a.notify(a.ownerChatID, fmt.Sprintf("approve %s failed: %v", target, err))
		return
	}
	if err := a.authStore.Persist(); err != nil {
		a.logger.Debug("pairing: approve persist failed", "error", err)
		a.emitAuditEvent("pairing_approve_persist_failed")
		a.notify(a.ownerChatID, fmt.Sprintf("approve %s failed to persist: %v", target, err))
		return
	}

	a.emitAuditEvent(string(authz.ReasonPairingApproved))
	a.notify(a.ownerChatID, fmt.Sprintf("approved: user %s can now send commands", target))
}

// handlePairingDeny processes an owner "deny <id>": audit the denial and confirm to the
// owner WITHOUT mutating the store. A denied sender is not permanently blocked — it may
// re-request and re-enter the pending flow on a future message (ADR 063 Decision 3).
func (a *Adapter) handlePairingDeny(update Update, targetID int64) {
	target := strconv.FormatInt(targetID, 10)
	a.logger.Debug("pairing: owner deny", "update_id", update.UpdateID, "target", target)

	a.emitAuditEvent(string(authz.ReasonPairingDenied))
	a.notify(a.ownerChatID, fmt.Sprintf("denied: user %s was not approved", target))
}

// handlePairingMalformed processes an owner approve/deny with a missing/non-numeric ID:
// audit the malformed attempt and tell the owner the correct grammar. No store mutation,
// and no fall-through to deriveMessage (the update is consumed here).
func (a *Adapter) handlePairingMalformed(update Update, reason authz.AuditReason) {
	a.logger.Debug("pairing: owner malformed approve/deny", "update_id", update.UpdateID)
	a.emitAuditEvent(string(reason))
	a.notify(a.ownerChatID, `malformed command — use "approve <id>" or "deny <id>" with a numeric id`)
}

// notify sends a plaintext pairing message via the configured PairingNotifier. A nil
// notifier or a blank chat ID is a no-op (audit already recorded the decision); a send
// error is logged, never fatal to the poll loop.
func (a *Adapter) notify(chatID, text string) {
	if a.notifier == nil || chatID == "" {
		return
	}
	if err := a.notifier.Notify(context.Background(), chatID, text); err != nil {
		a.logger.Debug("pairing: notify failed", "chat_id", chatID, "error", err)
	}
}

// pairingPendingMessage is the reply sent to an unknown sender. It carries a recognizable
// "pending"/"awaiting approval" marker distinguishing it from a normal command result.
func pairingPendingMessage() string {
	return "Access pending: your request is awaiting owner approval."
}

// pairingOwnerNotification is the message sent to the owner's chat when an unknown sender
// requests access. It contains the sender's ID and the literal approve/deny instruction.
func pairingOwnerNotification(senderID string) string {
	return fmt.Sprintf(`User %s requests access — reply "approve %s" or "deny %s"`, senderID, senderID, senderID)
}
