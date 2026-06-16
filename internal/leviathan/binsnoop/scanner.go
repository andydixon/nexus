package binsnoop

import "sort"

// Scanner is the core scanning engine. It holds compiled signature patterns
// and performs Aho-Corasick based multi-pattern matching on binary data.
type Scanner struct {
	signatureCount  int
	patternCount    int
	shortSignatures []Signature
	patterns        [][]byte
	patternSigTable map[int]Signature
	extractorLookup map[string]*Extractor
	ac              *ahoCorasick
}

// compile builds the Aho-Corasick automaton from loaded patterns.
func (s *Scanner) compile() {
	if len(s.patterns) > 0 {
		s.ac = newAhoCorasick(s.patterns)
	}
}

// Scan searches data for all known file signatures and returns validated results.
// This implements the same algorithm as the Rust binwalk scanner:
//  1. Check short signatures at file offset 0
//  2. Run Aho-Corasick scan with restart on high-confidence sized matches
//  3. Sort and de-conflict overlapping results
//  4. Estimate sizes for signatures with unknown size
func (s *Scanner) Scan(data []byte) []SignatureResult {
	var fileMap []SignatureResult
	nextValidOffset := 0
	availableData := len(data)

	// Phase 1: Check short signatures at offset 0
	for i := range s.shortSignatures {
		sig := &s.shortSignatures[i]
		for _, magic := range sig.Magic {
			magicStart := sig.MagicOffset
			magicEnd := magicStart + len(magic)

			if len(data) >= magicEnd && bytesEqual(data[magicStart:magicEnd], magic) {
				result, err := sig.Parser(data, magicStart)
				if err != nil {
					continue
				}
				autoPopulate(result, sig)
				fileMap = append(fileMap, *result)

				if result.Confidence >= ConfidenceHigh {
					nextValidOffset = result.Offset + result.Size
				}
				break
			}
		}
	}

	// Phase 2: Aho-Corasick scan.
	//
	// The automaton is run exactly once over the whole buffer; we then walk
	// the matches in position order. When a high/medium-confidence sized
	// signature is accepted, nextValidOffset jumps past it and any later
	// matches that start inside that consumed region are skipped. This is the
	// same result the binwalk scanner produces, but without re-running the
	// search from every new offset — a previous implementation rescanned the
	// tail of the file on each accepted match, which is O(n^2) on inputs that
	// contain many closely-spaced signatures (e.g. real firmware images) and
	// could appear to hang.
	if s.ac == nil {
		return s.postProcess(data, fileMap)
	}

	matches := s.ac.findOverlapping(data, nextValidOffset)
	for _, m := range matches {
		magicOffset := m.Start
		if magicOffset < nextValidOffset {
			continue
		}

		sig, ok := s.patternSigTable[m.PatternIndex]
		if !ok {
			continue
		}

		result, err := sig.Parser(data, magicOffset)
		if err != nil {
			continue
		}

		sigEnd := result.Offset + result.Size
		if sigEnd > availableData {
			continue
		}

		autoPopulate(result, &sig)
		fileMap = append(fileMap, *result)

		if result.Confidence >= ConfidenceMedium && result.Size > 0 {
			nextValidOffset = sigEnd
		}
	}

	return s.postProcess(data, fileMap)
}

// postProcess sorts results, removes conflicts, and estimates unknown sizes.
func (s *Scanner) postProcess(data []byte, fileMap []SignatureResult) []SignatureResult {
	if len(fileMap) == 0 {
		return fileMap
	}

	// Sort by offset
	sort.Slice(fileMap, func(i, j int) bool {
		return fileMap[i].Offset < fileMap[j].Offset
	})

	// De-conflict overlapping signatures in a single pass, filtering in place.
	// (The previous implementation removed elements with
	// append(fileMap[:i], fileMap[i+1:]...) inside the loop; each removal is
	// O(n), so a file with many overlapping hits was O(n^2) and could appear
	// to hang.) Removed entries are dropped; on a duplicate offset the
	// higher-confidence signature wins, matching the original behaviour.
	out := fileMap[:0]
	nextValidOffset := 0
	for _, thisSig := range fileMap {
		// Duplicate offset: keep only the highest-confidence signature.
		if len(out) > 0 && thisSig.Offset == out[len(out)-1].Offset {
			if thisSig.Confidence > out[len(out)-1].Confidence {
				out[len(out)-1] = thisSig
				if thisSig.Confidence >= ConfidenceMedium {
					nextValidOffset = thisSig.Offset + thisSig.Size
				}
			}
			continue
		}

		// Drop signatures that fall inside a previously accepted sized region.
		if thisSig.Offset < nextValidOffset {
			continue
		}

		// Drop signatures whose declared size runs past EOF or wraps.
		remainingSize := len(data) - thisSig.Offset
		if thisSig.Size > remainingSize || (thisSig.Offset+thisSig.Size) < 0 {
			continue
		}

		out = append(out, thisSig)
		if thisSig.Confidence >= ConfidenceMedium {
			nextValidOffset = thisSig.Offset + thisSig.Size
		}
	}
	fileMap = out

	// Estimate sizes for signatures with unknown size (0): extend each to the
	// offset of the next medium-or-higher confidence signature, or to EOF.
	// Computed in a single right-to-left pass to stay O(n).
	nextMediumOffset := len(data)
	for i := len(fileMap) - 1; i >= 0; i-- {
		if fileMap[i].Size == 0 {
			fileMap[i].Size = nextMediumOffset - fileMap[i].Offset
		}
		if fileMap[i].Confidence >= ConfidenceMedium {
			nextMediumOffset = fileMap[i].Offset
		}
	}

	return fileMap
}

// ScanDeep validates signatures without suppressing hits inside an already
// identified container. Results are bounded and annotated with containment.
func (s *Scanner) ScanDeep(data []byte, maxResults int) DeepScan {
	if maxResults <= 0 || maxResults > 4096 {
		maxResults = 4096
	}
	result := DeepScan{}
	if len(data) == 0 {
		return result
	}

	results := make([]SignatureResult, 0)
	seen := make(map[struct {
		offset int
		name   string
	}]int)
	add := func(parsed *SignatureResult, sig *Signature) {
		if parsed == nil || parsed.Offset < 0 || parsed.Offset > len(data) {
			return
		}
		if parsed.Size < 0 || parsed.Size > len(data)-parsed.Offset {
			return
		}
		autoPopulate(parsed, sig)
		key := struct {
			offset int
			name   string
		}{parsed.Offset, parsed.Name}
		if idx, ok := seen[key]; ok {
			if parsed.Confidence > results[idx].Confidence {
				results[idx] = *parsed
			}
			return
		}
		if len(results) >= maxResults {
			result.Truncated = true
			return
		}
		seen[key] = len(results)
		results = append(results, *parsed)
	}

	for i := range s.shortSignatures {
		sig := &s.shortSignatures[i]
		for _, magic := range sig.Magic {
			start := sig.MagicOffset
			end := start + len(magic)
			if start >= 0 && end <= len(data) && bytesEqual(data[start:end], magic) {
				parsed, err := sig.Parser(data, start)
				if err == nil {
					add(parsed, sig)
				}
				break
			}
		}
	}

	if s.ac != nil && len(results) < maxResults {
		matchLimit := maxResults * 16
		matches, truncated := s.ac.findOverlappingLimit(data, 0, matchLimit)
		result.Truncated = result.Truncated || truncated
		for _, match := range matches {
			sig, ok := s.patternSigTable[match.PatternIndex]
			if !ok {
				continue
			}
			parsed, err := sig.Parser(data, match.Start)
			if err != nil {
				continue
			}
			add(parsed, &sig)
			if len(results) >= maxResults {
				result.Truncated = true
				break
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Offset != results[j].Offset {
			return results[i].Offset < results[j].Offset
		}
		if results[i].Confidence != results[j].Confidence {
			return results[i].Confidence > results[j].Confidence
		}
		return results[i].Size > results[j].Size
	})
	unique := results[:0]
	for _, hit := range results {
		if len(unique) > 0 && unique[len(unique)-1].Offset == hit.Offset {
			continue
		}
		unique = append(unique, hit)
	}
	results = unique

	nextOffset := len(data)
	for i := len(results) - 1; i >= 0; i-- {
		if results[i].Size == 0 {
			results[i].Size = nextOffset - results[i].Offset
		}
		nextOffset = results[i].Offset
	}

	active := make([]int, 0)
	for _, hit := range results {
		end := hit.Offset + hit.Size
		kept := active[:0]
		for _, idx := range active {
			if result.Results[idx].End > hit.Offset {
				kept = append(kept, idx)
			}
		}
		active = kept
		parent := -1
		overlap := false
		for i := len(active) - 1; i >= 0; i-- {
			candidate := result.Results[active[i]]
			if candidate.End >= end && candidate.Offset < hit.Offset {
				parent = active[i]
				break
			}
			overlap = true
		}
		deep := DeepScanResult{SignatureResult: hit, End: end, ParentOffset: -1, Overlap: overlap}
		if parent >= 0 {
			deep.ParentOffset = result.Results[parent].Offset
			deep.Depth = result.Results[parent].Depth + 1
		}
		result.Results = append(result.Results, deep)
		active = append(active, len(result.Results)-1)
	}
	return result
}

// Extract performs extraction on all extractable signatures in the file map.
func (s *Scanner) Extract(data []byte, filePath string, fileMap []SignatureResult) map[string]*ExtractionResult {
	results := make(map[string]*ExtractionResult)

	for _, sig := range fileMap {
		if sig.ExtractionDeclined {
			continue
		}

		ext, ok := s.extractorLookup[sig.Name]
		if !ok || ext == nil {
			continue
		}

		result := ExecuteExtraction(data, filePath, &sig, ext)
		if !result.Success && sig.Size < len(data)-sig.Offset {
			// Retry with all available data
			expandedSig := sig
			expandedSig.Size = len(data) - sig.Offset
			result = ExecuteExtraction(data, filePath, &expandedSig, ext)
		}

		results[sig.ID] = result
	}

	return results
}

// bytesEqual compares two byte slices.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// autoPopulate fills in auto-generated fields on a SignatureResult.
func autoPopulate(result *SignatureResult, sig *Signature) {
	result.ID = newUUID()
	result.Name = sig.Name
	result.AlwaysDisplay = sig.AlwaysDisplay
}
