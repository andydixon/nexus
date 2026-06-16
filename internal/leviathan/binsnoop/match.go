package binsnoop

// ahoCorasick implements the Aho-Corasick multi-pattern byte matching algorithm.
type ahoCorasick struct {
	gotoFn   []map[byte]int
	failFn   []int
	outputFn [][]int
	patterns [][]byte
}

// ahoMatch represents a single pattern match.
type ahoMatch struct {
	PatternIndex int
	Start        int
}

// newAhoCorasick builds an Aho-Corasick automaton from the given byte patterns.
func newAhoCorasick(patterns [][]byte) *ahoCorasick {
	ac := &ahoCorasick{
		gotoFn:   []map[byte]int{make(map[byte]int)},
		failFn:   []int{0},
		outputFn: [][]int{nil},
		patterns: patterns,
	}

	// Build the trie (goto function)
	for i, pattern := range patterns {
		state := 0
		for _, b := range pattern {
			if next, ok := ac.gotoFn[state][b]; ok {
				state = next
			} else {
				newState := len(ac.gotoFn)
				ac.gotoFn[state][b] = newState
				ac.gotoFn = append(ac.gotoFn, make(map[byte]int))
				ac.failFn = append(ac.failFn, 0)
				ac.outputFn = append(ac.outputFn, nil)
				state = newState
			}
		}
		ac.outputFn[state] = append(ac.outputFn[state], i)
	}

	// Build failure links (BFS)
	queue := make([]int, 0)
	for _, next := range ac.gotoFn[0] {
		queue = append(queue, next)
	}

	for len(queue) > 0 {
		r := queue[0]
		queue = queue[1:]

		for b, s := range ac.gotoFn[r] {
			queue = append(queue, s)
			state := ac.failFn[r]
			for {
				if _, ok := ac.gotoFn[state][b]; ok || state == 0 {
					break
				}
				state = ac.failFn[state]
			}
			if next, ok := ac.gotoFn[state][b]; ok && next != s {
				ac.failFn[s] = next
			} else {
				ac.failFn[s] = 0
			}
			// Merge output from failure state
			if ac.outputFn[ac.failFn[s]] != nil {
				ac.outputFn[s] = append(ac.outputFn[s], ac.outputFn[ac.failFn[s]]...)
			}
		}
	}

	return ac
}

// findOverlapping searches data starting at startOffset and returns all overlapping matches.
func (ac *ahoCorasick) findOverlapping(data []byte, startOffset int) []ahoMatch {
	var matches []ahoMatch
	state := 0

	for i := startOffset; i < len(data); i++ {
		b := data[i]
		for {
			if _, ok := ac.gotoFn[state][b]; ok || state == 0 {
				break
			}
			state = ac.failFn[state]
		}
		if next, ok := ac.gotoFn[state][b]; ok {
			state = next
		}
		for _, patIdx := range ac.outputFn[state] {
			matchStart := i - len(ac.patterns[patIdx]) + 1
			matches = append(matches, ahoMatch{
				PatternIndex: patIdx,
				Start:        matchStart,
			})
		}
	}

	return matches
}

// findOverlappingLimit is the bounded variant used by deep scans. It stops
// collecting once limit matches have been found so adversarial input cannot
// force an unbounded match slice.
func (ac *ahoCorasick) findOverlappingLimit(data []byte, startOffset, limit int) ([]ahoMatch, bool) {
	if limit <= 0 {
		return nil, false
	}
	matches := make([]ahoMatch, 0, min(limit, 256))
	state := 0
	for i := startOffset; i < len(data); i++ {
		b := data[i]
		for {
			if _, ok := ac.gotoFn[state][b]; ok || state == 0 {
				break
			}
			state = ac.failFn[state]
		}
		if next, ok := ac.gotoFn[state][b]; ok {
			state = next
		}
		for _, patIdx := range ac.outputFn[state] {
			matches = append(matches, ahoMatch{PatternIndex: patIdx, Start: i - len(ac.patterns[patIdx]) + 1})
			if len(matches) >= limit {
				return matches, true
			}
		}
	}
	return matches, false
}
