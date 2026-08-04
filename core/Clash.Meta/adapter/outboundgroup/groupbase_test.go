package outboundgroup

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/common/utils"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"
)

func failedTimesForTest(group *GroupBase) int {
	group.failedTestMux.Lock()
	defer group.failedTestMux.Unlock()
	return group.failedTimes
}

func TestFailureHealthCheckUsesCooldownWithoutSuccess(t *testing.T) {
	group := NewGroupBase(GroupBaseOption{
		Name:           "test",
		Type:           C.URLTest,
		MaxFailedTimes: 2,
	})
	var healthChecks atomic.Int32
	trigger := func() {
		healthChecks.Add(1)
	}

	group.handleDialFailure(errors.New("dial failed"), trigger)
	group.handleDialFailure(errors.New("dial failed"), trigger)
	if actual := healthChecks.Load(); actual != 1 {
		t.Fatalf("first failure burst ran %d health checks, want 1", actual)
	}

	group.handleDialFailure(errors.New("dial failed"), trigger)
	group.handleDialFailure(errors.New("dial failed"), trigger)
	if actual := healthChecks.Load(); actual != 1 {
		t.Fatalf("second failure burst inside cooldown ran %d total checks, want 1", actual)
	}

	group.forcedCheckMux.Lock()
	group.lastForcedCheck = time.Now().Add(-failureRecheckCooldown)
	group.forcedCheckMux.Unlock()

	group.handleDialFailure(errors.New("dial failed"), trigger)
	group.handleDialFailure(errors.New("dial failed"), trigger)
	if actual := healthChecks.Load(); actual != 2 {
		t.Fatalf("failure burst after cooldown ran %d total checks, want 2", actual)
	}
}

func TestConnectionRefusedHealthCheckUsesCooldown(t *testing.T) {
	group := NewGroupBase(GroupBaseOption{Name: "test", Type: C.URLTest})
	var healthChecks atomic.Int32
	trigger := func() {
		healthChecks.Add(1)
	}

	for i := 0; i < 32; i++ {
		group.handleDialFailure(errors.New("connection refused"), trigger)
	}
	if actual := healthChecks.Load(); actual != 1 {
		t.Fatalf("connection-refused burst ran %d health checks, want 1", actual)
	}

	group.forcedCheckMux.Lock()
	group.lastForcedCheck = time.Now().Add(-failureRecheckCooldown)
	group.forcedCheckMux.Unlock()
	group.handleDialFailure(errors.New("connection refused"), trigger)
	if actual := healthChecks.Load(); actual != 2 {
		t.Fatalf("connection refused after cooldown ran %d total checks, want 2", actual)
	}
}

func TestFailureHealthCheckCooldownIsRaceFree(t *testing.T) {
	group := NewGroupBase(GroupBaseOption{Name: "test", Type: C.URLTest})
	var healthChecks atomic.Int32
	var passed atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if group.tryForceHealthCheck(failureRecheckCooldown, func() {
				healthChecks.Add(1)
			}) {
				passed.Add(1)
			}
		}()
	}

	close(start)
	wg.Wait()

	if actual := passed.Load(); actual != 1 {
		t.Fatalf("cooldown admitted %d callers, want 1", actual)
	}
	if actual := healthChecks.Load(); actual != 1 {
		t.Fatalf("health check ran %d times, want 1", actual)
	}
}

func TestFailureWindowExpiryDoesNotLeakLock(t *testing.T) {
	group := NewGroupBase(GroupBaseOption{
		Name:           "test",
		Type:           C.URLTest,
		TestTimeout:    1,
		MaxFailedTimes: 2,
	})

	group.handleDialFailure(errors.New("dial failed"), func() {})
	time.Sleep(5 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		group.handleDialFailure(errors.New("dial failed"), func() {})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("failure window expiry left failedTestMux locked")
	}
	if actual := failedTimesForTest(group); actual != 0 {
		t.Fatalf("expired failure window left failedTimes at %d, want 0", actual)
	}
}

func TestFailureHealthCheckCallbackRunsWithoutStateLocks(t *testing.T) {
	group := NewGroupBase(GroupBaseOption{
		Name:           "test",
		Type:           C.URLTest,
		MaxFailedTimes: 2,
	})
	group.handleDialFailure(errors.New("dial failed"), func() {})

	done := make(chan struct{})
	go func() {
		group.handleDialFailure(errors.New("dial failed"), func() {
			group.onDialSuccess()
			group.tryForceHealthCheck(0, func() {})
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("health check callback ran while a failure-state lock was held")
	}
}

func TestDialFailureAndSuccessStateIsRaceFree(t *testing.T) {
	group := NewGroupBase(GroupBaseOption{
		Name:           "test",
		Type:           C.URLTest,
		MaxFailedTimes: 1_000_000,
	})
	var concurrent sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < 64; i++ {
		concurrent.Add(2)
		go func() {
			defer concurrent.Done()
			<-start
			for j := 0; j < 100; j++ {
				group.recordDialFailure()
			}
		}()
		go func() {
			defer concurrent.Done()
			<-start
			for j := 0; j < 100; j++ {
				group.onDialSuccess()
			}
		}()
	}

	close(start)
	concurrent.Wait()
}

type blockingHealthCheckProvider struct {
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
	calls       atomic.Int32
}

func (p *blockingHealthCheckProvider) Name() string               { return "test-provider" }
func (p *blockingHealthCheckProvider) VehicleType() P.VehicleType { return P.Compatible }
func (p *blockingHealthCheckProvider) Type() P.ProviderType       { return P.Proxy }
func (p *blockingHealthCheckProvider) Initial() error             { return nil }
func (p *blockingHealthCheckProvider) Update() error              { return nil }
func (p *blockingHealthCheckProvider) Proxies() []C.Proxy         { return nil }
func (p *blockingHealthCheckProvider) Count() int                 { return 0 }
func (p *blockingHealthCheckProvider) Touch()                     {}
func (p *blockingHealthCheckProvider) Version() uint32            { return 0 }
func (p *blockingHealthCheckProvider) HealthCheckURL() string     { return "" }
func (p *blockingHealthCheckProvider) RegisterHealthCheckTask(string, utils.IntRanges[uint16], string, uint) {
}
func (p *blockingHealthCheckProvider) HealthCheck() {
	if p.calls.Add(1) != 1 {
		return
	}
	p.startedOnce.Do(func() { close(p.started) })
	<-p.release
}

func (p *blockingHealthCheckProvider) releaseChecks() {
	p.releaseOnce.Do(func() { close(p.release) })
}

func TestHealthCheckSuppressesConcurrentRuns(t *testing.T) {
	provider := &blockingHealthCheckProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	t.Cleanup(provider.releaseChecks)
	group := NewGroupBase(GroupBaseOption{
		Name:      "test",
		Type:      C.URLTest,
		Providers: []P.ProxyProvider{provider},
	})

	firstDone := make(chan struct{})
	go func() {
		group.healthCheck()
		close(firstDone)
	}()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("first provider health check did not start")
	}

	var concurrent sync.WaitGroup
	for i := 0; i < 64; i++ {
		concurrent.Add(1)
		go func() {
			defer concurrent.Done()
			group.healthCheck()
		}()
	}
	concurrent.Wait()
	if actual := provider.calls.Load(); actual != 1 {
		t.Fatalf("concurrent health checks reached provider %d times, want 1", actual)
	}

	provider.releaseChecks()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first health check did not finish")
	}

	group.healthCheck()
	if actual := provider.calls.Load(); actual != 2 {
		t.Fatalf("completed health check did not reopen the gate: got %d calls, want 2", actual)
	}
}

var _ P.ProxyProvider = (*blockingHealthCheckProvider)(nil)
