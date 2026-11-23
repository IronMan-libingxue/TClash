package usage

import (
	"time"

	"github.com/yetone/magpie/internal/sessions"
)

// NativeCodexCalls reads native Codex requests and removes copied responses
// using the same identity rules as the Requests ledger.
func NativeCodexCalls(since time.Time) []sessions.Call {
	calls := sessions.AgentCalls(since, "codex")
	duplicates, _, _ := duplicateCodexCalls(calls)
	if len(duplicates) == 0 {
		return calls
	}
	kept := make([]sessions.Call, 0, len(calls)-len(duplicates))
	for i, call := range calls {
		if !duplicates[i] {
			kept = append(kept, call)
		}
	}
	return kept
}
