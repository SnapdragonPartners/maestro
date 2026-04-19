package pm

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// syncAskToStateData mirrors the current ask to a state data key for FSM visibility.
func (d *Driver) syncAskToStateData() {
	if d.currentAsk == nil {
		d.SetStateData(StateKeyCurrentAsk, "")
		return
	}
	raw, err := json.Marshal(d.currentAsk)
	if err != nil {
		d.logger.Warn("Failed to marshal current ask for state data: %v", err)
		return
	}
	d.SetStateData(StateKeyCurrentAsk, string(raw))
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

// handleIncidentOpened processes an incident_opened notification from the architect.
func (d *Driver) handleIncidentOpened(payload *proto.MessagePayload) (proto.State, error) {
	incident, err := payload.ExtractIncidentOpened()
	if err != nil {
		d.logger.Warn("Failed to extract incident_opened payload: %v", err)
		return StateWorking, nil
	}

	d.openIncidents[incident.ID] = incident
	d.syncIncidentsToStateData()

	d.logger.Info("Received incident_opened: %s (%s)", incident.ID, incident.Title)

	var sb strings.Builder
	fmt.Fprintf(&sb, "[SYSTEM] New incident reported by architect:\n\n")
	fmt.Fprintf(&sb, "INCIDENT [%s]: %s\n", incident.ID, incident.Title)
	fmt.Fprintf(&sb, "  %s\n", incident.Summary)
	if len(incident.AllowedActions) > 0 {
		fmt.Fprintf(&sb, "  Suggested actions: %v\n", incident.AllowedActions)
	}
	sb.WriteString("\nPlease inform the user about this situation and ask how they'd like to proceed.")

	d.contextManager.AddMessage("user", sb.String())

	return StateWorking, nil
}

// handleIncidentResolved processes an incident_resolved notification from the architect.
func (d *Driver) handleIncidentResolved(payload *proto.MessagePayload) (proto.State, error) {
	resolved, err := payload.ExtractIncidentResolved()
	if err != nil {
		d.logger.Warn("Failed to extract incident_resolved payload: %v", err)
		return StateWorking, nil
	}

	delete(d.openIncidents, resolved.IncidentID)
	d.syncIncidentsToStateData()

	d.logger.Info("Incident resolved: %s (%s)", resolved.IncidentID, resolved.Resolution)

	msg := fmt.Sprintf("[SYSTEM] Incident %s has been resolved: %s", resolved.IncidentID, resolved.Message)
	d.contextManager.AddMessage("user", msg)

	return StateWorking, nil
}

// maybeInjectPendingItemsSummary injects a summary of pending asks/incidents into context,
// but only when the digest changes to avoid bloating context on handleWorking re-entry.
func (d *Driver) maybeInjectPendingItemsSummary() {
	summary := d.buildPendingSummary()
	if summary == "" {
		d.SetStateData(StateKeyPendingSummaryHash, "")
		return
	}

	hash := sha256Hex(summary)
	prevHash := ""
	if v, ok := d.GetStateValue(StateKeyPendingSummaryHash); ok {
		if s, ok := utils.SafeAssert[string](v); ok {
			prevHash = s
		}
	}
	if hash == prevHash {
		return
	}

	d.SetStateData(StateKeyPendingSummaryHash, hash)
	d.contextManager.AddMessage("user", summary)
}

// buildPendingSummary constructs the pending items summary text.
// Returns empty string if nothing is pending.
func (d *Driver) buildPendingSummary() string {
	if d.currentAsk == nil && len(d.openIncidents) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[SYSTEM] Pending items requiring attention:\n\n")

	ids := make([]string, 0, len(d.openIncidents))
	for id := range d.openIncidents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		inc := d.openIncidents[id]
		fmt.Fprintf(&sb, "INCIDENT [%s]: %s\n  %s\n  Allowed actions: %v\n\n",
			inc.ID, inc.Title, inc.Summary, inc.AllowedActions)
	}

	if d.currentAsk != nil {
		fmt.Fprintf(&sb, "UNANSWERED QUESTION [%s]: %s\n\n", d.currentAsk.ID, d.currentAsk.Prompt)
	}

	sb.WriteString("Use chat_ask_user to address these items with the user. Present the situation clearly and ask how they'd like to proceed.")
	return sb.String()
}

// resolveCurrentAsk resolves the active ask (if any) when the user responds.
func (d *Driver) resolveCurrentAsk() {
	if d.currentAsk == nil {
		return
	}
	d.currentAsk.ResolvedAt = time.Now().UTC().Format(time.RFC3339)
	d.currentAsk = nil
	d.syncAskToStateData()
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}
