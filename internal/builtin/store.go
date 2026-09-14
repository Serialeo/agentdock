// Package builtin owns the persisted user choices for optional built-in tool groups.
package builtin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/uvwt/agentdock/internal/fs/atomicfile"
)

type Choices struct {
	Browser bool `json:"browser"`
	ACP     bool `json:"acp"`
}

func Path(home string) string { return filepath.Join(home, "builtin-capabilities.json") }

func Load(home string, defaults Choices) (Choices, error) {
	data, err := os.ReadFile(Path(home))
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return Choices{}, fmt.Errorf("read built-in capability choices: %w", err)
	}
	var choices Choices
	if err := json.Unmarshal(data, &choices); err != nil {
		return Choices{}, fmt.Errorf("decode built-in capability choices: %w", err)
	}
	return choices, nil
}

func Save(home string, choices Choices) error {
	data, err := json.MarshalIndent(choices, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.Write(Path(home), append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("save built-in capability choices: %w", err)
	}
	return nil
}
