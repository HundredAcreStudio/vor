package tasks

import (
	"context"
	"errors"
	"testing"
)

// fakeTask is a configurable Task for registry/run tests.
type fakeTask struct {
	id      string
	def     bool
	ran     *bool
	err     error
	skipped bool
}

func (f fakeTask) ID() string          { return f.id }
func (f fakeTask) Name() string        { return f.id + " name" }
func (f fakeTask) Description() string { return f.id + " desc" }
func (f fakeTask) DefaultEnabled() bool { return f.def }
func (f fakeTask) Run(_ context.Context, _ Context) (Result, error) {
	if f.ran != nil {
		*f.ran = true
	}
	return Result{Skipped: f.skipped}, f.err
}

func TestEnabled_DefaultAndOverride(t *testing.T) {
	on := fakeTask{id: "a", def: true}
	off := fakeTask{id: "b", def: false}

	// No overrides → falls back to DefaultEnabled.
	if !Enabled(on, nil) {
		t.Error("default-on task should be enabled with no overrides")
	}
	if Enabled(off, nil) {
		t.Error("default-off task should be disabled with no overrides")
	}

	// Explicit override wins either way.
	if Enabled(on, map[string]bool{"a": false}) {
		t.Error("override should disable a default-on task")
	}
	if !Enabled(off, map[string]bool{"b": true}) {
		t.Error("override should enable a default-off task")
	}
}

func TestRegisterGetRegistered(t *testing.T) {
	// Registry is package-global; use IDs unique to this test to avoid
	// collisions with other tests in the package.
	Register(fakeTask{id: "reg_z", def: true})
	Register(fakeTask{id: "reg_a", def: true})

	got, ok := Get("reg_a")
	if !ok || got.ID() != "reg_a" {
		t.Fatalf("Get(reg_a) = %v, %v", got, ok)
	}
	if _, ok := Get("reg_missing"); ok {
		t.Error("Get of unregistered ID returned ok")
	}

	// Registered is sorted by ID; our two should appear in a<z order.
	var ai, zi = -1, -1
	for i, tk := range Registered() {
		switch tk.ID() {
		case "reg_a":
			ai = i
		case "reg_z":
			zi = i
		}
	}
	if ai == -1 || zi == -1 || ai > zi {
		t.Errorf("Registered not sorted: reg_a at %d, reg_z at %d", ai, zi)
	}
}

// orderedTask is a fakeTask that also implements Ordered.
type orderedTask struct {
	fakeTask
	order int
}

func (o orderedTask) Order() int { return o.order }

func TestRegistered_SortsByOrderThenID(t *testing.T) {
	// Higher Order runs later even if its ID sorts earlier; an unordered
	// task (defaultOrder 100) runs after low-Order tasks.
	Register(orderedTask{fakeTask: fakeTask{id: "ord_late_aaa"}, order: 50})
	Register(orderedTask{fakeTask: fakeTask{id: "ord_early_zzz"}, order: 5})

	var early, late = -1, -1
	for i, tk := range Registered() {
		switch tk.ID() {
		case "ord_early_zzz":
			early = i
		case "ord_late_aaa":
			late = i
		}
	}
	if early == -1 || late == -1 {
		t.Fatalf("ordered tasks missing: early=%d late=%d", early, late)
	}
	if early > late {
		t.Errorf("Order ignored: ord_early_zzz (Order 5) at %d should precede ord_late_aaa (Order 50) at %d", early, late)
	}
}

func TestRegister_DuplicatePanics(t *testing.T) {
	Register(fakeTask{id: "dup_x"})
	defer func() {
		if recover() == nil {
			t.Error("registering a duplicate ID should panic")
		}
	}()
	Register(fakeTask{id: "dup_x"})
}

func TestRunEnabled_RunsOnlyEnabled(t *testing.T) {
	var ranOn, ranOff bool
	Register(fakeTask{id: "run_on", def: true, ran: &ranOn})
	Register(fakeTask{id: "run_off", def: false, ran: &ranOff})

	// Only default-on tasks among ours should run (no overrides).
	RunEnabled(context.Background(), Context{}, nil)
	if !ranOn {
		t.Error("default-on task did not run")
	}
	if ranOff {
		t.Error("default-off task ran without an enabling override")
	}

	// An override can disable the on task and enable the off task.
	ranOn, ranOff = false, false
	RunEnabled(context.Background(), Context{}, map[string]bool{"run_on": false, "run_off": true})
	if ranOn {
		t.Error("override should have disabled run_on")
	}
	if !ranOff {
		t.Error("override should have enabled run_off")
	}
}

func TestRunEnabled_CapturesError(t *testing.T) {
	boom := errors.New("boom")
	Register(fakeTask{id: "err_task", def: true, err: boom})

	outcomes := RunEnabled(context.Background(), Context{}, nil)
	var found bool
	for _, o := range outcomes {
		if o.TaskID == "err_task" {
			found = true
			if !errors.Is(o.Err, boom) {
				t.Errorf("outcome err = %v, want boom", o.Err)
			}
		}
	}
	if !found {
		t.Error("errored task missing from outcomes")
	}
}

func TestRunEnabled_StopsOnCancelledContext(t *testing.T) {
	var ran bool
	Register(fakeTask{id: "cancel_task", def: true, ran: &ran})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	RunEnabled(ctx, Context{}, nil)
	if ran {
		t.Error("task ran despite cancelled context")
	}
}
