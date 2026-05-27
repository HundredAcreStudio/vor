package providers_test

import (
	"context"
	"slices"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/providers"
)

// fake is a minimal in-test Provider used to verify the registry surface.
type fake struct{ name string }

func (f *fake) Name() string                                                              { return f.name }
func (f *fake) Models() []string                                                          { return []string{f.name + "-1"} }
func (f *fake) Generate(_ context.Context, _ providers.Request) (providers.Response, error) {
	return providers.Response{Content: "ok"}, nil
}
func (f *fake) GenerateStream(_ context.Context, _ providers.Request) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent)
	close(ch)
	return ch, nil
}

func TestRegisterAndLookup(t *testing.T) {
	providers.ResetForTest()
	providers.RegisterProvider("fake", func(_ providers.Options) (providers.Provider, error) {
		return &fake{name: "fake"}, nil
	})

	if got := providers.RegisteredProviders(); !slices.Contains(got, "fake") {
		t.Errorf("RegisteredProviders missing \"fake\": %v", got)
	}

	p, err := providers.NewProvider("fake", nil)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if p.Name() != "fake" {
		t.Errorf("Name = %q, want fake", p.Name())
	}
}

func TestNewProvider_UnknownReturnsError(t *testing.T) {
	providers.ResetForTest()
	if _, err := providers.NewProvider("nope", nil); err == nil {
		t.Errorf("expected error for unregistered provider")
	}
}

func TestRegisterPanicsOnDuplicate(t *testing.T) {
	providers.ResetForTest()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on duplicate registration")
		}
	}()
	providers.RegisterProvider("dup", func(_ providers.Options) (providers.Provider, error) {
		return &fake{name: "dup"}, nil
	})
	providers.RegisterProvider("dup", func(_ providers.Options) (providers.Provider, error) {
		return &fake{name: "dup"}, nil
	})
}

func TestIsTransient(t *testing.T) {
	if !providers.IsTransient(providers.ErrTransient) {
		t.Errorf("ErrTransient should be transient")
	}
	if providers.IsTransient(providers.ErrUnauthenticated) {
		t.Errorf("ErrUnauthenticated should not be transient")
	}
}
