package file

import (
	"strings"
	"testing"
)

func TestValidateMutationResultAcceptsJSONEncodableResult(t *testing.T) {
	result := Result{
		"action":        "add",
		"path":          "example.txt",
		"changed":       true,
		"files_changed": 1,
		"insertions":    1,
		"deletions":     0,
		"summary":       "updated example.txt",
	}
	if err := validateMutationResult(result); err != nil {
		t.Fatalf("validateMutationResult() error = %v", err)
	}
}

func TestValidateMutationResultRejectsNonJSONValue(t *testing.T) {
	err := validateMutationResult(Result{"invalid": func() {}})
	if err == nil {
		t.Fatal("validateMutationResult() accepted a non-JSON value")
	}
	if !strings.Contains(err.Error(), "encode file_edit result before commit") {
		t.Fatalf("validateMutationResult() error = %q", err)
	}
}
