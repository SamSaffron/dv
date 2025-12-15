package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestProcessEventsFlushTimeout verifies that processEvents doesn't hang
// if flush() takes too long during context cancellation.
func TestProcessEventsFlushTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var logBuf bytes.Buffer
	s := &extractSync{
		ctx:           ctx,
		cancel:        cancel,
		containerName: "fake-container",
		workdir:       "/fake/workdir",
		localRepo:     "/fake/local",
		logOut:        &logBuf,
		errOut:        &logBuf,
		debug:         true,
		events:        make(chan watcherEvent, 256),
	}

	// Start processEvents in a goroutine
	done := make(chan error, 1)
	go func() {
		done <- s.processEvents()
	}()

	// Queue some events
	s.events <- watcherEvent{source: sourceHost, path: "test.go"}
	s.events <- watcherEvent{source: sourceContainer, path: "other.go"}

	// Give it a moment to receive events
	time.Sleep(50 * time.Millisecond)

	// Cancel context - this should trigger cleanup
	cancel()

	// processEvents should exit within a reasonable time (the 2s flush timeout + buffer)
	select {
	case <-done:
		// Success - processEvents exited
	case <-time.After(5 * time.Second):
		t.Fatal("processEvents hung after context cancellation - deadlock detected")
	}
}

// TestQueueEventDoesNotBlockOnFullChannel verifies that queueEvent
// respects context cancellation even when the channel is full.
func TestQueueEventDoesNotBlockOnFullChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var logBuf bytes.Buffer
	s := &extractSync{
		ctx:           ctx,
		cancel:        cancel,
		containerName: "fake-container",
		workdir:       "/fake/workdir",
		localRepo:     "/fake/local",
		logOut:        &logBuf,
		errOut:        &logBuf,
		debug:         false,
		events:        make(chan watcherEvent, 2), // Small buffer to fill quickly
	}

	// Fill the channel
	s.events <- watcherEvent{source: sourceHost, path: "1.go"}
	s.events <- watcherEvent{source: sourceHost, path: "2.go"}

	// Cancel context
	cancel()

	// queueEvent should not block because context is cancelled
	done := make(chan struct{})
	go func() {
		s.queueEvent(watcherEvent{source: sourceHost, path: "3.go"})
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("queueEvent blocked on full channel despite cancelled context")
	}
}

// TestTimerResetRace simulates rapid event arrival to check for timer state issues.
func TestTimerResetRace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var logBuf bytes.Buffer
	s := &extractSync{
		ctx:           ctx,
		cancel:        cancel,
		containerName: "fake-container",
		workdir:       "/fake/workdir",
		localRepo:     "/fake/local",
		logOut:        &logBuf,
		errOut:        &logBuf,
		debug:         false,
		events:        make(chan watcherEvent, 256),
	}

	// Start processEvents
	done := make(chan error, 1)
	go func() {
		done <- s.processEvents()
	}()

	// Rapidly send events from multiple goroutines
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				select {
				case <-ctx.Done():
					return
				default:
					s.queueEvent(watcherEvent{source: sourceHost, path: "test.go"})
					time.Sleep(time.Millisecond)
				}
			}
		}(i)
	}

	// Let events flow for a bit
	time.Sleep(100 * time.Millisecond)

	// Cancel and verify clean exit
	cancel()
	wg.Wait()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("processEvents hung under rapid event load - possible timer race")
	}
}

// TestChannelCapacityUnderLoad verifies the event channel doesn't cause
// deadlocks when events arrive faster than processing.
func TestChannelCapacityUnderLoad(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var logBuf bytes.Buffer
	s := &extractSync{
		ctx:           ctx,
		cancel:        cancel,
		containerName: "fake-container",
		workdir:       "/fake/workdir",
		localRepo:     "/fake/local",
		logOut:        &logBuf,
		errOut:        &logBuf,
		debug:         false,
		events:        make(chan watcherEvent, 10), // Intentionally small
	}

	// Start processEvents
	done := make(chan error, 1)
	go func() {
		done <- s.processEvents()
	}()

	// Try to overwhelm with events
	blocked := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			select {
			case <-ctx.Done():
				close(blocked)
				return
			case s.events <- watcherEvent{source: sourceHost, path: "test.go"}:
			}
		}
		close(blocked)
	}()

	// Give it time to potentially block
	time.Sleep(500 * time.Millisecond)

	cancel()

	select {
	case <-blocked:
		// Producer finished or was cancelled
	case <-time.After(2 * time.Second):
		t.Fatal("Event producer blocked - channel backpressure issue")
	}

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("processEvents hung")
	}
}

// TestProcessHostChangesWithSlowDocker simulates what happens when docker exec
// hangs during processHostChanges. This is the most likely deadlock scenario.
func TestProcessHostChangesWithSlowDocker(t *testing.T) {
	// This test doesn't actually call docker - it tests the timeout behavior
	// when the underlying operations would block.

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Create a temp directory to act as local repo
	tmpDir, err := os.MkdirTemp("", "dv-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Initialize a git repo
	if err := runInDir(tmpDir, nil, nil, "git", "init"); err != nil {
		t.Fatalf("git init failed: %v", err)
	}
	if err := runInDir(tmpDir, nil, nil, "git", "config", "user.email", "test@test.com"); err != nil {
		t.Fatalf("git config email failed: %v", err)
	}
	if err := runInDir(tmpDir, nil, nil, "git", "config", "user.name", "Test"); err != nil {
		t.Fatalf("git config name failed: %v", err)
	}

	// Create and commit a file
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runInDir(tmpDir, nil, nil, "git", "add", "."); err != nil {
		t.Fatal(err)
	}
	if err := runInDir(tmpDir, nil, nil, "git", "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}

	var logBuf bytes.Buffer
	s := &extractSync{
		ctx:           ctx,
		cancel:        cancel,
		containerName: "nonexistent-container-that-will-fail-fast",
		workdir:       "/fake/workdir",
		localRepo:     tmpDir,
		logOut:        &logBuf,
		errOut:        &logBuf,
		debug:         true,
		events:        make(chan watcherEvent, 256),
	}

	// Modify the file to create a change
	if err := os.WriteFile(testFile, []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}

	// Start processEvents
	done := make(chan error, 1)
	go func() {
		done <- s.processEvents()
	}()

	// Queue an event for the modified file
	s.queueEvent(watcherEvent{source: sourceHost, path: "test.txt"})

	// The processHostChanges will try to call docker, which should fail quickly
	// for a nonexistent container. The test verifies we don't hang.

	select {
	case err := <-done:
		// Expect an error (container doesn't exist) but should NOT hang
		if err != nil {
			t.Logf("Got expected error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("processEvents hung when docker should have failed fast")
	}
}

func TestCopyHostToContainerSkipsIfFileVanishes(t *testing.T) {
	s := &extractSync{
		containerName: "fake-container",
		workdir:       "/fake/workdir",
		localRepo:     t.TempDir(),
		logOut:        io.Discard,
		errOut:        io.Discard,
	}

	err := s.copyHostToContainer("spec/lib/.conform.7348585.search_spec.rb")
	if err == nil || !errors.Is(err, errSyncSkipped) {
		t.Fatalf("expected errSyncSkipped, got %v", err)
	}
}

// TestFlushTimeoutDuringCancellation specifically tests the 2-second timeout
// in processEvents cleanup path.
func TestFlushTimeoutDuringCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var logBuf bytes.Buffer
	s := &extractSync{
		ctx:           ctx,
		cancel:        cancel,
		containerName: "fake-container",
		workdir:       "/fake/workdir",
		localRepo:     "/nonexistent/path/that/will/fail",
		logOut:        &logBuf,
		errOut:        &logBuf,
		debug:         true,
		events:        make(chan watcherEvent, 256),
	}

	done := make(chan error, 1)
	go func() {
		done <- s.processEvents()
	}()

	// Queue events so there's something to flush
	for i := 0; i < 10; i++ {
		s.queueEvent(watcherEvent{source: sourceHost, path: "test.go"})
		s.queueEvent(watcherEvent{source: sourceContainer, path: "other.go"})
	}

	// Small delay to let events queue up
	time.Sleep(50 * time.Millisecond)

	// Cancel immediately - this triggers the flush timeout path
	cancel()

	// Should complete within the 2-second flush timeout + margin
	select {
	case <-done:
		t.Log("processEvents exited cleanly")
	case <-time.After(5 * time.Second):
		t.Fatal("processEvents exceeded flush timeout - deadlock in cleanup")
	}
}

// TestNormalFlushHasNoTimeout demonstrates that the normal flush path
// (triggered by timer, not cancellation) has no timeout and could hang.
// This test documents the issue - it will fail if docker hangs.
func TestNormalFlushHasNoTimeout(t *testing.T) {
	// Skip in normal test runs - this test is for documentation/reproduction
	if os.Getenv("DV_TEST_DEADLOCK") == "" {
		t.Skip("Set DV_TEST_DEADLOCK=1 to run deadlock reproduction tests")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a temp directory to act as local repo
	tmpDir, err := os.MkdirTemp("", "dv-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Initialize a git repo
	if err := runInDir(tmpDir, nil, nil, "git", "init"); err != nil {
		t.Fatalf("git init failed: %v", err)
	}
	if err := runInDir(tmpDir, nil, nil, "git", "config", "user.email", "test@test.com"); err != nil {
		t.Fatalf("git config email failed: %v", err)
	}
	if err := runInDir(tmpDir, nil, nil, "git", "config", "user.name", "Test"); err != nil {
		t.Fatalf("git config name failed: %v", err)
	}

	// Create and commit a file
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runInDir(tmpDir, nil, nil, "git", "add", "."); err != nil {
		t.Fatal(err)
	}
	if err := runInDir(tmpDir, nil, nil, "git", "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}

	var logBuf bytes.Buffer
	s := &extractSync{
		ctx:           ctx,
		cancel:        cancel,
		containerName: "nonexistent-container",
		workdir:       "/fake/workdir",
		localRepo:     tmpDir,
		logOut:        &logBuf,
		errOut:        &logBuf,
		debug:         true,
		events:        make(chan watcherEvent, 256),
	}

	// Modify the file
	if err := os.WriteFile(testFile, []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- s.processEvents()
	}()

	// Queue an event
	s.queueEvent(watcherEvent{source: sourceHost, path: "test.txt"})

	// Wait for the timer-triggered flush (250ms settle + processing)
	// The flush will call processHostChanges which calls docker commands
	select {
	case err := <-done:
		if err != nil {
			t.Logf("Got expected error (docker failed): %v", err)
		}
	case <-time.After(10 * time.Second):
		// This timeout being hit means docker exec hung without timeout
		t.Fatal("Timer-triggered flush hung - no timeout on docker operations")
	}
}

// TestFlushTimeoutWorks validates that the flush timeout prevents hangs.
func TestFlushTimeoutWorks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var logBuf bytes.Buffer
	s := &extractSync{
		ctx:           ctx,
		cancel:        cancel,
		containerName: "fake-container",
		workdir:       "/fake/workdir",
		localRepo:     "/nonexistent/path",
		logOut:        &logBuf,
		errOut:        &logBuf,
		debug:         true,
		events:        make(chan watcherEvent, 256),
	}

	done := make(chan error, 1)
	go func() {
		done <- s.processEvents()
	}()

	// Send events
	s.queueEvent(watcherEvent{source: sourceHost, path: "test.go"})

	// Wait for settle delay + some margin for flush to start
	time.Sleep(300 * time.Millisecond)

	// Cancel context
	cancel()

	// Should exit quickly due to the timeout mechanism
	select {
	case <-done:
		t.Log("processEvents exited cleanly with timeout protection")
	case <-time.After(5 * time.Second):
		t.Fatal("processEvents hung despite timeout protection")
	}
}
