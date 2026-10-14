package update

import (
	"errors"
	"fmt"
	"strings"

	"aead.dev/minisign"
)

// ErrBadSignature is returned when no accepted key vouches for the message.
var ErrBadSignature = errors.New("signature does not match any trusted outfit release key")

// Verify reports whether sig (the text of a .minisig file) is a valid minisign
// signature of message by one of keys. The key ID in the signature only picks
// the candidate; the signature itself is what is checked.
func Verify(keys []minisign.PublicKey, message, sig []byte) error {
	var s minisign.Signature
	if err := s.UnmarshalText(sig); err != nil {
		return fmt.Errorf("unreadable signature: %w", err)
	}
	for _, k := range keys {
		if k.ID() == s.KeyID && minisign.Verify(k, message, sig) {
			return nil
		}
	}
	return fmt.Errorf("%w (signed by key %016X)", ErrBadSignature, s.KeyID)
}

// VerifyEmbedded is Verify against the keys compiled into this binary.
func VerifyEmbedded(message, sig []byte) error {
	return Verify(Keys(), message, sig)
}

// KeyIDs lists the embedded key IDs as minisign prints them.
func KeyIDs() []string {
	var ids []string
	for _, k := range Keys() {
		ids = append(ids, strings.ToUpper(fmt.Sprintf("%016x", k.ID())))
	}
	return ids
}
