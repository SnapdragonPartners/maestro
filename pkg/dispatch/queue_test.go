package dispatch

import (
	"context"
	"sync"
	"testing"
	"time"

	"orchestrator/pkg/proto"
)

// queueTestAgent for testing - simpler than the existing MockAgent
type queueTestAgent struct {
	id       string
	messages []*proto.AgentMsg
	mu       sync.Mutex
}

func (m *queueTestAgent) GetID() string { return m.id }
func (m *queueTestAgent) ProcessMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)

	// Return a simple response
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, m.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload("status", "processed")
	return response, nil
}
func (m *queueTestAgent) Shutdown(ctx context.Context) error { return nil }

func (m *queueTestAgent) getMessages() []*proto.AgentMsg {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*proto.AgentMsg{}, m.messages...)
}

func createQueueTestDispatcher(t *testing.T) (*Dispatcher, func()) {
	dispatcher, _, cleanup := createTestDispatcher(t)
	return dispatcher, cleanup
}

func TestPullArchitectWork(t *testing.T) {
	dispatcher, cleanup := createQueueTestDispatcher(t)
	defer cleanup()

	// Test empty queue
	msg := dispatcher.PullArchitectWork()
	if msg != nil {
		t.Errorf("Expected nil from empty architect queue, got %v", msg)
	}

	// Add questions to architect queue
	question1 := proto.NewAgentMsg(proto.MsgTypeQUESTION, "coder1", "architect")
	question1.SetPayload("question", "How should I implement this?")

	question2 := proto.NewAgentMsg(proto.MsgTypeQUESTION, "coder2", "architect")
	question2.SetPayload("question", "What's the best approach?")

	// Add to queue via sendResponse (simulates questions coming in)
	dispatcher.sendResponse(question1)
	dispatcher.sendResponse(question2)

	// Verify queue size
	stats := dispatcher.GetStats()
	if stats["architect_queue_size"] != 2 {
		t.Errorf("Expected architect queue size 2, got %v", stats["architect_queue_size"])
	}

	// Pull first question (FIFO)
	pulled1 := dispatcher.PullArchitectWork()
	if pulled1 == nil {
		t.Fatal("Expected to pull first question, got nil")
	}
	if pulled1.ID != question1.ID {
		t.Errorf("Expected first question ID %s, got %s", question1.ID, pulled1.ID)
	}

	// Pull second question
	pulled2 := dispatcher.PullArchitectWork()
	if pulled2 == nil {
		t.Fatal("Expected to pull second question, got nil")
	}
	if pulled2.ID != question2.ID {
		t.Errorf("Expected second question ID %s, got %s", question2.ID, pulled2.ID)
	}

	// Queue should be empty now
	pulled3 := dispatcher.PullArchitectWork()
	if pulled3 != nil {
		t.Errorf("Expected empty queue, got %v", pulled3)
	}

	// Verify queue size is now 0
	stats = dispatcher.GetStats()
	if stats["architect_queue_size"] != 0 {
		t.Errorf("Expected architect queue size 0, got %v", stats["architect_queue_size"])
	}
}

func TestPullCoderFeedback(t *testing.T) {
	dispatcher, cleanup := createQueueTestDispatcher(t)
	defer cleanup()

	// Test empty queue
	msg := dispatcher.PullCoderFeedback("coder1")
	if msg != nil {
		t.Errorf("Expected nil from empty coder queue, got %v", msg)
	}

	// Add results for different coders
	result1 := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", "coder1")
	result1.SetPayload("status", "approved")

	result2 := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", "coder2")
	result2.SetPayload("status", "needs_changes")

	result3 := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", "coder1")
	result3.SetPayload("status", "final_approved")

	// Add to coder queue
	dispatcher.sendResponse(result1)
	dispatcher.sendResponse(result2)
	dispatcher.sendResponse(result3)

	// Verify queue size
	stats := dispatcher.GetStats()
	if stats["coder_queue_size"] != 3 {
		t.Errorf("Expected coder queue size 3, got %v", stats["coder_queue_size"])
	}

	// Pull for coder1 - should get first result for coder1
	pulled1 := dispatcher.PullCoderFeedback("coder1")
	if pulled1 == nil {
		t.Fatal("Expected to pull result for coder1, got nil")
	}
	if pulled1.ID != result1.ID {
		t.Errorf("Expected result1 ID %s, got %s", result1.ID, pulled1.ID)
	}

	// Pull for coder2 - should get result for coder2
	pulled2 := dispatcher.PullCoderFeedback("coder2")
	if pulled2 == nil {
		t.Fatal("Expected to pull result for coder2, got nil")
	}
	if pulled2.ID != result2.ID {
		t.Errorf("Expected result2 ID %s, got %s", result2.ID, pulled2.ID)
	}

	// Pull for coder1 again - should get second result for coder1
	pulled3 := dispatcher.PullCoderFeedback("coder1")
	if pulled3 == nil {
		t.Fatal("Expected to pull second result for coder1, got nil")
	}
	if pulled3.ID != result3.ID {
		t.Errorf("Expected result3 ID %s, got %s", result3.ID, pulled3.ID)
	}

	// Queue should be empty now
	pulled4 := dispatcher.PullCoderFeedback("coder1")
	if pulled4 != nil {
		t.Errorf("Expected empty queue for coder1, got %v", pulled4)
	}

	// Verify queue size is now 0
	stats = dispatcher.GetStats()
	if stats["coder_queue_size"] != 0 {
		t.Errorf("Expected coder queue size 0, got %v", stats["coder_queue_size"])
	}
}

func TestPullSharedWork(t *testing.T) {
	dispatcher, cleanup := createQueueTestDispatcher(t)
	defer cleanup()

	// Test empty queue
	msg := dispatcher.PullSharedWork()
	if msg != nil {
		t.Errorf("Expected nil from empty shared work queue, got %v", msg)
	}

	// Create test tasks
	task1 := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "")
	task1.SetPayload("story_id", "001")
	task1.SetPayload("content", "Implement feature A")

	task2 := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "")
	task2.SetPayload("story_id", "002")
	task2.SetPayload("content", "Implement feature B")

	// Process tasks through dispatcher (they should go to shared work queue)
	ctx := context.Background()
	dispatcher.processMessage(ctx, task1)
	dispatcher.processMessage(ctx, task2)

	// Verify queue size
	stats := dispatcher.GetStats()
	if stats["shared_work_queue_size"] != 2 {
		t.Errorf("Expected shared work queue size 2, got %v", stats["shared_work_queue_size"])
	}

	// Pull first task (FIFO)
	pulled1 := dispatcher.PullSharedWork()
	if pulled1 == nil {
		t.Fatal("Expected to pull first task, got nil")
	}
	if pulled1.ID != task1.ID {
		t.Errorf("Expected first task ID %s, got %s", task1.ID, pulled1.ID)
	}

	// Pull second task
	pulled2 := dispatcher.PullSharedWork()
	if pulled2 == nil {
		t.Fatal("Expected to pull second task, got nil")
	}
	if pulled2.ID != task2.ID {
		t.Errorf("Expected second task ID %s, got %s", task2.ID, pulled2.ID)
	}

	// Queue should be empty now
	pulled3 := dispatcher.PullSharedWork()
	if pulled3 != nil {
		t.Errorf("Expected empty queue, got %v", pulled3)
	}

	// Verify queue size is now 0
	stats = dispatcher.GetStats()
	if stats["shared_work_queue_size"] != 0 {
		t.Errorf("Expected shared work queue size 0, got %v", stats["shared_work_queue_size"])
	}
}

func TestConcurrentQueueAccess(t *testing.T) {
	dispatcher, cleanup := createQueueTestDispatcher(t)
	defer cleanup()

	// Test concurrent access to architect queue
	const numGoroutines = 10
	const messagesPerGoroutine = 5

	var wg sync.WaitGroup

	// Add messages concurrently
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < messagesPerGoroutine; j++ {
				question := proto.NewAgentMsg(proto.MsgTypeQUESTION, "coder", "architect")
				question.SetPayload("question", "Question from worker %d message %d")
				dispatcher.sendResponse(question)
			}
		}(i)
	}

	wg.Wait()

	// Verify total messages
	stats := dispatcher.GetStats()
	expectedSize := numGoroutines * messagesPerGoroutine
	if stats["architect_queue_size"] != expectedSize {
		t.Errorf("Expected architect queue size %d, got %v", expectedSize, stats["architect_queue_size"])
	}

	// Pull all messages concurrently
	pulled := make([]*proto.AgentMsg, expectedSize)
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < messagesPerGoroutine; j++ {
				index := workerID*messagesPerGoroutine + j
				pulled[index] = dispatcher.PullArchitectWork()
			}
		}(i)
	}

	wg.Wait()

	// Verify all messages were pulled
	for i, msg := range pulled {
		if msg == nil {
			t.Errorf("Message at index %d was nil", i)
		}
	}

	// Queue should be empty
	stats = dispatcher.GetStats()
	if stats["architect_queue_size"] != 0 {
		t.Errorf("Expected empty architect queue, got size %v", stats["architect_queue_size"])
	}
}

func TestMessageRouting(t *testing.T) {
	dispatcher, cleanup := createQueueTestDispatcher(t)
	defer cleanup()

	// Test that different message types go to correct queues

	// 1. TASK messages should go to shared work queue
	task := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "coder1")
	task.SetPayload("content", "Test task")

	ctx := context.Background()
	dispatcher.processMessage(ctx, task)

	stats := dispatcher.GetStats()
	if stats["shared_work_queue_size"] != 1 {
		t.Errorf("Expected 1 task in shared work queue, got %v", stats["shared_work_queue_size"])
	}
	if stats["architect_queue_size"] != 0 {
		t.Errorf("Expected 0 messages in architect queue, got %v", stats["architect_queue_size"])
	}
	if stats["coder_queue_size"] != 0 {
		t.Errorf("Expected 0 messages in coder queue, got %v", stats["coder_queue_size"])
	}

	// 2. QUESTION messages should go to architect queue
	question := proto.NewAgentMsg(proto.MsgTypeQUESTION, "coder1", "architect")
	question.SetPayload("question", "How should I proceed?")

	dispatcher.sendResponse(question)

	stats = dispatcher.GetStats()
	if stats["architect_queue_size"] != 1 {
		t.Errorf("Expected 1 question in architect queue, got %v", stats["architect_queue_size"])
	}
	if stats["shared_work_queue_size"] != 1 {
		t.Errorf("Expected 1 task in shared work queue, got %v", stats["shared_work_queue_size"])
	}
	if stats["coder_queue_size"] != 0 {
		t.Errorf("Expected 0 messages in coder queue, got %v", stats["coder_queue_size"])
	}

	// 3. RESULT messages should go to coder queue
	result := proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", "coder1")
	result.SetPayload("status", "approved")

	dispatcher.sendResponse(result)

	stats = dispatcher.GetStats()
	if stats["coder_queue_size"] != 1 {
		t.Errorf("Expected 1 result in coder queue, got %v", stats["coder_queue_size"])
	}
	if stats["architect_queue_size"] != 1 {
		t.Errorf("Expected 1 question in architect queue, got %v", stats["architect_queue_size"])
	}
	if stats["shared_work_queue_size"] != 1 {
		t.Errorf("Expected 1 task in shared work queue, got %v", stats["shared_work_queue_size"])
	}
}

func TestQueueFIFOOrdering(t *testing.T) {
	dispatcher, cleanup := createQueueTestDispatcher(t)
	defer cleanup()

	// Test FIFO ordering for architect queue
	questions := make([]*proto.AgentMsg, 5)
	for i := 0; i < 5; i++ {
		questions[i] = proto.NewAgentMsg(proto.MsgTypeQUESTION, "coder", "architect")
		questions[i].SetPayload("order", i)
		dispatcher.sendResponse(questions[i])

		// Add small delay to ensure different timestamps
		time.Sleep(time.Millisecond)
	}

	// Pull all questions and verify order
	for i := 0; i < 5; i++ {
		pulled := dispatcher.PullArchitectWork()
		if pulled == nil {
			t.Fatalf("Expected question at position %d, got nil", i)
		}
		if pulled.ID != questions[i].ID {
			t.Errorf("Expected question %d ID %s, got %s", i, questions[i].ID, pulled.ID)
		}
	}

	// Test FIFO ordering for shared work queue
	tasks := make([]*proto.AgentMsg, 3)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		tasks[i] = proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "")
		tasks[i].SetPayload("order", i)
		dispatcher.processMessage(ctx, tasks[i])

		time.Sleep(time.Millisecond)
	}

	// Pull all tasks and verify order
	for i := 0; i < 3; i++ {
		pulled := dispatcher.PullSharedWork()
		if pulled == nil {
			t.Fatalf("Expected task at position %d, got nil", i)
		}
		if pulled.ID != tasks[i].ID {
			t.Errorf("Expected task %d ID %s, got %s", i, tasks[i].ID, pulled.ID)
		}
	}
}

func TestCoderSpecificFeedback(t *testing.T) {
	dispatcher, cleanup := createQueueTestDispatcher(t)
	defer cleanup()

	// Add results for different coders mixed together
	results := []*proto.AgentMsg{
		proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", "coder1"),
		proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", "coder2"),
		proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", "coder1"),
		proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", "coder3"),
		proto.NewAgentMsg(proto.MsgTypeRESULT, "architect", "coder2"),
	}

	for i, result := range results {
		result.SetPayload("order", i)
		dispatcher.sendResponse(result)
	}

	// Verify total queue size
	stats := dispatcher.GetStats()
	if stats["coder_queue_size"] != 5 {
		t.Errorf("Expected coder queue size 5, got %v", stats["coder_queue_size"])
	}

	// Pull for coder1 - should get results 0 and 2
	coder1_result1 := dispatcher.PullCoderFeedback("coder1")
	if coder1_result1 == nil || coder1_result1.ID != results[0].ID {
		t.Errorf("Expected first result for coder1, got %v", coder1_result1)
	}

	coder1_result2 := dispatcher.PullCoderFeedback("coder1")
	if coder1_result2 == nil || coder1_result2.ID != results[2].ID {
		t.Errorf("Expected second result for coder1, got %v", coder1_result2)
	}

	// No more results for coder1
	coder1_result3 := dispatcher.PullCoderFeedback("coder1")
	if coder1_result3 != nil {
		t.Errorf("Expected no more results for coder1, got %v", coder1_result3)
	}

	// Pull for coder2 - should get results 1 and 4
	coder2_result1 := dispatcher.PullCoderFeedback("coder2")
	if coder2_result1 == nil || coder2_result1.ID != results[1].ID {
		t.Errorf("Expected first result for coder2, got %v", coder2_result1)
	}

	coder2_result2 := dispatcher.PullCoderFeedback("coder2")
	if coder2_result2 == nil || coder2_result2.ID != results[4].ID {
		t.Errorf("Expected second result for coder2, got %v", coder2_result2)
	}

	// Pull for coder3 - should get result 3
	coder3_result1 := dispatcher.PullCoderFeedback("coder3")
	if coder3_result1 == nil || coder3_result1.ID != results[3].ID {
		t.Errorf("Expected result for coder3, got %v", coder3_result1)
	}

	// Queue should be empty now
	stats = dispatcher.GetStats()
	if stats["coder_queue_size"] != 0 {
		t.Errorf("Expected empty coder queue, got size %v", stats["coder_queue_size"])
	}
}
