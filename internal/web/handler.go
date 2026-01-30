// Package web provides the HTTP handlers for the matfmonitor web interface.
package web

import (
	"embed"
	"html/template"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/joesiltberg/bowness/fedtls"
	"github.com/joesiltberg/matfmonitor/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

// Handler handles HTTP requests for the status page
type Handler struct {
	store         *store.Store
	metadataStore *fedtls.MetadataStore
	template      *template.Template
}

// NewHandler creates a new Handler
func NewHandler(store *store.Store, metadataStore *fedtls.MetadataStore) (*Handler, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}

	return &Handler{
		store:         store,
		metadataStore: metadataStore,
		template:      tmpl,
	}, nil
}

// EntityView represents an entity for display
type EntityView struct {
	EntityID            string
	Organization        string
	OrganizationID      string
	OrganizationDisplay string
	HealthStatus        string // "healthy", "unhealthy", or "unchecked"
	Servers             []ServerView
}

// ServerView represents a server for display
type ServerView struct {
	BaseURI              string
	Tags                 []string
	HealthStatus         string // "healthy", "unhealthy", or "unchecked"
	IsHealthy            bool
	ErrorMessage         string
	LastChecked          *time.Time
	LastCheckedFormatted string
	CertCN               string
	CertExpires          *time.Time
	CertExpiresFormatted string
}

// PageData is the data passed to the template
type PageData struct {
	Entities       []EntityView
	HealthyCount   int
	UnhealthyCount int
	UncheckedCount int
	GeneratedAt    string
}

// ServeHTTP handles the HTTP request
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	data := h.buildPageData()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.template.ExecuteTemplate(w, "status.html", data); err != nil {
		log.Printf("Error executing template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (h *Handler) buildPageData() PageData {
	data := PageData{
		GeneratedAt: time.Now().Format("2006-01-02 15:04:05 MST"),
	}

	// Get metadata for entity info
	metadata := h.metadataStore.GetMetadata()
	if metadata == nil {
		return data
	}

	// Get all statuses from store
	statuses, err := h.store.GetAllStatuses()
	if err != nil {
		log.Printf("Error getting statuses: %v", err)
		return data
	}

	// Build a map of statuses by entity_id + base_uri
	statusMap := make(map[string]*store.ServerStatus)
	for _, s := range statuses {
		key := s.EntityID + "|" + s.BaseURI
		statusMap[key] = s
	}

	// Build entity views from metadata
	entityMap := make(map[string]*EntityView)

	for _, entity := range metadata.Entities {
		if len(entity.Servers) == 0 {
			continue
		}

		org := "Unknown"
		if entity.Organization != nil {
			org = *entity.Organization
		}

		orgID := ""
		if entity.OrganizationID != nil {
			orgID = *entity.OrganizationID
		}

		ev := &EntityView{
			EntityID:            entity.EntityID,
			Organization:        org,
			OrganizationID:      orgID,
			OrganizationDisplay: org,
			Servers:             make([]ServerView, 0, len(entity.Servers)),
		}

		hasUnhealthy := false
		allChecked := true

		for _, server := range entity.Servers {
			sv := ServerView{
				BaseURI: server.BaseURI,
				Tags:    server.Tags,
			}

			key := entity.EntityID + "|" + server.BaseURI
			if status, ok := statusMap[key]; ok {
				sv.LastChecked = status.LastChecked
				sv.ErrorMessage = status.ErrorMessage
				sv.CertCN = status.CertCN
				sv.CertExpires = status.CertExpires

				if sv.LastChecked != nil {
					sv.LastCheckedFormatted = sv.LastChecked.Format("2006-01-02 15:04:05")
				}
				if sv.CertExpires != nil {
					sv.CertExpiresFormatted = sv.CertExpires.Format("2006-01-02")
				}

				if status.IsHealthy == nil {
					sv.HealthStatus = "unchecked"
					allChecked = false
					data.UncheckedCount++
				} else if *status.IsHealthy {
					sv.HealthStatus = "healthy"
					sv.IsHealthy = true
					data.HealthyCount++
				} else {
					sv.HealthStatus = "unhealthy"
					sv.IsHealthy = false
					hasUnhealthy = true
					data.UnhealthyCount++
				}
			} else {
				sv.HealthStatus = "unchecked"
				allChecked = false
				data.UncheckedCount++
			}

			ev.Servers = append(ev.Servers, sv)
		}

		// Determine entity health status
		if hasUnhealthy {
			ev.HealthStatus = "unhealthy"
		} else if !allChecked {
			ev.HealthStatus = "unchecked"
		} else {
			ev.HealthStatus = "healthy"
		}

		entityMap[entity.EntityID] = ev
	}

	// Convert map to slice and sort by organization name
	entities := make([]EntityView, 0, len(entityMap))
	for _, ev := range entityMap {
		entities = append(entities, *ev)
	}

	sort.Slice(entities, func(i, j int) bool {
		return entities[i].Organization < entities[j].Organization
	})

	data.Entities = entities
	return data
}
