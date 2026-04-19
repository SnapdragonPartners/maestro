package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"orchestrator/pkg/proto"
)

// openIncident stores an incident locally and notifies the PM.
// Thread-safe: acquires incidentsMu.
func (d *Driver) openIncident(ctx context.Context, incident *proto.Incident) {
	d.incidentsMu.Lock()
	d.openIncidents[incident.ID] = incident
	d.syncIncidentsToStateDataLocked()
	d.incidentsMu.Unlock()

	msg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), "pm-001")
	msg.SetTypedPayload(proto.NewIncidentOpenedPayload(incident))
	if err := d.ExecuteEffect(ctx, &SendMessageEffect{Message: msg}); err != nil {
		d.logger.Warn("Failed to send incident_opened for %s: %v", incident.ID, err)
	}

	d.logger.Info("Opened incident %s: %s", incident.ID, incident.Title)
}

// resolveIncidentLocked closes an open incident and returns the notification to send.
// Caller must hold incidentsMu.
func (d *Driver) resolveIncidentLocked(incidentID, resolution, message string) *proto.AgentMsg {
	inc, exists := d.openIncidents[incidentID]
	if !exists {
		return nil
	}

	inc.ResolvedAt = time.Now().UTC().Format(time.RFC3339)
	inc.Resolution = resolution
	delete(d.openIncidents, incidentID)
	d.syncIncidentsToStateDataLocked()

	msg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), "pm-001")
	msg.SetTypedPayload(proto.NewIncidentResolvedPayload(&proto.IncidentResolvedPayload{
		IncidentID: incidentID,
		Resolution: resolution,
		Message:    message,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}))
	return msg
}

// resolveIncident closes an open incident and notifies the PM.
// Thread-safe: acquires incidentsMu.
func (d *Driver) resolveIncident(ctx context.Context, incidentID, resolution, message string) {
	d.incidentsMu.Lock()
	msg := d.resolveIncidentLocked(incidentID, resolution, message)
	d.incidentsMu.Unlock()

	if msg == nil {
		return
	}
	if err := d.ExecuteEffect(ctx, &SendMessageEffect{Message: msg}); err != nil {
		d.logger.Warn("Failed to send incident_resolved for %s: %v", incidentID, err)
	}
	d.logger.Info("Resolved incident %s: %s (%s)", incidentID, resolution, message)
}

// resolveAllIncidents closes all open incidents with the given resolution.
// Thread-safe: acquires incidentsMu.
func (d *Driver) resolveAllIncidents(ctx context.Context, resolution, message string) {
	d.incidentsMu.Lock()
	var msgs []*proto.AgentMsg
	for id := range d.openIncidents {
		if msg := d.resolveIncidentLocked(id, resolution, message); msg != nil {
			msgs = append(msgs, msg)
		}
	}
	d.incidentsMu.Unlock()

	for _, msg := range msgs {
		if err := d.ExecuteEffect(ctx, &SendMessageEffect{Message: msg}); err != nil {
			d.logger.Warn("Failed to send incident_resolved: %v", err)
		}
	}
}

// resolveIncidentsByFailureID closes incidents matching a specific failure ID.
// Thread-safe: acquires incidentsMu.
func (d *Driver) resolveIncidentsByFailureID(ctx context.Context, failureID, resolution, message string) {
	d.incidentsMu.Lock()
	var msgs []*proto.AgentMsg
	for id, inc := range d.openIncidents {
		if inc.FailureID == failureID {
			if msg := d.resolveIncidentLocked(id, resolution, message); msg != nil {
				msgs = append(msgs, msg)
			}
		}
	}
	d.incidentsMu.Unlock()

	for _, msg := range msgs {
		if err := d.ExecuteEffect(ctx, &SendMessageEffect{Message: msg}); err != nil {
			d.logger.Warn("Failed to send incident_resolved: %v", err)
		}
	}
}

// syncIncidentsToStateDataLocked mirrors open incidents to a state data key.
// Caller must hold incidentsMu.
func (d *Driver) syncIncidentsToStateDataLocked() {
	if len(d.openIncidents) == 0 {
		d.SetStateData(StateKeyOpenIncidents, "")
		return
	}
	raw, err := json.Marshal(d.openIncidents)
	if err != nil {
		d.logger.Warn("Failed to marshal open incidents for state data: %v", err)
		return
	}
	d.SetStateData(StateKeyOpenIncidents, string(raw))
}

// collectOpenIncidentsJSON serializes open incidents as a JSON string for persistence.
// Thread-safe: acquires incidentsMu.
func (d *Driver) collectOpenIncidentsJSON() *string {
	d.incidentsMu.Lock()
	defer d.incidentsMu.Unlock()

	if len(d.openIncidents) == 0 {
		return nil
	}
	data, err := json.Marshal(d.openIncidents)
	if err != nil {
		d.logger.Warn("Failed to marshal open incidents: %v", err)
		return nil
	}
	s := string(data)
	return &s
}

// isSystemIdlePredicate checks the core idle conditions (1-3) without timing or blocking-incident guards.
// Used for closing idle incidents — once work resumes, close immediately.
// Does NOT acquire incidentsMu (no map access).
func (d *Driver) isSystemIdlePredicate() bool {
	if d.queue.AllStoriesTerminal() {
		return false
	}

	pendingCount := len(d.queue.GetStoriesByStatus(StatusPending))
	dispatchedCount := len(d.queue.GetStoriesByStatus(StatusDispatched))
	if pendingCount == 0 && dispatchedCount == 0 {
		return false
	}

	agents := d.dispatcher.GetRegisteredAgents()
	for i := range agents {
		if agents[i].Type != "coder" {
			continue
		}
		switch agents[i].State {
		case "PLANNING", "CODING", "TESTING":
			return false
		}
	}

	return true
}

// isSystemIdleLocked checks all five conditions for opening a system_idle incident.
// Includes 60s debounce guard and blocking-incident check.
// Caller must hold incidentsMu.
func (d *Driver) isSystemIdleLocked() bool {
	if !d.isSystemIdlePredicate() {
		return false
	}

	for _, inc := range d.openIncidents {
		if inc.Blocking {
			return false
		}
	}

	if d.monitoringIdleSince.IsZero() {
		return false
	}
	return time.Since(d.monitoringIdleSince) > 60*time.Second
}

// checkAndOpenIdleIncident opens a system_idle incident if conditions are met.
// Thread-safe: acquires incidentsMu.
func (d *Driver) checkAndOpenIdleIncident(ctx context.Context) {
	// Update debounce timer based on current idle predicate.
	// This ensures the 60s guard measures time since idle predicate first became true,
	// not time since entering MONITORING.
	if d.isSystemIdlePredicate() {
		if d.monitoringIdleSince.IsZero() {
			d.monitoringIdleSince = time.Now()
		}
	} else {
		d.monitoringIdleSince = time.Time{}
		return
	}

	d.incidentsMu.Lock()
	for _, inc := range d.openIncidents {
		if inc.Kind == proto.IncidentKindSystemIdle {
			d.incidentsMu.Unlock()
			return
		}
	}

	if !d.isSystemIdleLocked() {
		d.incidentsMu.Unlock()
		return
	}
	d.incidentsMu.Unlock()

	pending := d.queue.GetStoriesByStatus(StatusPending)
	dispatched := d.queue.GetStoriesByStatus(StatusDispatched)
	affected := make([]string, 0, len(pending)+len(dispatched))
	for _, s := range pending {
		affected = append(affected, s.ID)
	}
	for _, s := range dispatched {
		affected = append(affected, s.ID)
	}

	incident := &proto.Incident{
		ID:               fmt.Sprintf("incident-system_idle-system-%s", time.Now().UTC().Format("20060102T150405Z")),
		Kind:             proto.IncidentKindSystemIdle,
		Scope:            "system",
		Title:            "System idle with pending work",
		Summary:          "No active coders are making progress but there are stories waiting for work.",
		AffectedStoryIDs: affected,
		AllowedActions:   []proto.IncidentAction{proto.IncidentActionTryAgain, proto.IncidentActionResume},
		Blocking:         true,
		OpenedAt:         time.Now().UTC().Format(time.RFC3339),
	}
	d.openIncident(ctx, incident)
}

// reconcileOpenIncidents auto-closes incidents whose conditions are no longer true.
// Thread-safe: acquires incidentsMu for snapshot, then resolves outside lock.
func (d *Driver) reconcileOpenIncidents(ctx context.Context) {
	type resolution struct {
		id, reason, message string
	}
	var toResolve []resolution

	d.incidentsMu.Lock()
	for id, inc := range d.openIncidents {
		switch inc.Kind {
		case proto.IncidentKindSystemIdle:
			if !d.isSystemIdlePredicate() {
				toResolve = append(toResolve, resolution{id, "work_resumed", "Active coder detected, system no longer idle"})
			}
		case proto.IncidentKindStoryBlocked:
			story, exists := d.queue.GetStory(inc.StoryID)
			if exists && story.GetStatus() != StatusFailed && story.GetStatus() != StatusOnHold {
				toResolve = append(toResolve, resolution{id, "story_requeued", fmt.Sprintf("Story %s resumed", inc.StoryID)})
			}
		case proto.IncidentKindClarification:
			// Closed by repair_complete handler, not by reconciliation
		}
	}
	d.incidentsMu.Unlock()

	for i := range toResolve {
		d.resolveIncident(ctx, toResolve[i].id, toResolve[i].reason, toResolve[i].message)
	}
}

// handleIncidentAction processes an incident_action request from PM.
// Performs recovery first, resolves incident on success. Returns next state or "".
func (d *Driver) handleIncidentAction(ctx context.Context, msg *proto.AgentMsg) proto.State {
	typedPayload := msg.GetTypedPayload()
	if typedPayload == nil {
		d.logger.Warn("Incident action has no payload")
		return ""
	}

	action, err := typedPayload.ExtractIncidentAction()
	if err != nil {
		d.logger.Warn("Failed to extract incident_action payload: %v", err)
		return ""
	}

	d.logger.Info("🔧 Incident action received: %s on %s (%s)", action.Action, action.IncidentID, action.Reason)

	// Look up incident under lock and take a snapshot
	d.incidentsMu.Lock()
	inc, exists := d.openIncidents[action.IncidentID]
	var incidentCopy proto.Incident
	if exists {
		incidentCopy = *inc
	}
	d.incidentsMu.Unlock()

	if !exists {
		d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
			fmt.Sprintf("Incident %s not found", action.IncidentID))
		return ""
	}

	// Validate action is allowed
	allowed := false
	for _, a := range incidentCopy.AllowedActions {
		if string(a) == action.Action {
			allowed = true
			break
		}
	}
	if !allowed {
		d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
			fmt.Sprintf("Action %q not allowed for incident %s", action.Action, action.IncidentID))
		return ""
	}

	switch incidentCopy.Kind {
	case proto.IncidentKindSystemIdle:
		return d.handleSystemIdleAction(ctx, action, &incidentCopy)
	case proto.IncidentKindStoryBlocked:
		return d.handleStoryBlockedAction(ctx, action, &incidentCopy)
	case proto.IncidentKindClarification:
		return d.handleClarificationAction(ctx, action, &incidentCopy)
	default:
		d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
			fmt.Sprintf("Unknown incident kind: %s", incidentCopy.Kind))
		return ""
	}
}

func (d *Driver) handleSystemIdleAction(ctx context.Context, action *proto.IncidentActionPayload, inc *proto.Incident) proto.State {
	switch action.Action {
	case string(proto.IncidentActionResume), string(proto.IncidentActionTryAgain):
		return d.resumeSystemIdle(ctx, action, inc)
	default:
		d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
			fmt.Sprintf("Action %q not supported for system_idle incidents", action.Action))
		return ""
	}
}

func (d *Driver) handleStoryBlockedAction(ctx context.Context, action *proto.IncidentActionPayload, inc *proto.Incident) proto.State {
	switch action.Action {
	case string(proto.IncidentActionResume), string(proto.IncidentActionTryAgain):
		return d.resumeStoryBlocked(ctx, action, inc)
	case string(proto.IncidentActionSkip):
		return d.skipStoryBlocked(ctx, action, inc)
	case string(proto.IncidentActionChangeRequest):
		return d.changeRequestStoryBlocked(ctx, action, inc)
	default:
		d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
			fmt.Sprintf("Action %q not supported for story_blocked incidents", action.Action))
		return ""
	}
}

func (d *Driver) handleClarificationAction(ctx context.Context, action *proto.IncidentActionPayload, inc *proto.Incident) proto.State {
	switch action.Action {
	case string(proto.IncidentActionResume):
		return d.resumeClarification(ctx, action, inc)
	default:
		d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
			fmt.Sprintf("Action %q not supported for clarification incidents", action.Action))
		return ""
	}
}

func (d *Driver) resumeSystemIdle(ctx context.Context, action *proto.IncidentActionPayload, _ *proto.Incident) proto.State {
	// Sweep orphaned dispatched stories before re-dispatching.
	// Use the dispatcher's lease table as source of truth for ownership —
	// QueuedStory.AssignedAgent is not populated during live dispatch.
	leasedStoryIDs := d.dispatcher.GetLeasedStoryIDs()
	requeued := d.queue.RequeueOrphanedDispatched(leasedStoryIDs)
	if len(requeued) > 0 {
		d.logger.Info("🔄 Requeued %d orphaned dispatched stories: %v", len(requeued), requeued)
	}

	// Resume dispatch if suppressed
	if suppressed, reason := d.queue.IsDispatchSuppressed(); suppressed {
		d.queue.ResumeDispatch()
		d.logger.Info("▶️ Dispatch resumed (was suppressed: %s)", reason)
	}

	d.resolveIncident(ctx, action.IncidentID, "resumed", action.Reason)
	d.monitoringIdleSince = time.Time{}
	d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, true,
		fmt.Sprintf("System resumed, %d orphaned stories requeued", len(requeued)))
	return StateDispatching
}

func (d *Driver) resumeStoryBlocked(ctx context.Context, action *proto.IncidentActionPayload, inc *proto.Incident) proto.State {
	story, exists := d.queue.GetStory(inc.StoryID)
	if !exists {
		d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
			fmt.Sprintf("Story %s not found", inc.StoryID))
		return ""
	}

	status := story.GetStatus()
	switch status {
	case StatusFailed:
		if retryErr := d.queue.RetryFailedStory(inc.StoryID); retryErr != nil {
			d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
				fmt.Sprintf("Failed to retry story %s: %v", inc.StoryID, retryErr))
			return ""
		}
		d.logger.Info("🔄 Retrying failed story %s", inc.StoryID)

	case StatusOnHold:
		failureID := inc.FailureID
		if failureID == "" {
			d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
				"Story is on_hold but incident has no failure ID")
			return ""
		}
		released, _ := d.queue.ReleaseHeldStoriesByFailure(failureID, action.Reason)
		if len(released) == 0 {
			d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
				fmt.Sprintf("No held stories released for failure %s — story may have already been resumed", failureID))
			return ""
		}
		d.logger.Info("🔓 Released %d held stories for failure %s", len(released), failureID)

	default:
		d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
			fmt.Sprintf("Story %s is in unexpected status %s", inc.StoryID, status))
		return ""
	}

	// Resume dispatch if suppressed
	if suppressed, reason := d.queue.IsDispatchSuppressed(); suppressed {
		d.queue.ResumeDispatch()
		d.logger.Info("▶️ Dispatch resumed (was suppressed: %s)", reason)
	}

	// Resolve all incidents with matching failure ID (not just this one)
	if inc.FailureID != "" {
		d.resolveIncidentsByFailureID(ctx, inc.FailureID, "resumed", action.Reason)
	}
	d.resolveIncident(ctx, action.IncidentID, "resumed", action.Reason)

	d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, true,
		fmt.Sprintf("Story %s recovery initiated", inc.StoryID))
	return StateDispatching
}

func (d *Driver) resumeClarification(ctx context.Context, action *proto.IncidentActionPayload, inc *proto.Incident) proto.State {
	failureID := inc.FailureID
	if failureID == "" {
		d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
			"Clarification incident has no failure ID")
		return ""
	}

	released, _ := d.queue.ReleaseHeldStoriesByFailure(failureID, action.Reason)
	if len(released) == 0 {
		d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
			fmt.Sprintf("No held stories released for failure %s — issue may have already been resolved", failureID))
		return ""
	}
	d.logger.Info("🔓 Released %d held stories for failure %s", len(released), failureID)

	// Resume dispatch if suppressed
	if suppressed, reason := d.queue.IsDispatchSuppressed(); suppressed {
		d.queue.ResumeDispatch()
		d.logger.Info("▶️ Dispatch resumed (was suppressed: %s)", reason)
	}

	// Resolve all incidents with matching failure ID
	d.resolveIncidentsByFailureID(ctx, failureID, "resumed", action.Reason)
	d.resolveIncident(ctx, action.IncidentID, "resumed", action.Reason)

	d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, true,
		fmt.Sprintf("Released %d held stories, clarification resolved", len(released)))
	return StateDispatching
}

func (d *Driver) skipStoryBlocked(ctx context.Context, action *proto.IncidentActionPayload, inc *proto.Incident) proto.State {
	if err := d.queue.SkipStory(inc.StoryID); err != nil {
		d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
			fmt.Sprintf("Failed to skip story %s: %v", inc.StoryID, err))
		return ""
	}
	d.logger.Info("⏭️ Skipped story %s: %s", inc.StoryID, action.Reason)

	// Resume dispatch if suppressed
	if suppressed, reason := d.queue.IsDispatchSuppressed(); suppressed {
		d.queue.ResumeDispatch()
		d.logger.Info("▶️ Dispatch resumed (was suppressed: %s)", reason)
	}

	// Resolve only this specific incident — skip does NOT release failure-group siblings
	d.resolveIncident(ctx, action.IncidentID, "skipped", action.Reason)

	d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, true,
		fmt.Sprintf("Story %s skipped", inc.StoryID))
	return StateDispatching
}

func (d *Driver) changeRequestStoryBlocked(ctx context.Context, action *proto.IncidentActionPayload, inc *proto.Incident) proto.State {
	story, exists := d.queue.GetStory(inc.StoryID)
	if !exists {
		d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
			fmt.Sprintf("Story %s not found", inc.StoryID))
		return ""
	}

	if story.GetStatus() != StatusFailed {
		d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
			fmt.Sprintf("change_request requires a failed story (status=%s); use resume to release held stories", story.GetStatus()))
		return ""
	}

	if action.Content != "" {
		story.Content += "\n\n## Change Request (User)\n\n" + action.Content
		d.logger.Info("📝 Appended change request to story %s (%d chars)", inc.StoryID, len(action.Content))
	}

	// Reset attempt count for a fresh budget
	story.AttemptCount = 0

	if retryErr := d.queue.RetryFailedStory(inc.StoryID); retryErr != nil {
		d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, false,
			fmt.Sprintf("Failed to retry story %s after change request: %v", inc.StoryID, retryErr))
		return ""
	}
	d.logger.Info("🔄 Retrying story %s with change request", inc.StoryID)

	// Resume dispatch if suppressed
	if suppressed, reason := d.queue.IsDispatchSuppressed(); suppressed {
		d.queue.ResumeDispatch()
		d.logger.Info("▶️ Dispatch resumed (was suppressed: %s)", reason)
	}

	// Resolve incidents by failure ID (same as resume for failed stories)
	if inc.FailureID != "" {
		d.resolveIncidentsByFailureID(ctx, inc.FailureID, "change_request", action.Reason)
	}
	d.resolveIncident(ctx, action.IncidentID, "change_request", action.Reason)

	d.sendIncidentActionResult(ctx, action.IncidentID, action.Action, true,
		fmt.Sprintf("Story %s modified and retried with fresh budget", inc.StoryID))
	return StateDispatching
}

// sendIncidentActionResult sends the outcome of an incident action back to PM.
func (d *Driver) sendIncidentActionResult(ctx context.Context, incidentID, action string, success bool, message string) {
	msg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), "pm-001")
	msg.SetTypedPayload(proto.NewIncidentActionResultPayload(&proto.IncidentActionResultPayload{
		IncidentID: incidentID,
		Action:     action,
		Success:    success,
		Message:    message,
	}))
	if err := d.ExecuteEffect(ctx, &SendMessageEffect{Message: msg}); err != nil {
		d.logger.Warn("Failed to send incident_action_result: %v", err)
	}
}
