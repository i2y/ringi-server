package wasmkind

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"github.com/tetratelabs/wazero/sys"
)

// What stopped a call that answered nothing (StopError.Err).
var (
	// ErrTime is a call that ran past its time (Options.Time).
	ErrTime = errors.New("the call ran past its time")
	// ErrMemory is a call that grew its instance's memory past its pages (Options.Pages).
	ErrMemory = errors.New("the call grew its memory past its pages")
	// ErrTrap is a call the module stopped with a trap of WebAssembly (an unreachable, a division by
	// zero, a read outside its memory, its stack used up), or that answered outside its memory.
	ErrTrap = errors.New("the module trapped")
)

// StopError is a call of an entry that was stopped: by its time (ErrTime), by its memory
// (ErrMemory), or by the module (ErrTrap). The instance it ran in is not used again.
type StopError struct {
	// Module and Function are the module and the function of the door the entry called.
	Module, Function string
	// Err is what stopped it: ErrTime, ErrMemory or ErrTrap.
	Err error
	// Limit is the time or the pages the call ran past, as the sentence says it; "" for a trap.
	Limit string
	// Cause is what the runtime said, for a trap.
	Cause error
}

func (e *StopError) Error() string {
	head := "the call of `" + e.Function + "` of the module `" + e.Module + "`"
	switch e.Err {
	case ErrTime:
		return head + " ran past its time of " + e.Limit + ", and was stopped"
	case ErrMemory:
		return head + " grew its memory past " + e.Limit + ", and was stopped"
	}
	if e.Cause != nil {
		return head + " was stopped by the module: " + firstLine(e.Cause.Error())
	}
	return head + " was stopped by the module"
}

// Unwrap answers what stopped the call, and what the runtime said.
func (e *StopError) Unwrap() []error {
	if e.Cause != nil {
		return []error{e.Err, e.Cause}
	}
	return []error{e.Err}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

// runner runs the calls of one compiled module: in instances kept between calls (or one for each
// call, Options.Fresh), no more of them at once than Options.Instances.
type runner struct {
	rt       wazero.Runtime
	compiled wazero.CompiledModule
	file     string
	// the names of the exports
	callName, reallocName, postName, memoryName string
	o                                           Options
	// slots holds a token for each call that runs, and idle the instances calls left
	slots chan struct{}
	idle  chan *instance
	// keep is the most pages an instance may have to be kept for the next call: a call that grew it
	// past them leaves it to be dropped, so that no instance keeps the memory one large call took.
	keep uint32
}

// newRunner compiles the module and checks its exports, and makes its first instance.
func newRunner(ctx context.Context, bin []byte, file, call, realloc, post, memory string, o Options) (*runner, error) {
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true))
	r := &runner{rt: rt, file: file, callName: call, reallocName: realloc, postName: post, memoryName: memory, o: o,
		slots: make(chan struct{}, o.Instances)}
	if !o.Fresh {
		r.idle = make(chan *instance, o.Instances)
	}
	compiled, err := rt.CompileModule(ctx, bin)
	if err != nil {
		return nil, r.fail(fmt.Errorf("%s: %w", file, err))
	}
	r.compiled = compiled
	fns := compiled.ExportedFunctions()
	for _, x := range []struct {
		name string
		typ  [2][]api.ValueType
	}{{call, callType}, {realloc, reallocTy}, {post, postType}} {
		f, ok := fns[x.name]
		if !ok {
			return nil, r.fail(fmt.Errorf("%s exports no function %s, which wasm.json names", file, x.name))
		}
		if !slices.Equal(f.ParamTypes(), x.typ[0]) || !slices.Equal(f.ResultTypes(), x.typ[1]) {
			return nil, r.fail(fmt.Errorf("%s's %s takes %s and answers %s, and a module of kinds' takes %s and answers %s", file, x.name, typesSaid(f.ParamTypes()), typesSaid(f.ResultTypes()), typesSaid(x.typ[0]), typesSaid(x.typ[1])))
		}
	}
	mem, ok := compiled.ExportedMemories()[memory]
	if !ok {
		return nil, r.fail(fmt.Errorf("%s exports no memory %s, which wasm.json names", file, memory))
	}
	if mem.Min() > o.Pages {
		return nil, r.fail(fmt.Errorf("%s's memory starts with %d pages, past the %s its instances may grow to", file, mem.Min(), pagesSaid(o.Pages)))
	}
	r.keep = min(o.Pages, max(2*mem.Min(), mem.Min()+64))
	in, err := r.instance()
	if err != nil {
		return nil, r.fail(fmt.Errorf("%s cannot be started: %w", file, err))
	}
	r.leave(in)
	return r, nil
}

// fail closes the runtime of a module that is not loaded, and answers err.
func (r *runner) fail(err error) error {
	r.rt.Close(context.Background())
	return err
}

// instance is one instance of the module, with its exports and its memory.
type instance struct {
	mod                 api.Module
	call, realloc, post api.Function
	memory              api.Memory
	mem                 *memory
}

// instance makes an instance of the module, its memory held to the pages of the options. Its start
// runs within the time of a call.
func (r *runner) instance() (*instance, error) {
	in := &instance{}
	alloc := experimental.MemoryAllocatorFunc(func(capacity, _ uint64) experimental.LinearMemory {
		in.mem = &memory{limit: uint64(r.o.Pages) * 65536}
		in.mem.buf = make([]byte, 0, min(capacity, in.mem.limit))
		return in.mem
	})
	ctx, cancel := context.WithTimeout(experimental.WithMemoryAllocator(context.Background(), alloc), r.o.Time)
	defer cancel()
	mod, err := r.rt.InstantiateModule(ctx, r.compiled, wazero.NewModuleConfig().WithName("").WithStartFunctions())
	if err != nil {
		return nil, err
	}
	in.mod = mod
	in.call, in.realloc, in.post = mod.ExportedFunction(r.callName), mod.ExportedFunction(r.reallocName), mod.ExportedFunction(r.postName)
	in.memory = mod.ExportedMemory(r.memoryName)
	return in, nil
}

// take answers an instance a call left, or a new one.
func (r *runner) take() (*instance, error) {
	select {
	case in := <-r.idle:
		return in, nil
	default:
		return r.instance()
	}
}

// leave keeps an instance for the next call, or drops it: under Options.Fresh, past the pages kept,
// or with as many kept already.
func (r *runner) leave(in *instance) {
	if r.idle != nil && in.memory.Size()/65536 <= r.keep {
		select {
		case r.idle <- in:
			return
		default:
		}
	}
	in.mod.Close(context.Background())
}

// call gives the module one line and answers what it answers, within the time and the pages of
// the options: a call stopped by either, or by the module, is a *StopError, and its instance is
// dropped.
func (r *runner) call(e *entry, line []byte) ([]byte, error) {
	r.slots <- struct{}{}
	defer func() { <-r.slots }()
	in, err := r.take()
	if err != nil {
		return nil, &StopError{Module: e.module, Function: e.function, Err: ErrTrap, Cause: err}
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.o.Time)
	defer cancel()
	in.mem.hit = false
	answer, err := in.answer(ctx, line)
	if err != nil {
		in.mod.Close(context.Background())
		s := &StopError{Module: e.module, Function: e.function, Err: ErrTrap, Cause: err}
		var exit *sys.ExitError
		switch {
		case errors.As(err, &exit) && (exit.ExitCode() == sys.ExitCodeDeadlineExceeded || exit.ExitCode() == sys.ExitCodeContextCanceled):
			s.Err, s.Limit, s.Cause = ErrTime, r.o.Time.String(), nil
		case in.mem.hit:
			s.Err, s.Limit, s.Cause = ErrMemory, pagesSaid(r.o.Pages), nil
		}
		return nil, s
	}
	r.leave(in)
	return answer, nil
}

// errOutside is an answer of the module outside its memory.
var errOutside = errors.New("the module answered a place outside its memory")

// answer runs one call of the canonical ABI (kaname's DESIGN 36.11): the line is put where the
// module's allocator says, `call` reads it and frees it, and what it answers points at the place and
// the length of the answer, which `cabi_post_call` frees once it is read.
func (in *instance) answer(ctx context.Context, line []byte) ([]byte, error) {
	at, err := in.realloc.Call(ctx, 0, 0, 1, uint64(len(line)))
	if err != nil {
		return nil, err
	}
	p := uint32(at[0])
	if !in.memory.Write(p, line) {
		return nil, errOutside
	}
	rs, err := in.call.Call(ctx, uint64(p), uint64(len(line)))
	if err != nil {
		return nil, err
	}
	ret := uint32(rs[0])
	ap, ok1 := in.memory.ReadUint32Le(ret)
	an, ok2 := in.memory.ReadUint32Le(ret + 4)
	b, ok3 := in.memory.Read(ap, an)
	if !ok1 || !ok2 || !ok3 {
		return nil, errOutside
	}
	answer := bytes.Clone(b)
	if _, err := in.post.Call(ctx, uint64(ret)); err != nil {
		return nil, err
	}
	return answer, nil
}

// memory is the memory of an instance, held to its limit: a growth past it is refused (the module
// sees memory.grow answer -1), and the refusal noted, so that a call it stops is said to be stopped
// by its memory.
type memory struct {
	buf   []byte
	limit uint64
	hit   bool
}

func (m *memory) Reallocate(size uint64) []byte {
	if size > m.limit {
		m.hit = true
		return nil
	}
	if size <= uint64(cap(m.buf)) {
		m.buf = m.buf[:size]
		return m.buf
	}
	b := make([]byte, size, min(max(size, 2*uint64(cap(m.buf))), m.limit))
	copy(b, m.buf)
	m.buf = b
	return b
}

func (m *memory) Free() { m.buf = nil }

// pagesSaid says a number of pages, with what they hold.
func pagesSaid(n uint32) string {
	b := uint64(n) * 65536
	switch {
	case b%(1<<20) == 0:
		return fmt.Sprintf("%d pages (%d MiB)", n, b>>20)
	default:
		return fmt.Sprintf("%d pages (%d KiB)", n, b>>10)
	}
}

// typesSaid says the types of a function's parameters or results: (i32, i32), or ().
func typesSaid(ts []api.ValueType) string {
	words := make([]string, len(ts))
	for i, t := range ts {
		words[i] = api.ValueTypeName(t)
	}
	return "(" + strings.Join(words, ", ") + ")"
}

// the exports a module of kinds has, with their types (kaname's DESIGN 36.11, the canonical ABI of
// `call: func(input: string) -> string`)
var (
	callType  = [2][]api.ValueType{{api.ValueTypeI32, api.ValueTypeI32}, {api.ValueTypeI32}}
	reallocTy = [2][]api.ValueType{{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32}, {api.ValueTypeI32}}
	postType  = [2][]api.ValueType{{api.ValueTypeI32}, {}}
)
