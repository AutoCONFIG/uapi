package anthropic

import (
	"crypto/rand"
	"encoding/hex"
)

// cryptoRandRead is a package-level variable for testability.
var cryptoRandRead = rand.Read

// hexEncodeToString wraps hex.EncodeToString for consistency.
func hexEncodeToString(b []byte) string {
	return hex.EncodeToString(b)
}
