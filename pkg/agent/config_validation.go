package agent

import "fmt"

// Validate validates the agent configuration
func (ac *AgentConfig) Validate() error {
	if ac.ID == "" {
		return fmt.Errorf("%w: ID cannot be empty", ErrInvalidConfig)
	}
	if ac.Type == "" {
		return fmt.Errorf("%w: Type cannot be empty", ErrInvalidConfig)
	}
	if ac.Context.Store == nil {
		return fmt.Errorf("%w: Store cannot be nil", ErrInvalidConfig)
	}
	if ac.LLMConfig != nil {
		if err := ac.LLMConfig.Validate(); err != nil {
			return fmt.Errorf("%w: invalid LLM config: %v", ErrInvalidConfig, err)
		}
	}
	return nil
}

// Validate validates the LLM configuration
func (c *LLMConfig) Validate() error {
	// Set reasonable defaults for zero values
	if c.MaxContextTokens == 0 {
		c.MaxContextTokens = 8192 // Claude 3.5 Sonnet limit
	}
	if c.MaxOutputTokens == 0 {
		c.MaxOutputTokens = 4096 // Half of max context
	}
	if c.CompactIfOver == 0 {
		c.CompactIfOver = 1000 // Default compaction buffer
	}

	// Validate token limits
	if c.MaxContextTokens > 8192 {
		return fmt.Errorf("%w: max context tokens %d exceeds model limit of 8192", ErrInvalidConfig, c.MaxContextTokens)
	}
	if c.MaxOutputTokens > c.MaxContextTokens {
		return fmt.Errorf("%w: max output tokens %d exceeds max context tokens %d", ErrInvalidConfig, c.MaxOutputTokens, c.MaxContextTokens)
	}
	if c.CompactIfOver >= c.MaxContextTokens {
		return fmt.Errorf("%w: compact threshold %d must be less than max context tokens %d", ErrInvalidConfig, c.CompactIfOver, c.MaxContextTokens)
	}

	return nil
}