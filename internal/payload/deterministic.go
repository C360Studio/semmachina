package payload

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
)

// deterministicID hashes a domain-separated, length-prefixed tuple.
//
// Length framing is part of the identity contract: without it, ("ab", "c")
// and ("a", "bc") would feed identical bytes to the digest. The helper stays
// package-private so each exported identity function fixes its own domain and
// tuple order rather than letting callers invent either.
func deterministicID(domain string, values ...string) string {
	digest := sha256.New()
	writeDeterministicMember(digest, domain)
	for _, value := range values {
		writeDeterministicMember(digest, value)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func writeDeterministicMember(digest hash.Hash, value string) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	_, _ = digest.Write(length[:])
	_, _ = digest.Write([]byte(value))
}
