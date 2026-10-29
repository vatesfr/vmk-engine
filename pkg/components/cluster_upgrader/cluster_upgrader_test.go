package cluster_upgrader

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	versionmanager "github.com/vatesfr/vmk-engine/pkg/components/version_manager"
	uuids "github.com/vatesfr/vmk-engine/pkg/helpers/uuids"
	"github.com/vatesfr/vmk-engine/pkg/strategies"
	"github.com/vatesfr/vmk-engine/pkg/strategies/mocks"
)

const (
	controlPlanePhase      = "control plane"
	workersPhase           = "workers"
	clusterName            = "cluster-1"
	secondClusterName      = "cluster-2"
	clusterVersion         = "1.36.5"
	upgradeObservedVersion = "v1.36.4"
	upgradeOperationID     = "op-1"
)

var (
	errTest     = errors.New("test error")
	clusterUUID = uuids.ClusterID(clusterName)
)

type upgradeTestData struct {
	strategies.UpgradeContext
	ControlPlane strategies.UpgradeResourceState
	Workers      []strategies.UpgradeWorkerState
	Machines     []strategies.UpgradeMachineState
}

func healthyUpgradeState() upgradeTestData {
	return upgradeTestData{
		UpgradeContext: strategies.UpgradeContext{Cluster: strategies.ResourceSnapshot{Name: clusterName, UID: "cluster-uid", ResourceVersion: "10"}},
		ControlPlane:   strategies.UpgradeResourceState{Resource: strategies.ResourceSnapshot{Name: "cp", UID: "cp-uid", ResourceVersion: "20"}, Version: upgradeObservedVersion, Ready: true},
		Workers: []strategies.UpgradeWorkerState{{UpgradeResourceState: strategies.UpgradeResourceState{
			Resource: strategies.ResourceSnapshot{Name: "workers", UID: "workers-uid", ResourceVersion: "30"},
			Version:  upgradeObservedVersion, Ready: true,
		}}},
		Machines: []strategies.UpgradeMachineState{{Version: upgradeObservedVersion, KubeletVersion: upgradeObservedVersion, Ready: true}},
	}
}

func validatedPlan() clusterUpgradePlan {
	return clusterUpgradePlan{
		Version:      "v" + clusterVersion,
		ControlPlane: strategies.UpgradeTarget{Name: "cp", UID: "cp-uid", Version: upgradeObservedVersion},
		Workers:      []strategies.UpgradeTarget{{Name: "workers", UID: "workers-uid", Version: upgradeObservedVersion}},
	}
}

func TestStartUpgradePersistsOperationAndLock(t *testing.T) {
	for _, tc := range []struct {
		name            string
		saveErr         error
		alreadyAtTarget bool
	}{
		{name: "accepted"},
		{name: "ambiguous write", saveErr: errTest},
		{name: "concurrent change", saveErr: strategies.ErrUpgradeStateChanged},
		{name: "already at target", alreadyAtTarget: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
			upgrader := NewClusterUpgrader(t.Context(), resources, versionmanager.NewVersionManager())
			defer upgrader.Stop()
			state := healthyUpgradeState()
			plan := validatedPlan()
			if tc.alreadyAtTarget {
				plan.Version = upgradeObservedVersion
			}
			if !tc.alreadyAtTarget && !errors.Is(tc.saveErr, strategies.ErrUpgradeStateChanged) {
				resources.EXPECT().GetUpgradeContext(upgrader.ctx, clusterName).Return(strategies.UpgradeContext{}, nil).MaxTimes(1)
			}
			var saved strategies.UpgradeOperation
			expectUpgradePreflight(t, resources, upgrader, state, plan)
			resources.EXPECT().SaveUpgrade(gomock.Any(), state.Cluster, gomock.Any()).DoAndReturn(func(_ context.Context, _ strategies.ResourceSnapshot, op strategies.UpgradeOperation) error {
				saved = op
				assert.NotEmpty(t, op.ID)
				assert.NotEmpty(t, op.StartedAt)
				assert.Equal(t, plan.Version, op.Version)
				assert.Equal(t, plan.ControlPlane, op.ControlPlane)
				assert.Equal(t, plan.Workers, op.Workers)
				if tc.alreadyAtTarget {
					assert.False(t, op.Locked)
					assert.NotEmpty(t, op.CompletedAt)
				} else {
					assert.True(t, op.Locked)
					assert.Empty(t, op.CompletedAt)
				}
				return tc.saveErr
			})
			status, err := upgrader.StartUpgrade(t.Context(), clusterUUID, strings.TrimPrefix(plan.Version, "v"))
			if tc.saveErr == nil {
				require.NoError(t, err)
				assert.Equal(t, saved, status)
			} else {
				require.ErrorIs(t, err, tc.saveErr)
			}
			if errors.Is(tc.saveErr, strategies.ErrUpgradeStateChanged) {
				require.ErrorIs(t, err, strategies.ErrClusterUpgradeConflict)
			}
		})
	}
}

func TestUpgradeExecutionSurvivesRequestCancellationAndDeduplicates(t *testing.T) {
	resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
	serviceCtx, stopService := context.WithCancel(t.Context())
	defer stopService()
	upgrader := NewClusterUpgrader(serviceCtx, resources, versionmanager.NewVersionManager())
	t.Cleanup(upgrader.Stop)
	requestCtx, endRequest := context.WithCancel(t.Context())
	defer endRequest()
	state := healthyUpgradeState()
	plan := validatedPlan()
	entered := make(chan struct{})
	release := make(chan struct{})
	signalEntered := sync.OnceFunc(func() { close(entered) })
	var operation strategies.UpgradeOperation
	expectUpgradePreflight(t, resources, upgrader, state, plan)
	resources.EXPECT().SaveUpgrade(gomock.Any(), state.Cluster, gomock.Any()).DoAndReturn(func(_ context.Context, _ strategies.ResourceSnapshot, op strategies.UpgradeOperation) error {
		operation = op
		return nil
	})
	resources.EXPECT().GetUpgradeContext(upgrader.ctx, clusterName).DoAndReturn(func(ctx context.Context, _ string) (strategies.UpgradeContext, error) {
		signalEntered()
		select {
		case <-release:
		case <-ctx.Done():
			return strategies.UpgradeContext{}, ctx.Err()
		}
		execution := state.UpgradeContext
		execution.Operation = &operation
		return execution, nil
	}).AnyTimes()
	resources.EXPECT().GetUpgradeControlPlane(upgrader.ctx, state.Cluster).Return(state.ControlPlane, nil)
	resources.EXPECT().ApplyControlPlaneUpgrade(upgrader.ctx, state.Cluster, plan.Version, plan.ControlPlane).Return(nil)
	resources.EXPECT().IsControlPlaneUpgradeReady(upgrader.ctx, state.Cluster, plan.Version, plan.ControlPlane).
		DoAndReturn(func(ctx context.Context, _ strategies.ResourceSnapshot, _ string, _ strategies.UpgradeTarget) (bool, error) {
			assert.NoError(t, ctx.Err())
			assert.ErrorIs(t, requestCtx.Err(), context.Canceled)
			assert.True(t, operation.Locked)
			stopService()
			return false, nil
		})
	status, err := upgrader.StartUpgrade(requestCtx, clusterUUID, clusterVersion)
	require.NoError(t, err)
	waitForSignal(t, entered)
	endRequest()
	active := state.UpgradeContext
	active.Operation = &status
	resources.EXPECT().GetByUUID(gomock.Not(upgrader.ctx), clusterUUID).Return(strategies.ClusterInfo{Name: clusterName}, nil).AnyTimes()
	resources.EXPECT().GetUpgradeContext(gomock.Not(upgrader.ctx), clusterName).Return(active, nil).Times(20)
	var submissions sync.WaitGroup
	for range 20 {
		submissions.Add(1)
		go func() {
			defer submissions.Done()
			resumed, err := upgrader.StartUpgrade(t.Context(), clusterUUID, clusterVersion)
			assert.NoError(t, err)
			assert.Equal(t, status, resumed)
		}()
	}
	submissions.Wait()
	close(release)
	waitForSignal(t, upgrader.ctx.Done())
	upgrader.Stop()
}

func TestUpgradeRecoveryScansOnceAndResumesOnlyActiveOperations(t *testing.T) {
	resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
	upgrader := NewClusterUpgrader(t.Context(), resources, versionmanager.NewVersionManager())
	t.Cleanup(upgrader.Stop)
	state := healthyUpgradeState()
	state.Operation = &strategies.UpgradeOperation{
		ID: upgradeOperationID, Version: "v" + clusterVersion, Locked: true,
	}
	completed := state.UpgradeContext
	completed.Operation = &strategies.UpgradeOperation{ID: upgradeOperationID, Version: "v" + clusterVersion}
	resources.EXPECT().ListActiveUpgrades(t.Context()).Return([]string{clusterName, secondClusterName}, nil).Times(1)
	resources.EXPECT().GetUpgradeContext(t.Context(), clusterName).Return(state.UpgradeContext, nil)
	resources.EXPECT().GetUpgradeContext(t.Context(), secondClusterName).Return(strategies.UpgradeContext{}, nil)
	resources.EXPECT().GetUpgradeContext(upgrader.ctx, clusterName).Return(completed, nil).Times(1)
	require.NoError(t, upgrader.ResumeClusterUpgrades(t.Context()))
	waitForWorkers(t, upgrader)
}

func TestAmbiguousLockWriteVerifiesPersistedIdentityBeforeExecuting(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			upgrader := NewClusterUpgrader(ctx, resources, versionmanager.NewVersionManager())
			t.Cleanup(upgrader.Stop)
			state := healthyUpgradeState()
			plan := validatedPlan()
			var operation strategies.UpgradeOperation
			expectUpgradePreflight(t, resources, upgrader, state, plan)
			resources.EXPECT().SaveUpgrade(gomock.Any(), state.Cluster, gomock.Any()).DoAndReturn(func(_ context.Context, _ strategies.ResourceSnapshot, op strategies.UpgradeOperation) error {
				operation = op
				return errTest
			})
			resources.EXPECT().GetUpgradeContext(upgrader.ctx, clusterName).DoAndReturn(func(context.Context, string) (strategies.UpgradeContext, error) {
				execution := state.UpgradeContext
				if committed {
					execution.Operation = &operation
				}
				return execution, nil
			}).AnyTimes()
			if committed {
				resources.EXPECT().GetUpgradeControlPlane(upgrader.ctx, state.Cluster).Return(state.ControlPlane, nil)
				resources.EXPECT().ApplyControlPlaneUpgrade(upgrader.ctx, state.Cluster, plan.Version, plan.ControlPlane).Return(nil)
				resources.EXPECT().IsControlPlaneUpgradeReady(upgrader.ctx, state.Cluster, plan.Version, plan.ControlPlane).
					DoAndReturn(func(context.Context, strategies.ResourceSnapshot, string, strategies.UpgradeTarget) (bool, error) {
						cancel()
						return false, nil
					})
			}
			_, err := upgrader.StartUpgrade(t.Context(), clusterUUID, strings.TrimPrefix(plan.Version, "v"))
			require.ErrorIs(t, err, errTest)
			waitForWorkers(t, upgrader)
		})
	}
}

func TestUpgradeWorkerRetriesAndStopsWhenOperationFinishes(t *testing.T) {
	resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
	upgrader := NewClusterUpgrader(t.Context(), resources, versionmanager.NewVersionManager())
	upgrader.interval = time.Millisecond
	t.Cleanup(upgrader.Stop)
	reads := atomic.Int32{}
	resources.EXPECT().GetUpgradeContext(upgrader.ctx, clusterName).DoAndReturn(func(context.Context, string) (strategies.UpgradeContext, error) {
		if reads.Add(1) == 1 {
			return strategies.UpgradeContext{}, errTest
		}
		return strategies.UpgradeContext{}, nil
	}).Times(2)
	require.NoError(t, upgrader.startUpgradeWorkerIfAbsent(clusterName, upgradeOperationID))
	waitForWorkers(t, upgrader)
	assert.EqualValues(t, 2, reads.Load())
}

func TestUpgraderStopCancelsExecutionAndWaitsForIt(t *testing.T) {
	resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
	upgrader := NewClusterUpgrader(t.Context(), resources, versionmanager.NewVersionManager())
	t.Cleanup(upgrader.Stop)
	entered := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	releaseWorker := sync.OnceFunc(func() { close(release) })
	defer releaseWorker()
	resources.EXPECT().GetUpgradeContext(upgrader.ctx, clusterName).DoAndReturn(func(ctx context.Context, _ string) (strategies.UpgradeContext, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return strategies.UpgradeContext{}, ctx.Err()
	})
	require.NoError(t, upgrader.startUpgradeWorkerIfAbsent(clusterName, upgradeOperationID))
	waitForSignal(t, entered)
	stopped := make(chan struct{})
	go func() { upgrader.Stop(); close(stopped) }()
	waitForSignal(t, canceled)
	select {
	case <-stopped:
		t.Fatal("Stop returned before its operation finished")
	default:
	}
	releaseWorker()
	waitForSignal(t, stopped)
	require.ErrorIs(t, upgrader.startUpgradeWorkerIfAbsent(secondClusterName, upgradeOperationID), context.Canceled) // No further read is allowed.
	upgrader.Stop()
}

func TestUpgraderRunsDifferentClustersIndependently(t *testing.T) {
	resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
	upgrader := NewClusterUpgrader(t.Context(), resources, versionmanager.NewVersionManager())
	t.Cleanup(upgrader.Stop)
	blocked := make(chan struct{})
	resources.EXPECT().GetUpgradeContext(upgrader.ctx, clusterName).DoAndReturn(func(ctx context.Context, _ string) (strategies.UpgradeContext, error) {
		close(blocked)
		<-ctx.Done()
		return strategies.UpgradeContext{}, ctx.Err()
	})
	finished := make(chan struct{})
	resources.EXPECT().GetUpgradeContext(upgrader.ctx, secondClusterName).DoAndReturn(func(context.Context, string) (strategies.UpgradeContext, error) {
		close(finished)
		return strategies.UpgradeContext{}, nil
	})
	require.NoError(t, upgrader.startUpgradeWorkerIfAbsent(clusterName, upgradeOperationID))
	waitForSignal(t, blocked)
	require.NoError(t, upgrader.startUpgradeWorkerIfAbsent(secondClusterName, upgradeOperationID))
	waitForSignal(t, finished)
}

func waitForWorkers(t *testing.T, upgrader *ClusterUpgrader) {
	t.Helper()
	done := make(chan struct{})
	go func() { upgrader.workers.Wait(); close(done) }()
	waitForSignal(t, done)
}

func waitForSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("upgrade component did not reach the expected step")
	}
}

func TestStartUpgradeHandlesExistingOperation(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		conflict      bool
	}{
		{name: "same target resumes", version: "v" + clusterVersion},
		{name: "different target conflicts", version: "v1.37.1", conflict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
			upgrader := NewClusterUpgrader(t.Context(), resources, versionmanager.NewVersionManager())
			t.Cleanup(upgrader.Stop)
			state := healthyUpgradeState()
			state.Operation = &strategies.UpgradeOperation{
				ID: upgradeOperationID, Version: "v" + clusterVersion, Locked: true,
			}
			if !tc.conflict {
				finished := state.UpgradeContext
				operation := *state.Operation
				operation.Locked = false
				finished.Operation = &operation
				resources.EXPECT().GetUpgradeContext(upgrader.ctx, clusterName).Return(finished, nil).Times(1)
			}
			resources.EXPECT().GetByUUID(gomock.Not(upgrader.ctx), clusterUUID).Return(strategies.ClusterInfo{Name: clusterName}, nil).AnyTimes()
			resources.EXPECT().GetUpgradeContext(gomock.Not(upgrader.ctx), clusterName).Return(state.UpgradeContext, nil)
			operation, err := upgrader.StartUpgrade(t.Context(), clusterUUID, strings.TrimPrefix(tc.version, "v"))
			if tc.conflict {
				require.ErrorIs(t, err, strategies.ErrClusterUpgradeConflict)
			} else {
				require.NoError(t, err)
				assert.Equal(t, *state.Operation, operation)
			}
			waitForWorkers(t, upgrader)
		})
	}
}

func TestUpgradeWaitsOnlyReadResources(t *testing.T) {
	const machinePhase = "machines"
	for _, phase := range []string{controlPlanePhase, workersPhase, machinePhase} {
		t.Run(phase, func(t *testing.T) {
			resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
			upgrader := NewClusterUpgrader(t.Context(), resources, versionmanager.NewVersionManager())
			upgrader.interval = time.Millisecond
			t.Cleanup(upgrader.Stop)
			state := healthyUpgradeState()
			plan := validatedPlan()
			state.Operation = &strategies.UpgradeOperation{
				ID: upgradeOperationID, Version: plan.Version, Locked: true,
				ControlPlane: plan.ControlPlane, Workers: plan.Workers,
			}
			resources.EXPECT().GetUpgradeContext(t.Context(), clusterName).Return(state.UpgradeContext, nil).Times(2)
			reads := 0
			resources.EXPECT().IsControlPlaneUpgradeReady(t.Context(), state.Cluster, plan.Version, plan.ControlPlane).
				DoAndReturn(func(context.Context, strategies.ResourceSnapshot, string, strategies.UpgradeTarget) (bool, error) {
					if phase == controlPlanePhase {
						reads++
						return reads > 1, nil
					}
					return true, nil
				}).Times(2)
			if phase != controlPlanePhase {
				resources.EXPECT().GetUpgradeWorkers(t.Context(), state.Cluster).Return(state.Workers, nil).Times(2)
				resources.EXPECT().IsWorkerUpgradeReady(t.Context(), state.Cluster, upgradeOperationID, plan.Version, plan.Workers).
					DoAndReturn(func(context.Context, strategies.ResourceSnapshot, string, string, []strategies.UpgradeTarget) (bool, error) {
						if phase == workersPhase {
							reads++
							return reads > 1, nil
						}
						return true, nil
					}).Times(2)
			}
			if phase == machinePhase {
				resources.EXPECT().GetUpgradeMachines(t.Context(), state.Cluster).
					DoAndReturn(func(context.Context, strategies.ResourceSnapshot) ([]strategies.UpgradeMachineState, error) {
						reads++
						machines := append([]strategies.UpgradeMachineState(nil), state.Machines...)
						if reads > 1 {
							machines[0].Version, machines[0].KubeletVersion = plan.Version, plan.Version
						}
						return machines, nil
					}).Times(2)
			}
			key := upgradeKey{clusterName: clusterName, operationID: upgradeOperationID}
			var err error
			switch phase {
			case controlPlanePhase:
				err = upgrader.waitForControlPlane(t.Context(), key)
			case workersPhase:
				err = upgrader.waitForWorkers(t.Context(), key)
			case machinePhase:
				err = upgrader.waitForMachines(t.Context(), key)
			}
			require.NoError(t, err)
			assert.Equal(t, 2, reads)
			// No Apply or Save calls are expected, including while waiting or retrying.
		})
	}
}

func TestUpgradeWaitStopsWithoutWriting(t *testing.T) {
	for _, tc := range []struct {
		name      string
		operation *strategies.UpgradeOperation
		err       error
	}{
		{name: "missing operation"},
		{name: "completed", operation: &strategies.UpgradeOperation{ID: upgradeOperationID}},
		{name: "replaced", operation: &strategies.UpgradeOperation{ID: "another-operation", Locked: true}},
		{name: "deleted", err: strategies.ErrClusterNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
			upgrader := NewClusterUpgrader(t.Context(), resources, versionmanager.NewVersionManager())
			t.Cleanup(upgrader.Stop)
			resources.EXPECT().GetUpgradeContext(t.Context(), clusterName).Return(strategies.UpgradeContext{Operation: tc.operation}, tc.err)
			err := upgrader.waitForControlPlane(t.Context(), upgradeKey{clusterName: clusterName, operationID: upgradeOperationID})
			require.ErrorIs(t, err, errUpgradeFinished)
		})
	}
}

func TestUpgradeRunsSequentiallyAndRetriesBeforeUnlocking(t *testing.T) {
	resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
	upgrader := NewClusterUpgrader(t.Context(), resources, versionmanager.NewVersionManager())
	upgrader.interval = time.Millisecond
	t.Cleanup(upgrader.Stop)
	state := healthyUpgradeState()
	plan := validatedPlan()
	operation := strategies.UpgradeOperation{
		ID: upgradeOperationID, Version: plan.Version, Locked: true,
		ControlPlane: plan.ControlPlane, Workers: plan.Workers,
	}
	controlPlaneWrites, workerWrites, controlPlaneReads, workerReads, machineReads, completionWrites := 0, 0, 0, 0, 0, 0
	retainedLockAfterCompletionFailure := false
	resources.EXPECT().GetUpgradeContext(upgrader.ctx, clusterName).DoAndReturn(func(context.Context, string) (strategies.UpgradeContext, error) {
		current := state.UpgradeContext
		snapshot := operation
		current.Operation = &snapshot
		return current, nil
	}).AnyTimes()
	resources.EXPECT().GetUpgradeControlPlane(upgrader.ctx, state.Cluster).Return(state.ControlPlane, nil).Times(2)
	resources.EXPECT().ApplyControlPlaneUpgrade(upgrader.ctx, state.Cluster, plan.Version, plan.ControlPlane).
		DoAndReturn(func(context.Context, strategies.ResourceSnapshot, string, strategies.UpgradeTarget) error {
			controlPlaneWrites++
			if controlPlaneWrites == 1 {
				return errTest
			}
			return nil
		}).Times(2)
	resources.EXPECT().IsControlPlaneUpgradeReady(upgrader.ctx, state.Cluster, plan.Version, plan.ControlPlane).
		DoAndReturn(func(context.Context, strategies.ResourceSnapshot, string, strategies.UpgradeTarget) (bool, error) {
			controlPlaneReads++
			assert.Equal(t, 2, controlPlaneWrites)
			return controlPlaneReads > 1, nil
		}).AnyTimes()
	resources.EXPECT().GetUpgradeWorkers(upgrader.ctx, state.Cluster).Return(state.Workers, nil).AnyTimes()
	resources.EXPECT().ApplyWorkerUpgrade(upgrader.ctx, state.Cluster, upgradeOperationID, plan.Version, plan.Workers).
		DoAndReturn(func(context.Context, strategies.ResourceSnapshot, string, string, []strategies.UpgradeTarget) error {
			assert.Greater(t, controlPlaneReads, 1)
			workerWrites++
			if workerWrites == 1 {
				return errTest
			}
			return nil
		}).Times(2)
	resources.EXPECT().IsWorkerUpgradeReady(upgrader.ctx, state.Cluster, upgradeOperationID, plan.Version, plan.Workers).
		DoAndReturn(func(context.Context, strategies.ResourceSnapshot, string, string, []strategies.UpgradeTarget) (bool, error) {
			workerReads++
			assert.Equal(t, 2, workerWrites)
			return workerReads > 1, nil
		}).AnyTimes()
	resources.EXPECT().GetUpgradeMachines(upgrader.ctx, state.Cluster).DoAndReturn(func(context.Context, strategies.ResourceSnapshot) ([]strategies.UpgradeMachineState, error) {
		machineReads++
		assert.Greater(t, workerReads, 1)
		assert.True(t, operation.Locked)
		machines := append([]strategies.UpgradeMachineState(nil), state.Machines...)
		if machineReads > 1 {
			machines[0].Version, machines[0].KubeletVersion = plan.Version, plan.Version
		}
		return machines, nil
	}).AnyTimes()
	resources.EXPECT().SaveUpgrade(upgrader.ctx, state.Cluster, gomock.Any()).DoAndReturn(func(_ context.Context, _ strategies.ResourceSnapshot, next strategies.UpgradeOperation) error {
		if !next.Locked {
			completionWrites++
			assert.Greater(t, machineReads, 1)
			if completionWrites == 1 {
				retainedLockAfterCompletionFailure = operation.Locked && operation.CompletedAt == ""
				return errTest
			}
		}
		operation = next
		return nil
	}).AnyTimes()
	require.NoError(t, upgrader.startUpgradeWorkerIfAbsent(clusterName, upgradeOperationID))
	waitForWorkers(t, upgrader)
	assert.True(t, retainedLockAfterCompletionFailure)
	assert.Equal(t, 2, completionWrites)
	assert.Equal(t, 2, controlPlaneWrites)
	assert.Equal(t, 2, workerWrites)
	assert.False(t, operation.Locked)
	assert.NotEmpty(t, operation.CompletedAt)
	assert.NoError(t, upgrader.ctx.Err())
}

func TestStartUpgradeRejectsStoppedServiceWithoutWriting(t *testing.T) {
	for _, shutdown := range []string{"stop", "service cancellation"} {
		for _, active := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/active=%t", shutdown, active), func(t *testing.T) {
				resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				upgrader := NewClusterUpgrader(ctx, resources, versionmanager.NewVersionManager())
				t.Cleanup(upgrader.Stop)
				if shutdown == "stop" {
					upgrader.Stop()
				} else {
					cancel()
				}
				state := healthyUpgradeState()
				plan := validatedPlan()
				if active {
					state.Operation = &strategies.UpgradeOperation{ID: upgradeOperationID, Version: plan.Version, Locked: true}
				}
				operation, err := upgrader.StartUpgrade(t.Context(), clusterUUID, strings.TrimPrefix(plan.Version, "v"))
				require.ErrorIs(t, err, context.Canceled)
				assert.Empty(t, operation.ID)
				// Neither persistence nor execution is allowed after admission closes.
			})
		}
	}
}

func TestStopCancelsAndWaitsForStartInFlight(t *testing.T) {
	resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
	upgrader := NewClusterUpgrader(t.Context(), resources, versionmanager.NewVersionManager())
	t.Cleanup(upgrader.Stop)
	state := healthyUpgradeState()
	plan := validatedPlan()
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseWrite := sync.OnceFunc(func() { close(release) })
	defer releaseWrite()
	var saved strategies.UpgradeOperation
	expectUpgradePreflight(t, resources, upgrader, state, plan)
	resources.EXPECT().SaveUpgrade(gomock.Any(), state.Cluster, gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ strategies.ResourceSnapshot, operation strategies.UpgradeOperation) error {
			saved = operation
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			// Simulate a committed write whose successful response arrives during shutdown.
			return nil
		})
	startFinished := make(chan error, 1)
	go func() {
		_, err := upgrader.StartUpgrade(t.Context(), clusterUUID, strings.TrimPrefix(plan.Version, "v"))
		startFinished <- err
	}()
	waitForSignal(t, entered)
	stopped := make(chan struct{})
	go func() { upgrader.Stop(); close(stopped) }()
	waitForSignal(t, canceled)
	_, err := upgrader.StartUpgrade(t.Context(), clusterUUID, strings.TrimPrefix(plan.Version, "v"))
	require.ErrorIs(t, err, context.Canceled)
	select {
	case <-stopped:
		t.Fatal("Stop returned before lock persistence finished")
	default:
	}
	releaseWrite()
	require.ErrorIs(t, <-startFinished, context.Canceled)
	waitForSignal(t, stopped)
	assert.True(t, saved.Locked, "a committed lock must be retained for startup recovery")
	upgrader.mu.Lock()
	assert.Empty(t, upgrader.active)
	upgrader.mu.Unlock()
}

func TestRecoveryIsolatesClusterReadFailures(t *testing.T) {
	resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
	upgrader := NewClusterUpgrader(t.Context(), resources, versionmanager.NewVersionManager())
	t.Cleanup(upgrader.Stop)
	resources.EXPECT().ListActiveUpgrades(t.Context()).Return([]string{"malformed", clusterName}, nil)
	resources.EXPECT().GetUpgradeContext(t.Context(), "malformed").Return(strategies.UpgradeContext{}, strategies.ErrClusterUpgradeConflict)
	state := healthyUpgradeState()
	state.Operation = &strategies.UpgradeOperation{ID: upgradeOperationID, Version: "v" + clusterVersion, Locked: true}
	resources.EXPECT().GetUpgradeContext(t.Context(), clusterName).Return(state.UpgradeContext, nil)
	resources.EXPECT().GetUpgradeContext(upgrader.ctx, clusterName).Return(strategies.UpgradeContext{}, nil)
	require.NoError(t, upgrader.ResumeClusterUpgrades(t.Context()))
	waitForWorkers(t, upgrader)
}

func TestRecoveryStillReportsGlobalListFailure(t *testing.T) {
	resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
	upgrader := NewClusterUpgrader(t.Context(), resources, versionmanager.NewVersionManager())
	t.Cleanup(upgrader.Stop)
	resources.EXPECT().ListActiveUpgrades(t.Context()).Return(nil, errTest)
	require.ErrorIs(t, upgrader.ResumeClusterUpgrades(t.Context()), errTest)
}

func TestUpgradeRules(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reads    int
		target   string
		mutate   func(*upgradeTestData)
		expected error
	}{
		{name: "downgrade", reads: 1, target: "1.36.3", expected: strategies.ErrInvalidUpgradeVersion},
		{name: "minor skip", reads: 1, target: "1.37.1", mutate: func(s *upgradeTestData) { s.ControlPlane.Version = "v1.35.9" }, expected: strategies.ErrInvalidUpgradeVersion},
		{name: "invalid current version", reads: 1, mutate: func(s *upgradeTestData) { s.ControlPlane.Version = "invalid" }, expected: strategies.ErrInvalidUpgradeVersion},
		{name: "prerelease source", reads: 1, mutate: func(s *upgradeTestData) { s.ControlPlane.Version = "v1.36.0-rc.1" }, expected: strategies.ErrInvalidUpgradeVersion},
		{name: "paused cluster", reads: 0, mutate: func(s *upgradeTestData) { s.Paused = true }},
		{name: "deleting cluster", reads: 0, mutate: func(s *upgradeTestData) { s.Deleting = true }},
		{name: "managed topology", reads: 0, mutate: func(s *upgradeTestData) { s.TopologyManaged = true }},
		{name: "paused control plane", reads: 1, mutate: func(s *upgradeTestData) { s.ControlPlane.Paused = true }},
		{name: "control plane not converged", reads: 1, mutate: func(s *upgradeTestData) { s.ControlPlane.Ready = false }},
		{name: "no workers", reads: 2, mutate: func(s *upgradeTestData) { s.Workers = nil }},
		{name: "paused workers", reads: 2, mutate: func(s *upgradeTestData) { s.Workers[0].Paused = true }},
		{name: "deleting workers", reads: 2, mutate: func(s *upgradeTestData) { s.Workers[0].Deleting = true }},
		{name: "manual rollout", reads: 2, mutate: func(s *upgradeTestData) { s.Workers[0].RolloutStrategy = "OnDelete" }},
		{name: "workers not converged", reads: 2, mutate: func(s *upgradeTestData) { s.Workers[0].Ready = false }},
		{name: "mixed versions", reads: 2, mutate: func(s *upgradeTestData) { s.Workers[0].Version = "v1.35.9" }},
		{name: "no observed nodes", reads: 3, mutate: func(s *upgradeTestData) { s.Machines = nil }},
		{name: "old observed kubelet", reads: 3, mutate: func(s *upgradeTestData) { s.Machines[0].KubeletVersion = "v1.36.3" }},
		{name: "unhealthy node", reads: 3, mutate: func(s *upgradeTestData) { s.Machines[0].Ready = false }},
		{name: "paused node", reads: 3, mutate: func(s *upgradeTestData) { s.Machines[0].Paused = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
			upgrader := NewClusterUpgrader(t.Context(), resources, versionmanager.NewVersionManager())
			t.Cleanup(upgrader.Stop)
			state := healthyUpgradeState()
			if tc.mutate != nil {
				tc.mutate(&state)
			}
			resources.EXPECT().GetByUUID(gomock.Not(upgrader.ctx), clusterUUID).Return(strategies.ClusterInfo{Name: clusterName}, nil).AnyTimes()
			resources.EXPECT().GetUpgradeContext(gomock.Not(upgrader.ctx), clusterName).Return(state.UpgradeContext, nil)
			if tc.reads >= 1 {
				resources.EXPECT().GetUpgradeControlPlane(gomock.Not(upgrader.ctx), state.Cluster).Return(state.ControlPlane, nil)
			}
			if tc.reads >= 2 {
				resources.EXPECT().GetUpgradeWorkers(gomock.Not(upgrader.ctx), state.Cluster).Return(state.Workers, nil)
			}
			if tc.reads >= 3 {
				resources.EXPECT().GetUpgradeMachines(gomock.Not(upgrader.ctx), state.Cluster).Return(state.Machines, nil)
			}
			target := tc.target
			if target == "" {
				target = clusterVersion
			}
			_, err := upgrader.StartUpgrade(t.Context(), clusterUUID, target)
			expected := tc.expected
			if expected == nil {
				expected = strategies.ErrClusterUpgradeConflict
			}
			require.ErrorIs(t, err, expected)
			// No target construction, persistence or apply call is expected after a policy rejection.
		})
	}
}

func TestUpgradeClusterPropagatesContextReadError(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
			upgrader := NewClusterUpgrader(t.Context(), resources, versionmanager.NewVersionManager())
			t.Cleanup(upgrader.Stop)
			clusterContext := strategies.UpgradeContext{}
			if active {
				clusterContext.Operation = &strategies.UpgradeOperation{
					ID: upgradeOperationID, Version: "v" + clusterVersion, Locked: true,
				}
			}
			resources.EXPECT().GetByUUID(gomock.Not(upgrader.ctx), clusterUUID).Return(strategies.ClusterInfo{Name: clusterName}, nil).AnyTimes()
			resources.EXPECT().GetUpgradeContext(gomock.Not(upgrader.ctx), clusterName).Return(clusterContext, errTest)
			_, err := upgrader.StartUpgrade(t.Context(), clusterUUID, clusterVersion)
			require.ErrorIs(t, err, errTest)
		})
	}
}

func TestUpgradeCluster(t *testing.T) {
	ctrl := gomock.NewController(t)
	resources := mocks.NewMockForManagingClusterResources(ctrl)
	upgrader := NewClusterUpgrader(t.Context(), resources, versionmanager.NewVersionManager())
	t.Cleanup(upgrader.Stop)
	for _, tc := range []struct {
		id       uuid.UUID
		version  string
		expected error
	}{
		{id: uuid.Nil, version: "1.36.5", expected: strategies.ErrClusterIDRequired},
		{id: clusterUUID, version: "", expected: strategies.ErrClusterVersionRequired},
		{id: clusterUUID, version: "v1.36.5", expected: strategies.ErrClusterVersionUnavailable},
		{id: clusterUUID, version: "1.36.99", expected: strategies.ErrClusterVersionUnavailable},
	} {
		_, err := upgrader.StartUpgrade(t.Context(), tc.id, tc.version)
		require.ErrorIs(t, err, tc.expected)
	}
}

// Expect request-side reads separately from the worker's service context.
func expectUpgradePreflight(t *testing.T, resources *mocks.MockForManagingClusterResources, upgrader *ClusterUpgrader, state upgradeTestData, plan clusterUpgradePlan) {
	t.Helper()
	requestContext := gomock.Not(upgrader.ctx)
	resources.EXPECT().GetByUUID(requestContext, clusterUUID).Return(strategies.ClusterInfo{Name: clusterName}, nil)
	resources.EXPECT().GetUpgradeContext(requestContext, clusterName).Return(state.UpgradeContext, nil)
	resources.EXPECT().GetUpgradeControlPlane(requestContext, state.Cluster).Return(state.ControlPlane, nil)
	resources.EXPECT().GetUpgradeWorkers(requestContext, state.Cluster).Return(state.Workers, nil)
	resources.EXPECT().GetUpgradeMachines(requestContext, state.Cluster).Return(state.Machines, nil)
	resources.EXPECT().BuildControlPlaneUpgradeTarget(requestContext, state.Cluster, state.ControlPlane.Resource, plan.Version).Return(plan.ControlPlane, nil)
	for i, worker := range state.Workers {
		resources.EXPECT().BuildWorkerUpgradeTarget(requestContext, state.Cluster, worker.Resource, plan.Version).Return(plan.Workers[i], nil)
	}
}

func TestStopCancelsAndWaitsForPreflightWithoutAcquiringLock(t *testing.T) {
	resources := mocks.NewMockForManagingClusterResources(gomock.NewController(t))
	upgrader := NewClusterUpgrader(t.Context(), resources, versionmanager.NewVersionManager())
	t.Cleanup(upgrader.Stop)
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseRead := sync.OnceFunc(func() { close(release) })
	defer releaseRead()
	resources.EXPECT().GetByUUID(gomock.Not(upgrader.ctx), clusterUUID).Return(strategies.ClusterInfo{Name: clusterName}, nil).AnyTimes()
	resources.EXPECT().GetUpgradeContext(gomock.Not(upgrader.ctx), clusterName).
		DoAndReturn(func(ctx context.Context, _ string) (strategies.UpgradeContext, error) {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			return strategies.UpgradeContext{}, ctx.Err()
		})
	finished := make(chan error, 1)
	go func() {
		_, err := upgrader.StartUpgrade(t.Context(), clusterUUID, clusterVersion)
		finished <- err
	}()
	waitForSignal(t, entered)
	stopped := make(chan struct{})
	go func() { upgrader.Stop(); close(stopped) }()
	waitForSignal(t, canceled)
	select {
	case <-stopped:
		t.Fatal("Stop returned before preflight finished")
	default:
	}
	releaseRead()
	require.ErrorIs(t, <-finished, context.Canceled)
	waitForSignal(t, stopped)
	// No target construction, persistence or worker read is allowed.
}
