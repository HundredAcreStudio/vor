package wikitask

import (
	"context"
	"testing"

	"github.com/HundredAcreStudio/vor/internal/pipeline/tasks"
)

func TestTask_Metadata(t *testing.T) {
	var tk Task
	if tk.ID() != TaskID {
		t.Errorf("ID = %q, want %q", tk.ID(), TaskID)
	}
	if tk.Name() == "" || tk.Description() == "" {
		t.Error("Name and Description should be non-empty")
	}
	if !tk.DefaultEnabled() {
		t.Error("wiki generation should be enabled by default")
	}
	if req := tk.Requires(); len(req) != 1 || req[0] != tasks.RequiresProvider {
		t.Errorf("Requires = %v, want [provider]", req)
	}
}

func TestTask_RegisteredViaInit(t *testing.T) {
	// The package init() registers the task; it should be discoverable.
	got, ok := tasks.Get(TaskID)
	if !ok {
		t.Fatalf("task %q not registered", TaskID)
	}
	if got.ID() != TaskID {
		t.Errorf("registered task ID = %q, want %q", got.ID(), TaskID)
	}
}

func TestTask_SkipsWithoutProvider(t *testing.T) {
	// With no provider, Run must self-skip (no error, no DB access) so that a
	// "default on" task stays a no-op for repos without an LLM configured.
	res, err := Task{}.Run(context.Background(), tasks.Context{Provider: nil})
	if err != nil {
		t.Fatalf("Run without provider returned error: %v", err)
	}
	if !res.Skipped {
		t.Error("Run without provider should report Skipped")
	}
	if res.Detail == "" {
		t.Error("skip should explain why")
	}
}
