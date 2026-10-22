// Package lanleecher holds files from the repository root that the binary
// embeds, so there is exactly one source for each of them.
package lanleecher

import _ "embed"

// LLMsTxt is llms.txt from the repository root, served at /llms.txt.
//
//go:embed llms.txt
var LLMsTxt string
