package ai

import "time"

const (
	TutorRetryMaxAttempts    = 3
	TutorRetryBaseDelay      = 2 * time.Second
	TutorRetryMaxJitter      = time.Second
	TutorRetryMaxWait        = 20 * time.Second
	TutorRetryOverallTimeout = 60 * time.Second
)
