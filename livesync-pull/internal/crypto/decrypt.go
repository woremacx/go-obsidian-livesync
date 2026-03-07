package crypto

import (
	"fmt"
	"strings"

	"github.com/vrtmrz/obsidian-livesync/cmd/livesync-pull/logw"
)

// Decrypt dispatches to the appropriate decryption function based on the data prefix.
// passphrase is the user's passphrase.
// pbkdf2Salt is the salt from _local/obsidian_livesync_sync_parameters (may be nil for V1/V3).
// dynamicIter controls whether V1 uses dynamic iteration count.
func Decrypt(data string, passphrase string, pbkdf2Salt []byte, dynamicIter bool) (string, error) {
	prefix := data
	if len(prefix) > 20 {
		prefix = prefix[:20]
	}

	if strings.HasPrefix(data, "%=") {
		logw.Tracef("[crypto] dispatch: HKDF (%%=), data_len=%d", len(data))
		return decryptHKDF(data[2:], passphrase, pbkdf2Salt)
	}
	if strings.HasPrefix(data, "%$") {
		logw.Tracef("[crypto] dispatch: HKDF-Ephemeral (%%$), data_len=%d", len(data))
		return decryptHKDFEphemeral(data[2:], passphrase)
	}
	if strings.HasPrefix(data, "%~") {
		logw.Tracef("[crypto] dispatch: V3 (%%~), data_len=%d", len(data))
		return decryptV3(data, passphrase)
	}
	if strings.HasPrefix(data, "%") {
		logw.Tracef("[crypto] dispatch: V1-Hex (%%...), data_len=%d, dynamicIter=%v, prefix=%q", len(data), dynamicIter, prefix)
		return decryptV1Hex(data, passphrase, dynamicIter)
	}
	if strings.HasPrefix(data, "[") && strings.HasSuffix(data, "]") {
		logw.Tracef("[crypto] dispatch: V1-JSON, data_len=%d", len(data))
		return decryptV1JSON(data, passphrase, dynamicIter)
	}
	return "", fmt.Errorf("unknown encryption format: prefix=%q", prefix)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
