package transforms

import (
	"math"
)

// Caesar shifts A-Z/a-z forward by n, leaving other bytes alone.
func Caesar(in []byte, n int) []byte {
	n = ((n % 26) + 26) % 26
	out := make([]byte, len(in))
	for i, b := range in {
		switch {
		case b >= 'A' && b <= 'Z':
			out[i] = byte('A' + (int(b-'A')+n)%26)
		case b >= 'a' && b <= 'z':
			out[i] = byte('a' + (int(b-'a')+n)%26)
		default:
			out[i] = b
		}
	}
	return out
}

// Atbash maps A..Z -> Z..A (and lowercase equivalently).
func Atbash(in []byte) []byte {
	out := make([]byte, len(in))
	for i, b := range in {
		switch {
		case b >= 'A' && b <= 'Z':
			out[i] = 'A' + 'Z' - b
		case b >= 'a' && b <= 'z':
			out[i] = 'a' + 'z' - b
		default:
			out[i] = b
		}
	}
	return out
}

// Vigenere applies the Vigenère cipher with an ASCII key. encrypt=true
// shifts forward, false shifts backward. Non-letters are preserved and
// do not advance the key.
func Vigenere(in, key []byte, encrypt bool) []byte {
	if len(key) == 0 {
		return append([]byte(nil), in...)
	}
	normKey := make([]int, 0, len(key))
	for _, k := range key {
		switch {
		case k >= 'A' && k <= 'Z':
			normKey = append(normKey, int(k-'A'))
		case k >= 'a' && k <= 'z':
			normKey = append(normKey, int(k-'a'))
		}
	}
	if len(normKey) == 0 {
		return append([]byte(nil), in...)
	}
	out := make([]byte, len(in))
	ki := 0
	for i, b := range in {
		var base byte
		var letter bool
		if b >= 'A' && b <= 'Z' {
			base = 'A'
			letter = true
		} else if b >= 'a' && b <= 'z' {
			base = 'a'
			letter = true
		}
		if letter {
			shift := normKey[ki%len(normKey)]
			if !encrypt {
				shift = -shift
			}
			out[i] = byte(int(base) + ((int(b-base)+shift)%26+26)%26)
			ki++
		} else {
			out[i] = b
		}
	}
	return out
}

// English letter frequency (A..Z) in percent.
var englishFreq = [26]float64{
	8.167, 1.492, 2.782, 4.253, 12.702, 2.228, 2.015, 6.094, 6.966, 0.153,
	0.772, 4.025, 2.406, 6.749, 7.507, 1.929, 0.095, 5.987, 6.327, 9.056,
	2.758, 0.978, 2.360, 0.150, 1.974, 0.074,
}

// ScoreEnglish returns a heuristic fit to English letter frequencies.
// Higher score = closer to English. ASCII-printable bonus added.
func ScoreEnglish(in []byte) float64 {
	if len(in) == 0 {
		return 0
	}
	var counts [26]int
	total := 0
	printable := 0
	for _, b := range in {
		if b >= 'A' && b <= 'Z' {
			counts[b-'A']++
			total++
		} else if b >= 'a' && b <= 'z' {
			counts[b-'a']++
			total++
		}
		if (b >= 0x20 && b <= 0x7e) || b == '\n' || b == '\r' || b == '\t' {
			printable++
		}
	}
	printRatio := float64(printable) / float64(len(in))
	if total == 0 {
		return printRatio * 10 // give credit for printability
	}
	// Chi-squared inverse-like score
	chi := 0.0
	for i := 0; i < 26; i++ {
		obs := float64(counts[i]) / float64(total) * 100
		exp := englishFreq[i]
		chi += math.Abs(obs - exp) // L1 distance; simpler than chi^2 for small samples
	}
	// Lower chi -> better. Convert to a bounded score.
	score := (200.0 - chi) + printRatio*50
	if score < 0 {
		score = 0
	}
	return score
}

// BruteCaesar returns all 26 shifts with scores, best-first.
func BruteCaesar(in []byte) []map[string]any {
	out := make([]map[string]any, 26)
	for n := 0; n < 26; n++ {
		dec := Caesar(in, n)
		out[n] = map[string]any{
			"shift":  n,
			"score":  ScoreEnglish(dec),
			"sample": previewText(dec, 120),
			"text":   string(dec),
		}
	}
	// sort by score desc (simple insertion)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1]["score"].(float64) < out[j]["score"].(float64); j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// BruteXORByte returns scored 1-byte XOR candidates, best-first.
func BruteXORByte(in []byte) []map[string]any {
	out := make([]map[string]any, 256)
	for k := 0; k < 256; k++ {
		buf := make([]byte, len(in))
		for i, b := range in {
			buf[i] = b ^ byte(k)
		}
		out[k] = map[string]any{
			"key":    k,
			"keyHex": byteHex(byte(k)),
			"score":  ScoreEnglish(buf),
			"sample": previewText(buf, 120),
			"text":   string(buf),
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1]["score"].(float64) < out[j]["score"].(float64); j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// RepeatingXORGuess attempts to recover short repeating XOR keys using
// normalized Hamming distance for key-size ranking and per-column
// single-byte XOR scoring.
func RepeatingXORGuess(in []byte, minKey, maxKey, top int) []map[string]any {
	if minKey <= 0 {
		minKey = 2
	}
	if maxKey < minKey {
		maxKey = minKey
	}
	if maxKey > 64 {
		maxKey = 64
	}
	if top <= 0 {
		top = 5
	}
	type keySizeScore struct {
		size  int
		score float64
	}
	sizes := []keySizeScore{}
	for keySize := minKey; keySize <= maxKey; keySize++ {
		if len(in) < keySize*4 {
			continue
		}
		dist := 0.0
		pairs := 0
		for block := 0; block < 4; block++ {
			a := block * keySize
			b := a + keySize
			if b+keySize > len(in) {
				break
			}
			dist += float64(hamming(in[a:a+keySize], in[b:b+keySize])) / float64(keySize)
			pairs++
		}
		if pairs > 0 {
			sizes = append(sizes, keySizeScore{size: keySize, score: dist / float64(pairs)})
		}
	}
	for i := 1; i < len(sizes); i++ {
		for j := i; j > 0 && sizes[j-1].score > sizes[j].score; j-- {
			sizes[j-1], sizes[j] = sizes[j], sizes[j-1]
		}
	}
	limit := top
	if limit > len(sizes) {
		limit = len(sizes)
	}
	out := make([]map[string]any, 0, limit)
	for _, ks := range sizes[:limit] {
		key := recoverRepeatingXORKey(in, ks.size)
		dec := XORRepeating(in, key)
		out = append(out, map[string]any{
			"keySize":         ks.size,
			"distance":        ks.score,
			"keyHex":          bytesHex(key),
			"keyText":         previewText(key, len(key)),
			"score":           ScoreEnglish(dec),
			"sample":          previewText(dec, 120),
			"text":            string(dec),
			"normalizedScore": ScoreEnglish(dec) - ks.score,
		})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1]["normalizedScore"].(float64) < out[j]["normalizedScore"].(float64); j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func recoverRepeatingXORKey(in []byte, keySize int) []byte {
	key := make([]byte, keySize)
	for pos := 0; pos < keySize; pos++ {
		col := make([]byte, 0, len(in)/keySize+1)
		for i := pos; i < len(in); i += keySize {
			col = append(col, in[i])
		}
		bestScore := -1.0
		bestKey := byte(0)
		for k := 0; k < 256; k++ {
			buf := make([]byte, len(col))
			for i, b := range col {
				buf[i] = b ^ byte(k)
			}
			score := ScoreEnglish(buf)
			if score > bestScore {
				bestScore = score
				bestKey = byte(k)
			}
		}
		key[pos] = bestKey
	}
	return key
}

func XORRepeating(in, key []byte) []byte {
	if len(key) == 0 {
		return append([]byte(nil), in...)
	}
	out := make([]byte, len(in))
	for i, b := range in {
		out[i] = b ^ key[i%len(key)]
	}
	return out
}

func hamming(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	out := 0
	for i := 0; i < n; i++ {
		x := a[i] ^ b[i]
		for x != 0 {
			out++
			x &= x - 1
		}
	}
	return out
}

// TryEverything tries a handful of simple decodes and scores them.
func TryEverything(in []byte) []map[string]any {
	candidates := []map[string]any{
		{"name": "input", "text": string(in), "score": ScoreEnglish(in)},
		{"name": "rot13", "text": string(Caesar(in, 13)), "score": ScoreEnglish(Caesar(in, 13))},
		{"name": "atbash", "text": string(Atbash(in)), "score": ScoreEnglish(Atbash(in))},
	}
	if b, err := Base64Decode(in); err == nil && len(b) > 0 {
		candidates = append(candidates, map[string]any{"name": "base64.decode", "text": string(b), "score": ScoreEnglish(b)})
	}
	if b, err := HexDecode(in); err == nil && len(b) > 0 {
		candidates = append(candidates, map[string]any{"name": "hex.decode", "text": string(b), "score": ScoreEnglish(b)})
	}
	if b, err := URLDecode(in); err == nil && len(b) > 0 {
		candidates = append(candidates, map[string]any{"name": "url.decode", "text": string(b), "score": ScoreEnglish(b)})
	}
	// Top 3 XOR 1-byte candidates
	xr := BruteXORByte(in)
	for i := 0; i < 3 && i < len(xr); i++ {
		candidates = append(candidates, map[string]any{
			"name":  "xor1:" + xr[i]["keyHex"].(string),
			"text":  xr[i]["text"].(string),
			"score": xr[i]["score"].(float64),
		})
	}
	// Top repeating-key XOR candidates.
	rx := RepeatingXORGuess(in, 2, 40, 3)
	for i := 0; i < 3 && i < len(rx); i++ {
		candidates = append(candidates, map[string]any{
			"name":    "xor-repeating:" + rx[i]["keyHex"].(string),
			"keyHex":  rx[i]["keyHex"].(string),
			"keyText": rx[i]["keyText"].(string),
			"text":    rx[i]["text"].(string),
			"score":   rx[i]["score"].(float64),
		})
	}
	// Top 2 caesar
	cz := BruteCaesar(in)
	for i := 0; i < 2 && i < len(cz); i++ {
		candidates = append(candidates, map[string]any{
			"name":  "caesar:" + byteDec(byte(cz[i]["shift"].(int))),
			"text":  cz[i]["text"].(string),
			"score": cz[i]["score"].(float64),
		})
	}
	// Sort
	for i := 1; i < len(candidates); i++ {
		for j := i; j > 0 && candidates[j-1]["score"].(float64) < candidates[j]["score"].(float64); j-- {
			candidates[j-1], candidates[j] = candidates[j], candidates[j-1]
		}
	}
	// Samples
	for _, c := range candidates {
		t, _ := c["text"].(string)
		c["sample"] = previewText([]byte(t), 120)
	}
	return candidates
}

func previewText(buf []byte, n int) string {
	if n > len(buf) {
		n = len(buf)
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		if buf[i] >= 0x20 && buf[i] <= 0x7e {
			out[i] = buf[i]
		} else if buf[i] == '\n' {
			out[i] = ' '
		} else {
			out[i] = '.'
		}
	}
	return string(out)
}

func byteHex(b byte) string {
	const h = "0123456789abcdef"
	return string([]byte{h[b>>4], h[b&0x0f]})
}

func bytesHex(buf []byte) string {
	out := make([]byte, 0, len(buf)*2)
	for _, b := range buf {
		out = append(out, byteHex(b)...)
	}
	return string(out)
}

func byteDec(b byte) string {
	if b == 0 {
		return "0"
	}
	out := [3]byte{}
	i := 3
	for b > 0 {
		i--
		out[i] = '0' + b%10
		b /= 10
	}
	return string(out[i:])
}
