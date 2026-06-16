package disasm

import (
	"sort"
	"strconv"
	"strings"
)

// cfg.go builds control-flow graphs and cross-references on top of the
// per-architecture decoders. It performs a recursive-descent sweep from an
// entry point: it follows direct branches and the fall-through after calls
// (calls leave the function, so their targets are recorded as xrefs rather
// than followed), splits the decoded instructions into basic blocks, and
// records every direct code/data reference it can resolve statically.

// Block is one basic block: a maximal straight-line instruction run.
type Block struct {
	Start  int64            `json:"start"`
	End    int64            `json:"end"` // address after the last instruction
	Instrs []map[string]any `json:"instrs"`
	Succs  []int64          `json:"succs"` // successor block start addresses
	Kind   string           `json:"kind"`  // ret | jmp | cond | call | fallthrough
}

// Xref is a single static reference from one address to another.
type Xref struct {
	From int64  `json:"from"`
	To   int64  `json:"to"`
	Kind string `json:"kind"` // call | jump | data
}

// CFG is the analysis result for one entry point.
type CFG struct {
	Entry   int64    `json:"entry"`
	Arch    string   `json:"arch"`
	Blocks  []*Block `json:"blocks"`
	Calls   []int64  `json:"calls"`   // distinct call targets (in or out of range)
	Xrefs   []Xref   `json:"xrefs"`   // all resolved references
	Unknown int      `json:"unknown"` // indirect/unresolved branch+call count
}

// Function is a discovered function and its structural metrics.
type Function struct {
	Name         string   `json:"name"`
	Entry        int64    `json:"entry"`
	End          int64    `json:"end"`
	Blocks       []*Block `json:"blocks"`
	Instructions int      `json:"instructions"`
	Complexity   int      `json:"complexity"`
	Calls        []int64  `json:"calls"`
	CalledBy     []int64  `json:"calledBy"`
	Unknown      int      `json:"unknown"`
}

// CallEdge is one direct call from a discovered function.
type CallEdge struct {
	Caller   int64 `json:"caller"`
	CallSite int64 `json:"callSite"`
	Callee   int64 `json:"callee"`
	External bool  `json:"external"`
}

// Seed is a known or suspected function entry supplied by executable metadata.
type Seed struct {
	Name  string `json:"name"`
	Entry int64  `json:"entry"`
}

// Program is a bounded recursive function-discovery result for one byte range.
type Program struct {
	Entry         int64       `json:"entry"`
	Base          int64       `json:"base"`
	End           int64       `json:"end"`
	Arch          string      `json:"arch"`
	Functions     []*Function `json:"functions"`
	Edges         []CallEdge  `json:"edges"`
	ExternalCalls []int64     `json:"externalCalls"`
	Instructions  int         `json:"instructions"`
	Unknown       int         `json:"unknown"`
	Truncated     bool        `json:"truncated"`
}

type rawInstr struct {
	addr   int64
	length int
	mnem   string
	class  string
	target int64
	hasTgt bool
	data   int64
	hasDat bool
	m      map[string]any
}

// BuildCFG decodes from entry (within buf, which starts at address base) and
// returns the control-flow graph plus discovered references.
func BuildCFG(buf []byte, base int64, arch string, entry int64, maxInstr int) *CFG {
	if maxInstr <= 0 {
		maxInstr = 8192
	}
	cfg := &CFG{Entry: entry, Arch: arch}
	decoded := map[int64]*rawInstr{}
	inRange := func(a int64) bool { return a >= base && a < base+int64(len(buf)) }

	var stack []int64
	push := func(a int64) {
		if inRange(a) {
			stack = append(stack, a)
		}
	}
	push(entry)

	for len(stack) > 0 && len(decoded) < maxInstr {
		addr := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for inRange(addr) && len(decoded) < maxInstr {
			if _, seen := decoded[addr]; seen {
				break
			}
			ri := decodeAt(buf, addr, base, arch)
			if ri == nil {
				break
			}
			decoded[addr] = ri
			next := addr + int64(ri.length)
			switch ri.class {
			case "ret":
				addr = -1
			case "jmp":
				if ri.hasTgt {
					addr = ri.target
				} else {
					cfg.Unknown++
					addr = -1
				}
			case "cond":
				if ri.hasTgt {
					push(ri.target)
				} else {
					cfg.Unknown++
				}
				addr = next
			case "call":
				if !ri.hasTgt {
					cfg.Unknown++
				}
				addr = next
			default:
				addr = next
			}
			if addr < 0 {
				break
			}
		}
	}

	// Collect references and call targets.
	callSet := map[int64]bool{}
	for _, ri := range decoded {
		if ri.hasTgt {
			switch ri.class {
			case "call":
				cfg.Xrefs = append(cfg.Xrefs, Xref{From: ri.addr, To: ri.target, Kind: "call"})
				if !callSet[ri.target] {
					callSet[ri.target] = true
					cfg.Calls = append(cfg.Calls, ri.target)
				}
			case "jmp", "cond":
				cfg.Xrefs = append(cfg.Xrefs, Xref{From: ri.addr, To: ri.target, Kind: "jump"})
			}
		}
		if ri.hasDat {
			cfg.Xrefs = append(cfg.Xrefs, Xref{From: ri.addr, To: ri.data, Kind: "data"})
		}
	}
	sort.Slice(cfg.Calls, func(i, j int) bool { return cfg.Calls[i] < cfg.Calls[j] })
	sort.Slice(cfg.Xrefs, func(i, j int) bool {
		if cfg.Xrefs[i].From != cfg.Xrefs[j].From {
			return cfg.Xrefs[i].From < cfg.Xrefs[j].From
		}
		return cfg.Xrefs[i].To < cfg.Xrefs[j].To
	})

	cfg.Blocks = buildBlocks(decoded, inRange)
	return cfg
}

// DiscoverFunctions recursively follows direct calls from entry, building a
// bounded call graph over buf. Indirect calls are counted but cannot be
// discovered statically. maxInstr is a total budget across all functions.
func DiscoverFunctions(buf []byte, base int64, arch string, entry int64, maxFunctions, maxInstr int) *Program {
	return DiscoverFunctionsFromSeeds(buf, base, arch, []Seed{{Entry: entry}}, maxFunctions, maxInstr)
}

// DiscoverFunctionsFromSeeds analyzes named and unnamed roots plus every
// direct in-range call target reached from them.
func DiscoverFunctionsFromSeeds(buf []byte, base int64, arch string, seeds []Seed, maxFunctions, maxInstr int) *Program {
	if maxFunctions <= 0 || maxFunctions > 4096 {
		maxFunctions = 256
	}
	if maxInstr <= 0 || maxInstr > 1_000_000 {
		maxInstr = 65_536
	}
	end := base + int64(len(buf))
	entry := base
	if len(seeds) > 0 {
		entry = seeds[0].Entry
	}
	program := &Program{Entry: entry, Base: base, End: end, Arch: arch}
	inRange := func(addr int64) bool { return addr >= base && addr < end }
	queue := make([]int64, 0, len(seeds))
	queued := map[int64]bool{}
	names := map[int64]string{}
	for _, seed := range seeds {
		if !inRange(seed.Entry) {
			continue
		}
		if !queued[seed.Entry] {
			queued[seed.Entry] = true
			queue = append(queue, seed.Entry)
		}
		if seed.Name != "" {
			names[seed.Entry] = seed.Name
		}
	}
	if len(queue) == 0 {
		program.Truncated = true
		return program
	}

	discovered := map[int64]*Function{}
	callerSets := map[int64]map[int64]bool{}
	externalSet := map[int64]bool{}

	for len(queue) > 0 && len(discovered) < maxFunctions && program.Instructions < maxInstr {
		fnEntry := queue[0]
		queue = queue[1:]
		if discovered[fnEntry] != nil {
			continue
		}
		remaining := maxInstr - program.Instructions
		perFunction := remaining
		if perFunction > 8192 {
			perFunction = 8192
		}
		cfg := BuildCFG(buf, base, arch, fnEntry, perFunction)
		fn := summarizeFunction(cfg, names[fnEntry])
		discovered[fnEntry] = fn
		program.Functions = append(program.Functions, fn)
		program.Instructions += fn.Instructions
		program.Unknown += fn.Unknown

		for _, xref := range cfg.Xrefs {
			if xref.Kind != "call" {
				continue
			}
			external := !inRange(xref.To)
			program.Edges = append(program.Edges, CallEdge{
				Caller: fnEntry, CallSite: xref.From, Callee: xref.To, External: external,
			})
			if external {
				externalSet[xref.To] = true
				continue
			}
			if callerSets[xref.To] == nil {
				callerSets[xref.To] = map[int64]bool{}
			}
			callerSets[xref.To][fnEntry] = true
			if !queued[xref.To] {
				queued[xref.To] = true
				queue = append(queue, xref.To)
			}
		}
	}

	if len(queue) > 0 || program.Instructions >= maxInstr {
		program.Truncated = true
	}
	for _, fn := range program.Functions {
		for caller := range callerSets[fn.Entry] {
			fn.CalledBy = append(fn.CalledBy, caller)
		}
		sort.Slice(fn.CalledBy, func(i, j int) bool { return fn.CalledBy[i] < fn.CalledBy[j] })
	}
	for target := range externalSet {
		program.ExternalCalls = append(program.ExternalCalls, target)
	}
	sort.Slice(program.Functions, func(i, j int) bool { return program.Functions[i].Entry < program.Functions[j].Entry })
	sort.Slice(program.Edges, func(i, j int) bool {
		if program.Edges[i].Caller != program.Edges[j].Caller {
			return program.Edges[i].Caller < program.Edges[j].Caller
		}
		if program.Edges[i].CallSite != program.Edges[j].CallSite {
			return program.Edges[i].CallSite < program.Edges[j].CallSite
		}
		return program.Edges[i].Callee < program.Edges[j].Callee
	})
	sort.Slice(program.ExternalCalls, func(i, j int) bool { return program.ExternalCalls[i] < program.ExternalCalls[j] })
	return program
}

func summarizeFunction(cfg *CFG, name string) *Function {
	if name == "" {
		name = unknownFunctionName(cfg.Entry)
	}
	fn := &Function{
		Name: name, Entry: cfg.Entry, Blocks: cfg.Blocks,
		Calls: append([]int64(nil), cfg.Calls...), Unknown: cfg.Unknown,
	}
	edges := 0
	for _, block := range cfg.Blocks {
		if block.End > fn.End {
			fn.End = block.End
		}
		fn.Instructions += len(block.Instrs)
		edges += len(block.Succs)
	}
	fn.Complexity = edges - len(cfg.Blocks) + 2
	if fn.Complexity < 1 {
		fn.Complexity = 1
	}
	return fn
}

func unknownFunctionName(entry int64) string {
	return "sub_" + strconv.FormatInt(entry, 16)
}

func buildBlocks(decoded map[int64]*rawInstr, inRange func(int64) bool) []*Block {
	if len(decoded) == 0 {
		return nil
	}
	addrs := make([]int64, 0, len(decoded))
	for a := range decoded {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i] < addrs[j] })

	// Leaders: first instr, every branch target, and the instruction after any
	// branch/call/ret.
	leaders := map[int64]bool{addrs[0]: true}
	for _, a := range addrs {
		ri := decoded[a]
		next := a + int64(ri.length)
		switch ri.class {
		case "ret":
			if _, ok := decoded[next]; ok {
				leaders[next] = true
			}
		case "jmp":
			if ri.hasTgt {
				if _, ok := decoded[ri.target]; ok {
					leaders[ri.target] = true
				}
			}
			if _, ok := decoded[next]; ok {
				leaders[next] = true
			}
		case "cond", "call":
			if ri.hasTgt {
				if _, ok := decoded[ri.target]; ok {
					leaders[ri.target] = true
				}
			}
			if _, ok := decoded[next]; ok {
				leaders[next] = true
			}
		}
	}

	var blocks []*Block
	var cur *Block
	flush := func() {
		if cur != nil {
			blocks = append(blocks, cur)
			cur = nil
		}
	}
	for idx, a := range addrs {
		ri := decoded[a]
		if leaders[a] {
			flush()
			cur = &Block{Start: a}
		}
		if cur == nil {
			cur = &Block{Start: a}
		}
		cur.Instrs = append(cur.Instrs, ri.m)
		next := a + int64(ri.length)
		cur.End = next

		// Decide whether this instruction terminates the block.
		isLast := idx == len(addrs)-1
		nextIsLeader := false
		if !isLast {
			if leaders[addrs[idx+1]] {
				nextIsLeader = true
			}
		}
		terminator := ri.class == "ret" || ri.class == "jmp" || ri.class == "cond"
		if terminator || nextIsLeader || isLast {
			cur.Kind = blockKind(ri.class)
			switch ri.class {
			case "ret":
				// no successors
			case "jmp":
				if ri.hasTgt && inRange(ri.target) {
					cur.Succs = []int64{ri.target}
				}
			case "cond":
				if ri.hasTgt && inRange(ri.target) {
					cur.Succs = append(cur.Succs, ri.target)
				}
				if _, ok := decoded[next]; ok {
					cur.Succs = append(cur.Succs, next)
				}
			default:
				if _, ok := decoded[next]; ok {
					cur.Succs = []int64{next}
				}
			}
			flush()
		}
	}
	flush()
	return blocks
}

func blockKind(class string) string {
	switch class {
	case "ret", "jmp", "cond", "call":
		return class
	default:
		return "fallthrough"
	}
}

// decodeAt decodes a single instruction at address addr (buf starts at base).
func decodeAt(buf []byte, addr, base int64, arch string) *rawInstr {
	off := int(addr - base)
	if off < 0 || off >= len(buf) {
		return nil
	}
	end := off + 16 // max x86 instruction length; fixed-width arches need <= 4
	if end > len(buf) {
		end = len(buf)
	}
	res := Disassemble(buf[off:end], addr, arch, 1)
	if len(res) == 0 {
		return nil
	}
	m := res[0]
	ri := &rawInstr{addr: addr, m: m}
	ri.length = byteCount(m["bytes"].(string))
	if ri.length <= 0 {
		ri.length = 1
	}
	ri.mnem, _ = m["mnemonic"].(string)
	ops, _ := m["operands"].(string)
	ri.class = classify(ri.mnem)
	if t, ok := directTarget(ops); ok {
		ri.target, ri.hasTgt = t, true
	}
	if dataReferencing(ri.class) {
		if d, ok := dataTarget(ops); ok {
			ri.data, ri.hasDat = d, true
		}
	}
	return ri
}

func byteCount(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Fields(s))
}

func classify(mnem string) string {
	switch mnem {
	case "ret", "retf", "iret":
		return "ret"
	case "jmp", "jmpf", "b", "br":
		return "jmp"
	case "call", "callf", "bl", "blr", "jal", "jalr":
		return "call"
	case "cbz", "cbnz", "tbz", "tbnz", "loop", "loope", "loopne", "jecxz", "jrcxz":
		return "cond"
	}
	if strings.HasPrefix(mnem, "b.") { // AArch64 conditional branch
		return "cond"
	}
	if strings.HasPrefix(mnem, "j") { // x86 Jcc (jmp already handled above)
		return "cond"
	}
	if strings.HasPrefix(mnem, "beq") || strings.HasPrefix(mnem, "bne") { // MIPS
		return "cond"
	}
	return "normal"
}

func dataReferencing(class string) bool {
	// RIP-relative resolved targets show up as "; 0x.." comments on any
	// instruction; only mine them on non-control-flow ops as data xrefs.
	return class == "normal" || class == "call"
}

// directTarget extracts a branch/call target when the final operand is a bare
// absolute address (a direct, statically known target). Register/memory
// indirect operands return false.
func directTarget(operands string) (int64, bool) {
	if i := strings.Index(operands, ";"); i >= 0 {
		operands = operands[:i]
	}
	if c := strings.LastIndex(operands, ","); c >= 0 {
		operands = operands[c+1:]
	}
	operands = strings.TrimSpace(operands)
	return parseHexAddr(operands)
}

// dataTarget extracts a resolved address from a "; 0x.." trailing comment
// (e.g. an x86 RIP-relative memory operand).
func dataTarget(operands string) (int64, bool) {
	i := strings.Index(operands, ";")
	if i < 0 {
		return 0, false
	}
	return parseHexAddr(strings.TrimSpace(operands[i+1:]))
}

func parseHexAddr(s string) (int64, bool) {
	if !strings.HasPrefix(s, "0x") {
		return 0, false
	}
	v, err := strconv.ParseUint(s[2:], 16, 64)
	if err != nil {
		return 0, false
	}
	return int64(v), true
}
