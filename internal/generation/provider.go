package generation

import (
	"context"
	"errors"
	"fmt"
)

// Request is a single image-generation call.
type Request struct {
	// Prompt is the fully assembled, server-controlled prompt.
	Prompt string
	// SourceImage is the input image bytes to edit (may be nil for pure text-to-image).
	SourceImage []byte
	SourceMime  string
	// Width/Height are the desired output dimensions (0 = provider default).
	Width  int
	Height int
}

// Output is the produced image plus the provider that made it.
type Output struct {
	Data     []byte
	Mime     string
	Provider string
}

// Provider abstracts an image-generation backend (gpt-image-1 over an
// OpenAI-compatible endpoint). Two instances (primary + backup) are used with
// failover.
type Provider interface {
	// Name identifies the provider for recording on assets.
	Name() string
	// Generate produces an image for the request.
	Generate(ctx context.Context, req Request) (Output, error)
}

// FailoverGenerator tries the primary provider first and falls back to the
// backup on error, recording which provider produced the result (design: image
// generation failover requirement).
type FailoverGenerator struct {
	Primary Provider
	Backup  Provider
}

// NewFailoverGenerator constructs a failover generator. Backup may be nil.
func NewFailoverGenerator(primary, backup Provider) *FailoverGenerator {
	return &FailoverGenerator{Primary: primary, Backup: backup}
}

// Generate runs the request against primary then backup. The returned Output's
// Provider field records the source. If both fail, the combined error is returned.
func (g *FailoverGenerator) Generate(ctx context.Context, req Request) (Output, error) {
	if g.Primary == nil {
		return Output{}, errors.New("no primary image provider configured")
	}
	out, errPrimary := g.Primary.Generate(ctx, req)
	if errPrimary == nil {
		out.Provider = g.Primary.Name()
		return out, nil
	}
	if g.Backup == nil {
		return Output{}, fmt.Errorf("primary provider %s failed: %w", g.Primary.Name(), errPrimary)
	}
	out, errBackup := g.Backup.Generate(ctx, req)
	if errBackup == nil {
		out.Provider = g.Backup.Name()
		return out, nil
	}
	return Output{}, fmt.Errorf("both providers failed: primary %s: %v; backup %s: %w",
		g.Primary.Name(), errPrimary, g.Backup.Name(), errBackup)
}
