package checker

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/joesiltberg/bowness/fedtls"
	"github.com/joesiltberg/matfmonitor/internal/store"
)

// Scheduler manages rate-limited health checks for all servers
type Scheduler struct {
	checker          Checker
	store            *store.Store
	metadataStore    *fedtls.MetadataStore
	maxParallel      int
	checksPerMinute  int
	minCheckInterval time.Duration

	// Track servers currently being checked to avoid duplicate checks
	inFlight     map[string]bool
	inFlightLock sync.Mutex

	// For graceful shutdown
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewScheduler creates a new Scheduler
func NewScheduler(
	checker Checker,
	store *store.Store,
	metadataStore *fedtls.MetadataStore,
	maxParallel int,
	checksPerMinute int,
	minCheckInterval time.Duration,
) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Scheduler{
		checker:          checker,
		store:            store,
		metadataStore:    metadataStore,
		maxParallel:      maxParallel,
		checksPerMinute:  checksPerMinute,
		minCheckInterval: minCheckInterval,
		inFlight:         make(map[string]bool),
		ctx:              ctx,
		cancel:           cancel,
	}
}

// Start begins the scheduling loop
func (s *Scheduler) Start() {
	s.wg.Add(1)
	go s.run()
}

// Stop gracefully stops the scheduler and waits for in-progress checks
func (s *Scheduler) Stop() {
	s.cancel()
	s.wg.Wait()
}

// serverKey creates a unique key for a server
func serverKey(entityID, baseURI string) string {
	return entityID + "|" + baseURI
}

// markInFlight marks a server as being checked. Returns false if already in-flight.
func (s *Scheduler) markInFlight(entityID, baseURI string) bool {
	s.inFlightLock.Lock()
	defer s.inFlightLock.Unlock()
	key := serverKey(entityID, baseURI)
	if s.inFlight[key] {
		return false
	}
	s.inFlight[key] = true
	return true
}

// clearInFlight marks a server as no longer being checked
func (s *Scheduler) clearInFlight(entityID, baseURI string) {
	s.inFlightLock.Lock()
	defer s.inFlightLock.Unlock()
	delete(s.inFlight, serverKey(entityID, baseURI))
}

func (s *Scheduler) run() {
	defer s.wg.Done()

	// Calculate interval between checks based on rate limit
	checkInterval := time.Minute / time.Duration(s.checksPerMinute)

	// Semaphore for parallel limit
	semaphore := make(chan struct{}, s.maxParallel)

	// Track in-flight checks
	var inflightWg sync.WaitGroup

	// Listen for metadata changes
	metadataChanged := make(chan int, 1)
	s.metadataStore.AddChangeListener(metadataChanged)

	// Wait for metadata to be available (either already loaded or wait for first load)
	for {
		metadata := s.metadataStore.GetMetadata()
		if metadata != nil && len(metadata.Entities) > 0 {
			log.Println("Metadata available, starting health checks")
			s.syncServersFromMetadata()
			break
		}
		log.Println("Waiting for metadata to load...")
		select {
		case <-s.ctx.Done():
			return
		case <-metadataChanged:
			// Loop will check if metadata is now available
		}
	}

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			// Wait for in-flight checks to complete
			inflightWg.Wait()
			return

		case <-metadataChanged:
			s.syncServersFromMetadata()

		case <-ticker.C:
			// Get servers that need checking (fetch a few to find one not in-flight)
			servers, err := s.store.GetServersNeedingCheck(s.minCheckInterval, s.maxParallel+1, nil, 0)
			if err != nil {
				log.Printf("Error getting servers to check: %v", err)
				continue
			}

			if len(servers) == 0 {
				continue
			}

			// Find first server not already in-flight
			var server *store.ServerToCheck
			for _, srv := range servers {
				if s.markInFlight(srv.EntityID, srv.BaseURI) {
					server = srv
					break
				}
			}
			if server == nil {
				// All candidates are already being checked
				continue
			}

			// Find the server in metadata to get pins
			metadata := s.getServerFromMetadata(server.EntityID, server.BaseURI)
			if metadata == nil {
				// Server no longer in metadata, will be cleaned up on next sync
				s.clearInFlight(server.EntityID, server.BaseURI)
				continue
			}

			// Try to acquire semaphore (non-blocking)
			select {
			case semaphore <- struct{}{}:
				inflightWg.Add(1)
				go func(entityID, baseURI string, srv fedtls.Server) {
					defer func() {
						<-semaphore
						inflightWg.Done()
						s.clearInFlight(entityID, baseURI)
					}()
					s.checkServer(entityID, srv)
				}(server.EntityID, server.BaseURI, *metadata)
			default:
				// All parallel slots in use, skip this tick
				s.clearInFlight(server.EntityID, server.BaseURI)
			}
		}
	}
}

func (s *Scheduler) syncServersFromMetadata() {
	parsed := s.metadataStore.GetMetadata()
	if parsed == nil {
		return
	}

	var currentServers []store.ServerKey

	for _, entity := range parsed.Entities {
		for _, server := range entity.Servers {
			currentServers = append(currentServers, store.ServerKey{
				EntityID: entity.EntityID,
				BaseURI:  server.BaseURI,
			})

			// Ensure server exists in database
			if err := s.store.EnsureServerExists(entity.EntityID, server.BaseURI); err != nil {
				log.Printf("Error ensuring server exists: %v", err)
			}
		}
	}

	// Remove servers no longer in metadata
	if len(currentServers) > 0 {
		if err := s.store.RemoveServersNotIn(currentServers); err != nil {
			log.Printf("Error removing old servers: %v", err)
		}
	}

	log.Printf("Synced %d servers from metadata", len(currentServers))
}

func (s *Scheduler) getServerFromMetadata(entityID, baseURI string) *fedtls.Server {
	parsed := s.metadataStore.GetMetadata()
	if parsed == nil {
		return nil
	}

	for i := range parsed.Entities {
		if parsed.Entities[i].EntityID == entityID {
			for j := range parsed.Entities[i].Servers {
				if parsed.Entities[i].Servers[j].BaseURI == baseURI {
					return &parsed.Entities[i].Servers[j]
				}
			}
		}
	}
	return nil
}

func (s *Scheduler) checkServer(entityID string, server fedtls.Server) {
	result := s.checker.Check(entityID, server)

	status := &store.ServerStatus{
		ServerKey: store.ServerKey{
			EntityID: result.EntityID,
			BaseURI:  result.BaseURI,
		},
		LastChecked:     &result.CheckedAt,
		IsHealthy:       &result.IsHealthy,
		ErrorMessage:    result.ErrorMessage,
		CertExpires:     result.CertExpires,
		CertCN:          result.CertCN,
		CertFingerprint: result.CertFingerprint,
	}

	if err := s.store.SaveStatus(status); err != nil {
		log.Printf("Error saving status for %s: %v", server.BaseURI, err)
	}

	statusStr := "healthy"
	if !result.IsHealthy {
		statusStr = "unhealthy"
	}
	log.Printf("Checked %s: %s", server.BaseURI, statusStr)
}
