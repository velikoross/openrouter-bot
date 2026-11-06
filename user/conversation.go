package user

import (
	"context"
	"sync"
	"time"
)

type Message struct {
	Role    string
	Content string
}

// Conversation is the dialog of one user in one chat.
type Conversation struct {
	mu           sync.Mutex
	messages     []Message
	lastActivity time.Time
	systemPrompt string // custom prompt set by /reset <prompt>; "" means default
	cancel       context.CancelFunc
	requestID    uint64

	// run serialises requests so that history stays consistent.
	run sync.Mutex
	now func() time.Time
}

func newConversation() *Conversation {
	return &Conversation{now: time.Now}
}

// Lock/Unlock serialise LLM requests for this conversation.
func (c *Conversation) Lock()   { c.run.Lock() }
func (c *Conversation) Unlock() { c.run.Unlock() }

// Messages returns a copy of the history after dropping expired/excess entries.
func (c *Conversation) Messages(maxMessages, maxMinutes int) []Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trimLocked(maxMessages, maxMinutes)
	return append([]Message(nil), c.messages...)
}

func (c *Conversation) trimLocked(maxMessages, maxMinutes int) {
	if maxMinutes > 0 && !c.lastActivity.IsZero() &&
		c.now().Sub(c.lastActivity) > time.Duration(maxMinutes)*time.Minute {
		c.messages = nil
	}
	if maxMessages >= 0 && len(c.messages) > maxMessages {
		c.messages = append([]Message(nil), c.messages[len(c.messages)-maxMessages:]...)
	}
}

// AddExchange appends a user message and the assistant answer.
func (c *Conversation) AddExchange(userText, answer string, maxMessages int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages,
		Message{Role: "user", Content: userText},
		Message{Role: "assistant", Content: answer})
	c.lastActivity = c.now()
	c.trimLocked(maxMessages, 0)
}

// Len returns the number of stored messages.
func (c *Conversation) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.messages)
}

func (c *Conversation) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = nil
}

// SystemPrompt returns the custom prompt or the given default.
func (c *Conversation) SystemPrompt(def string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.systemPrompt != "" {
		return c.systemPrompt
	}
	return def
}

func (c *Conversation) SetSystemPrompt(p string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.systemPrompt = p
}

// StartRequest registers the cancel func of the active request and returns
// a function that must be called when the request finishes.
func (c *Conversation) StartRequest(cancel context.CancelFunc) (done func()) {
	c.mu.Lock()
	c.requestID++
	id := c.requestID
	c.cancel = cancel
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		if c.requestID == id {
			c.cancel = nil
		}
		c.mu.Unlock()
		cancel()
	}
}

// Stop cancels the active request. It returns false if nothing was running.
func (c *Conversation) Stop() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel == nil {
		return false
	}
	c.cancel()
	c.cancel = nil
	return true
}
