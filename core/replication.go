package core

import "context"

// BlockImporter verifies, executes and atomically imports a branch block, then
// maintains the mempool for a canonical switch. A supplied result is still fully
// validated. CommittedError distinguishes committed maintenance failure.
// Implementations serialize imports for one ledger owner.
type BlockImporter interface {
	ProtocolBound
	LedgerBound
	Import(context.Context, Block, *ExecutionResult) error
}

// Replicator owns chain catch-up and transaction propagation, independently of
// proof production. Exactly one owner installs the Network handler. Run blocks
// until cancellation or a local service fault, and releases its workers.
type Replicator interface {
	ProtocolBound
	LedgerBound
	Run(context.Context) error
}

// LocalServiceError distinguishes failures of trusted local dependencies from
// untrusted peer input. Supervisors must preserve these even during shutdown.
type LocalServiceError struct{ Cause error }

func (e *LocalServiceError) Error() string { return "local chain service failed: " + e.Cause.Error() }
func (e *LocalServiceError) Unwrap() error { return e.Cause }
