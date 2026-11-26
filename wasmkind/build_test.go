package wasmkind

import (
	"bytes"
	"strconv"
	"strings"
)

// The modules the tests run are written here as bytes, in the binary format of WebAssembly: a
// module of the four exports of kaname's Wasm whose call is a few instructions, to stop it, trap it,
// grow its memory or answer a line the test chooses.

// The instructions the modules use, and the types of their values.
const (
	opUnreachable = 0x00
	opLoop        = 0x03
	opIf          = 0x04
	opEnd         = 0x0b
	opBr          = 0x0c
	opBrIf        = 0x0d
	opDrop        = 0x1a
	opLocalGet    = 0x20
	opLocalSet    = 0x21
	opGlobalGet   = 0x23
	opGlobalSet   = 0x24
	opI32Store    = 0x36
	opI32Store8   = 0x3a
	opMemorySize  = 0x3f
	opMemoryGrow  = 0x40
	opI32Const    = 0x41
	opI32Eqz      = 0x45
	opI32GtU      = 0x4b
	opI32Add      = 0x6a
	opI32Sub      = 0x6b
	opI32Mul      = 0x6c
	opI32DivU     = 0x6e
	vI32          = 0x7f
	vI64          = 0x7e
	blockEmpty    = 0x40
)

// fn is a function of a module: its parameters and results, its locals of i32, its body.
type fn struct {
	params, results []byte
	locals          int
	body            []byte
}

// wmod is a module to write.
type wmod struct {
	imports   []byte // the import section's entries, written whole
	nImports  int
	funcs     []fn
	exports   []wexport
	memMin    uint32
	memMax    int64 // -1 for none
	noMemory  bool
	globals   []int32 // mutable globals of i32
	data      []wdata
	startFunc int // -1 for none
}

type wexport struct {
	name string
	kind byte // 0 a function, 2 a memory
	idx  uint32
}

type wdata struct {
	at    uint32
	bytes []byte
}

func uleb(n uint64) []byte {
	var b []byte
	for {
		c := byte(n & 0x7f)
		n >>= 7
		if n != 0 {
			b = append(b, c|0x80)
			continue
		}
		return append(b, c)
	}
}

func sleb(n int64) []byte {
	var b []byte
	for {
		c := byte(n & 0x7f)
		n >>= 7
		if n == 0 && c&0x40 == 0 || n == -1 && c&0x40 != 0 {
			return append(b, c)
		}
		b = append(b, c|0x80)
	}
}

func i32c(n int32) []byte { return append([]byte{opI32Const}, sleb(int64(n))...) }

func vec(n int, body []byte) []byte { return append(uleb(uint64(n)), body...) }

func name(s string) []byte { return append(uleb(uint64(len(s))), s...) }

func section(id byte, body []byte) []byte {
	return append(append([]byte{id}, uleb(uint64(len(body)))...), body...)
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// bytes writes the module: a type for each function, in its order.
func (m wmod) bytes() []byte {
	out := []byte("\x00asm\x01\x00\x00\x00")
	var types []byte
	for _, f := range m.funcs {
		types = append(types, 0x60)
		types = append(types, vec(len(f.params), f.params)...)
		types = append(types, vec(len(f.results), f.results)...)
	}
	out = append(out, section(1, vec(len(m.funcs), types))...)
	if m.nImports > 0 {
		out = append(out, section(2, vec(m.nImports, m.imports))...)
	}
	var funcs []byte
	for i := range m.funcs {
		funcs = append(funcs, uleb(uint64(i))...)
	}
	out = append(out, section(3, vec(len(m.funcs), funcs))...)
	if !m.noMemory {
		lim := cat([]byte{0}, uleb(uint64(m.memMin)))
		if m.memMax >= 0 {
			lim = cat([]byte{1}, uleb(uint64(m.memMin)), uleb(uint64(m.memMax)))
		}
		out = append(out, section(5, vec(1, lim))...)
	}
	if len(m.globals) > 0 {
		var g []byte
		for _, v := range m.globals {
			g = append(g, vI32, 1)
			g = append(g, i32c(v)...)
			g = append(g, opEnd)
		}
		out = append(out, section(6, vec(len(m.globals), g))...)
	}
	var ex []byte
	for _, e := range m.exports {
		ex = append(ex, name(e.name)...)
		ex = append(ex, e.kind)
		ex = append(ex, uleb(uint64(e.idx))...)
	}
	out = append(out, section(7, vec(len(m.exports), ex))...)
	if m.startFunc >= 0 {
		out = append(out, section(8, uleb(uint64(m.startFunc)))...)
	}
	var code []byte
	for _, f := range m.funcs {
		var body []byte
		if f.locals > 0 {
			body = append(body, vec(1, cat(uleb(uint64(f.locals)), []byte{vI32}))...)
		} else {
			body = append(body, 0)
		}
		body = append(body, f.body...)
		body = append(body, opEnd)
		code = append(code, vec(len(body), body)...)
	}
	out = append(out, section(10, vec(len(m.funcs), code))...)
	if len(m.data) > 0 {
		var d []byte
		for _, x := range m.data {
			d = append(d, 0)
			d = append(d, i32c(int32(x.at))...)
			d = append(d, opEnd)
			d = append(d, vec(len(x.bytes), x.bytes)...)
		}
		out = append(out, section(11, vec(len(m.data), d))...)
	}
	return out
}

// The places in the memory of the modules below: the pair of the answer's place and length that
// call answers, the answer a module holds, and where the allocator starts.
const (
	pairAt   = 16
	answerAt = 1024
	heapAt   = 8192
)

// door is a module of the four exports whose call is body, with the allocator of the others: a
// bump of the global 0 from heapAt, which grows the memory where it needs to. The global 1 counts
// the calls, for a module that answers by it. call's locals are 0 the input's place, 1 its length,
// and 2 and 3 of its own.
func door(body []byte, data ...wdata) wmod {
	realloc := cat(
		// at = heap; heap += size; grow while heap is past the memory
		[]byte{opGlobalGet, 0, opLocalSet, 4},
		[]byte{opGlobalGet, 0, opLocalGet, 3, opI32Add, opGlobalSet, 0},
		[]byte{opLoop, blockEmpty},
		[]byte{opGlobalGet, 0, opMemorySize, 0}, i32c(65536), []byte{opI32Mul, opI32GtU, opIf, blockEmpty},
		i32c(1), []byte{opMemoryGrow, 0}, i32c(-1), []byte{0x46 /* i32.eq */, opIf, blockEmpty, opUnreachable, opEnd},
		[]byte{opBr, 1, opEnd, opEnd},
		[]byte{opLocalGet, 4},
	)
	return wmod{
		funcs: []fn{
			{params: []byte{vI32, vI32}, results: []byte{vI32}, locals: 2, body: body},
			{params: []byte{vI32, vI32, vI32, vI32}, results: []byte{vI32}, locals: 1, body: realloc},
			{params: []byte{vI32}},
		},
		exports:   []wexport{{"call", 0, 0}, {"cabi_realloc", 0, 1}, {"cabi_post_call", 0, 2}, {"memory", 2, 0}},
		memMin:    1,
		memMax:    -1,
		globals:   []int32{heapAt, 0},
		data:      data,
		startFunc: -1,
	}
}

// pair stores the place and the length of the answer at pairAt, and answers pairAt: the body's end
// for a call whose answer is where the two locals given say.
func pair(at, n byte) []byte {
	return cat(
		i32c(pairAt), []byte{opLocalGet, at, opI32Store, 2, 0},
		i32c(pairAt+4), []byte{opLocalGet, n, opI32Store, 2, 0},
		i32c(pairAt),
	)
}

// echo answers the line it is given.
func echo() wmod { return door(pair(0, 1)) }

// answering answers a line it holds, whatever it is given.
func answering(answer string) wmod {
	body := cat(i32c(answerAt), []byte{opLocalSet, 2}, i32c(int32(len(answer))), []byte{opLocalSet, 3}, pair(2, 3))
	return door(body, wdata{answerAt, []byte(answer)})
}

// counting answers `{"out":N}`, N the number of calls the instance has had (one digit). Of a line
// longer than past, it does what after says first: trap, loop (and never answer), grow (its memory a
// page at a time until it cannot, and then trap, as Rust's allocator stops a module whose memory
// cannot grow), or "grow <n>" (by n pages, and answer).
func counting(past int32, after string) wmod {
	answer := `{"out":0}`
	var long []byte
	switch {
	case after == "trap":
		long = []byte{opUnreachable}
	case after == "loop":
		long = []byte{opLoop, blockEmpty, opBr, 0, opEnd}
	case after == "grow":
		long = growAll
	case strings.HasPrefix(after, "grow "):
		n, _ := strconv.Atoi(strings.TrimPrefix(after, "grow "))
		long = cat(i32c(int32(n)), []byte{opMemoryGrow, 0, opDrop})
	}
	body := cat(
		[]byte{opLocalGet, 1}, i32c(past), []byte{opI32GtU, opIf, blockEmpty}, long, []byte{opEnd},
		[]byte{opGlobalGet, 1}, i32c(1), []byte{opI32Add, opGlobalSet, 1},
		i32c(answerAt+7), []byte{opGlobalGet, 1}, i32c('0'), []byte{opI32Add, opI32Store8, 0, 0},
		i32c(answerAt), []byte{opLocalSet, 2}, i32c(int32(len(answer))), []byte{opLocalSet, 3}, pair(2, 3),
	)
	return door(body, wdata{answerAt, []byte(answer)})
}

// growAll grows the memory a page at a time until it cannot, and then traps.
var growAll = cat(
	[]byte{opLoop, blockEmpty},
	i32c(1), []byte{opMemoryGrow, 0}, i32c(-1), []byte{0x47 /* i32.ne */, opBrIf, 0, opEnd},
	[]byte{opUnreachable},
)

// looping never answers.
func looping() wmod {
	return door(cat([]byte{opLoop, blockEmpty, opBr, 0, opEnd}, i32c(0)))
}

// growing grows its memory a page at a time until it cannot, and then traps, as Rust's allocator
// stops a module whose memory cannot grow.
func growing() wmod { return door(growAll) }

// trapping traps at once.
func trapping() wmod { return door([]byte{opUnreachable}) }

// dividing divides by zero.
func dividing() wmod { return door(cat(i32c(1), i32c(0), []byte{opI32DivU})) }

// outside answers a place past its memory.
func outside() wmod {
	body := cat(i32c(1<<30), []byte{opLocalSet, 2}, i32c(4), []byte{opLocalSet, 3}, pair(2, 3))
	return door(body)
}

// withImport is m with one import of a function of the type of its first function, from env.
func withImport(m wmod, what byte) wmod {
	var desc []byte
	switch what {
	case 0:
		desc = cat([]byte{0}, uleb(2)) // the type of cabi_post_call, (i32) -> ()
	case 3:
		desc = []byte{3, vI32, 0}
	}
	m.imports = cat(name("env"), name("hook"), desc)
	m.nImports = 1
	// the indexes of the functions move past the imported one
	if what == 0 {
		for i := range m.exports {
			if m.exports[i].kind == 0 {
				m.exports[i].idx++
			}
		}
	}
	return m
}
