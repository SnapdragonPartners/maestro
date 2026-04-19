package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"orchestrator/pkg/proto"
)

// openIncident stores an incident locally and notifies the PM.
func (d *Driver) openIncident(ctx context.Context, incident *proto.Incident) {
	d.openIncidents[incident.ID] = incident
	d.syncIncidentsToStateData()

	msg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), "pm-001")
	msg.SetTypedPayload(proto.NewIncidentOpenedPayload(incident))
	if err := d.ExecuteEffect(ctx, &SendMessageEffect{Message: msg}); err != nil {
		d.logger.Warn("Failed to send incident_opened for %s: %v", incident.ID, err)
	}

	d.logger.Info("Opened incident %s: %s", incident.ID, incident.Title)
}

// resolveIncident closes an open incident and notifies the PM.
func (d *Driver) resolveIncident(ctx context.Context, incidentID, resolution, message string) {
	inc, exists := d.openIncidents[incidentID]
	if !exists {
		return
	}

	inc.ResolvedAt = time.Now().UTC().Format(time.RFC3339)
	inc.Resolution = resolution
	delete(d.openIncidents, incidentID)
	d.syncIncidentsToStateData()

	msg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), "pm-001")
	msg.SetTypedPayload(proto.NewIncidentResolvedPayload(&proto.IncidentResolvedPayload{
		IncidentID: incidentID,
		Resolution: resolution,
		Message:    message,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}))
	if err := d.ExecuteEffect(ctx, &SendMessageEffect{Message: msg}); err != nil {
		d.logger.Warn("Failed to send incident_resolved for %s: %v", incidentID, err)
	}

	d.logger.Info("Resolved incident %s: %s (%s)", incidentID, resolution, message)
}

// resolveAllIncidents closes all open incidents with the given resolution.
func (d *Driver) resolveAllIncidents(ctx context.Context, resolution, message string) {
	for id := range d.openIncidents {
		d.resolveIncident(ctx, id, resolution, message)
	}
}

// resolveIncidentsByFailureID closes incidents matching a specific failure ID.
func (d *Driver) resolveIncidentsByFailureID(ctx context.Context, failureID, resolution, message string) {
	for id, inc := range d.openIncidents {
		if inc.FailureID == failureID {
			d.resolveIncident(ctx, id, resolution, message)
		}
	}
}

// syncIncidentsToStateData mirrors open incidents to a state data key for FSM visibility.
func (d *Driver) syncIncidentsToStateData() {
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
func (d *Driver) collectOpenIncidentsJSON() *string {
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

// isSystemIdle checks all five conditions for opening a system_idle incident.
// Includes 60s debounce guard and blocking-incident check.
func (d *Driver) isSystemIdle() bool {
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
func (d *Driver) checkAndOpenIdleIncident(ctx context.Context) {
	for _, inc := range d.openIncidents {
		if inc.Kind == proto.IncidentKindSystemIdle {
			return
		}
	}

	if !d.isSystemIdle() {
		return
	}

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
func (d *Driver) reconcileOpenIncidents(ctx context.Context) {
	for id, inc := range d.openIncidents {
		switch inc.Kind {
		case proto.IncidentKindSystemIdle:
			if !d.isSystemIdlePredicate() {
				d.resolveIncident(ctx, id, "work_resumed", "Active coder detected, system no longer idle")
			}
		case proto.IncidentKindStoryBlocked:
			story, exists := d.queue.GetStory(inc.StoryID)
			if exists && story.GetStatus() != StatusFailed && story.GetStatus() != StatusOnHold {
				d.resolveIncident(ctx, id, "story_requeued", fmt.Sprintf("Story %s resumed", inc.StoryID))
			}
		case proto.IncidentKindClarification:
			// Closed by repair_complete handler, not by reconciliation
		}
	}
}
