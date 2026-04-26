package supplychain

import "os"

// osReadDir is split out so dependabot.go doesn't import os directly,
// keeping the recipe body focused on the dependabot-specific logic.
func osReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}
