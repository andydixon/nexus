package binsnoop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Carve writes a slice of data to a file on disk.
func Carve(outputPath string, data []byte, offset, size int) error {
	if offset < 0 || offset+size > len(data) {
		return fmt.Errorf("carve: offset %d + size %d exceeds data length %d", offset, size, len(data))
	}
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("carve: mkdir: %w", err)
	}
	return os.WriteFile(outputPath, data[offset:offset+size], 0644)
}

// executeExternal runs an external extraction tool.
func executeExternal(ext *Extractor, carvedFilePath, outputDir string) *ExtractionResult {
	result := &ExtractionResult{
		ExtractorName:   ext.Command,
		OutputDirectory: outputDir,
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return result
	}

	// Build arguments, replacing the source file placeholder
	args := make([]string, len(ext.Arguments))
	for i, arg := range ext.Arguments {
		args[i] = strings.ReplaceAll(arg, SourceFilePlaceholder, carvedFilePath)
	}

	cmd := exec.Command(ext.Command, args...)
	cmd.Dir = outputDir

	output, err := cmd.CombinedOutput()
	_ = output

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return result
		}
	}

	// Check if exit code is in the accepted list
	for _, code := range ext.ExitCodes {
		if exitCode == code {
			result.Success = true
			break
		}
	}

	result.DoNotRecurse = ext.DoNotRecurse
	return result
}

// executeInternal runs an internal extraction function.
func executeInternal(ext *Extractor, data []byte, offset int, outputDir string) *ExtractionResult {
	if ext.Function == nil {
		return &ExtractionResult{}
	}
	result := ext.Function(data, offset, outputDir)
	if result == nil {
		result = &ExtractionResult{}
	}
	result.DoNotRecurse = ext.DoNotRecurse
	return result
}

// ExecuteExtraction performs extraction for a signature result.
func ExecuteExtraction(data []byte, filePath string, sig *SignatureResult, ext *Extractor) *ExtractionResult {
	if ext == nil {
		return &ExtractionResult{}
	}

	// Use preferred extractor if specified
	actualExt := ext
	if sig.PreferredExtractor != nil {
		actualExt = sig.PreferredExtractor
	}

	// Build output directory path
	baseName := filepath.Base(filePath)
	outputDir := filepath.Join(filepath.Dir(filePath), baseName+".extracted",
		fmt.Sprintf("%x", sig.Offset))

	switch actualExt.Type {
	case ExtractorTypeInternal:
		return executeInternal(actualExt, data, sig.Offset, outputDir)

	case ExtractorTypeExternal:
		// Carve data to a temp file for external tools
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return &ExtractionResult{OutputDirectory: outputDir}
		}

		carvedFile := filepath.Join(outputDir, fmt.Sprintf("carved.%s", actualExt.Extension))
		endOffset := sig.Offset + sig.Size
		if endOffset > len(data) {
			endOffset = len(data)
		}
		if err := os.WriteFile(carvedFile, data[sig.Offset:endOffset], 0644); err != nil {
			return &ExtractionResult{OutputDirectory: outputDir}
		}

		result := executeExternal(actualExt, carvedFile, outputDir)
		result.OutputDirectory = outputDir
		result.ExtractorName = actualExt.Command
		return result

	default:
		return &ExtractionResult{}
	}
}
