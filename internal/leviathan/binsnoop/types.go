package binsnoop

import (
	"crypto/rand"
	"fmt"
	"sort"
)

// Confidence levels for SignatureResult
const (
	ConfidenceLow    uint8 = 0
	ConfidenceMedium uint8 = 128
	ConfidenceHigh   uint8 = 250
)

// SignatureResult describes a validated file signature found during scanning.
type SignatureResult struct {
	Offset             int        `json:"offset"`
	ID                 string     `json:"id"`
	Size               int        `json:"size"`
	Name               string     `json:"name"`
	Confidence         uint8      `json:"confidence"`
	Description        string     `json:"description"`
	AlwaysDisplay      bool       `json:"always_display"`
	ExtractionDeclined bool       `json:"extraction_declined"`
	PreferredExtractor *Extractor `json:"-"`
}

// DeepScanResult is a validated signature retained by a non-suppressing scan.
// ParentOffset is -1 for a top-level hit.
type DeepScanResult struct {
	SignatureResult
	End          int  `json:"end"`
	Depth        int  `json:"depth"`
	ParentOffset int  `json:"parent_offset"`
	Overlap      bool `json:"overlap"`
}

// DeepScan is a bounded nested-signature analysis result.
type DeepScan struct {
	Results   []DeepScanResult `json:"results"`
	Truncated bool             `json:"truncated"`
}

// SignatureParser validates a magic byte match and returns a SignatureResult or error.
type SignatureParser func(data []byte, offset int) (*SignatureResult, error)

// Signature defines a file signature to search for.
type Signature struct {
	Name          string
	Short         bool
	Magic         [][]byte
	MagicOffset   int
	Description   string
	AlwaysDisplay bool
	Parser        SignatureParser
	Extractor     *Extractor
}

// ExtractorType defines the type of extractor.
type ExtractorType int

const (
	ExtractorTypeNone ExtractorType = iota
	ExtractorTypeInternal
	ExtractorTypeExternal
)

// SourceFilePlaceholder is replaced at runtime with the actual file path.
const SourceFilePlaceholder = "%e"

// InternalExtractorFunc is the function signature for built-in extractors.
type InternalExtractorFunc func(data []byte, offset int, outputDir string) *ExtractionResult

// Extractor describes how to extract a file type.
type Extractor struct {
	Type         ExtractorType
	Command      string
	Function     InternalExtractorFunc
	Extension    string
	Arguments    []string
	ExitCodes    []int
	DoNotRecurse bool
}

// ExtractionResult stores information about a completed extraction.
type ExtractionResult struct {
	Size            *int   `json:"size,omitempty"`
	Success         bool   `json:"success"`
	ExtractorName   string `json:"extractor"`
	DoNotRecurse    bool   `json:"do_not_recurse"`
	OutputDirectory string `json:"output_directory"`
}

// AnalysisResults contains all results from analysing a file or buffer.
type AnalysisResults struct {
	FilePath    string                       `json:"file_path"`
	FileMap     []SignatureResult            `json:"file_map"`
	Extractions map[string]*ExtractionResult `json:"extractions"`
}

// sortSignatureResults sorts by offset for proper file mapping.
func sortSignatureResults(results []SignatureResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Offset < results[j].Offset
	})
}

// newUUID generates a v4 UUID string with no external dependencies.
func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
