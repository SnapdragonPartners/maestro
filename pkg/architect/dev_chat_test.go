package architect

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/chat"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
)

// newTestChatService creates a chat.Service for testing without database.
func newTestChatService() *chat.Service {
	return chat.NewService(nil, nil)
}

// newTestDriverWithDevChat creates a test driver wired with a dev chat service.
func newTestDriverWithDevChat(chatSvc *chat.Service) *Driver {
	persistCh := make(chan<- *persistence.Request, 10)
	baseSM := agent.NewBaseStateMachine("test-arch", StateWaiting, nil, architectTransitions)

	// Create minimal dispatcher with config
	cfg := &config.Config{
		Agents: &config.AgentConfig{MaxCoders: 2},
	}
	disp, _ := dispatch.NewDispatcher(cfg)

	d := &Driver{
		BaseStateMachine:   baseSM,
		agentContexts:      make(map[string]*contextmgr.ContextManager),
		contextMutex:       sync.RWMutex{},
		logger:             logx.NewLogger("test-arch"),
		queue:              NewQueue(nil),
		escalationHandler:  NewEscalationHandler(NewQueue(nil)),
		persistenceChannel: persistCh,
		workDir:            "/tmp/test-workspace",
		devChatService:     chatSvc,
		reviewStreaks:      make(map[string]map[string]int),
		dispatcher:         disp,
	}

	return d
}

func TestHandleDevChat_NoServiceReturnsError(t *testing.T) {
	driver := newTestDriver()
	// devChatService is nil
	err := driver.handleDevChat(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestHandleDevChat_NoMessagesIsNoop(t *testing.T) {
	chatSvc := newTestChatService()
	chatSvc.RegisterAgent("test-arch", []string{"development", "product"})

	driver := newTestDriverWithDevChat(chatSvc)

	err := driver.handleDevChat(context.Background())
	assert.NoError(t, err)
}

func TestHandleDevChat_UpdatesCursorAfterProcessing(t *testing.T) {
	chatSvc := newTestChatService()
	chatSvc.RegisterAgent("test-arch", []string{"development", "product"})

	// Post a message from a human (doesn't need LLM/toolloop — handled with simple ack)
	ctx := context.Background()
	resp, err := chatSvc.Post(ctx, &chat.PostRequest{
		Author:   "@human",
		Text:     "Hello architect",
		Channel:  "development",
		PostType: "chat",
	})
	require.NoError(t, err)
	require.True(t, resp.Success)

	driver := newTestDriverWithDevChat(chatSvc)

	err = driver.handleDevChat(ctx)
	assert.NoError(t, err)

	// Cursor should be advanced — no new messages remaining
	assert.False(t, chatSvc.HaveNewMessagesForChannel("test-arch", "development"))
}

func TestHandleDevChat_ProductCursorUntouched(t *testing.T) {
	chatSvc := newTestChatService()
	chatSvc.RegisterAgent("test-arch", []string{"development", "product"})

	ctx := context.Background()

	// Post to both channels
	_, err := chatSvc.Post(ctx, &chat.PostRequest{
		Author:  "@human",
		Text:    "dev message",
		Channel: "development",
	})
	require.NoError(t, err)

	_, err = chatSvc.Post(ctx, &chat.PostRequest{
		Author:  "@pm-001",
		Text:    "product message",
		Channel: "product",
	})
	require.NoError(t, err)

	driver := newTestDriverWithDevChat(chatSvc)

	err = driver.handleDevChat(ctx)
	assert.NoError(t, err)

	// Development should be consumed
	assert.False(t, chatSvc.HaveNewMessagesForChannel("test-arch", "development"))

	// Product cursor should be untouched
	assert.True(t, chatSvc.HaveNewMessagesForChannel("test-arch", "product"))
}

func TestHandleDevChat_PostsThreadedReply(t *testing.T) {
	chatSvc := newTestChatService()
	chatSvc.RegisterAgent("test-arch", []string{"development"})

	ctx := context.Background()

	// Post a human message
	postResp, err := chatSvc.Post(ctx, &chat.PostRequest{
		Author:  "@human",
		Text:    "How's the build going?",
		Channel: "development",
	})
	require.NoError(t, err)
	originalMsgID := postResp.ID

	driver := newTestDriverWithDevChat(chatSvc)
	err = driver.handleDevChat(ctx)
	assert.NoError(t, err)

	// Check that a threaded reply was posted
	allMsgs := chatSvc.GetAllMessages()
	var reply *persistence.ChatMessage
	for _, m := range allMsgs {
		if m.ReplyTo != nil && *m.ReplyTo == originalMsgID {
			reply = m
			break
		}
	}

	require.NotNil(t, reply, "expected a threaded reply to the original message")
	assert.Equal(t, chat.FormatAuthor("test-arch"), reply.Author)
	assert.Equal(t, "development", reply.Channel)
	assert.Equal(t, chat.PostTypeReply, reply.PostType)
}

func TestHandleDevChat_ExcludesOwnMessages(t *testing.T) {
	chatSvc := newTestChatService()
	chatSvc.RegisterAgent("test-arch", []string{"development"})

	ctx := context.Background()

	// Post a message from the architect itself
	_, err := chatSvc.Post(ctx, &chat.PostRequest{
		Author:  chat.FormatAuthor("test-arch"),
		Text:    "My own message",
		Channel: "development",
	})
	require.NoError(t, err)

	driver := newTestDriverWithDevChat(chatSvc)
	err = driver.handleDevChat(ctx)
	assert.NoError(t, err)

	// Should be no replies posted (only the original message exists)
	allMsgs := chatSvc.GetAllMessages()
	assert.Equal(t, 1, len(allMsgs), "no replies should be posted for own messages")
}

func TestMonitoring_DevChatWakesState(t *testing.T) {
	chatSvc := newTestChatService()
	chatSvc.RegisterAgent("test-arch", []string{"development"})

	ctx := context.Background()

	// Post a message from a coder
	_, err := chatSvc.Post(ctx, &chat.PostRequest{
		Author:  "@coder-001",
		Text:    "Need help with this test",
		Channel: "development",
	})
	require.NoError(t, err)

	// Verify HaveNewMessagesForChannel returns true (this is what MONITORING checks)
	assert.True(t, chatSvc.HaveNewMessagesForChannel("test-arch", "development"))
}

func TestDevChatPending_RoutedInRequest(t *testing.T) {
	chatSvc := newTestChatService()
	chatSvc.RegisterAgent("test-arch", []string{"development"})

	ctx := context.Background()

	// Post a human message to dev chat
	_, err := chatSvc.Post(ctx, &chat.PostRequest{
		Author:  "@human",
		Text:    "status update please",
		Channel: "development",
	})
	require.NoError(t, err)

	driver := newTestDriverWithDevChat(chatSvc)

	// Set the dev-chat pending flag (as MONITORING would)
	driver.SetStateData(StateKeyDevChatPending, true)

	// Call handleRequest — it should route to dev-chat handler and return to MONITORING
	nextState, err := driver.handleRequest(ctx)
	assert.NoError(t, err)
	assert.Equal(t, StateMonitoring, nextState)

	// Flag should be cleared
	stateData := driver.GetStateData()
	pending, _ := stateData[StateKeyDevChatPending].(bool)
	assert.False(t, pending)
}
