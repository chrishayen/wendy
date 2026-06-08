package leases

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"pacp/internal/contracts"
)

var (
	ErrNotFound            = errors.New("lease resource not found")
	ErrValidation          = errors.New("validation failed")
	ErrResourceConflict    = errors.New("resource conflict")
	ErrResourceUnavailable = errors.New("resource unavailable")
	ErrNoCapacity          = errors.New("resource has no leasing capacity")
	ErrHolderMismatch      = errors.New("holder mismatch")
	ErrLeaseExpired        = errors.New("lease expired")
	ErrInvalidTransition   = errors.New("invalid lease transition")
	ErrMissingIdempotency  = errors.New("missing idempotency key")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
)

type Store struct {
	mu                 sync.RWMutex
	now                func() time.Time
	nextResourceID     int
	nextRequestID      int
	nextLeaseID        int
	nextSequence       int
	resources          map[string]*resourceRecord
	requests           map[string]*leaseRequestRecord
	leases             map[string]*leaseRecord
	queues             map[string][]string
	releaseIdempotency map[string]idempotentRelease
	audit              []contracts.LeaseAuditEvent
	snapshotPath       string
}

type resourceRecord struct {
	resource contracts.ResourceRecord
}

type leaseRequestRecord struct {
	request          contracts.LeaseRequest
	requesterID      string
	priority         int
	sequence         int
	heartbeatTimeout time.Duration
	leaseID          string
}

type leaseState string

const (
	leaseActive   leaseState = "active"
	leaseReleased leaseState = "released"
	leaseExpired  leaseState = "expired"
)

type leaseRecord struct {
	lease     contracts.Lease
	requestID string
	timeout   time.Duration
	state     leaseState
}

type idempotentRelease struct {
	fingerprint string
	leaseID     string
	response    contracts.Lease
}

type snapshotFile struct {
	Version            int                                  `json:"version"`
	NextResourceID     int                                  `json:"next_resource_id"`
	NextRequestID      int                                  `json:"next_request_id"`
	NextLeaseID        int                                  `json:"next_lease_id"`
	NextSequence       int                                  `json:"next_sequence"`
	Resources          map[string]contracts.ResourceRecord  `json:"resources"`
	Requests           map[string]leaseRequestSnapshot      `json:"requests"`
	Leases             map[string]leaseSnapshot             `json:"leases"`
	Queues             map[string][]string                  `json:"queues"`
	ReleaseIdempotency map[string]idempotentReleaseSnapshot `json:"release_idempotency"`
	Audit              []contracts.LeaseAuditEvent          `json:"audit,omitempty"`
}

type leaseRequestSnapshot struct {
	Request                 contracts.LeaseRequest `json:"request"`
	RequesterID             string                 `json:"requester_id"`
	Priority                int                    `json:"priority"`
	Sequence                int                    `json:"sequence"`
	HeartbeatTimeoutSeconds int64                  `json:"heartbeat_timeout_seconds"`
	LeaseID                 string                 `json:"lease_id,omitempty"`
}

type leaseSnapshot struct {
	Lease          contracts.Lease `json:"lease"`
	RequestID      string          `json:"request_id"`
	TimeoutSeconds int64           `json:"timeout_seconds"`
	State          leaseState      `json:"state"`
}

type idempotentReleaseSnapshot struct {
	Fingerprint string          `json:"fingerprint"`
	LeaseID     string          `json:"lease_id"`
	Response    contracts.Lease `json:"response"`
}

func NewPersistentStore(path string) (*Store, error) {
	store := NewStore()
	store.snapshotPath = path
	if path == "" {
		return store, nil
	}
	if err := store.loadSnapshot(path); err != nil {
		return nil, err
	}
	return store, nil
}

func NewStore() *Store {
	return &Store{
		now:                time.Now,
		nextResourceID:     1,
		nextRequestID:      1,
		nextLeaseID:        1,
		nextSequence:       1,
		resources:          map[string]*resourceRecord{},
		requests:           map[string]*leaseRequestRecord{},
		leases:             map[string]*leaseRecord{},
		queues:             map[string][]string{},
		releaseIdempotency: map[string]idempotentRelease{},
	}
}

func (s *Store) HealthDetails() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	activeLeases := 0
	for _, lease := range s.leases {
		if lease.state == leaseActive {
			activeLeases++
		}
	}
	pendingRequests := 0
	grantedRequests := 0
	for _, request := range s.requests {
		switch request.request.State {
		case contracts.LeaseRequestPending:
			pendingRequests++
		case contracts.LeaseRequestGranted:
			grantedRequests++
		}
	}
	queueDepth := 0
	for _, queue := range s.queues {
		queueDepth += len(queue)
	}
	return map[string]any{
		"store_backend":         backendLabel(s.snapshotPath),
		"resource_count":        len(s.resources),
		"lease_request_count":   len(s.requests),
		"pending_request_count": pendingRequests,
		"granted_request_count": grantedRequests,
		"active_lease_count":    activeLeases,
		"queue_depth":           queueDepth,
		"audit_event_count":     len(s.audit),
		"schema_version":        "v1",
		"queue_manager":         "inline_on_request",
	}
}

func (s *Store) Metrics() contracts.ComponentMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	samples := []contracts.MetricSample{
		contracts.CountMetric("lease_resources_total", len(s.resources), nil),
		contracts.CountMetric("lease_requests_total", len(s.requests), nil),
	}
	requestsByState := map[string]int{
		string(contracts.LeaseRequestPending):  0,
		string(contracts.LeaseRequestGranted):  0,
		string(contracts.LeaseRequestCanceled): 0,
		string(contracts.LeaseRequestExpired):  0,
	}
	activeLeases := 0
	waitTotals := map[string]float64{}
	waitCounts := map[string]int{}
	for _, rec := range s.requests {
		requestsByState[string(rec.request.State)]++
		if rec.request.State == contracts.LeaseRequestGranted {
			if seconds, ok := leaseGrantWaitSeconds(rec.request); ok {
				selector := rec.request.ResourceSelector
				if selector == "" {
					selector = "unknown"
				}
				waitTotals[selector] += seconds
				waitCounts[selector]++
			}
		}
	}
	for _, rec := range s.leases {
		if rec.state == leaseActive {
			activeLeases++
		}
	}
	for state, count := range requestsByState {
		samples = append(samples, contracts.CountMetric("lease_requests_by_state", count, map[string]string{"state": state}))
	}
	for selector, queue := range s.queues {
		samples = append(samples, contracts.CountMetric("lease_queue_depth", len(queue), map[string]string{"selector": selector}))
	}
	for selector, total := range waitTotals {
		count := waitCounts[selector]
		if count == 0 {
			continue
		}
		samples = append(samples, contracts.GaugeMetric("lease_grant_wait_seconds_avg", total/float64(count), "seconds", map[string]string{"selector": selector}))
	}
	samples = append(samples, contracts.CountMetric("leases_active_total", activeLeases, nil))
	return contracts.NewComponentMetrics("leases", samples)
}

func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

func (s *Store) RegisterResource(req contracts.RegisterResourceRequest) (contracts.ResourceRecord, error) {
	if req.Selector == "" {
		return contracts.ResourceRecord{}, fmt.Errorf("%w: selector is required", ErrValidation)
	}
	if req.Status == "" {
		req.Status = contracts.ResourceAvailable
	}
	if req.Status != contracts.ResourceAvailable && req.Status != contracts.ResourceUnavailable {
		return contracts.ResourceRecord{}, fmt.Errorf("%w: status must be available or unavailable", ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	resourceID := req.ResourceID
	if resourceID == "" {
		resourceID = fmt.Sprintf("res_%06d", s.nextResourceID)
		s.nextResourceID++
	}
	if _, exists := s.resources[resourceID]; exists {
		return contracts.ResourceRecord{}, ErrResourceConflict
	}

	record := contracts.ResourceRecord{
		ResourceID:  resourceID,
		Selector:    req.Selector,
		DisplayName: req.DisplayName,
		Status:      req.Status,
		NodeID:      req.NodeID,
		Tags:        append([]string(nil), req.Tags...),
		Metadata:    cloneMap(req.Metadata),
		Links:       resourceLinks(resourceID),
	}
	s.resources[resourceID] = &resourceRecord{resource: record}
	s.allocatePendingLocked(req.Selector)
	for _, tag := range req.Tags {
		s.allocatePendingLocked(tag)
	}
	if err := s.saveLocked(); err != nil {
		return contracts.ResourceRecord{}, err
	}
	return cloneResource(record), nil
}

func (s *Store) ListResources(selector string) []contracts.ResourceRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLeasesLocked()
	defer func() { _ = s.saveLocked() }()

	ids := make([]string, 0, len(s.resources))
	for id := range s.resources {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	resources := make([]contracts.ResourceRecord, 0, len(ids))
	for _, id := range ids {
		rec := s.resources[id]
		if selector != "" && !matchesSelector(rec.resource, selector) {
			continue
		}
		resources = append(resources, cloneResource(rec.resource))
	}
	return resources
}

func (s *Store) GetResource(resourceID string) (contracts.ResourceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.resources[resourceID]
	if !ok {
		return contracts.ResourceRecord{}, ErrNotFound
	}
	return cloneResource(rec.resource), nil
}

func (s *Store) InspectResource(resourceID string) (contracts.ResourceInspection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLeasesLocked()
	defer func() { _ = s.saveLocked() }()

	rec, ok := s.resources[resourceID]
	if !ok {
		return contracts.ResourceInspection{}, ErrNotFound
	}
	resource := rec.resource
	selectorQueue := s.queueRecordsLocked(resource.Selector)
	active := s.activeLeaseForResourceLocked(resource.ResourceID)
	return contracts.ResourceInspection{
		Resource:    cloneResource(resource),
		ActiveLease: active,
		QueueLength: len(selectorQueue),
		Queue:       selectorQueue,
	}, nil
}

func (s *Store) CreateLeaseRequest(req contracts.CreateLeaseRequest) (contracts.LeaseRequest, error) {
	if req.RequesterID == "" {
		return contracts.LeaseRequest{}, fmt.Errorf("%w: requester_id is required", ErrValidation)
	}
	if req.ResourceSelector == "" {
		return contracts.LeaseRequest{}, fmt.Errorf("%w: resource_selector is required", ErrValidation)
	}
	if req.HeartbeatTimeoutSeconds < 0 {
		return contracts.LeaseRequest{}, fmt.Errorf("%w: heartbeat_timeout_seconds must be positive", ErrValidation)
	}
	if req.HeartbeatTimeoutSeconds == 0 {
		req.HeartbeatTimeoutSeconds = 60
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLeasesLocked()

	matches := s.matchingResourcesLocked(req.ResourceSelector)
	if len(matches) == 0 {
		return contracts.LeaseRequest{}, ErrResourceUnavailable
	}
	if !hasAvailableResource(matches) {
		return contracts.LeaseRequest{}, ErrNoCapacity
	}

	now := s.formatNow()
	requestID := fmt.Sprintf("lease_req_%06d", s.nextRequestID)
	s.nextRequestID++
	rec := &leaseRequestRecord{
		requesterID:      req.RequesterID,
		priority:         req.Priority,
		sequence:         s.nextSequence,
		heartbeatTimeout: time.Duration(req.HeartbeatTimeoutSeconds) * time.Second,
		request: contracts.LeaseRequest{
			RequestID:        requestID,
			State:            contracts.LeaseRequestPending,
			RequesterID:      req.RequesterID,
			ResourceSelector: req.ResourceSelector,
			QueuePosition:    nil,
			Lease:            nil,
			CreatedAt:        now,
			UpdatedAt:        now,
			Links:            pendingRequestLinks(requestID),
		},
	}
	s.nextSequence++
	s.requests[requestID] = rec

	if resource := s.freeResourceLocked(req.ResourceSelector); resource != nil && len(s.queues[req.ResourceSelector]) == 0 {
		s.grantLocked(rec, resource)
		if err := s.saveLocked(); err != nil {
			return contracts.LeaseRequest{}, err
		}
		return cloneLeaseRequest(rec.request), nil
	}

	s.enqueueLocked(req.ResourceSelector, requestID)
	if err := s.saveLocked(); err != nil {
		return contracts.LeaseRequest{}, err
	}
	return cloneLeaseRequest(rec.request), nil
}

func (s *Store) GetLeaseRequest(requestID string) (contracts.LeaseRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLeasesLocked()
	defer func() { _ = s.saveLocked() }()

	rec, ok := s.requests[requestID]
	if !ok {
		return contracts.LeaseRequest{}, ErrNotFound
	}
	return cloneLeaseRequest(rec.request), nil
}

func (s *Store) CancelLeaseRequest(requestID string, req contracts.CancelRequest) (contracts.LeaseRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLeasesLocked()

	rec, ok := s.requests[requestID]
	if !ok {
		return contracts.LeaseRequest{}, ErrNotFound
	}
	switch rec.request.State {
	case contracts.LeaseRequestPending:
		s.removeFromQueueLocked(rec.request.ResourceSelector, requestID)
		rec.request.State = contracts.LeaseRequestCanceled
		rec.request.QueuePosition = nil
		rec.request.Links = map[string]any{}
		rec.request.UpdatedAt = s.formatNow()
		s.updateQueuePositionsLocked(rec.request.ResourceSelector)
		if err := s.saveLocked(); err != nil {
			return contracts.LeaseRequest{}, err
		}
		return cloneLeaseRequest(rec.request), nil
	case contracts.LeaseRequestCanceled, contracts.LeaseRequestExpired:
		return cloneLeaseRequest(rec.request), nil
	default:
		return contracts.LeaseRequest{}, ErrInvalidTransition
	}
}

func (s *Store) GetLease(leaseID string) (contracts.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLeasesLocked()
	defer func() { _ = s.saveLocked() }()

	rec, ok := s.leases[leaseID]
	if !ok {
		return contracts.Lease{}, ErrNotFound
	}
	return cloneLease(rec.lease), nil
}

func (s *Store) Heartbeat(leaseID string, req contracts.LeaseHeartbeatRequest) (contracts.Lease, error) {
	if req.HolderID == "" {
		return contracts.Lease{}, fmt.Errorf("%w: holder_id is required", ErrValidation)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLeasesLocked()

	rec, ok := s.leases[leaseID]
	if !ok {
		return contracts.Lease{}, ErrNotFound
	}
	if rec.lease.HolderID != req.HolderID {
		return contracts.Lease{}, ErrHolderMismatch
	}
	if rec.state == leaseExpired {
		return contracts.Lease{}, ErrLeaseExpired
	}
	if rec.state != leaseActive {
		return contracts.Lease{}, ErrInvalidTransition
	}

	rec.lease.ExpiresAt = formatTime(s.now().UTC().Add(rec.timeout))
	if reqRec, ok := s.requests[rec.requestID]; ok {
		reqRec.request.Lease = leasePtr(rec.lease)
		reqRec.request.UpdatedAt = s.formatNow()
	}
	if err := s.saveLocked(); err != nil {
		return contracts.Lease{}, err
	}
	return cloneLease(rec.lease), nil
}

func (s *Store) Release(leaseID string, req contracts.LeaseReleaseRequest, idempotencyKey, actorSubjectID string) (contracts.Lease, error) {
	if idempotencyKey == "" {
		return contracts.Lease{}, ErrMissingIdempotency
	}
	if req.HolderID == "" {
		return contracts.Lease{}, fmt.Errorf("%w: holder_id is required", ErrValidation)
	}
	fingerprint, err := fingerprint(req)
	if err != nil {
		return contracts.Lease{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.releaseIdempotency[idempotencyKey]; ok {
		if existing.fingerprint != fingerprint {
			return contracts.Lease{}, ErrIdempotencyConflict
		}
		return cloneLease(existing.response), nil
	}

	s.expireLeasesLocked()
	rec, ok := s.leases[leaseID]
	if !ok {
		return contracts.Lease{}, ErrNotFound
	}
	if rec.lease.HolderID != req.HolderID {
		return contracts.Lease{}, ErrHolderMismatch
	}
	if rec.state == leaseExpired {
		return contracts.Lease{}, ErrLeaseExpired
	}
	if rec.state == leaseReleased {
		s.releaseIdempotency[idempotencyKey] = idempotentRelease{fingerprint: fingerprint, leaseID: leaseID, response: cloneLease(rec.lease)}
		if err := s.saveLocked(); err != nil {
			return contracts.Lease{}, err
		}
		return cloneLease(rec.lease), nil
	}

	now := s.formatNow()
	rec.state = leaseReleased
	rec.lease.ReleasedAt = now
	rec.lease.ReleasedBy = actorSubjectID
	rec.lease.ReleaseReason = req.Reason
	rec.lease.Links = map[string]any{}

	if reqRec, ok := s.requests[rec.requestID]; ok {
		reqRec.request.Lease = leasePtr(rec.lease)
		reqRec.request.UpdatedAt = now
	}
	s.audit = append(s.audit, contracts.LeaseAuditEvent{
		EventType:      "lease.released",
		LeaseID:        leaseID,
		HolderID:       rec.lease.HolderID,
		ActorSubjectID: actorSubjectID,
		OccurredAt:     now,
	})
	s.releaseIdempotency[idempotencyKey] = idempotentRelease{fingerprint: fingerprint, leaseID: leaseID, response: cloneLease(rec.lease)}

	selector := s.requestSelectorForLeaseLocked(rec)
	s.allocatePendingLocked(selector)
	if err := s.saveLocked(); err != nil {
		return contracts.Lease{}, err
	}
	return cloneLease(rec.lease), nil
}

func (s *Store) AuditEvents() []contracts.LeaseAuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]contracts.LeaseAuditEvent(nil), s.audit...)
}

func (s *Store) expireLeasesLocked() {
	now := s.now().UTC()
	selectors := map[string]bool{}
	for _, rec := range s.leases {
		if rec.state != leaseActive {
			continue
		}
		expiresAt, err := time.Parse(time.RFC3339, rec.lease.ExpiresAt)
		if err != nil || !now.Before(expiresAt) {
			rec.state = leaseExpired
			rec.lease.Links = map[string]any{}
			selector := s.selectorForResourceLocked(rec.lease.ResourceID)
			if reqRec, ok := s.requests[rec.requestID]; ok {
				selector = reqRec.request.ResourceSelector
				reqRec.request.State = contracts.LeaseRequestExpired
				reqRec.request.Lease = leasePtr(rec.lease)
				reqRec.request.QueuePosition = nil
				reqRec.request.Links = map[string]any{}
				reqRec.request.UpdatedAt = formatTime(now)
			}
			selectors[selector] = true
		}
	}
	for selector := range selectors {
		s.allocatePendingLocked(selector)
	}
}

func (s *Store) allocatePendingLocked(selector string) {
	for {
		resource := s.freeResourceLocked(selector)
		if resource == nil {
			s.updateQueuePositionsLocked(selector)
			return
		}
		queue := s.queues[selector]
		if len(queue) == 0 {
			return
		}
		requestID := queue[0]
		s.queues[selector] = queue[1:]
		reqRec, ok := s.requests[requestID]
		if !ok || reqRec.request.State != contracts.LeaseRequestPending {
			continue
		}
		s.grantLocked(reqRec, resource)
		s.updateQueuePositionsLocked(selector)
	}
}

func (s *Store) grantLocked(reqRec *leaseRequestRecord, resource *resourceRecord) {
	now := s.now().UTC()
	leaseID := fmt.Sprintf("lease_%06d", s.nextLeaseID)
	s.nextLeaseID++
	lease := contracts.Lease{
		LeaseID:    leaseID,
		ResourceID: resource.resource.ResourceID,
		HolderID:   reqRec.requesterID,
		ExpiresAt:  formatTime(now.Add(reqRec.heartbeatTimeout)),
		Links:      leaseLinks(leaseID),
	}
	s.leases[leaseID] = &leaseRecord{
		lease:     lease,
		requestID: reqRec.request.RequestID,
		timeout:   reqRec.heartbeatTimeout,
		state:     leaseActive,
	}
	reqRec.leaseID = leaseID
	reqRec.request.State = contracts.LeaseRequestGranted
	reqRec.request.QueuePosition = nil
	reqRec.request.Lease = leasePtr(lease)
	reqRec.request.UpdatedAt = formatTime(now)
	reqRec.request.Links = grantedRequestLinks(reqRec.request.RequestID)
}

func (s *Store) enqueueLocked(selector, requestID string) {
	s.queues[selector] = append(s.queues[selector], requestID)
	sort.SliceStable(s.queues[selector], func(i, j int) bool {
		left := s.requests[s.queues[selector][i]]
		right := s.requests[s.queues[selector][j]]
		if left.priority == right.priority {
			return left.sequence < right.sequence
		}
		return left.priority > right.priority
	})
	s.updateQueuePositionsLocked(selector)
}

func (s *Store) removeFromQueueLocked(selector, requestID string) {
	queue := s.queues[selector]
	next := queue[:0]
	for _, id := range queue {
		if id != requestID {
			next = append(next, id)
		}
	}
	s.queues[selector] = next
}

func (s *Store) updateQueuePositionsLocked(selector string) {
	position := 1
	for _, requestID := range s.queues[selector] {
		rec, ok := s.requests[requestID]
		if !ok || rec.request.State != contracts.LeaseRequestPending {
			continue
		}
		rec.request.QueuePosition = intPtr(position)
		rec.request.UpdatedAt = s.formatNow()
		position++
	}
}

func (s *Store) matchingResourcesLocked(selector string) []*resourceRecord {
	var matches []*resourceRecord
	for _, rec := range s.resources {
		if matchesSelector(rec.resource, selector) {
			matches = append(matches, rec)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].resource.ResourceID < matches[j].resource.ResourceID
	})
	return matches
}

func (s *Store) freeResourceLocked(selector string) *resourceRecord {
	for _, rec := range s.matchingResourcesLocked(selector) {
		if rec.resource.Status != contracts.ResourceAvailable {
			continue
		}
		if s.activeLeaseForResourceLocked(rec.resource.ResourceID) != nil {
			continue
		}
		return rec
	}
	return nil
}

func (s *Store) activeLeaseForResourceLocked(resourceID string) *contracts.Lease {
	for _, rec := range s.leases {
		if rec.lease.ResourceID == resourceID && rec.state == leaseActive {
			return leasePtr(rec.lease)
		}
	}
	return nil
}

func (s *Store) selectorForResourceLocked(resourceID string) string {
	if rec, ok := s.resources[resourceID]; ok {
		return rec.resource.Selector
	}
	return ""
}

func (s *Store) requestSelectorForLeaseLocked(rec *leaseRecord) string {
	if reqRec, ok := s.requests[rec.requestID]; ok {
		return reqRec.request.ResourceSelector
	}
	return s.selectorForResourceLocked(rec.lease.ResourceID)
}

func (s *Store) queueRecordsLocked(selector string) []contracts.LeaseQueueRecord {
	queue := s.queues[selector]
	records := make([]contracts.LeaseQueueRecord, 0, len(queue))
	position := 1
	for _, requestID := range queue {
		rec, ok := s.requests[requestID]
		if !ok || rec.request.State != contracts.LeaseRequestPending {
			continue
		}
		records = append(records, contracts.LeaseQueueRecord{
			RequestID:     requestID,
			RequesterID:   rec.requesterID,
			Priority:      rec.priority,
			QueuePosition: position,
		})
		position++
	}
	return records
}

func (s *Store) formatNow() string {
	return formatTime(s.now().UTC())
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func hasAvailableResource(resources []*resourceRecord) bool {
	for _, rec := range resources {
		if rec.resource.Status == contracts.ResourceAvailable {
			return true
		}
	}
	return false
}

func matchesSelector(resource contracts.ResourceRecord, selector string) bool {
	if resource.Selector == selector {
		return true
	}
	for _, tag := range resource.Tags {
		if tag == selector {
			return true
		}
	}
	return false
}

func resourceLinks(resourceID string) map[string]any {
	return map[string]any{
		"self":       map[string]any{"method": "GET", "href": "/v1/resources/" + resourceID},
		"inspection": map[string]any{"method": "GET", "href": "/v1/resources/" + resourceID + "/inspection"},
	}
}

func pendingRequestLinks(requestID string) map[string]any {
	return map[string]any{
		"status": map[string]any{"method": "GET", "href": "/v1/lease-requests/" + requestID, "description": "Read lease request."},
		"cancel": map[string]any{"method": "POST", "href": "/v1/lease-requests/" + requestID + "/cancel", "description": "Cancel request."},
	}
}

func grantedRequestLinks(requestID string) map[string]any {
	return map[string]any{
		"status": map[string]any{"method": "GET", "href": "/v1/lease-requests/" + requestID, "description": "Read lease request."},
	}
}

func leaseLinks(leaseID string) map[string]any {
	return map[string]any{
		"heartbeat": map[string]any{"method": "POST", "href": "/v1/leases/" + leaseID + "/heartbeat", "description": "Refresh lease."},
		"release":   map[string]any{"method": "POST", "href": "/v1/leases/" + leaseID + "/release", "description": "Release lease."},
	}
}

func intPtr(value int) *int {
	return &value
}

func leasePtr(lease contracts.Lease) *contracts.Lease {
	cloned := cloneLease(lease)
	return &cloned
}

func cloneResource(resource contracts.ResourceRecord) contracts.ResourceRecord {
	raw, _ := json.Marshal(resource)
	var cloned contracts.ResourceRecord
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func cloneLeaseRequest(request contracts.LeaseRequest) contracts.LeaseRequest {
	raw, _ := json.Marshal(request)
	var cloned contracts.LeaseRequest
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func cloneLease(lease contracts.Lease) contracts.Lease {
	raw, _ := json.Marshal(lease)
	var cloned contracts.Lease
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	raw, _ := json.Marshal(value)
	var cloned map[string]any
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

func (s *Store) loadSnapshot(path string) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var snapshot snapshotFile
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return fmt.Errorf("%w: invalid lease snapshot: %v", ErrValidation, err)
	}
	s.nextResourceID = positiveOrDefault(snapshot.NextResourceID, 1)
	s.nextRequestID = positiveOrDefault(snapshot.NextRequestID, 1)
	s.nextLeaseID = positiveOrDefault(snapshot.NextLeaseID, 1)
	s.nextSequence = positiveOrDefault(snapshot.NextSequence, 1)
	s.resources = map[string]*resourceRecord{}
	for resourceID, resource := range snapshot.Resources {
		if resource.ResourceID == "" {
			resource.ResourceID = resourceID
		}
		resource.Links = resourceLinks(resource.ResourceID)
		s.resources[resource.ResourceID] = &resourceRecord{resource: cloneResource(resource)}
	}
	s.requests = map[string]*leaseRequestRecord{}
	for requestID, rec := range snapshot.Requests {
		request := cloneLeaseRequest(rec.Request)
		if request.RequestID == "" {
			request.RequestID = requestID
		}
		request.Links = leaseRequestLinks(request)
		timeout := time.Duration(rec.HeartbeatTimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = time.Minute
		}
		s.requests[request.RequestID] = &leaseRequestRecord{
			request:          request,
			requesterID:      rec.RequesterID,
			priority:         rec.Priority,
			sequence:         rec.Sequence,
			heartbeatTimeout: timeout,
			leaseID:          rec.LeaseID,
		}
	}
	s.leases = map[string]*leaseRecord{}
	for leaseID, rec := range snapshot.Leases {
		lease := cloneLease(rec.Lease)
		if lease.LeaseID == "" {
			lease.LeaseID = leaseID
		}
		state := rec.State
		if state == "" {
			state = leaseActive
		}
		lease.Links = leaseLinksForState(lease.LeaseID, state)
		timeout := time.Duration(rec.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = time.Minute
		}
		s.leases[lease.LeaseID] = &leaseRecord{lease: lease, requestID: rec.RequestID, timeout: timeout, state: state}
	}
	s.queues = map[string][]string{}
	for selector, queue := range snapshot.Queues {
		s.queues[selector] = append([]string(nil), queue...)
	}
	s.releaseIdempotency = map[string]idempotentRelease{}
	for key, rec := range snapshot.ReleaseIdempotency {
		s.releaseIdempotency[key] = idempotentRelease{
			fingerprint: rec.Fingerprint,
			leaseID:     rec.LeaseID,
			response:    cloneLease(rec.Response),
		}
	}
	s.audit = append([]contracts.LeaseAuditEvent(nil), snapshot.Audit...)
	for selector := range s.queues {
		s.updateQueuePositionsLocked(selector)
	}
	return nil
}

func (s *Store) saveLocked() error {
	if s.snapshotPath == "" {
		return nil
	}
	snapshot := snapshotFile{
		Version:            1,
		NextResourceID:     s.nextResourceID,
		NextRequestID:      s.nextRequestID,
		NextLeaseID:        s.nextLeaseID,
		NextSequence:       s.nextSequence,
		Resources:          map[string]contracts.ResourceRecord{},
		Requests:           map[string]leaseRequestSnapshot{},
		Leases:             map[string]leaseSnapshot{},
		Queues:             map[string][]string{},
		ReleaseIdempotency: map[string]idempotentReleaseSnapshot{},
		Audit:              append([]contracts.LeaseAuditEvent(nil), s.audit...),
	}
	for resourceID, rec := range s.resources {
		snapshot.Resources[resourceID] = cloneResource(rec.resource)
	}
	for requestID, rec := range s.requests {
		snapshot.Requests[requestID] = leaseRequestSnapshot{
			Request:                 cloneLeaseRequest(rec.request),
			RequesterID:             rec.requesterID,
			Priority:                rec.priority,
			Sequence:                rec.sequence,
			HeartbeatTimeoutSeconds: int64(rec.heartbeatTimeout / time.Second),
			LeaseID:                 rec.leaseID,
		}
	}
	for leaseID, rec := range s.leases {
		snapshot.Leases[leaseID] = leaseSnapshot{
			Lease:          cloneLease(rec.lease),
			RequestID:      rec.requestID,
			TimeoutSeconds: int64(rec.timeout / time.Second),
			State:          rec.state,
		}
	}
	for selector, queue := range s.queues {
		snapshot.Queues[selector] = append([]string(nil), queue...)
	}
	for key, rec := range s.releaseIdempotency {
		snapshot.ReleaseIdempotency[key] = idempotentReleaseSnapshot{
			Fingerprint: rec.fingerprint,
			LeaseID:     rec.leaseID,
			Response:    cloneLease(rec.response),
		}
	}
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.snapshotPath), 0o755); err != nil {
		return err
	}
	tmpPath := s.snapshotPath + ".tmp"
	if err := os.WriteFile(tmpPath, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.snapshotPath)
}

func leaseRequestLinks(request contracts.LeaseRequest) map[string]any {
	switch request.State {
	case contracts.LeaseRequestPending:
		return pendingRequestLinks(request.RequestID)
	case contracts.LeaseRequestGranted:
		return grantedRequestLinks(request.RequestID)
	default:
		return map[string]any{}
	}
}

func leaseLinksForState(leaseID string, state leaseState) map[string]any {
	if state == leaseActive {
		return leaseLinks(leaseID)
	}
	return map[string]any{}
}

func positiveOrDefault(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func backendLabel(path string) string {
	if path == "" {
		return "memory"
	}
	return "file_snapshot"
}

func leaseGrantWaitSeconds(request contracts.LeaseRequest) (float64, bool) {
	createdAt, err := time.Parse(time.RFC3339, request.CreatedAt)
	if err != nil {
		return 0, false
	}
	updatedAt, err := time.Parse(time.RFC3339, request.UpdatedAt)
	if err != nil {
		return 0, false
	}
	if updatedAt.Before(createdAt) {
		return 0, false
	}
	return updatedAt.Sub(createdAt).Seconds(), true
}
