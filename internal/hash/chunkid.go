package hash

import (
	"strconv"

	"github.com/cespare/xxhash/v2"
)

const prefixEncryptedChunk = "h:+"

// ComputeChunkID generates a chunk ID compatible with JS XXHash64HashManager.
// JS: "h:+" + xxhash.h64(`${piece}-${hashedPassphrase}-${piece.length}`).toString(36)
// piece.length is UTF-16 code unit count.
func ComputeChunkID(piece string, hashedPassphrase string) string {
	pieceLen := utf16Len(piece)
	input := piece + "-" + hashedPassphrase + "-" + strconv.Itoa(pieceLen)
	h := xxhash.Sum64String(input)
	return prefixEncryptedChunk + strconv.FormatUint(h, 36)
}
