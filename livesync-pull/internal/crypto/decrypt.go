package crypto

import (
	"fmt"
	"strings"
)

// Decrypt dispatches to the appropriate decryption function based on the data prefix.
// passphrase is the user's passphrase.
// pbkdf2Salt is the salt from _local/obsidian_livesync_sync_parameters (may be nil for V1/V3).
// dynamicIter controls whether V1 uses dynamic iteration count.
func Decrypt(data string, passphrase string, pbkdf2Salt []byte, dynamicIter bool) (string, error) {
	if strings.HasPrefix(data, "%=") {
		return decryptHKDF(data[2:], passphrase, pbkdf2Salt)
	}
	if strings.HasPrefix(data, "%$") {
		return decryptHKDFEphemeral(data[2:], passphrase)
	}
	if strings.HasPrefix(data, "%~") {
		return decryptV3(data, passphrase)
	}
	if strings.HasPrefix(data, "%") {
		return decryptV1Hex(data, passphrase, dynamicIter)
	}
	if strings.HasPrefix(data, "[") && strings.HasSuffix(data, "]") {
		return decryptV1JSON(data, passphrase, dynamicIter)
	}
	return "", fmt.Errorf("unknown encryption format: prefix=%q", data[:min(len(data), 10)])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
