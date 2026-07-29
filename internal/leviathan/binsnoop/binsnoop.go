// Package binsnoop identifies and optionally extracts files embedded inside binary data.
//
// It is a Go port of the binwalk scanning engine, implementing all magic byte signatures,
// structure parsers, and the Aho-Corasick based multi-pattern matching algorithm.
//
// # Quick Start
//
//	scanner := binsnoop.New()
//	data, _ := os.ReadFile("firmware.bin")
//	results := scanner.Scan(data)
//	for _, r := range results {
//	    fmt.Printf("0x%08X  %s\n", r.Offset, r.Description)
//	}
//
// # Extraction
//
//	// Extract a specific result
//	err := binsnoop.CarveResult(data, results[0], "/tmp/output")
//
// # Filtering
//
//	// Only scan for specific signatures
//	scanner := binsnoop.NewWithOptions(Options{Include: []string{"gzip", "squashfs"}})
package binsnoop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Options configures the Scanner.
type Options struct {
	// Include restricts scanning to only these signature names.
	Include []string
	// Exclude removes these signature names from scanning.
	Exclude []string
	// FullSearch makes short signatures match at any offset, not just offset 0.
	FullSearch bool
	// AdditionalSignatures adds user-defined signatures to the scanner.
	AdditionalSignatures []Signature
}

// New creates a Scanner with all default signatures enabled.
func New() *Scanner {
	s, _ := NewWithOptions(Options{})
	return s
}

// NewWithOptions creates a Scanner with the specified options.
func NewWithOptions(opts Options) (*Scanner, error) {
	s := &Scanner{
		patternSigTable: make(map[int]Signature),
		extractorLookup: make(map[string]*Extractor),
	}

	allSigs := AllSignatures()
	if opts.AdditionalSignatures != nil {
		allSigs = append(allSigs, opts.AdditionalSignatures...)
	}

	for _, sig := range allSigs {
		if !includeSig(&sig, opts.Include, opts.Exclude) {
			continue
		}

		s.signatureCount++
		s.patternCount += len(sig.Magic)

		// Register extractor
		var ext *Extractor
		if sig.Extractor != nil {
			extCopy := *sig.Extractor
			ext = &extCopy
		}
		s.extractorLookup[sig.Name] = ext

		for _, pattern := range sig.Magic {
			if sig.Short && !opts.FullSearch {
				s.shortSignatures = append(s.shortSignatures, sig)
				break
			}
			s.patternSigTable[len(s.patterns)] = sig
			s.patterns = append(s.patterns, pattern)
		}
	}

	s.compile()
	return s, nil
}

// ScanBytes scans raw bytes for signatures and returns all matches.
func (s *Scanner) ScanBytes(data []byte) []SignatureResult {
	return s.Scan(data)
}

// ScanFile reads a file from disk and scans it for signatures.
func (s *Scanner) ScanFile(path string) ([]SignatureResult, error) {
	data, err := ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("binsnoop: read file: %w", err)
	}
	return s.Scan(data), nil
}

// AnalyzeBytes scans and optionally extracts from raw bytes.
func (s *Scanner) AnalyzeBytes(data []byte, filePath string, extract bool) *AnalysisResults {
	results := &AnalysisResults{
		FilePath:    filePath,
		Extractions: make(map[string]*ExtractionResult),
	}

	results.FileMap = s.Scan(data)

	if extract && len(results.FileMap) > 0 {
		results.Extractions = s.Extract(data, filePath, results.FileMap)
	}

	return results
}

// AnalyzeFile reads a file from disk, scans, and optionally extracts.
func (s *Scanner) AnalyzeFile(path string, extract bool) (*AnalysisResults, error) {
	data, err := ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("binsnoop: read file: %w", err)
	}
	return s.AnalyzeBytes(data, path, extract), nil
}

// CarveResult extracts raw bytes for a specific signature result to a file.
func CarveResult(data []byte, result SignatureResult, outputDir string) error {
	if result.Offset < 0 || result.Offset+result.Size > len(data) {
		return fmt.Errorf("binsnoop: result offset/size exceeds data bounds")
	}
	if err := os.MkdirAll(outputDir, 0700); err != nil {
		return fmt.Errorf("binsnoop: mkdir: %w", err)
	}
	outPath := filepath.Join(outputDir, fmt.Sprintf("%x_%s.bin", result.Offset, result.Name))
	return os.WriteFile(outPath, data[result.Offset:result.Offset+result.Size], 0600)
}

// SignatureCount returns the number of loaded signatures.
func (s *Scanner) SignatureCount() int { return s.signatureCount }

// PatternCount returns the total number of magic byte patterns.
func (s *Scanner) PatternCount() int { return s.patternCount }

// ListSignatures returns the names of all available signatures.
func ListSignatures() []string {
	sigs := AllSignatures()
	names := make([]string, len(sigs))
	for i, s := range sigs {
		names[i] = s.Name
	}
	return names
}

func includeSig(sig *Signature, include, exclude []string) bool {
	if len(include) > 0 {
		for _, inc := range include {
			if strings.EqualFold(sig.Name, inc) {
				return true
			}
		}
		return false
	}
	if len(exclude) > 0 {
		for _, exc := range exclude {
			if strings.EqualFold(sig.Name, exc) {
				return false
			}
		}
	}
	return true
}
