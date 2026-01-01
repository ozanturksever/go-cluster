package cluster

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// PlacementConstraint represents a placement constraint for an app.
type PlacementConstraint struct {
	Type     ConstraintType
	Key      string
	Value    string
	Weight   int  // For soft constraints (higher = more preferred)
	Required bool // Hard constraint if true, soft if false
}

// ConstraintType defines the type of placement constraint.
type ConstraintType int

const (
	// ConstraintLabel requires node to have a specific label
	ConstraintLabel ConstraintType = iota
	// ConstraintAvoidLabel requires node to NOT have a specific label
	ConstraintAvoidLabel
	// ConstraintPreferLabel prefers nodes with a specific label (soft)
	ConstraintPreferLabel
	// ConstraintNode requires specific node(s)
	ConstraintNode
	// ConstraintWithApp requires co-location with another app
	ConstraintWithApp
	// ConstraintAwayFromApp requires anti-affinity with another app
	ConstraintAwayFromApp
)

// PlacementResult represents the result of a placement evaluation.
type PlacementResult struct {
	NodeID     string
	Score      int
	Satisfied  bool
	Violations []string
}

// MoveRequest represents a request to move an app instance.
type MoveRequest struct {
	App      string
	FromNode string
	ToNode   string // Optional - if empty, scheduler chooses best target
	Reason   string
	Force    bool // Force migration even if constraints not fully satisfied
}

// DrainOptions configures node draining behavior.
type DrainOptions struct {
	Timeout     time.Duration
	Force       bool
	DeleteLocal bool // Delete local data after drain
	ExcludeApps []string
}

// MigrationStatus represents the status of a migration.
type MigrationStatus string

const (
	MigrationPending    MigrationStatus = "pending"
	MigrationInProgress MigrationStatus = "in_progress"
	MigrationCompleted  MigrationStatus = "completed"
	MigrationFailed     MigrationStatus = "failed"
	MigrationRejected   MigrationStatus = "rejected"
)

// Migration represents an app migration.
type Migration struct {
	ID        string
	App       string
	FromNode  string
	ToNode    string
	Status    MigrationStatus
	Reason    string
	StartedAt time.Time
	EndedAt   time.Time
	Error     string
}

// MigrationPolicy configures migration safety guardrails.
type MigrationPolicy struct {
	CooldownPeriod     time.Duration
	MaxConcurrent      int
	RequireApproval    bool
	MaintenanceWindows []TimeWindow
	LoadThreshold      float64
}

// TimeWindow represents a time window for maintenance.
type TimeWindow struct {
	Start    string // HH:MM format
	End      string // HH:MM format
	Timezone string
	Days     []time.Weekday
}

// DefaultMigrationPolicy returns a sensible default migration policy.
func DefaultMigrationPolicy() MigrationPolicy {
	return MigrationPolicy{
		CooldownPeriod: 5 * time.Minute,
		MaxConcurrent:  2,
		LoadThreshold:  0.8,
	}
}

// Scheduler handles app placement decisions.
type Scheduler struct {
	platform *Platform
	policy   MigrationPolicy

	// Migration tracking
	mu                sync.RWMutex
	activeMigrations  map[string]*Migration // migrationID -> migration
	lastMigration     map[string]time.Time  // appName -> last migration time
	labelWatchers     []LabelWatcherFunc

	// Cordon state
	cordonedNodes map[string]bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// LabelWatcherFunc is called when node labels change.
type LabelWatcherFunc func(nodeID string, labels map[string]string)

// NewScheduler creates a new scheduler for the platform.
func NewScheduler(platform *Platform, policy MigrationPolicy) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		platform:         platform,
		policy:           policy,
		activeMigrations: make(map[string]*Migration),
		lastMigration:    make(map[string]time.Time),
		cordonedNodes:    make(map[string]bool),
		ctx:              ctx,
		cancel:           cancel,
	}
}

// Start starts the scheduler.
func (s *Scheduler) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	// Start watching for label changes
	s.wg.Add(1)
	go s.watchLabelChanges()

	return nil
}

// Stop stops the scheduler.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

// EvaluatePlacement evaluates placement for an app on all available nodes.
func (s *Scheduler) EvaluatePlacement(app *App) []PlacementResult {
	members := s.platform.membership.Members()
	results := make([]PlacementResult, 0, len(members))

	for _, member := range members {
		result := s.evaluateNode(app, member)
		results = append(results, result)
	}

	return results
}

// FindBestNode finds the best node for an app based on constraints.
func (s *Scheduler) FindBestNode(app *App, excludeNodes ...string) (*PlacementResult, error) {
	excludeSet := make(map[string]bool)
	for _, n := range excludeNodes {
		excludeSet[n] = true
	}

	results := s.EvaluatePlacement(app)

	var best *PlacementResult
	for i := range results {
		r := &results[i]

		// Skip excluded nodes
		if excludeSet[r.NodeID] {
			continue
		}

		// Skip cordoned nodes
		if s.IsNodeCordoned(r.NodeID) {
			continue
		}

		// Skip nodes that don't satisfy hard constraints
		if !r.Satisfied {
			continue
		}

		// Select highest score
		if best == nil || r.Score > best.Score {
			best = r
		}
	}

	if best == nil {
		return nil, fmt.Errorf("no eligible nodes found for app %s", app.name)
	}

	return best, nil
}

// evaluateNode evaluates placement constraints for an app on a specific node.
func (s *Scheduler) evaluateNode(app *App, member Member) PlacementResult {
	result := PlacementResult{
		NodeID:    member.NodeID,
		Score:     100, // Base score
		Satisfied: true,
	}

	// Check node health
	if !member.IsHealthy {
		result.Satisfied = false
		result.Violations = append(result.Violations, "node is unhealthy")
		return result
	}

	// Check node selector (specific nodes)
	if len(app.nodeSelector) > 0 {
		found := false
		for _, n := range app.nodeSelector {
			if n == member.NodeID {
				found = true
				break
			}
		}
		if !found {
			result.Satisfied = false
			result.Violations = append(result.Violations, "node not in allowed list")
		}
	}

	// Check label selectors (hard constraints)
	for key, value := range app.labelSelector {
		if member.Labels == nil || member.Labels[key] != value {
			result.Satisfied = false
			result.Violations = append(result.Violations,
				fmt.Sprintf("missing required label %s=%s", key, value))
		}
	}

	// Check avoid labels (hard constraints)
	for key, value := range app.avoidLabels {
		if member.Labels != nil && member.Labels[key] == value {
			result.Satisfied = false
			result.Violations = append(result.Violations,
				fmt.Sprintf("node has avoided label %s=%s", key, value))
		}
	}

	// Check prefer labels (soft constraints - affect score)
	for _, pref := range app.preferLabels {
		if member.Labels != nil && member.Labels[pref.Key] == pref.Value {
			result.Score += pref.Weight
		}
	}

	// Check app affinity (with apps)
	for _, requiredApp := range app.withApps {
		hasApp := false
		for _, a := range member.Apps {
			if a == requiredApp {
				hasApp = true
				break
			}
		}
		if !hasApp {
			result.Satisfied = false
			result.Violations = append(result.Violations,
				fmt.Sprintf("required co-located app %s not present", requiredApp))
		}
	}

	// Check app anti-affinity (away from apps)
	for _, avoidApp := range app.awayFromApps {
		for _, a := range member.Apps {
			if a == avoidApp {
				result.Satisfied = false
				result.Violations = append(result.Violations,
					fmt.Sprintf("anti-affinity app %s is present", avoidApp))
				break
			}
		}
	}

	return result
}

// ValidatePlacement checks if an app's current placement is valid.
func (s *Scheduler) ValidatePlacement(app *App) (bool, []string) {
	member, ok := s.platform.membership.GetMember(s.platform.nodeID)
	if !ok {
		return false, []string{"current node not found in membership"}
	}

	result := s.evaluateNode(app, *member)
	return result.Satisfied, result.Violations
}

// MoveApp initiates a migration of an app from one node to another.
func (s *Scheduler) MoveApp(ctx context.Context, req MoveRequest) (*Migration, error) {
	s.mu.Lock()

	// Check cooldown
	if lastTime, ok := s.lastMigration[req.App]; ok {
		if time.Since(lastTime) < s.policy.CooldownPeriod {
			s.mu.Unlock()
			return nil, fmt.Errorf("migration cooldown not expired for app %s (wait %v)",
				req.App, s.policy.CooldownPeriod-time.Since(lastTime))
		}
	}

	// Check concurrent migrations
	activeCnt := 0
	for _, m := range s.activeMigrations {
		if m.Status == MigrationInProgress || m.Status == MigrationPending {
			activeCnt++
		}
	}
	if activeCnt >= s.policy.MaxConcurrent {
		s.mu.Unlock()
		return nil, fmt.Errorf("max concurrent migrations reached (%d)", s.policy.MaxConcurrent)
	}

	// Create migration record
	migration := &Migration{
		ID:        fmt.Sprintf("%s-%d", req.App, time.Now().UnixNano()),
		App:       req.App,
		FromNode:  req.FromNode,
		ToNode:    req.ToNode,
		Status:    MigrationPending,
		Reason:    req.Reason,
		StartedAt: time.Now(),
	}

	s.activeMigrations[migration.ID] = migration
	s.mu.Unlock()

	// Find target node if not specified
	if req.ToNode == "" {
		s.platform.appsMu.RLock()
		app, ok := s.platform.apps[req.App]
		s.platform.appsMu.RUnlock()

		if !ok {
			s.failMigration(migration, "app not found")
			return migration, fmt.Errorf("app %s not found", req.App)
		}

		best, err := s.FindBestNode(app, req.FromNode)
		if err != nil {
			s.failMigration(migration, err.Error())
			return migration, err
		}
		migration.ToNode = best.NodeID
	}

	// Start migration in background
	go s.executeMigration(ctx, migration)

	return migration, nil
}

// executeMigration performs the actual migration.
func (s *Scheduler) executeMigration(ctx context.Context, migration *Migration) {
	s.mu.Lock()
	migration.Status = MigrationInProgress
	s.mu.Unlock()

	// Track in-progress migration
	s.platform.metrics.SetMigrationInProgress(migration.App, 1)

	// Log migration start
	s.platform.audit.Log(ctx, AuditEntry{
		Category: "placement",
		Action:   "migration.started",
		Data: map[string]any{
			"app":       migration.App,
			"from_node": migration.FromNode,
			"to_node":   migration.ToNode,
			"reason":    migration.Reason,
		},
	})

	// Get the app
	s.platform.appsMu.RLock()
	app, ok := s.platform.apps[migration.App]
	s.platform.appsMu.RUnlock()

	if !ok {
		s.failMigration(migration, "app not found")
		return
	}

	// Call OnMigrationStart hook (can reject)
	if app.onMigrationStart != nil {
		if err := app.onMigrationStart(ctx, migration.FromNode, migration.ToNode); err != nil {
			s.mu.Lock()
			migration.Status = MigrationRejected
			migration.Error = err.Error()
			migration.EndedAt = time.Now()
			s.mu.Unlock()

			// Clear in-progress metric on rejection
			s.platform.metrics.SetMigrationInProgress(migration.App, 0)

			s.platform.audit.Log(ctx, AuditEntry{
				Category: "placement",
				Action:   "migration.rejected",
				Data: map[string]any{
					"app":    migration.App,
					"reason": err.Error(),
				},
			})
			return
		}
	}

	// For singleton apps, the migration is handled by stepping down leadership
	// and letting the target node take over via election
	if app.mode == ModeSingleton && app.election != nil {
		// Only the current leader can initiate stepdown
		if app.election.IsLeader() && migration.FromNode == s.platform.nodeID {
			app.election.StepDown(ctx)
		}
	}

	// Mark migration as complete
	s.mu.Lock()
	migration.Status = MigrationCompleted
	migration.EndedAt = time.Now()
	s.lastMigration[migration.App] = time.Now()
	s.mu.Unlock()

	// Clear in-progress and record duration
	s.platform.metrics.SetMigrationInProgress(migration.App, 0)
	s.platform.metrics.ObserveMigrationDuration(migration.App, migration.EndedAt.Sub(migration.StartedAt))

	// Call OnMigrationComplete hook
	if app.onMigrationComplete != nil {
		app.onMigrationComplete(ctx, migration.FromNode, migration.ToNode)
	}

	// Log migration complete
	s.platform.audit.Log(ctx, AuditEntry{
		Category: "placement",
		Action:   "migration.completed",
		Data: map[string]any{
			"app":       migration.App,
			"from_node": migration.FromNode,
			"to_node":   migration.ToNode,
			"duration":  migration.EndedAt.Sub(migration.StartedAt).String(),
		},
	})

	// Update metrics
	s.platform.metrics.IncMigration(migration.App, migration.Reason, "success")
}

// failMigration marks a migration as failed.
func (s *Scheduler) failMigration(migration *Migration, reason string) {
	s.mu.Lock()
	migration.Status = MigrationFailed
	migration.Error = reason
	migration.EndedAt = time.Now()
	s.mu.Unlock()

	// Clear in-progress metric
	s.platform.metrics.SetMigrationInProgress(migration.App, 0)

	s.platform.audit.Log(s.ctx, AuditEntry{
		Category: "placement",
		Action:   "migration.failed",
		Data: map[string]any{
			"app":    migration.App,
			"reason": reason,
		},
	})

	s.platform.metrics.IncMigration(migration.App, migration.Reason, "failed")
}

// DrainNode moves all apps away from a node.
func (s *Scheduler) DrainNode(ctx context.Context, nodeID string, opts DrainOptions) error {
	// Mark node as cordoned to prevent new placements
	s.CordonNode(nodeID)

	s.platform.audit.Log(ctx, AuditEntry{
		Category: "placement",
		Action:   "node.drain.started",
		Data:     map[string]any{"node_id": nodeID},
	})

	// Get all apps on this node
	member, ok := s.platform.membership.GetMember(nodeID)
	if !ok {
		return fmt.Errorf("node %s not found", nodeID)
	}

	// Exclude apps from drain
	excludeSet := make(map[string]bool)
	for _, app := range opts.ExcludeApps {
		excludeSet[app] = true
	}

	// Create timeout context
	drainCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		drainCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// Move each app
	var lastErr error
	for _, appName := range member.Apps {
		if excludeSet[appName] {
			continue
		}

		_, err := s.MoveApp(drainCtx, MoveRequest{
			App:      appName,
			FromNode: nodeID,
			Reason:   "node_drain",
			Force:    opts.Force,
		})
		if err != nil {
			lastErr = err
			if !opts.Force {
				return fmt.Errorf("failed to move app %s: %w", appName, err)
			}
		}
	}

	s.platform.audit.Log(ctx, AuditEntry{
		Category: "placement",
		Action:   "node.drain.completed",
		Data:     map[string]any{"node_id": nodeID},
	})

	return lastErr
}

// RebalanceApp redistributes an app's instances across the cluster.
func (s *Scheduler) RebalanceApp(ctx context.Context, appName string) error {
	s.platform.appsMu.RLock()
	app, ok := s.platform.apps[appName]
	s.platform.appsMu.RUnlock()

	if !ok {
		return fmt.Errorf("app %s not found", appName)
	}

	s.platform.audit.Log(ctx, AuditEntry{
		Category: "placement",
		Action:   "rebalance.triggered",
		Data:     map[string]any{"app": appName},
	})

	// For ring apps, trigger ring rebalance
	if app.ring != nil {
		app.ring.ForceRebalance()
		return nil
	}

	// For singleton apps, evaluate if current placement is optimal
	if app.mode == ModeSingleton {
		valid, violations := s.ValidatePlacement(app)
		if !valid {
			// Current placement is invalid, trigger migration
			_, err := s.MoveApp(ctx, MoveRequest{
				App:      appName,
				FromNode: s.platform.nodeID,
				Reason:   fmt.Sprintf("constraint_violation: %v", violations),
			})
			return err
		}
	}

	return nil
}

// CordonNode marks a node as unschedulable.
func (s *Scheduler) CordonNode(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cordonedNodes[nodeID] = true

	s.platform.audit.Log(s.ctx, AuditEntry{
		Category: "placement",
		Action:   "node.cordoned",
		Data:     map[string]any{"node_id": nodeID},
	})
}

// UncordonNode marks a node as schedulable.
func (s *Scheduler) UncordonNode(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cordonedNodes, nodeID)

	s.platform.audit.Log(s.ctx, AuditEntry{
		Category: "placement",
		Action:   "node.uncordoned",
		Data:     map[string]any{"node_id": nodeID},
	})
}

// IsNodeCordoned returns true if the node is cordoned.
func (s *Scheduler) IsNodeCordoned(nodeID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cordonedNodes[nodeID]
}

// WatchLabels registers a callback for label changes.
func (s *Scheduler) WatchLabels(fn LabelWatcherFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.labelWatchers = append(s.labelWatchers, fn)
}

// notifyLabelChange notifies all watchers of a label change.
func (s *Scheduler) notifyLabelChange(nodeID string, labels map[string]string) {
	s.mu.RLock()
	watchers := make([]LabelWatcherFunc, len(s.labelWatchers))
	copy(watchers, s.labelWatchers)
	s.mu.RUnlock()

	for _, fn := range watchers {
		go fn(nodeID, labels)
	}

	s.platform.audit.Log(s.ctx, AuditEntry{
		Category: "placement",
		Action:   "node.labels.updated",
		Data:     map[string]any{"node_id": nodeID, "labels": labels},
	})
}

// watchLabelChanges watches for membership changes that include label updates.
func (s *Scheduler) watchLabelChanges() {
	defer s.wg.Done()

	// Store previous labels for comparison
	prevLabels := make(map[string]map[string]string)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			members := s.platform.membership.Members()
			for _, member := range members {
				prev, ok := prevLabels[member.NodeID]
				if !ok || !labelsEqual(prev, member.Labels) {
					prevLabels[member.NodeID] = copyLabels(member.Labels)
					if ok { // Only notify on changes, not initial discovery
						s.notifyLabelChange(member.NodeID, member.Labels)
						// Re-evaluate placement for all apps
						s.reevaluateAllPlacements()
					}
				}
			}
		}
	}
}

// reevaluateAllPlacements checks if any apps need to be migrated due to constraint changes.
func (s *Scheduler) reevaluateAllPlacements() {
	s.platform.appsMu.RLock()
	apps := make([]*App, 0, len(s.platform.apps))
	for _, app := range s.platform.apps {
		apps = append(apps, app)
	}
	s.platform.appsMu.RUnlock()

	for _, app := range apps {
		valid, violations := s.ValidatePlacement(app)
		if !valid {
			// Call OnPlacementInvalid hook
			if app.onPlacementInvalid != nil {
				go app.onPlacementInvalid(s.ctx, fmt.Sprintf("%v", violations))
			}

			s.platform.audit.Log(s.ctx, AuditEntry{
				Category: "placement",
				Action:   "constraint.invalid",
				Data: map[string]any{
					"app":        app.name,
					"violations": violations,
				},
			})

			// Increment metrics
			s.platform.metrics.IncPlacementViolation(app.name)
		}
	}
}

// GetMigration returns a migration by ID.
func (s *Scheduler) GetMigration(id string) (*Migration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.activeMigrations[id]
	if !ok {
		return nil, false
	}
	mCopy := *m
	return &mCopy, true
}

// GetActiveMigrations returns all active migrations.
func (s *Scheduler) GetActiveMigrations() []Migration {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Migration, 0)
	for _, m := range s.activeMigrations {
		if m.Status == MigrationPending || m.Status == MigrationInProgress {
			result = append(result, *m)
		}
	}
	return result
}

// Helper functions

func labelsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func copyLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}
	copy := make(map[string]string, len(labels))
	for k, v := range labels {
		copy[k] = v
	}
	return copy
}
