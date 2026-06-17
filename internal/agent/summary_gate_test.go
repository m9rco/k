package agent

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gameasset/internal/store"
)

// newGateOrch builds a bare orchestrator with just the maps the summary-confirm
// gate touches, so the tests exercise await/deliver without a full agent wiring.
func newGateOrch() *Orchestrator {
	return &Orchestrator{summaryConfirms: make(map[string]chan summaryConfirm)}
}

// TestAwaitSummaryConfirmDelivered covers the happy path: a confirmation arrives
// (user confirmed or edited) and awaitSummaryConfirm returns its summary + edited
// flag, releasing the gate with the final text.
func TestAwaitSummaryConfirmDelivered(t *testing.T) {
	o := newGateOrch()
	const sid, key = "s1", "k1"

	var (
		got    string
		edited bool
		wg     sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		got, edited = o.awaitSummaryConfirm(context.Background(), sid, key, "原始报告")
	}()

	// Wait for registration so the deliver isn't dropped as a no-op.
	waitForPending(t, o, sid, key)
	o.DeliverSummaryConfirm(sid, key, "编辑后的报告", true)
	wg.Wait()

	if got != "编辑后的报告" || !edited {
		t.Fatalf("got (%q, edited=%v), want (编辑后的报告, true)", got, edited)
	}
	// Channel must be unregistered after return.
	o.mu.Lock()
	_, still := o.summaryConfirms[confirmKey(sid, key)]
	o.mu.Unlock()
	if still {
		t.Error("confirm channel not unregistered after return")
	}
}

// TestAwaitSummaryConfirmCancelled verifies a turn interrupt (ctx cancel) releases
// the gate with the original report and edited=false (no cache write-back).
func TestAwaitSummaryConfirmCancelled(t *testing.T) {
	o := newGateOrch()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	var got string
	var edited bool
	go func() {
		got, edited = o.awaitSummaryConfirm(ctx, "s2", "k2", "原始报告")
		close(done)
	}()
	waitForPending(t, o, "s2", "k2")
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("awaitSummaryConfirm did not return after ctx cancel")
	}
	if got != "原始报告" || edited {
		t.Fatalf("got (%q, edited=%v), want (原始报告, false)", got, edited)
	}
}

// TestAwaitSummaryConfirmEmptyFallsBackToOriginal verifies an empty/whitespace
// confirm payload (e.g. a malformed countdown default) falls back to the original
// report rather than gating adaptation on a blank theme.
func TestAwaitSummaryConfirmEmptyFallsBackToOriginal(t *testing.T) {
	o := newGateOrch()
	const sid, key = "s3", "k3"
	done := make(chan struct{})
	var got string
	var edited bool
	go func() {
		got, edited = o.awaitSummaryConfirm(context.Background(), sid, key, "原始报告")
		close(done)
	}()
	waitForPending(t, o, sid, key)
	o.DeliverSummaryConfirm(sid, key, "   ", true)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("did not return")
	}
	if got != "原始报告" || edited {
		t.Fatalf("got (%q, edited=%v), want (原始报告, false)", got, edited)
	}
}

// TestDeliverSummaryConfirmNoWaiterIsNoop verifies a stale/duplicate confirm with
// no gated call waiting is harmlessly dropped (no panic, no block).
func TestDeliverSummaryConfirmNoWaiterIsNoop(t *testing.T) {
	o := newGateOrch()
	// Must not panic or block.
	o.DeliverSummaryConfirm("nobody", "nope", "x", true)
}

// TestGateSummaryConfirmEditWritesBack verifies that an edited summary (from
// either the live-analysis or cache-hit path) overwrites the cached report so
// the next reuse of the same image group picks up the edited version.
func TestGateSummaryConfirmEditWritesBack(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	const key = "k-edit"
	if err := st.InsertVisionReport(key, "原始报告"); err != nil {
		t.Fatal(err)
	}
	d := ToolDeps{
		Store: st,
		// Simulate the user editing the summary in the confirmation window.
		AwaitSummaryConfirm: func(_ context.Context, _ string, _ string) (string, bool) {
			return "编辑后的报告", true
		},
	}
	final := gateSummaryConfirm(context.Background(), d, key, "原始报告")
	if final != "编辑后的报告" {
		t.Fatalf("final theme = %q, want 编辑后的报告", final)
	}
	got, err := st.GetVisionReport(key)
	if err != nil {
		t.Fatal(err)
	}
	if got != "编辑后的报告" {
		t.Fatalf("cache not overwritten: got %q, want 编辑后的报告", got)
	}
}

// TestGateSummaryConfirmDefaultKeepsCache verifies the countdown default
// (edited=false) returns the original and leaves the cached report untouched.
func TestGateSummaryConfirmDefaultKeepsCache(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	const key = "k-default"
	if err := st.InsertVisionReport(key, "原始报告"); err != nil {
		t.Fatal(err)
	}
	d := ToolDeps{
		Store: st,
		// Countdown default: returns the original, not edited.
		AwaitSummaryConfirm: func(_ context.Context, _ string, original string) (string, bool) {
			return original, false
		},
	}
	final := gateSummaryConfirm(context.Background(), d, key, "原始报告")
	if final != "原始报告" {
		t.Fatalf("final theme = %q, want 原始报告", final)
	}
	got, _ := st.GetVisionReport(key)
	if got != "原始报告" {
		t.Fatalf("cache should be unchanged: got %q", got)
	}
}

// TestGateSummaryConfirmNoHookPassthrough verifies that with no gate hook
// injected (tests / transport-less), the original report passes through
// unchanged and the cache is not touched.
func TestGateSummaryConfirmNoHookPassthrough(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	final := gateSummaryConfirm(context.Background(), ToolDeps{Store: st}, "k-nohook", "原始报告")
	if final != "原始报告" {
		t.Fatalf("final theme = %q, want 原始报告 (passthrough)", final)
	}
}

// waitForPending spins until the gate channel for (sid,key) is registered.
func waitForPending(t *testing.T, o *Orchestrator, sid, key string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		o.mu.Lock()
		_, ok := o.summaryConfirms[confirmKey(sid, key)]
		o.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("confirm channel was never registered")
}
