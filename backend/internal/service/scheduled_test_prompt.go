package service

import (
	"context"
	"fmt"
	"math/rand/v2"
)

type scheduledTestPromptKey struct{}

func newScheduledTestPrompt() string {
	const operators = "+-*/"
	return fmt.Sprintf("%d%c%d=?", rand.IntN(9)+1, operators[rand.IntN(len(operators))], rand.IntN(9)+1)
}

// Keep one prompt across a scheduled run's protocol probes and retries.
func scheduledTestPrompt(ctx context.Context, fallback string) string {
	if prompt, ok := ctx.Value(scheduledTestPromptKey{}).(string); ok && prompt != "" {
		return prompt
	}
	return fallback
}
