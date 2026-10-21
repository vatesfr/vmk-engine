package cluster_upgrader

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gofrs/uuid"

	"capv-proxy/pkg/strategies"

	"k8s.io/apimachinery/pkg/util/version"
)

type upgradeKey struct {
	clusterName string
	operationID string
}

// ClusterUpgrader owns persisted upgrade execution, its lock and per-operation
// goroutines, including request validation and preflight checks.
type ClusterUpgrader struct {
	resources strategies.ForManagingClusterResources
	versions  strategies.ForManagingVersions
	ctx       context.Context
	cancel    context.CancelFunc
	interval  time.Duration
	mu        sync.Mutex
	active    map[upgradeKey]struct{}
	stopping  bool
	starts    sync.WaitGroup
	workers   sync.WaitGroup
}

var _ strategies.ForUpgradingClusters = (*ClusterUpgrader)(nil)

func NewClusterUpgrader(serviceContext context.Context, resources strategies.ForManagingClusterResources, versions strategies.ForManagingVersions) *ClusterUpgrader {
	ctx, cancel := context.WithCancel(serviceContext)
	return &ClusterUpgrader{resources: resources, versions: versions, ctx: ctx, cancel: cancel, interval: 10 * time.Second, active: make(map[upgradeKey]struct{})}
}

func (r *ClusterUpgrader) startUpgradeWorkerIfAbsent(clusterName, operationID string) error {
	key := upgradeKey{clusterName: clusterName, operationID: operationID}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if r.stopping {
		return context.Canceled
	}
	if _, exists := r.active[key]; exists {
		return nil
	}
	r.active[key] = struct{}{}
	r.workers.Add(1)
	go func() {
		defer r.workers.Done()
		defer func() { r.mu.Lock(); delete(r.active, key); r.mu.Unlock() }()
		if err := r.runUpgrade(key); err != nil {
			slog.Error("run cluster upgrade", "cluster", key.clusterName, "operation", key.operationID, "error", err)
		}
	}()
	return nil
}

// runUpgrade executes a registered worker until it finishes or the service stops.
// Wait functions only observe resources; writes happen in upgrade and finalization functions.
func (r *ClusterUpgrader) runUpgrade(key upgradeKey) error {
	ctx := r.ctx
	if err := r.upgradeControlPlane(ctx, key); err != nil {
		return upgradeExecutionError(err)
	}
	if err := r.waitForControlPlane(ctx, key); err != nil {
		return upgradeExecutionError(err)
	}
	if err := r.upgradeWorkers(ctx, key); err != nil {
		return upgradeExecutionError(err)
	}
	if err := r.waitForWorkers(ctx, key); err != nil {
		return upgradeExecutionError(err)
	}
	if err := r.waitForMachines(ctx, key); err != nil {
		return upgradeExecutionError(err)
	}
	return upgradeExecutionError(r.completeUpgrade(ctx, key))
}

var errUpgradeFinished = errors.New("upgrade operation is no longer active")

func upgradeExecutionError(err error) error {
	if errors.Is(err, errUpgradeFinished) {
		return nil
	}
	return err
}

// Stop rejects new starts, cancels in-flight requests and workers, and waits for both.
// It does not change persisted operations or release upgrade locks.
func (r *ClusterUpgrader) Stop() {
	r.mu.Lock()
	r.stopping = true
	r.cancel()
	r.mu.Unlock()
	r.starts.Wait()
	r.workers.Wait()
}

func (r *ClusterUpgrader) StartUpgrade(ctx context.Context, id uuid.UUID, requestedVersion string) (strategies.UpgradeOperation, error) {
	// Admission shares the scheduler mutex so Stop cannot miss an in-flight start.
	r.mu.Lock()
	if err := r.ctx.Err(); err != nil {
		r.mu.Unlock()
		return strategies.UpgradeOperation{}, err
	}
	if r.stopping {
		r.mu.Unlock()
		return strategies.UpgradeOperation{}, context.Canceled
	}
	r.starts.Add(1)
	r.mu.Unlock()
	defer r.starts.Done()

	// Lock persistence must stop with either the request or the service.
	ctx, cancel := context.WithCancel(ctx)
	stopCancellation := context.AfterFunc(r.ctx, cancel)
	defer cancel()
	defer stopCancellation()
	if err := r.ctx.Err(); err != nil {
		return strategies.UpgradeOperation{}, err
	}
	if id == uuid.Nil {
		return strategies.UpgradeOperation{}, strategies.ErrClusterIDRequired
	}
	if requestedVersion == "" {
		return strategies.UpgradeOperation{}, strategies.ErrClusterVersionRequired
	}
	if !r.versions.IsVersionAvailable(requestedVersion) {
		return strategies.UpgradeOperation{}, fmt.Errorf("%w: %q", strategies.ErrClusterVersionUnavailable, requestedVersion)
	}
	targetVersion := "v" + requestedVersion
	clusterInfo, err := r.resources.GetByUUID(ctx, id)
	if err != nil {
		return strategies.UpgradeOperation{}, err
	}
	clusterContext, err := r.resources.GetUpgradeContext(ctx, clusterInfo.Name)
	if err != nil {
		return strategies.UpgradeOperation{}, err
	}
	cluster := clusterContext.Cluster
	if operation := clusterContext.Operation; operation != nil && operation.Locked {
		if operation.Version != targetVersion {
			return strategies.UpgradeOperation{}, upgradeConflict("another upgrade holds the cluster lock")
		}
		if err := r.startUpgradeWorkerIfAbsent(cluster.Name, operation.ID); err != nil {
			return strategies.UpgradeOperation{}, err
		}
		return *operation, nil
	}
	plan, err := r.buildUpgradePlan(ctx, clusterContext, targetVersion)
	if err != nil {
		return strategies.UpgradeOperation{}, err
	}
	operationID, err := uuid.NewV4()
	if err != nil {
		return strategies.UpgradeOperation{}, err
	}
	operation := strategies.UpgradeOperation{
		ID: operationID.String(), Version: plan.Version, Locked: true, StartedAt: time.Now().UTC().Format(time.RFC3339),
		ControlPlane: plan.ControlPlane, Workers: plan.Workers,
	}
	if plan.ControlPlane.Version == plan.Version {
		operation.Locked, operation.CompletedAt = false, time.Now().UTC().Format(time.RFC3339)
	}
	if err := r.resources.SaveUpgrade(ctx, cluster, operation); err != nil {
		// A lost API response can hide a committed lock. The worker verifies this
		// operation's identity before applying anything, and exits if it was not saved.
		if operation.Locked && !errors.Is(err, strategies.ErrUpgradeStateChanged) {
			_ = r.startUpgradeWorkerIfAbsent(cluster.Name, operation.ID)
		}
		return strategies.UpgradeOperation{}, upgradePersistenceError(err)
	}
	if err := r.ctx.Err(); err != nil {
		return strategies.UpgradeOperation{}, err
	}
	if operation.Locked {
		if err := r.startUpgradeWorkerIfAbsent(cluster.Name, operation.ID); err != nil {
			return strategies.UpgradeOperation{}, err
		}
	}
	return operation, nil
}

// clusterUpgradePlan captures validated resource identities and bootstrap targets.
type clusterUpgradePlan struct {
	Version      string
	ControlPlane strategies.UpgradeTarget
	Workers      []strategies.UpgradeTarget
}

func (r *ClusterUpgrader) buildUpgradePlan(ctx context.Context, clusterContext strategies.UpgradeContext, targetVersion string) (clusterUpgradePlan, error) {
	if clusterContext.Paused || clusterContext.Deleting {
		return clusterUpgradePlan{}, upgradeConflict("cluster is paused or deleting")
	}
	if clusterContext.TopologyManaged {
		return clusterUpgradePlan{}, upgradeConflict("topology-managed clusters are not supported")
	}

	controlPlane, err := r.resources.GetUpgradeControlPlane(ctx, clusterContext.Cluster)
	if err != nil {
		return clusterUpgradePlan{}, upgradePersistenceError(err)
	}
	if err := validateControlPlaneNotPausedOrDeleting(controlPlane); err != nil {
		return clusterUpgradePlan{}, err
	}
	if err := validateUpgradeVersionTransition(controlPlane.Version, targetVersion); err != nil {
		return clusterUpgradePlan{}, err
	}
	if !controlPlane.Ready {
		return clusterUpgradePlan{}, upgradeConflict("control plane is not fully converged and available")
	}

	workers, err := r.resources.GetUpgradeWorkers(ctx, clusterContext.Cluster)
	if err != nil {
		return clusterUpgradePlan{}, upgradePersistenceError(err)
	}
	if err := validateWorkersAllowRollingUpgrade(workers); err != nil {
		return clusterUpgradePlan{}, err
	}
	if len(workers) == 0 {
		return clusterUpgradePlan{}, upgradeConflict("at least one worker MachineDeployment is required")
	}
	for _, worker := range workers {
		if !worker.Ready {
			return clusterUpgradePlan{}, upgradeConflict("workers are not fully converged and available")
		}
		if worker.Version != controlPlane.Version {
			return clusterUpgradePlan{}, upgradeConflict("control plane and workers must have the same version before upgrading")
		}
	}

	machines, err := r.resources.GetUpgradeMachines(ctx, clusterContext.Cluster)
	if err != nil {
		return clusterUpgradePlan{}, upgradePersistenceError(err)
	}
	if err := validateMachinesReadyAtVersion(machines, controlPlane.Version); err != nil {
		return clusterUpgradePlan{}, err
	}

	controlPlaneTarget, err := r.resources.BuildControlPlaneUpgradeTarget(ctx, clusterContext.Cluster, controlPlane.Resource, targetVersion)
	if err != nil {
		return clusterUpgradePlan{}, upgradePersistenceError(err)
	}
	workerTargets := make([]strategies.UpgradeTarget, 0, len(workers))
	for _, worker := range workers {
		workerTarget, err := r.resources.BuildWorkerUpgradeTarget(ctx, clusterContext.Cluster, worker.Resource, targetVersion)
		if err != nil {
			return clusterUpgradePlan{}, upgradePersistenceError(err)
		}
		workerTargets = append(workerTargets, workerTarget)
	}

	return clusterUpgradePlan{Version: targetVersion, ControlPlane: controlPlaneTarget, Workers: workerTargets}, nil
}

func validateUpgradeVersionTransition(current, target string) error {
	from, err := version.ParseSemantic(current)
	if err != nil {
		return strategies.ErrInvalidUpgradeVersion
	}
	to, err := version.ParseSemantic(target)
	if err != nil || from.PreRelease() != "" || to.PreRelease() != "" || to.BuildMetadata() != "" || from.Major() != to.Major() || to.LessThan(from) || to.Minor() > from.Minor()+1 {
		return strategies.ErrInvalidUpgradeVersion
	}
	return nil
}

func upgradeConflict(reason string) error {
	return fmt.Errorf("%w: %s", strategies.ErrClusterUpgradeConflict, reason)
}

func upgradePersistenceError(err error) error {
	if errors.Is(err, strategies.ErrUpgradeStateChanged) {
		return fmt.Errorf("%w: %w", strategies.ErrClusterUpgradeConflict, err)
	}
	return err
}

func validateControlPlaneNotPausedOrDeleting(state strategies.UpgradeResourceState) error {
	if state.Paused || state.Deleting {
		return upgradeConflict("control plane is paused or deleting")
	}
	return nil
}

func validateWorkersAllowRollingUpgrade(workers []strategies.UpgradeWorkerState) error {
	for _, worker := range workers {
		if worker.Paused || worker.Deleting {
			return upgradeConflict("worker deployment is paused or deleting")
		}
		if worker.RolloutStrategy == "OnDelete" {
			return upgradeConflict("OnDelete worker rollout is unsupported")
		}
	}
	return nil
}

func validateMachinesReadyAtVersion(machines []strategies.UpgradeMachineState, target string) error {
	if len(machines) == 0 {
		return upgradeConflict("waiting for observed Machines")
	}
	for _, machine := range machines {
		if machine.Paused || machine.Deleting || !machine.Ready || machine.Version != target || machine.KubeletVersion != target {
			return upgradeConflict("waiting for all Machines to be Ready at the target kubelet version")
		}
	}
	return nil
}

// ResumeClusterUpgrades reads persisted locks once at process startup. There is
// no global polling loop: each active operation gets its own worker.
func (r *ClusterUpgrader) ResumeClusterUpgrades(ctx context.Context) error {
	names, err := r.resources.ListActiveUpgrades(ctx)
	if err != nil {
		return err
	}
	for _, name := range names {
		state, err := r.resources.GetUpgradeContext(ctx, name)
		if errors.Is(err, strategies.ErrClusterNotFound) {
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Error("resume cluster upgrade", "cluster", name, "error", err)
			continue
		}
		if state.Operation != nil && state.Operation.Locked {
			if err := r.startUpgradeWorkerIfAbsent(name, state.Operation.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *ClusterUpgrader) upgradeControlPlane(ctx context.Context, key upgradeKey) error {
	return r.retryUpgradeWrite(ctx, key, func(state strategies.UpgradeContext) (bool, error) {
		cp, err := r.resources.GetUpgradeControlPlane(ctx, state.Cluster)
		if err != nil {
			return false, err
		}
		if err := validateControlPlaneNotPausedOrDeleting(cp); err != nil {
			return false, err
		}
		op := *state.Operation
		if err := r.resources.ApplyControlPlaneUpgrade(ctx, state.Cluster, op.Version, op.ControlPlane); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (r *ClusterUpgrader) waitForControlPlane(ctx context.Context, key upgradeKey) error {
	return r.retryWhileUpgradeActive(ctx, key, func(state strategies.UpgradeContext) (bool, error) {
		return r.controlPlaneReady(ctx, state)
	})
}

func (r *ClusterUpgrader) upgradeWorkers(ctx context.Context, key upgradeKey) error {
	return r.retryUpgradeWrite(ctx, key, func(state strategies.UpgradeContext) (bool, error) {
		ready, err := r.controlPlaneReady(ctx, state)
		if err != nil || !ready {
			return false, err
		}
		workers, err := r.resources.GetUpgradeWorkers(ctx, state.Cluster)
		if err != nil {
			return false, err
		}
		if err := validateWorkersAllowRollingUpgrade(workers); err != nil {
			return false, err
		}
		op := *state.Operation
		if err := r.resources.ApplyWorkerUpgrade(ctx, state.Cluster, op.ID, op.Version, op.Workers); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (r *ClusterUpgrader) waitForWorkers(ctx context.Context, key upgradeKey) error {
	return r.retryWhileUpgradeActive(ctx, key, func(state strategies.UpgradeContext) (bool, error) {
		return r.workersReady(ctx, state)
	})
}

func (r *ClusterUpgrader) waitForMachines(ctx context.Context, key upgradeKey) error {
	return r.retryWhileUpgradeActive(ctx, key, func(state strategies.UpgradeContext) (bool, error) {
		return r.machinesReady(ctx, state)
	})
}

func (r *ClusterUpgrader) completeUpgrade(ctx context.Context, key upgradeKey) error {
	return r.retryUpgradeWrite(ctx, key, func(state strategies.UpgradeContext) (bool, error) {
		// Recheck convergence with the latest persistence token before unlocking.
		ready, err := r.machinesReady(ctx, state)
		if err != nil || !ready {
			return false, err
		}
		op := *state.Operation
		op.Locked, op.CompletedAt = false, time.Now().UTC().Format(time.RFC3339)
		op.Message = "Upgrade completed"
		return true, r.saveUpgradeOperationIfChanged(ctx, state, op)
	})
}

func (r *ClusterUpgrader) controlPlaneReady(ctx context.Context, state strategies.UpgradeContext) (bool, error) {
	op := state.Operation
	return r.resources.IsControlPlaneUpgradeReady(ctx, state.Cluster, op.Version, op.ControlPlane)
}

func (r *ClusterUpgrader) workersReady(ctx context.Context, state strategies.UpgradeContext) (bool, error) {
	ready, err := r.controlPlaneReady(ctx, state)
	if err != nil || !ready {
		return false, err
	}
	workers, err := r.resources.GetUpgradeWorkers(ctx, state.Cluster)
	if err != nil {
		return false, err
	}
	if err := validateWorkersAllowRollingUpgrade(workers); err != nil {
		return false, err
	}
	op := state.Operation
	return r.resources.IsWorkerUpgradeReady(ctx, state.Cluster, op.ID, op.Version, op.Workers)
}

func (r *ClusterUpgrader) machinesReady(ctx context.Context, state strategies.UpgradeContext) (bool, error) {
	ready, err := r.workersReady(ctx, state)
	if err != nil || !ready {
		return false, err
	}
	machines, err := r.resources.GetUpgradeMachines(ctx, state.Cluster)
	if err != nil {
		return false, err
	}
	if err := validateMachinesReadyAtVersion(machines, state.Operation.Version); err != nil {
		return false, err
	}
	return true, nil
}

// retryUpgradeWrite persists diagnostics only for failed writes. Read-only waits
// report errors in logs and leave the operation untouched.
func (r *ClusterUpgrader) retryUpgradeWrite(ctx context.Context, key upgradeKey, action func(strategies.UpgradeContext) (bool, error)) error {
	return r.retryWhileUpgradeActive(ctx, key, func(state strategies.UpgradeContext) (bool, error) {
		ready, err := action(state)
		if err == nil || errors.Is(err, strategies.ErrUpgradeStateChanged) || ctx.Err() != nil {
			return ready, err
		}
		op := *state.Operation
		op.Message = "Upgrade write failed; lock retained. Inspect service logs and CAPI conditions."
		if errors.Is(err, strategies.ErrClusterUpgradeConflict) {
			op.Message = err.Error()
		}
		return false, errors.Join(err, r.saveUpgradeOperationIfChanged(ctx, state, op))
	})
}

// retryWhileUpgradeActive retries a phase while the same persisted operation remains active.
func (r *ClusterUpgrader) retryWhileUpgradeActive(ctx context.Context, key upgradeKey, action func(strategies.UpgradeContext) (bool, error)) error {
	for ctx.Err() == nil {
		state, err := r.resources.GetUpgradeContext(ctx, key.clusterName)
		if errors.Is(err, strategies.ErrClusterNotFound) {
			return errUpgradeFinished
		}
		if err == nil {
			if state.Operation == nil || !state.Operation.Locked || state.Operation.ID != key.operationID {
				return errUpgradeFinished
			}
			switch {
			case state.Paused || state.Deleting:
				err = upgradeConflict("cluster is paused or deleting")
			case state.TopologyManaged:
				err = upgradeConflict("topology-managed clusters are not supported")
			default:
				var ready bool
				ready, err = action(state)
				if err == nil && ready {
					return nil
				}
			}
		}
		if err != nil && ctx.Err() == nil {
			slog.Error("execute cluster upgrade", "cluster", key.clusterName, "operation", key.operationID, "error", err)
		}
		timer := time.NewTimer(r.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ctx.Err()
}

func (r *ClusterUpgrader) saveUpgradeOperationIfChanged(ctx context.Context, state strategies.UpgradeContext, op strategies.UpgradeOperation) error {
	// Only completion and diagnostics change; targets and identity stay fixed.
	if previous := state.Operation; previous != nil &&
		previous.Locked == op.Locked && previous.CompletedAt == op.CompletedAt && previous.Message == op.Message {
		return nil
	}
	return r.resources.SaveUpgrade(ctx, state.Cluster, op)
}
