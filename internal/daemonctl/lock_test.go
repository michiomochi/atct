package daemonctl

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestAcquireLockExcludesSecondHolder(t *testing.T) {
	dir := t.TempDir()

	first, err := AcquireLock(dir, time.Second)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}

	_, err = AcquireLock(dir, 200*time.Millisecond)
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("second AcquireLock err = %v, want ErrLockTimeout", err)
	}

	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	second, err := AcquireLock(dir, time.Second)
	if err != nil {
		t.Fatalf("AcquireLock after release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("Release second: %v", err)
	}
}

func TestAcquireLockSerializesConcurrentHolders(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var concurrent, maxConcurrent int

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := AcquireLock(dir, 5*time.Second)
			if err != nil {
				t.Errorf("AcquireLock: %v", err)
				return
			}
			mu.Lock()
			concurrent++
			if concurrent > maxConcurrent {
				maxConcurrent = concurrent
			}
			mu.Unlock()

			time.Sleep(5 * time.Millisecond)

			mu.Lock()
			concurrent--
			mu.Unlock()
			if err := l.Release(); err != nil {
				t.Errorf("Release: %v", err)
			}
		}()
	}
	wg.Wait()

	if maxConcurrent != 1 {
		t.Fatalf("max concurrent lock holders = %d, want 1", maxConcurrent)
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	l, err := AcquireLock(dir, time.Second)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}
