package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"pacp/internal/contracts"
)

var (
	ErrNotFound            = errors.New("job not found")
	ErrValidation          = errors.New("validation failed")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
	ErrWorkerMismatch      = errors.New("worker mismatch")
	ErrClaimConflict       = errors.New("job is claimed by another worker")
	ErrClaimExpired        = errors.New("job claim expired")
	ErrInvalidTransition   = errors.New("invalid job state transition")
	ErrTerminalState       = errors.New("job is terminal")
	ErrInvalidCursor       = errors.New("invalid cursor")
)

type Store struct {
	mu          sync.RWMutex
	now         func() time.Time
	nextID      int
	jobs        map[string]*record
	idempotency map[string]idempotentCreate
}

type record struct {
	job            contracts.Job
	requesterID    string
	ownerSubjectID string
	claimLease     time.Duration
	logs           []contracts.JobLogEntry
}

type idempotentCreate struct {
	fingerprint string
	jobID       string
}

type ListFilter struct {
	State        contracts.JobState
	CapabilityID string
	Cursor       string
	Limit        int
}

func NewStore() *Store {
	return &Store{
		now:         time.Now,
		nextID:      1,
		jobs:        map[string]*record{},
		idempotency: map[string]idempotentCreate{},
	}
}

func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

func (s *Store) Create(req contracts.CreateJobRequest, idempotencyKey string) (contracts.Job, bool, error) {
	if req.RequesterID == "" {
		return contracts.Job{}, false, fmt.Errorf("%w: requester_id is required", ErrValidation)
	}
	fingerprint, err := fingerprint(req)
	if err != nil {
		return contracts.Job{}, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if idempotencyKey != "" {
		if existing, ok := s.idempotency[idempotencyKey]; ok {
			if existing.fingerprint != fingerprint {
				return contracts.Job{}, false, ErrIdempotencyConflict
			}
			return cloneJob(s.jobs[existing.jobID].job), false, nil
		}
	}

	now := s.formatNow()
	jobID := fmt.Sprintf("job_%06d", s.nextID)
	s.nextID++
	job := contracts.Job{
		JobID:         jobID,
		State:         contracts.JobQueued,
		CreatedAt:     now,
		UpdatedAt:     now,
		InputSummary:  req.InputSummary,
		Metadata:      req.Metadata,
		Claim:         nil,
		ArtifactRefs:  []string{},
		LogCursor:     nil,
		TerminalError: nil,
		Links:         jobLinks(jobID),
	}
	rec := &record{
		job:            job,
		requesterID:    req.RequesterID,
		ownerSubjectID: ownerSubjectID(req),
		claimLease:     time.Minute,
	}
	s.jobs[jobID] = rec
	if idempotencyKey != "" {
		s.idempotency[idempotencyKey] = idempotentCreate{fingerprint: fingerprint, jobID: jobID}
	}
	return cloneJob(job), true, nil
}

func (s *Store) List(filter ListFilter) ([]contracts.Job, error) {
	if filter.Cursor != "" {
		return nil, ErrInvalidCursor
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.jobs))
	for id := range s.jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	jobs := make([]contracts.Job, 0, len(ids))
	for _, id := range ids {
		rec := s.jobs[id]
		if filter.State != "" && rec.job.State != filter.State {
			continue
		}
		if filter.CapabilityID != "" && capabilityID(rec.job.Metadata) != filter.CapabilityID {
			continue
		}
		jobs = append(jobs, cloneJob(rec.job))
	}
	if filter.Limit > 0 && len(jobs) > filter.Limit {
		jobs = jobs[:filter.Limit]
	}
	return jobs, nil
}

func (s *Store) Get(jobID string) (contracts.Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.jobs[jobID]
	if !ok {
		return contracts.Job{}, ErrNotFound
	}
	return cloneJob(rec.job), nil
}

func (s *Store) PolicyContext(jobID string) (contracts.JobPolicyContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.jobs[jobID]
	if !ok {
		return contracts.JobPolicyContext{}, ErrNotFound
	}
	return contracts.JobPolicyContext{
		ResourceKind:   "job",
		JobID:          jobID,
		OwnerSubjectID: rec.ownerSubjectID,
		RequesterID:    rec.requesterID,
		JobState:       string(rec.job.State),
		PolicyState:    policyState(rec.job.State),
	}, nil
}

func (s *Store) AgentProjection(jobID string) (contracts.AgentJob, error) {
	job, err := s.Get(jobID)
	if err != nil {
		return contracts.AgentJob{}, err
	}
	return agentProjection(job), nil
}

func (s *Store) Claim(jobID string, req contracts.JobClaimRequest) (contracts.Job, error) {
	if req.WorkerID == "" {
		return contracts.Job{}, fmt.Errorf("%w: worker_id is required", ErrValidation)
	}
	if req.LeaseSeconds <= 0 {
		req.LeaseSeconds = 60
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.jobs[jobID]
	if !ok {
		return contracts.Job{}, ErrNotFound
	}
	if isTerminal(rec.job.State) {
		return contracts.Job{}, ErrTerminalState
	}
	if rec.job.Claim != nil && !s.claimExpired(rec.job.Claim) {
		if rec.job.Claim.WorkerID != req.WorkerID {
			return contracts.Job{}, ErrClaimConflict
		}
		return cloneJob(rec.job), nil
	}

	now := s.now().UTC()
	rec.claimLease = time.Duration(req.LeaseSeconds) * time.Second
	rec.job.State = contracts.JobClaimed
	rec.job.UpdatedAt = formatTime(now)
	rec.job.Claim = &contracts.JobClaim{
		WorkerID:  req.WorkerID,
		ClaimedAt: formatTime(now),
		ExpiresAt: formatTime(now.Add(rec.claimLease)),
	}
	return cloneJob(rec.job), nil
}

func (s *Store) Heartbeat(jobID string, req contracts.JobHeartbeatRequest) (contracts.Job, error) {
	if req.WorkerID == "" {
		return contracts.Job{}, fmt.Errorf("%w: worker_id is required", ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.jobs[jobID]
	if !ok {
		return contracts.Job{}, ErrNotFound
	}
	if err := s.requireActiveClaim(rec, req.WorkerID); err != nil {
		return contracts.Job{}, err
	}
	if req.TransitionTo != "" {
		if req.TransitionTo != string(contracts.JobRunning) || rec.job.State != contracts.JobClaimed {
			return contracts.Job{}, ErrInvalidTransition
		}
		rec.job.State = contracts.JobRunning
	}
	if req.StatusMessage != "" {
		rec.job.StatusMessage = req.StatusMessage
	}
	s.refreshClaim(rec)
	return cloneJob(rec.job), nil
}

func (s *Store) Complete(jobID string, req contracts.JobCompleteRequest) (contracts.Job, error) {
	if req.WorkerID == "" {
		return contracts.Job{}, fmt.Errorf("%w: worker_id is required", ErrValidation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.jobs[jobID]
	if !ok {
		return contracts.Job{}, ErrNotFound
	}
	if isTerminal(rec.job.State) {
		return contracts.Job{}, ErrTerminalState
	}
	if err := s.requireActiveClaim(rec, req.WorkerID); err != nil {
		return contracts.Job{}, err
	}
	if rec.job.State != contracts.JobRunning {
		return contracts.Job{}, ErrInvalidTransition
	}
	rec.job.State = contracts.JobSucceeded
	rec.job.UpdatedAt = s.formatNow()
	rec.job.ArtifactRefs = artifactRefs(req)
	rec.job.TerminalError = nil
	rec.job.Claim = nil
	rec.job.StatusMessage = "completed"
	return cloneJob(rec.job), nil
}

func (s *Store) Fail(jobID string, req contracts.JobFailRequest) (contracts.Job, error) {
	if req.WorkerID == "" {
		return contracts.Job{}, fmt.Errorf("%w: worker_id is required", ErrValidation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.jobs[jobID]
	if !ok {
		return contracts.Job{}, ErrNotFound
	}
	if isTerminal(rec.job.State) {
		return contracts.Job{}, ErrTerminalState
	}
	if err := s.requireActiveClaim(rec, req.WorkerID); err != nil {
		return contracts.Job{}, err
	}
	rec.job.State = contracts.JobFailed
	rec.job.UpdatedAt = s.formatNow()
	rec.job.TerminalError = &req.Error
	rec.job.Claim = nil
	rec.job.StatusMessage = req.Error.Message
	return cloneJob(rec.job), nil
}

func (s *Store) Cancel(jobID string, req contracts.CancelRequest) (contracts.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.jobs[jobID]
	if !ok {
		return contracts.Job{}, ErrNotFound
	}
	if isTerminal(rec.job.State) {
		return cloneJob(rec.job), nil
	}
	if rec.job.State == contracts.JobRunning {
		return contracts.Job{}, ErrInvalidTransition
	}
	rec.job.State = contracts.JobCanceled
	rec.job.UpdatedAt = s.formatNow()
	message := req.Reason
	if message == "" {
		message = "canceled"
	}
	rec.job.StatusMessage = message
	rec.job.TerminalError = &contracts.ErrorObject{Code: "canceled", Message: message, Retryable: false}
	rec.job.Claim = nil
	return cloneJob(rec.job), nil
}

func (s *Store) AppendLogs(jobID string, req contracts.AppendJobLogRequest) ([]contracts.JobLogEntry, *string, error) {
	if req.WorkerID == "" {
		return nil, nil, fmt.Errorf("%w: worker_id is required", ErrValidation)
	}
	if len(req.Entries) == 0 {
		return nil, nil, fmt.Errorf("%w: entries are required", ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.jobs[jobID]
	if !ok {
		return nil, nil, ErrNotFound
	}
	if err := s.requireActiveClaim(rec, req.WorkerID); err != nil {
		return nil, nil, err
	}
	rec.logs = append(rec.logs, req.Entries...)
	cursor := logCursor(len(rec.logs))
	rec.job.LogCursor = &cursor
	rec.job.UpdatedAt = s.formatNow()
	return append([]contracts.JobLogEntry(nil), req.Entries...), &cursor, nil
}

func (s *Store) Logs(jobID, cursor string, limit int) ([]contracts.JobLogEntry, *string, error) {
	if limit <= 0 {
		limit = 100
	}
	start, err := parseCursor(cursor)
	if err != nil {
		return nil, nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.jobs[jobID]
	if !ok {
		return nil, nil, ErrNotFound
	}
	if start > len(rec.logs) {
		return nil, nil, ErrInvalidCursor
	}
	end := start + limit
	if end > len(rec.logs) {
		end = len(rec.logs)
	}
	items := append([]contracts.JobLogEntry(nil), rec.logs[start:end]...)
	var next *string
	if end < len(rec.logs) {
		value := logCursor(end)
		next = &value
	}
	return items, next, nil
}

func (s *Store) requireActiveClaim(rec *record, workerID string) error {
	if rec.job.Claim == nil {
		return ErrInvalidTransition
	}
	if rec.job.Claim.WorkerID != workerID {
		return ErrWorkerMismatch
	}
	if s.claimExpired(rec.job.Claim) {
		return ErrClaimExpired
	}
	return nil
}

func (s *Store) refreshClaim(rec *record) {
	now := s.now().UTC()
	rec.job.UpdatedAt = formatTime(now)
	if rec.job.Claim != nil {
		rec.job.Claim.ExpiresAt = formatTime(now.Add(rec.claimLease))
	}
}

func (s *Store) claimExpired(claim *contracts.JobClaim) bool {
	expires, err := time.Parse(time.RFC3339, claim.ExpiresAt)
	if err != nil {
		return true
	}
	return !s.now().UTC().Before(expires)
}

func (s *Store) formatNow() string {
	return formatTime(s.now().UTC())
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func isTerminal(state contracts.JobState) bool {
	return state == contracts.JobSucceeded || state == contracts.JobFailed || state == contracts.JobCanceled || state == contracts.JobExpired
}

func policyState(state contracts.JobState) string {
	switch state {
	case contracts.JobCanceled:
		return "canceled"
	case contracts.JobExpired:
		return "expired"
	case contracts.JobSucceeded, contracts.JobFailed:
		return "terminal"
	default:
		return "active"
	}
}

func ownerSubjectID(req contracts.CreateJobRequest) string {
	if plan, ok := req.Metadata["execution_plan"].(map[string]any); ok {
		if subject, ok := plan["subject_id"].(string); ok && subject != "" {
			return subject
		}
	}
	if req.RequesterID != "" {
		return req.RequesterID
	}
	return "sub_unknown"
}

func capabilityID(metadata map[string]any) string {
	if plan, ok := metadata["execution_plan"].(map[string]any); ok {
		if id, ok := plan["capability_id"].(string); ok {
			return id
		}
	}
	return ""
}

func artifactRefs(req contracts.JobCompleteRequest) []string {
	if len(req.ArtifactRefs) > 0 {
		return append([]string(nil), req.ArtifactRefs...)
	}
	if raw, ok := req.Output["artifact_refs"].([]any); ok {
		refs := make([]string, 0, len(raw))
		for _, item := range raw {
			if ref, ok := item.(string); ok {
				refs = append(refs, ref)
			}
		}
		return refs
	}
	return []string{}
}

func jobLinks(jobID string) map[string]any {
	return map[string]any{
		"self": map[string]any{"method": "GET", "href": "/v1/jobs/" + jobID},
		"logs": map[string]any{"method": "GET", "href": "/v1/jobs/" + jobID + "/logs"},
	}
}

func agentProjection(job contracts.Job) contracts.AgentJob {
	return contracts.AgentJob{
		JobID:         job.JobID,
		State:         job.State,
		CreatedAt:     job.CreatedAt,
		UpdatedAt:     job.UpdatedAt,
		StatusMessage: job.StatusMessage,
		InputSummary:  job.InputSummary,
		ArtifactRefs:  job.ArtifactRefs,
		LogCursor:     job.LogCursor,
		TerminalError: job.TerminalError,
		Links:         job.Links,
	}
}

func cloneJob(job contracts.Job) contracts.Job {
	raw, _ := json.Marshal(job)
	var cloned contracts.Job
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func fingerprint(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func logCursor(index int) string {
	return fmt.Sprintf("cursor_jobs_logs_%06d", index)
}

func parseCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	var index int
	if _, err := fmt.Sscanf(cursor, "cursor_jobs_logs_%06d", &index); err != nil {
		return 0, ErrInvalidCursor
	}
	return index, nil
}
