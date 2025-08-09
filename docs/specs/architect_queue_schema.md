# Architect Queue JSON Schema

## File Location
`state/architect_queue.json`

## Schema Structure

```json
{
  "version": "1.0",
  "last_updated": "2025-06-11T15:30:00Z",
  "architect_state": {
    "current_state": "QUEUE_MANAGEMENT",
    "transition_history": [
      {
        "from_state": "SPEC_PARSING", 
        "to_state": "STORY_GENERATION",
        "timestamp": "2025-06-11T15:20:00Z"
      }
    ]
  },
  "stories": {
    "050": {
      "id": "050",
      "title": "Implement user authentication",
      "status": "pending",
      "depends_on": ["049"],
      "est_points": 3,
      "assigned_agent": null,
      "created_at": "2025-06-11T15:15:00Z",
      "updated_at": "2025-06-11T15:15:00Z",
      "metadata": {
        "file_path": "stories/050.md",
        "generated_from_spec": true
      }
    },
    "051": {
      "id": "051", 
      "title": "Add user profile endpoints",
      "status": "in_progress",
      "depends_on": ["050"],
      "est_points": 2,
      "assigned_agent": "claude-coder-001",
      "created_at": "2025-06-11T15:15:00Z",
      "updated_at": "2025-06-11T15:25:00Z",
      "started_at": "2025-06-11T15:25:00Z",
      "metadata": {
        "file_path": "stories/051.md",
        "generated_from_spec": true
      }
    }
  },
  "agents": {
    "claude-coder-001": {
      "status": "busy",
      "current_story": "051",
      "assigned_at": "2025-06-11T15:25:00Z"
    },
    "claude-coder-002": {
      "status": "idle",
      "current_story": null,
      "assigned_at": null
    }
  },
  "questions": {
    "q_001": {
      "question_id": "q_001",
      "from_agent": "claude-coder-001",
      "story_id": "051",
      "question": "Should I use JWT or session cookies for auth?",
      "status": "answered",
      "created_at": "2025-06-11T15:27:00Z",
      "answered_at": "2025-06-11T15:28:00Z",
      "answer": "Use JWT for stateless API authentication..."
    }
  },
  "escalations": {
    "esc_001": {
      "escalation_id": "esc_001",
      "from_agent": "claude-coder-001", 
      "story_id": "051",
      "question": "Should we support OAuth login with Google?",
      "reason": "business_decision",
      "status": "pending",
      "created_at": "2025-06-11T15:29:00Z"
    }
  },
  "stats": {
    "total_stories": 15,
    "pending_stories": 8,
    "in_progress_stories": 2,
    "completed_stories": 5,
    "pending_escalations": 1
  }
}
```

## Status Values

### Story Status
- `pending` - Ready to be assigned
- `in_progress` - Currently being worked on
- `waiting_review` - Implementation submitted, under review
- `completed` - Finished and approved
- `blocked` - Dependencies not met

### Agent Status  
- `idle` - Available for new assignments
- `busy` - Working on a story
- `offline` - Not available

### Question Status
- `pending` - Awaiting architect response
- `answered` - Response provided
- `escalated` - Escalated to human review

### Escalation Status
- `pending` - Awaiting human review
- `resolved` - Human provided guidance
- `dismissed` - No action needed