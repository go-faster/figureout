package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/go-faster/sdk/gold"
	"github.com/stretchr/testify/require"
)

// TestConfigMarkdownIsCurrent dogfoods the documentation target: CONFIG.md is
// generated from the descriptor, so a configuration change that never reached
// the reference fails here rather than shipping as a stale document.
func TestConfigMarkdownIsCurrent(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, printReference(&buf))

	want, err := os.ReadFile("CONFIG.md")
	require.NoError(t, err)
	// A checkout on Windows rewrites the committed line endings, so the
	// comparison is about the content rather than about how git stored it.
	require.Equal(t, gold.NormalizeNewlines(string(want)), buf.String(),
		"CONFIG.md is stale, run make docs")
}
