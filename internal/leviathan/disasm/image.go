package disasm

import (
	"github.com/andydixon/nexus/internal/leviathan/document"
	"github.com/andydixon/nexus/internal/leviathan/parsers"
)

// ImageAnalysis is a metadata-seeded analysis across file-backed executable
// regions in an ELF or PE image.
type ImageAnalysis struct {
	Info      *parsers.ExecutableInfo `json:"info"`
	Programs  []*Program              `json:"programs"`
	Truncated bool                    `json:"truncated"`
}

// DiscoverExecutable analyzes executable regions using header entries,
// symbols and exports as roots. Budgets apply across the whole image.
func DiscoverExecutable(d *document.Document, maxBytes int64, maxFunctions, maxInstr int) (*ImageAnalysis, error) {
	info, err := parsers.InspectExecutable(d)
	if err != nil {
		return nil, err
	}
	if maxBytes <= 0 || maxBytes > 32<<20 {
		maxBytes = 8 << 20
	}
	if maxFunctions <= 0 || maxFunctions > 4096 {
		maxFunctions = 512
	}
	if maxInstr <= 0 || maxInstr > 1_000_000 {
		maxInstr = 131_072
	}
	analysis := &ImageAnalysis{Info: info}
	remainingBytes := maxBytes
	remainingFunctions := maxFunctions
	remainingInstr := maxInstr
	for _, region := range info.Regions {
		if remainingBytes <= 0 || remainingFunctions <= 0 || remainingInstr <= 0 {
			analysis.Truncated = true
			break
		}
		length := region.Size
		if length > 4<<20 {
			length = 4 << 20
			analysis.Truncated = true
		}
		if length > remainingBytes {
			length = remainingBytes
			analysis.Truncated = true
		}
		if length <= 0 {
			continue
		}
		buf, err := d.Range(region.Offset, length)
		if err != nil {
			return nil, err
		}
		seeds := make([]Seed, 0)
		for _, seed := range info.Seeds {
			if seed.Offset >= region.Offset && seed.Offset < region.Offset+length {
				seeds = append(seeds, Seed{Name: seed.Name, Entry: seed.Offset})
			}
		}
		if len(seeds) == 0 {
			seeds = append(seeds, Seed{Name: region.Name, Entry: region.Offset})
		}
		program := DiscoverFunctionsFromSeeds(buf, region.Offset, info.Arch, seeds, remainingFunctions, remainingInstr)
		analysis.Programs = append(analysis.Programs, program)
		remainingBytes -= length
		remainingFunctions -= len(program.Functions)
		remainingInstr -= program.Instructions
		analysis.Truncated = analysis.Truncated || program.Truncated
	}
	return analysis, nil
}
