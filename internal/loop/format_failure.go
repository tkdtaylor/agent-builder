package loop

import (
	"fmt"
)

// MaxFailureOutputBytes is the truncation cap for step output included in formatted
// failure strings fed back to retry attempts. The loop truncates the first failing
// step's Output to at most this many bytes before formatting.
const MaxFailureOutputBytes = 2000

// FormatFailure formats a failed Outcome for injection into the next retry attempt's
// prompt as the PriorFailure field. It intentionally mirrors runtime.writeFailureEvidence's
// "first failing step" rule: only the first failing step's name and (truncated) output
// are included. The truncation cap (MaxFailureOutputBytes) is prompt-only; the audit-stream
// path via runtime.writeFailureEvidence is not truncated. A future edit to one should
// check the other for consistency.
func FormatFailure(outcome Outcome) string {
	switch outcome.Failure.Reason {
	case FailureGate:
		return formatGateFailure(outcome)
	case FailureExecutorError:
		return formatExecutorError(outcome)
	case FailureExecutorIncomplete:
		return formatExecutorIncomplete(outcome)
	default:
		return "Unknown failure reason"
	}
}

func formatGateFailure(outcome Outcome) string {
	// Find the first failing step
	var failingStep *string
	var failingOutput *string
	for _, result := range outcome.Verdict.Results {
		if !result.OK {
			failingStep = &result.Name
			output := result.Output
			if len(output) > MaxFailureOutputBytes {
				// Truncate at the cap, respecting UTF-8 rune boundaries
				output = truncateAtRune(output, MaxFailureOutputBytes)
			}
			failingOutput = &output
			break
		}
	}

	if failingStep == nil || failingOutput == nil {
		return "Your previous attempt failed the verification gate.\nFix these issues before producing the branch."
	}

	formatted := fmt.Sprintf(
		"Your previous attempt failed the verification gate.\n\nFailed step: %s\nOutput:\n%s\n\nFix these issues before producing the branch.",
		*failingStep,
		*failingOutput,
	)
	return formatted
}

func formatExecutorError(outcome Outcome) string {
	errMsg := "unknown executor error"
	if outcome.Failure.Err != nil {
		errMsg = outcome.Failure.Err.Error()
	}
	return fmt.Sprintf("Your previous attempt failed: the executor encountered an error.\nError: %s\nFix any issues and retry.", errMsg)
}

func formatExecutorIncomplete(outcome Outcome) string {
	return "Your previous attempt did not complete: the executor did not produce a branch.\nEnsure your implementation writes the produced branch name to the designated output file."
}

// truncateAtRune truncates a string at byte position cap, ensuring we don't split
// a UTF-8 rune. If cap >= len(s), returns s unchanged.
func truncateAtRune(s string, cap int) string {
	if cap >= len(s) {
		return s
	}

	// Work backward from cap byte position to find a valid UTF-8 boundary
	b := s[:cap]
	for len(b) > 0 && (b[len(b)-1]&0xC0) == 0x80 {
		b = b[:len(b)-1]
	}
	return b
}
