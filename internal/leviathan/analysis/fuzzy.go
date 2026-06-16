package analysis

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/andydixon/nexus/internal/leviathan/document"
)

// fuzzy.go implements context-triggered piecewise hashing (CTPH) compatible
// with ssdeep. CTPH produces a short signature of the form
// "blocksize:hash1:hash2" that stays similar when the input changes only a
// little, so investigators can cluster related samples or match a file
// against a known-bad ssdeep hash even after minor edits.
//
// The signature format and the rolling/sum hashes follow Andrew Tridgell's
// spamsum / Jesse Kornblum's ssdeep so the output interoperates with the
// standard tooling (e.g. an empty input hashes to "3::").

const (
	spamsumLength = 64
	rollingWindow = 7
	minBlocksize  = 3
	hashInit      = uint32(0x28021967)
	hashPrime     = uint32(0x01000193)
)

const b64alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

type rollState struct {
	h1, h2, h3 uint32
	window     [rollingWindow]byte
	n          int
}

func (r *rollState) roll(c byte) uint32 {
	r.h2 -= r.h1
	r.h2 += rollingWindow * uint32(c)
	r.h1 += uint32(c)
	r.h1 -= uint32(r.window[r.n%rollingWindow])
	r.window[r.n%rollingWindow] = c
	r.n++
	r.h3 = r.h3 << 5
	r.h3 ^= uint32(c)
	return r.h1 + r.h2 + r.h3
}

func sumHash(c byte, h uint32) uint32 { return (h * hashPrime) ^ uint32(c) }

// FuzzyDigest computes the ssdeep-compatible signature for data.
func FuzzyDigest(data []byte) string {
	bs := uint32(minBlocksize)
	for uint64(bs)*spamsumLength < uint64(len(data)) {
		bs *= 2
	}
	for {
		sig1, sig2 := ctphSigs(data, bs)
		if bs > minBlocksize && len(sig1) < spamsumLength/2 {
			bs /= 2
			continue
		}
		return fmt.Sprintf("%d:%s:%s", bs, sig1, sig2)
	}
}

func ctphSigs(data []byte, bs uint32) (string, string) {
	var rs rollState
	h1, h2 := hashInit, hashInit
	var sig1, sig2 strings.Builder
	var lastRoll uint32

	for _, c := range data {
		h1 = sumHash(c, h1)
		h2 = sumHash(c, h2)
		rh := rs.roll(c)
		lastRoll = rh

		if rh%bs == bs-1 && sig1.Len() < spamsumLength-1 {
			sig1.WriteByte(b64alphabet[h1%64])
			h1 = hashInit
		}
		if rh%(bs*2) == bs*2-1 && sig2.Len() < spamsumLength/2-1 {
			sig2.WriteByte(b64alphabet[h2%64])
			h2 = hashInit
		}
	}
	if lastRoll != 0 {
		sig1.WriteByte(b64alphabet[h1%64])
		sig2.WriteByte(b64alphabet[h2%64])
	}
	return sig1.String(), sig2.String()
}

// FuzzyHash computes the CTPH signature over a document range.
func FuzzyHash(d *document.Document, start, length int64) (map[string]any, error) {
	if length < 0 {
		length = d.Size() - start
	}
	buf, err := d.Range(start, length)
	if err != nil {
		return nil, err
	}
	sig := FuzzyDigest(buf)
	bs := strings.SplitN(sig, ":", 2)[0]
	return map[string]any{
		"algo":      "ssdeep",
		"signature": sig,
		"blocksize": bs,
		"length":    length,
	}, nil
}

// FuzzyCompare returns a 0-100 similarity score between two ssdeep
// signatures. 0 means unrelated (or incomparable block sizes); 100 means the
// signatures are identical.
func FuzzyCompare(a, b string) int {
	bs1, a1, a2, ok1 := parseSig(a)
	bs2, b1, b2, ok2 := parseSig(b)
	if !ok1 || !ok2 {
		return 0
	}
	if bs1 != bs2 && bs1 != bs2*2 && bs2 != bs1*2 {
		return 0
	}
	switch {
	case bs1 == bs2:
		s1 := scoreStrings(a1, b1, bs1)
		s2 := scoreStrings(a2, b2, bs1*2)
		if s2 > s1 {
			return s2
		}
		return s1
	case bs1 == bs2*2:
		return scoreStrings(a1, b2, bs1)
	default: // bs2 == bs1*2
		return scoreStrings(a2, b1, bs2)
	}
}

func parseSig(s string) (uint32, string, string, bool) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) != 3 {
		return 0, "", "", false
	}
	bs, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil || bs == 0 {
		return 0, "", "", false
	}
	return uint32(bs), parts[1], parts[2], true
}

func scoreStrings(s1, s2 string, blocksize uint32) int {
	if !hasCommonSubstring(s1, s2, rollingWindow) {
		return 0
	}
	if s1 == s2 {
		return 100
	}
	d := editDistance(s1, s2)
	if len(s1)+len(s2) == 0 {
		return 0
	}
	score := (d * spamsumLength) / (len(s1) + len(s2))
	score = (100 * score) / spamsumLength
	score = 100 - score
	// Small block sizes describe little data, so cap the score they can claim.
	cap := int(blocksize) / minBlocksize * minInt(len(s1), len(s2))
	if score > cap {
		score = cap
	}
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	return score
}

func hasCommonSubstring(s1, s2 string, n int) bool {
	if len(s1) < n || len(s2) < n {
		return false
	}
	seen := make(map[string]struct{}, len(s1))
	for i := 0; i+n <= len(s1); i++ {
		seen[s1[i:i+n]] = struct{}{}
	}
	for i := 0; i+n <= len(s2); i++ {
		if _, ok := seen[s2[i:i+n]]; ok {
			return true
		}
	}
	return false
}

// editDistance is classic Levenshtein with unit insert/delete/substitute cost.
func editDistance(s1, s2 string) int {
	m, n := len(s1), len(s2)
	prev := make([]int, n+1)
	cur := make([]int, n+1)
	for j := 0; j <= n; j++ {
		prev[j] = j
	}
	for i := 1; i <= m; i++ {
		cur[0] = i
		for j := 1; j <= n; j++ {
			cost := 1
			if s1[i-1] == s2[j-1] {
				cost = 0
			}
			cur[j] = minInt(minInt(cur[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[n]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
