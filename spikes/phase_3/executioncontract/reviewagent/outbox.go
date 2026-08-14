package main

import "maestro-spike/phase3/executioncontract/contract"

// retained is one envelope this runtime must be able to replay until it is
// acknowledged (§4).
//
// Only the REPLAY-OBLIGATED types are retained. ADR 0032 §4 narrows the
// delivery guarantee by event kind rather than promising a universal durable
// outbox: `usage` and `provenance` carry accounting whose loss corrupts a
// total, while `activity`, `heartbeat` and `warning` are diagnostic and their
// loss is tolerable and declared.
type retained struct {
	epoch uint64
	seq   uint64
	kind  string
	body  any
}

// replayObligated reports whether a message type must survive to be replayed.
func replayObligated(kind string) bool {
	return kind == contract.TypeUsage || kind == contract.TypeProvenance
}

// sendRetained emits a message and, when its type carries a replay obligation,
// keeps it until acknowledged.
func (a *agent) sendRetained(kind string, body any) error {
	if err := a.send(kind, body); err != nil {
		return err
	}
	if !replayObligated(kind) {
		return nil
	}
	a.mu.Lock()
	a.outbox = append(a.outbox, retained{epoch: a.epoch, seq: a.w.LastSeq(), kind: kind, body: body})
	a.mu.Unlock()
	return nil
}

// replayUnacked re-emits everything still outstanding, under its ORIGINAL
// identity. Re-sending under a fresh identity would be a new event, not a
// replay, and the receiver would count it twice.
func (a *agent) replayUnacked() {
	a.mu.Lock()
	pending := make([]retained, len(a.outbox))
	copy(pending, a.outbox)
	a.mu.Unlock()
	for _, r := range pending {
		_ = a.w.SendAs(a.inv.ID(), r.epoch, r.seq, r.kind, r.body)
	}
}
