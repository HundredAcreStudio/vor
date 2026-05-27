// Package retry adapts cenkalti/backoff to the providers.IsTransient
// classifier. Provider implementations wrap their network calls in Do()
// so transient errors retry with jittered exponential backoff.
package retry

import (
	"context"
	"time"

	backoff "github.com/cenkalti/backoff/v4"

	"github.com/repowise-dev/repowise-go/internal/providers"
)

// Policy configures Do. Zero values are sensible defaults:
//
//	InitialInterval: 500ms
//	MaxInterval:     30s
//	MaxElapsedTime:  2m
//	Multiplier:      2.0
//	RandomFactor:    0.3
type Policy struct {
	InitialInterval time.Duration
	MaxInterval     time.Duration
	MaxElapsedTime  time.Duration
	Multiplier      float64
	RandomFactor    float64
}

// Default returns a Policy with the recommended values above.
func Default() Policy {
	return Policy{
		InitialInterval: 500 * time.Millisecond,
		MaxInterval:     30 * time.Second,
		MaxElapsedTime:  2 * time.Minute,
		Multiplier:      2.0,
		RandomFactor:    0.3,
	}
}

// Do runs op, retrying with exponential backoff while op returns an error
// classified as transient via providers.IsTransient. Non-transient errors
// (auth, model-not-found, ...) are returned immediately. Honours ctx
// cancellation.
func Do(ctx context.Context, p Policy, op func() error) error {
	if p.InitialInterval == 0 {
		p = Default()
	}
	exp := backoff.NewExponentialBackOff()
	exp.InitialInterval = p.InitialInterval
	exp.MaxInterval = p.MaxInterval
	exp.MaxElapsedTime = p.MaxElapsedTime
	exp.Multiplier = p.Multiplier
	exp.RandomizationFactor = p.RandomFactor
	exp.Reset()

	wrapped := func() error {
		if err := op(); err != nil {
			if providers.IsTransient(err) {
				return err
			}
			return backoff.Permanent(err)
		}
		return nil
	}
	return backoff.Retry(wrapped, backoff.WithContext(exp, ctx))
}
