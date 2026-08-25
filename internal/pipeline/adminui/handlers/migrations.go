package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/waxarsatia/denyra/internal/pipeline/adminui/middleware"
	"github.com/waxarsatia/denyra/internal/pipeline/adminui/views"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
)

func (c Console) unmanaged(w http.ResponseWriter, r *http.Request) {
	if c.dependencies.MigrationReader == nil {
		http.NotFound(w, r)
		return
	}
	filter := application.UnmanagedFilter{Query: r.URL.Query().Get("q"), Status: r.URL.Query().Get("status")}
	items, next, err := c.dependencies.MigrationReader.UnmanagedSummaries(r.Context(), filter, 50, r.URL.Query().Get("cursor"))
	page := views.UnmanagedPage{Shell: c.shell(r), Items: items, Query: filter.Query, Status: filter.Status, Next: next}
	if err != nil {
		page.Error = "Unable to load unmanaged releases."
	}
	c.render(w, r, views.Unmanaged(page), views.UnmanagedContent(page))
}

func (c Console) checkUnmanaged(w http.ResponseWriter, r *http.Request) {
	principal, _ := middleware.Principal(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid check form", http.StatusBadRequest)
		return
	}
	selection := application.Selection{ReleaseIDs: r.Form["release_id"], Revisions: make(map[string]uint64)}
	for _, id := range selection.ReleaseIDs {
		revision, err := strconv.ParseUint(r.Form.Get("state_revision_"+id), 10, 64)
		if err != nil {
			http.Error(w, "invalid unmanaged release revision", http.StatusBadRequest)
			return
		}
		selection.Revisions[id] = revision
	}
	batch, _, err := c.dependencies.MigrationChecks.CreateBatch(r.Context(), selection, principal.UserID)
	if err != nil {
		http.Error(w, "catalog check could not be created", http.StatusBadRequest)
		return
	}
	if c.dependencies.NotifyMigrationBatch != nil {
		c.dependencies.NotifyMigrationBatch(batch.ID)
	}
	http.Redirect(w, r, "/migration-batches/"+batch.ID, http.StatusSeeOther)
}

func (c Console) migrationBatch(w http.ResponseWriter, r *http.Request) {
	if c.dependencies.MigrationReader == nil {
		http.NotFound(w, r)
		return
	}
	detail, err := c.dependencies.MigrationReader.MigrationBatchDetail(r.Context(), r.PathValue("batchID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	page := views.MigrationPage{Shell: c.shell(r), Detail: detail}
	c.render(w, r, views.MigrationDetail(page), views.MigrationDetailContent(page))
}

func (c Console) confirmMigrations(w http.ResponseWriter, r *http.Request) {
	principal, _ := middleware.Principal(r)
	if err := r.ParseForm(); err != nil || r.Form.Get("confirm_migrations") != "yes" {
		http.Error(w, "explicit migration confirmation required", http.StatusBadRequest)
		return
	}
	ids := r.Form["item_id"]
	if len(ids) == 0 {
		http.Error(w, "migration selection is incomplete", http.StatusBadRequest)
		return
	}
	selections := make([]application.ConfirmedSelection, 0, len(ids))
	for _, id := range ids {
		revision, err := strconv.ParseUint(r.Form.Get("state_revision_"+id), 10, 64)
		if err != nil {
			http.Error(w, "invalid migration revision", http.StatusBadRequest)
			return
		}
		selections = append(selections, application.ConfirmedSelection{ItemID: id, ExpectedRevision: revision, ReleaseMBID: r.Form.Get("release_mbid_" + id)})
	}
	if err := c.dependencies.Migrations.ConfirmSelected(r.Context(), selections, principal.UserID); err != nil {
		http.Error(w, "migration confirmation failed", http.StatusConflict)
		return
	}
	if c.dependencies.NotifyMigrationBatch != nil {
		c.dependencies.NotifyMigrationBatch(r.PathValue("batchID"))
	}
	http.Redirect(w, r, "/migration-batches/"+r.PathValue("batchID"), http.StatusSeeOther)
}

func (c Console) retryMigration(w http.ResponseWriter, r *http.Request) {
	principal, _ := middleware.Principal(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid retry form", http.StatusBadRequest)
		return
	}
	revision, err := strconv.ParseUint(r.Form.Get("state_revision"), 10, 64)
	if err != nil {
		http.Error(w, "invalid migration revision", http.StatusBadRequest)
		return
	}
	if err := c.dependencies.Migrations.Retry(r.Context(), r.PathValue("itemID"), revision, principal.UserID); err != nil {
		http.Error(w, fmt.Sprintf("migration retry failed: %v", err), http.StatusConflict)
		return
	}
	batchID := r.Form.Get("batch_id")
	if c.dependencies.NotifyMigrationBatch != nil {
		c.dependencies.NotifyMigrationBatch(batchID)
	}
	http.Redirect(w, r, "/migration-batches/"+batchID, http.StatusSeeOther)
}
