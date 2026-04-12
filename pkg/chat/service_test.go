package chat

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/persistence"
)

// addTestMessage adds a message directly to in-memory state, bypassing Post (no DB needed).
func addTestMessage(s *Service, id int64, author, channel, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg := &persistence.ChatMessage{
		ID:        id,
		SessionID: "test",
		Channel:   channel,
		Author:    author,
		Text:      text,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		PostType:  PostTypeChat,
	}
	s.messages = append(s.messages, msg)
	if id >= s.nextID {
		s.nextID = id + 1
	}
}

func newTestService() *Service {
	return NewService(nil, nil)
}

func TestGetNewForChannel_OnlyReturnsRequestedChannel(t *testing.T) {
	svc := newTestService()
	svc.RegisterAgent("architect-001", []string{ChannelDevelopment, ChannelProduct})

	// Add messages to both channels from a coder
	addTestMessage(svc, 1, "@coder-001", ChannelDevelopment, "dev message 1")
	addTestMessage(svc, 2, "@coder-001", ChannelProduct, "product message 1")
	addTestMessage(svc, 3, "@coder-001", ChannelDevelopment, "dev message 2")

	ctx := context.Background()
	resp, err := svc.GetNewForChannel(ctx, "architect-001", ChannelDevelopment)
	if err != nil {
		t.Fatalf("GetNewForChannel failed: %v", err)
	}

	if len(resp.Messages) != 2 {
		t.Fatalf("expected 2 development messages, got %d", len(resp.Messages))
	}
	for _, msg := range resp.Messages {
		if msg.Channel != ChannelDevelopment {
			t.Errorf("expected channel %s, got %s", ChannelDevelopment, msg.Channel)
		}
	}
}

func TestGetNewForChannel_ExcludesOwnMessages(t *testing.T) {
	svc := newTestService()
	svc.RegisterAgent("architect-001", []string{ChannelDevelopment})

	// Message from the architect itself (uses FormatAuthor)
	addTestMessage(svc, 1, FormatAuthor("architect-001"), ChannelDevelopment, "my own message")
	// Message from a coder
	addTestMessage(svc, 2, "@coder-001", ChannelDevelopment, "coder message")

	ctx := context.Background()
	resp, err := svc.GetNewForChannel(ctx, "architect-001", ChannelDevelopment)
	if err != nil {
		t.Fatalf("GetNewForChannel failed: %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message (excluding own), got %d", len(resp.Messages))
	}
	if resp.Messages[0].Author != "@coder-001" {
		t.Errorf("expected message from @coder-001, got %s", resp.Messages[0].Author)
	}
}

func TestGetNewForChannel_RespectsChannelCursor(t *testing.T) {
	svc := newTestService()
	svc.RegisterAgent("architect-001", []string{ChannelDevelopment})

	addTestMessage(svc, 1, "@coder-001", ChannelDevelopment, "old message")
	addTestMessage(svc, 2, "@coder-001", ChannelDevelopment, "new message")

	// Advance cursor past message 1
	ctx := context.Background()
	if err := svc.UpdateCursorForChannel(ctx, "architect-001", ChannelDevelopment, 1); err != nil {
		t.Fatalf("UpdateCursorForChannel failed: %v", err)
	}

	resp, err := svc.GetNewForChannel(ctx, "architect-001", ChannelDevelopment)
	if err != nil {
		t.Fatalf("GetNewForChannel failed: %v", err)
	}

	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 new message after cursor, got %d", len(resp.Messages))
	}
	if resp.Messages[0].ID != 2 {
		t.Errorf("expected message ID 2, got %d", resp.Messages[0].ID)
	}
}

func TestGetNewForChannel_UnregisteredAgent(t *testing.T) {
	svc := newTestService()

	ctx := context.Background()
	resp, err := svc.GetNewForChannel(ctx, "unknown-agent", ChannelDevelopment)
	if err != nil {
		t.Fatalf("GetNewForChannel failed: %v", err)
	}

	if len(resp.Messages) != 0 {
		t.Errorf("expected 0 messages for unregistered agent, got %d", len(resp.Messages))
	}
}

func TestUpdateCursorForChannel_OnlyAdvancesSpecifiedChannel(t *testing.T) {
	svc := newTestService()
	svc.RegisterAgent("architect-001", []string{ChannelDevelopment, ChannelProduct})

	addTestMessage(svc, 1, "@coder-001", ChannelDevelopment, "dev msg")
	addTestMessage(svc, 2, "@pm-001", ChannelProduct, "product msg")

	ctx := context.Background()

	// Update only the development cursor
	if err := svc.UpdateCursorForChannel(ctx, "architect-001", ChannelDevelopment, 5); err != nil {
		t.Fatalf("UpdateCursorForChannel failed: %v", err)
	}

	// Development channel should have no new messages
	if svc.HaveNewMessagesForChannel("architect-001", ChannelDevelopment) {
		t.Error("expected no new development messages after cursor update")
	}

	// Product channel cursor should be untouched — message 2 should still be new
	if !svc.HaveNewMessagesForChannel("architect-001", ChannelProduct) {
		t.Error("expected product messages to still be new (cursor untouched)")
	}

	// Verify via GetNewForChannel that product still returns the message
	prodResp, err := svc.GetNewForChannel(ctx, "architect-001", ChannelProduct)
	if err != nil {
		t.Fatalf("GetNewForChannel for product failed: %v", err)
	}
	if len(prodResp.Messages) != 1 {
		t.Fatalf("expected 1 product message, got %d", len(prodResp.Messages))
	}
}

func TestUpdateCursorForChannel_ErrorOnUnregisteredAgent(t *testing.T) {
	svc := newTestService()

	ctx := context.Background()
	err := svc.UpdateCursorForChannel(ctx, "unknown-agent", ChannelDevelopment, 5)
	if err == nil {
		t.Error("expected error for unregistered agent")
	}
}

func TestUpdateCursorForChannel_ErrorOnUnregisteredChannel(t *testing.T) {
	svc := newTestService()
	svc.RegisterAgent("architect-001", []string{ChannelDevelopment})

	ctx := context.Background()
	err := svc.UpdateCursorForChannel(ctx, "architect-001", ChannelProduct, 5)
	if err == nil {
		t.Error("expected error for unregistered channel")
	}
}

func TestHaveNewMessagesForChannel_OnlyChecksSpecifiedChannel(t *testing.T) {
	svc := newTestService()
	svc.RegisterAgent("architect-001", []string{ChannelDevelopment, ChannelProduct})

	// Only add a message to the product channel
	addTestMessage(svc, 1, "@pm-001", ChannelProduct, "product msg")

	// Development should have no new messages
	if svc.HaveNewMessagesForChannel("architect-001", ChannelDevelopment) {
		t.Error("expected no new development messages")
	}

	// Product should have new messages
	if !svc.HaveNewMessagesForChannel("architect-001", ChannelProduct) {
		t.Error("expected new product messages")
	}
}

func TestHaveNewMessagesForChannel_ExcludesOwnMessages(t *testing.T) {
	svc := newTestService()
	svc.RegisterAgent("architect-001", []string{ChannelDevelopment})

	// Only the architect's own message in the channel
	addTestMessage(svc, 1, FormatAuthor("architect-001"), ChannelDevelopment, "my message")

	if svc.HaveNewMessagesForChannel("architect-001", ChannelDevelopment) {
		t.Error("expected no new messages (own messages excluded)")
	}
}

func TestHaveNewMessagesForChannel_UnregisteredAgent(t *testing.T) {
	svc := newTestService()

	if svc.HaveNewMessagesForChannel("unknown-agent", ChannelDevelopment) {
		t.Error("expected false for unregistered agent")
	}
}

func TestHaveNewMessagesForChannel_UnregisteredChannel(t *testing.T) {
	svc := newTestService()
	svc.RegisterAgent("architect-001", []string{ChannelDevelopment})

	addTestMessage(svc, 1, "@coder-001", ChannelProduct, "product msg")

	if svc.HaveNewMessagesForChannel("architect-001", ChannelProduct) {
		t.Error("expected false for unregistered channel")
	}
}

func TestProductCursorUnaffectedByDevChannelRead(t *testing.T) {
	svc := newTestService()
	svc.RegisterAgent("architect-001", []string{ChannelDevelopment, ChannelProduct})

	// Add messages to both channels
	addTestMessage(svc, 1, "@coder-001", ChannelDevelopment, "dev msg 1")
	addTestMessage(svc, 2, "@coder-001", ChannelDevelopment, "dev msg 2")
	addTestMessage(svc, 3, "@pm-001", ChannelProduct, "product msg 1")
	addTestMessage(svc, 4, "@pm-001", ChannelProduct, "product msg 2")

	ctx := context.Background()

	// Read development channel and update its cursor
	devResp, err := svc.GetNewForChannel(ctx, "architect-001", ChannelDevelopment)
	if err != nil {
		t.Fatalf("GetNewForChannel failed: %v", err)
	}
	if len(devResp.Messages) != 2 {
		t.Fatalf("expected 2 dev messages, got %d", len(devResp.Messages))
	}
	if updateErr := svc.UpdateCursorForChannel(ctx, "architect-001", ChannelDevelopment, devResp.NewPointer); updateErr != nil {
		t.Fatalf("UpdateCursorForChannel failed: %v", updateErr)
	}

	// Product channel should still return all messages (cursor not advanced)
	prodResp, err := svc.GetNewForChannel(ctx, "architect-001", ChannelProduct)
	if err != nil {
		t.Fatalf("GetNewForChannel for product failed: %v", err)
	}
	if len(prodResp.Messages) != 2 {
		t.Fatalf("expected 2 product messages (cursor untouched), got %d", len(prodResp.Messages))
	}

	// Also verify via HaveNewMessages
	if svc.HaveNewMessagesForChannel("architect-001", ChannelProduct) != true {
		t.Error("product channel should still have new messages")
	}
	if svc.HaveNewMessagesForChannel("architect-001", ChannelDevelopment) != false {
		t.Error("development channel should have no new messages after cursor update")
	}
}
