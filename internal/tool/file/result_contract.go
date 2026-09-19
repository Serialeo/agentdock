package file

import (
	"encoding/json"
	"fmt"
)

// validateMutationResult keeps result-contract failures on the safe side of a
// filesystem mutation. Network delivery can never be atomic with a local file
// commit, but AgentDock can at least guarantee that the structured result it
// intends to return is JSON-encodable before the mutation is committed.
func validateMutationResult(result Result) error {
	if _, err := json.Marshal(result); err != nil {
		return fmt.Errorf("encode file_edit result before commit: %w", err)
	}
	return nil
}
