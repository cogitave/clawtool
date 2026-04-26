package cli

import "os"

// osStat is a thin wrapper used only by agents_test.go's exists helper
// so the test file doesn't need to import os directly. Keeps the test
// file focused on assertions instead of stdlib imports.
var osStat = os.Stat
