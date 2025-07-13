# Multi‑Agent Orchestration – Channel Refactor  
*Engineering story bundle for a coding LLM*  
*Revision F – 2025‑07‑11*

---

## Context  

We are migrating the dispatcher and agent drivers from slice‑based, polled queues to **pure Go channels**.

Design anchors  

* **Single active spec** at a time, but **multiple architects** may answer coder questions.  
* **Channels block**; polling goroutines must disappear.  
* `storyCh` is heavily **buffered – default `8 × numCoders`** – so architects *effectively never block* while dispatching stories.  
* Architects can jump **WAITING → REQUEST** upon receiving a coder question (see *STATES.md* update).  
* A supervisor goroutine centralises error handling via `errCh`.

---

## Implementation Phases & Stories  

| Phase | ID | Title | Key theme |
|-------|----|-------|-----------|
| **1** | S‑1 | Replace `sharedWorkQueue` with `storyCh` | Channelification |
|       | S‑2 | Replace `architectRequestQueue` with `questionsCh` | Channelification |
| **2** | S‑3 | Introduce per‑coder `replyCh` map | Channelification |
|       | S‑4 | Prune legacy queues & polling loops | Cleanup |
| **3** | S‑5 | Oversize `storyCh` buffer & add metrics | Back‑pressure |
|       | S‑6 | Architect driver: WAITING → REQUEST transition | Behaviour |
|       | S‑7 | Remove `busyAgents` & refine idle tracking | Cleanup |
| **4** | S‑8 | Add `errCh` + supervisor loop | Resilience |
|       | S‑9 | Update *STATES.md* diagrams & tables | Docs |
|       | S‑10 | End‑to‑end integration test | QA |

---

## Detailed Stories  

### S‑1  Replace `sharedWorkQueue` with `storyCh`

**Definition of done**

1. Create `dispatcher.storyCh chan *proto.AgentMsg` (buffer size from config; placeholder 0 for now).  
2. Replace every `append(sharedWorkQueue, …)` with **buffered send** `storyCh <- msg` – blocking only if buffer totally full.  
3. Delete `sharedWorkQueue` slice, its mutex use, and `PullSharedWork`.  
4. Unit test: injector ⇒ architect ⇒ coder; coder blocks when channel is empty.

---

### S‑2  Replace `architectRequestQueue` with `questionsCh`

**D.o.D**

1. Add `dispatcher.questionsCh chan *proto.AgentMsg` (buffer ≈ numCoders, tweakable).  
2. Route message types `QUESTION` & `REQUEST` into `questionsCh`.  
3. Delete `architectRequestQueue` slice and `PullArchitectWork`.  
4. Update architect driver to `select` on `questionsCh`.  
5. Unit test: fake coder question; architect answers without a spec.

---

### S‑3  Introduce per‑coder `replyCh` map

**D.o.D**

1. Extend `AgentInfo` with `replyCh chan *proto.AgentMsg`.  
2. On `RegisterAgent(coder)`, allocate `make(chan *proto.AgentMsg, 1)`.  
3. Dispatcher routes `ANSWER` & `RESULT` to `replyCh[CoderID]`.  
4. Delete `coderQueue` slice & `PullCoderFeedback`.  
5. Coder driver waits on `select { case story := <-storyCh; case reply := <-myReplyCh }`.  
6. **Supervisor interaction** – When a coder terminates (fatal), supervisor drains and discards any pending messages in its `replyCh`, then closes and removes the channel.  
7. Unit test: ensure only addressed coder receives reply.

---

### S‑4  Prune legacy queues & polling loops

**D.o.D**

1. Delete `queueMutex` and all pull‑loop goroutines.  
2. Remove any `for { Pull… }` constructs in agents.  
3. Dispatcher `processMessage` is now lock‑free except around the agents map.  
4. Build passes with `-race`.

---

### S‑5  Oversize `storyCh` buffer & add metrics

**D.o.D**

1. Add `Config.StoryChannelFactor` (int, default **8**).  
2. Initialise `storyCh` with capacity `factor × numCoders`.  
3. Architect send `storyCh <- msg` expected never to block; log **ERROR** if it does.  
4. Emit gauge metric `storyCh.utilization` (`len(storyCh)/cap(storyCh)`). WARN if > 0.8 for > 30 s.  
5. Stress test: 1 architect, 50 coders, 1 000 stories; no deadlock.

---

### S‑6  Architect driver: WAITING → REQUEST transition

**D.o.D**

1. Modify driver FSM:  
   ```go
   case archstate.WAITING:
       select {
       case spec := <-specCh:
           handleSpec(spec)            // SCOPING …
       case q := <-questionsCh:
           setState(REQUEST)
           handleQuestion(q)
           // If this architect owns a spec → MONITORING; else → WAITING
       }
   ```  
2. Add logic to `handleQuestion` to return to **MONITORING** only if architect currently owns a spec; otherwise revert to **WAITING**.  
   *⚠  Note – The “owning architect” still must enqueue any newly‑unblocked stories; a future story will add that logic.*  
3. Update unit tests to cover both branches.

---

### S‑7  Remove `busyAgents` & refine idle tracking

**D.o.D**

1. Delete `busyAgents` map, mutex, & helpers.  
2. Create `idleCh chan string` with buffer `numArchitects`.  
3. Dispatcher sends non‑blocking notification:  
   ```go
   select { case idleCh <- coderID : default /*drop & log*/ }
   ```  
4. Architect listens on `idleCh` to decide merge timing.  
5. Integration test confirms architects receive idle notices.

---

### S‑8  Add `errCh` & supervisor loop

**D.o.D**

1. Define  
   ```go
   type Severity int
   const (
       Warn Severity = iota
       Fatal
   )
   type AgentError struct {
       ID  string
       Err error
       Sev Severity
   }
   ```  
2. Create `errCh chan AgentError`.  
3. Agents send **Warn** or **Fatal** to `errCh`.  
4. Supervisor goroutine:  
   * Logs every entry.  
   * On **Fatal**: removes agent from map, drains & closes its `replyCh`.  
   * If `len(architects)==0 || len(coders)==0`, log **WARN** “zero‑agent condition”.

---

### S‑9  Update *STATES.md*

1. Add mermaid arrow: `WAITING --> REQUEST : question received`.  
2. Matrix: mark ✔︎ for WAITING → REQUEST.  
3. Bump doc revision to *rev D – 2025‑07‑11*.

---

### S‑10  End‑to‑end integration test

**D.o.D**

1. Spin up dispatcher with config {coders:10, architects:3}.  
2. Inject sample spec; ensure all stories processed, questions answered.  
3. Verify no goroutine leaks (`goleak.VerifyNone`).  
4. Test passes in < 5 s on CI.

---

## Acceptance Checklist (Lead)

* [ ] Phased delivery 1 → 4 merged behind feature flag `channels_dispatcher`.  
* [ ] CI green, race detector clean, `go test ./...` ≤ 60 s.  
* [ ] Legacy poller code fully removed.  

---

*End of file*  
