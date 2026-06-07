package leases

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
	ErrNotFound            = errors.New("lease resource not found")
	ErrValidation          = errors.New("validation failed")
	ErrResourceConflict    = errors.New("resource conflict")
	ErrResourceUnavailable = errors.New("resource unavailable")
	ErrNoCapacity          = errors.New("resource has no leasing capacity")
	ErrHolderMismatch      = errors.New("holder mismatch")
	ErrLeaseExpired        = errors.New("lease expired")
	ErrInvalidTransition   = errors.New("invalid lease transition")
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
	return cloneResource(record), nil
}

func (s *Store) ListResources(selector string) []contracts.ResourceRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLeasesLocked()

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
		return cloneLeaseRequest(rec.request), nil
	}

	s.enqueueLocked(req.ResourceSelector, requestID)
	return cloneLeaseRequest(rec.request), nil
}

func (s *Store) GetLeaseRequest(requestID string) (contracts.LeaseRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLeasesLocked()

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
	return cloneLease(rec.lease), nil
}

func (s *Store) Release(leaseID string, req contracts.LeaseReleaseRequest, idempotencyKey, actorSubjectID string) (contracts.Lease, error) {
	if req.HolderID == "" {
		return contracts.Lease{}, fmt.Errorf("%w: holder_id is required", ErrValidation)
	}
	fingerprint, err := fingerprint(req)
	if err != nil {
		return contracts.Lease{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if idempotencyKey != "" {
		if existing, ok := s.releaseIdempotency[idempotencyKey]; ok {
			if existing.fingerprint != fingerprint {
				return contracts.Lease{}, ErrIdempotencyConflict
			}
			return cloneLease(existing.response), nil
		}
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
		if idempotencyKey != "" {
			s.releaseIdempotency[idempotencyKey] = idempotentRelease{fingerprint: fingerprint, leaseID: leaseID, response: cloneLease(rec.lease)}
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
	if idempotencyKey != "" {
		s.releaseIdempotency[idempotencyKey] = idempotentRelease{fingerprint: fingerprint, leaseID: leaseID, response: cloneLease(rec.lease)}
	}

	selector := s.requestSelectorForLeaseLocked(rec)
	s.allocatePendingLocked(selector)
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
