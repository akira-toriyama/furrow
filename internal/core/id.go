package core

import "io"

// idAlphabet is Crockford base32 (lowercase) minus the ambiguous letters
// i, l, o, u — exactly 32 symbols, so a random byte masked to its low 5 bits
// maps uniformly onto it (no modulo bias). Chosen so ids are short, lowercase,
// and unambiguous to read/type.
const idAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// RandomIDSuffix returns n characters drawn uniformly from idAlphabet, reading
// randomness from rd (crypto/rand.Reader in production). It is the random part
// of a task id — the store prepends the configured prefix. This is what makes
// concurrent `furrow add` from separate operators collision-resistant without
// any shared counter; the app additionally retries on the (astronomically rare)
// in-store collision. n must be positive; a non-positive n is treated as 1.
func RandomIDSuffix(n int, rd io.Reader) (string, error) {
	if n <= 0 {
		n = 1
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(rd, buf); err != nil {
		return "", Internalf("", "generate id: %v", err)
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = idAlphabet[b&31] // 32-symbol alphabet => low 5 bits, unbiased
	}
	return string(out), nil
}
