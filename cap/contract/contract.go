// Package contract defines optional deterministic contract execution. No runtime,
// compiler, journal implementation or deployment transaction is supplied here.
package contract

import (
	"context"
	"github.com/lengzhao/GoChainKit/cap/model"
	"github.com/lengzhao/GoChainKit/cap/state"
	"github.com/lengzhao/GoChainKit/core"
)

// RuntimeID binds instruction semantics, metering and artifact format versions.
type RuntimeID string

// Artifact.ID is a content address over Runtime and Code with a versioned,
// deterministic encoding; loaders verify it before execution. No local paths.
type Artifact struct {
	ID      core.ID
	Runtime RuntimeID
	Code    []byte
}
type CodeProvider interface {
	Code(context.Context, core.ID) (Artifact, error)
}

// Compiler is off-chain tooling: sources/options are canonical bytes defined by
// its format. Output must be reproducible; timestamps/local paths cannot affect
// chain identity. Deployment addressing belongs to the protocol, not compiler.
type Compiler interface {
	Compile(context.Context, []byte) (Artifact, error)
}
type Request struct {
	Code     core.ID
	Caller   core.ID
	Contract core.ID
	Input    []byte
	Budget   uint64
}
type Status uint8

const (
	Success Status = iota
	Revert
	OutOfGas
)

type Result struct {
	Status Status
	Output []byte
	Used   uint64
}
type Event struct {
	Kind string
	Data []byte
}

// Host provides isolated state and agreed execution context. Caller/Contract are
// opaque identities, not forced accounts. Events are execution-local and must
// roll back together with journal changes. Assets/cross-calls are extra optional
// interfaces owned by their consumers, not mandatory methods on this Host.
type Host interface {
	State() state.Journal
	Context() model.ExecutionContext
	Emit(context.Context, Event) error
}

// Runtime reports deterministic execution failure as Result, local faults as
// error. On Revert/OutOfGas callers discard this invocation's state/events, while
// chain-specific nonce/fee changes can live outside that checkpoint. Used never
// exceeds Budget. Context cancellation/local deadlines are errors, never agreed
// OutOfGas. Inputs are read-only; result bytes are caller-owned.
type Runtime interface {
	Identity() RuntimeID
	Execute(context.Context, Request, Host) (Result, error)
}
