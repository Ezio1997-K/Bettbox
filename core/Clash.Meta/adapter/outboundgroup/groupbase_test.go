package outboundgroup

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metacubex/mihomo/adapter/outbound"
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
	proxies     []C.Proxy
}

func (p *blockingHealthCheckProvider) Name() string               { return "test-provider" }
func (p *blockingHealthCheckProvider) VehicleType() P.VehicleType { return P.Compatible }
func (p *blockingHealthCheckProvider) Type() P.ProviderType       { return P.Proxy }
func (p *blockingHealthCheckProvider) Initial() error             { return nil }
func (p *blockingHealthCheckProvider) Update() error              { return nil }
func (p *blockingHealthCheckProvider) Proxies() []C.Proxy         { return p.proxies }
func (p *blockingHealthCheckProvider) Count() int                 { return len(p.proxies) }
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

type failingProxy struct {
	*outbound.Base
	dialError error
}

func (p *failingProxy) Adapter() C.ProxyAdapter { return p }
func (p *failingProxy) AliveForTestUrl(string) bool {
	return true
}
func (p *failingProxy) DelayHistory() []C.DelayHistory { return nil }
func (p *failingProxy) ExtraDelayHistories() map[string]C.ProxyState {
	return nil
}
func (p *failingProxy) LastDelayForTestUrl(string) uint16 { return 1 }
func (p *failingProxy) URLTest(context.Context, string, utils.IntRanges[uint16]) (uint16, error) {
	return 0, p.dialError
}
func (p *failingProxy) DialContext(context.Context, *C.Metadata) (C.Conn, error) {
	return nil, p.dialError
}

func waitForFailureGateState(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func TestURLTestDialFailuresUseFailureHealthCheckCooldown(t *testing.T) {
	dialError := errors.New("dial failed")
	proxy := &failingProxy{
		Base: outbound.NewBase(outbound.BaseOption{
			Name: "failing-proxy",
			Type: C.Vless,
		}),
		dialError: dialError,
	}
	provider := &blockingHealthCheckProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
		proxies: []C.Proxy{proxy},
	}
	t.Cleanup(provider.releaseChecks)
	group, err := NewURLTest(
		GroupCommonOption{
			Name:           "test",
			URL:            "https://health-check.invalid/generate_204",
			TestTimeout:    5000,
			MaxFailedTimes: 3,
		},
		URLTestOption{},
		proxy,
		[]P.ProxyProvider{provider},
	)
	if err != nil {
		t.Fatalf("create URLTest group: %v", err)
	}

	dial := func() {
		t.Helper()
		_, err := group.DialContext(context.Background(), &C.Metadata{})
		if !errors.Is(err, dialError) {
			t.Fatalf("DialContext error = %v, want %v", err, dialError)
		}
	}

	dial()
	dial()
	waitForFailureGateState(t, "the first two dial failures", func() bool {
		return failedTimesForTest(group.GroupBase) == 2
	})
	dial()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("failure threshold did not trigger provider health check")
	}
	if actual := provider.calls.Load(); actual != 1 {
		t.Fatalf("first failure burst ran %d provider checks, want 1", actual)
	}

	provider.releaseChecks()
	waitForFailureGateState(t, "the first provider health check to finish", func() bool {
		return !group.failedTesting.Load()
	})

	dial()
	dial()
	waitForFailureGateState(t, "the second pair of dial failures", func() bool {
		return failedTimesForTest(group.GroupBase) == 2
	})
	dial()
	waitForFailureGateState(t, "the second failure threshold", func() bool {
		return failedTimesForTest(group.GroupBase) == 0
	})
	if actual := provider.calls.Load(); actual != 1 {
		t.Fatalf("cooldown admitted %d provider checks, want 1", actual)
	}
}

func newSelectionRaceTestProxies() []C.Proxy {
	return []C.Proxy{
		&failingProxy{
			Base: outbound.NewBase(outbound.BaseOption{Name: "proxy-a", Type: C.Vless}),
		},
		&failingProxy{
			Base: outbound.NewBase(outbound.BaseOption{Name: "proxy-b", Type: C.Vless}),
		},
	}
}

func exerciseConcurrentSelection(t *testing.T, selectProxy func(string), currentProxy func() string) {
	t.Helper()
	const iterations = 10_000
	const readers = 8
	start := make(chan struct{})
	var concurrent sync.WaitGroup
	concurrent.Add(1 + readers)
	go func() {
		defer concurrent.Done()
		<-start
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				selectProxy("proxy-a")
			} else {
				selectProxy("proxy-b")
			}
		}
	}()
	for i := 0; i < readers; i++ {
		go func() {
			defer concurrent.Done()
			<-start
			for i := 0; i < iterations; i++ {
				if currentProxy() == "" {
					t.Error("selection returned an empty proxy name")
					return
				}
			}
		}()
	}
	close(start)
	concurrent.Wait()
}

func TestURLTestSelectionStateIsRaceFree(t *testing.T) {
	proxies := newSelectionRaceTestProxies()
	provider := &blockingHealthCheckProvider{proxies: proxies}
	group, err := NewURLTest(
		GroupCommonOption{Name: "test", URL: "https://health-check.invalid/generate_204"},
		URLTestOption{},
		proxies[0],
		[]P.ProxyProvider{provider},
	)
	if err != nil {
		t.Fatalf("create URLTest group: %v", err)
	}

	exerciseConcurrentSelection(t, func(name string) {
		if err := group.Set(name); err != nil {
			t.Errorf("select URLTest proxy %q: %v", name, err)
		}
	}, group.Now)
}

func TestFallbackSelectionStateIsRaceFree(t *testing.T) {
	proxies := newSelectionRaceTestProxies()
	provider := &blockingHealthCheckProvider{proxies: proxies}
	group, err := NewFallback(
		GroupCommonOption{Name: "test", URL: "https://health-check.invalid/generate_204"},
		FallbackOption{},
		proxies[0],
		[]P.ProxyProvider{provider},
	)
	if err != nil {
		t.Fatalf("create Fallback group: %v", err)
	}

	exerciseConcurrentSelection(t, func(name string) {
		if err := group.Set(name); err != nil {
			t.Errorf("select Fallback proxy %q: %v", name, err)
		}
	}, group.Now)
}

var _ P.ProxyProvider = (*blockingHealthCheckProvider)(nil)
var _ C.Proxy = (*failingProxy)(nil)
